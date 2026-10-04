// Dry-run: validate a pattern WITHOUT publishing it (strudel-agent-uvj.16).
//
// The server never evaluates JavaScript, so a dry-run cannot be a client-side
// flag: something must ask a CONNECTED BROWSER to evaluate a candidate that was
// never published. These tests drive the real route tree and a real listener
// socket, because the whole claim under test is the round trip
// agent -> server -> browser -> server -> agent.
//
// The properties that matter, in order:
//
//  1. With no listener the request fails FAST and clearly. Nobody can evaluate,
//     so blocking until a timeout would report "unknown" for a condition the
//     server already knows about. listenerCount exists for exactly this.
//  2. A dry-run changes NOTHING observable: not the version, not the history,
//     not the stored verdict. This is the entire point -- it exists so a broken
//     candidate does not become the published document.
//  3. It never hangs. Every wait is bounded by the request context AND by a
//     server-side timeout, and the timeout is a FIELD so it runs in milliseconds.

package srv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dryRunServer builds a Server with a short dry-run timeout, so a test that
// proves the timeout path does not have to spend the production value.
func dryRunServer(t *testing.T) *Server {
	t.Helper()
	s := New()
	s.dryRunTimeout = 250 * time.Millisecond
	return s
}

// wsListener starts a real server and dials one listener, returning both. The
// listener is what makes a dry-run possible at all, so tests that need an
// evaluator must have genuinely connected rather than poked the registry.
func wsListener(t *testing.T, s *Server) (*httptest.Server, *websocket.Conn) {
	t.Helper()
	ts := httptest.NewServer(s.routes())
	t.Cleanup(func() { wsCloseServer(t, ts) })

	conn := dialListener(t, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws")

	// Consume the catch-up snapshot the server sends on connect. Every caller
	// below wants the frame that comes AFTER it, and reading it here is what
	// keeps each test from having to know that a listener is greeted first.
	wsReadNextEvent(t, conn, EventSnapshot)

	// The count lands asynchronously (the hub's hook runs on the hub goroutine),
	// so wait for it rather than racing it: a dry-run issued one instruction
	// early would 409 for the wrong reason and the test would prove nothing.
	waitFor(t, 2*time.Second, func() bool {
		return s.Conductor.Snapshot().ListenerCount == 1
	}, "listener never registered on the conductor")
	return ts, conn
}

// TestDryRunWithNoListenerFailsFast is the "no hang" requirement. With nobody
// able to evaluate, the server must say so immediately rather than hold the
// request open until its timeout -- and must name the reason, because "it did
// not work" and "there was nobody to ask" call for different responses from the
// caller.
func TestDryRunWithNoListenerFailsFast(t *testing.T) {
	s := dryRunServer(t)

	start := time.Now()
	rec := postJSONTo(t, s, "/api/dry-run", `{"code":"s(\"bd\")"}`)
	elapsed := time.Since(start)

	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /api/dry-run with no listener = %d %q, want 409", rec.Code, snapBody(rec))
	}
	body := snapBody(rec)
	if !strings.Contains(body, "no listeners connected") {
		t.Errorf("error %q does not say no listener is connected; the caller cannot tell a missing evaluator from a broken candidate", body)
	}
	// Fast is the claim being tested. The server-side timeout here is 250ms, so
	// anything near it means the guard ran after (or instead of) the check.
	if elapsed > 100*time.Millisecond {
		t.Errorf("the no-listener refusal took %s; it should fail immediately rather than wait out the %s timeout", elapsed, s.dryRunTimeout)
	}
}

// TestDryRunChangesNothingObservable is acceptance criterion 2: the version is
// unchanged and the history does not grow. It also pins the verdict, because a
// dry-run verdict that reached Conductor.RecordEvalResult would be a verdict
// about code that was never published -- corrupting the only feedback signal
// the agent has.
func TestDryRunChangesNothingObservable(t *testing.T) {
	s := dryRunServer(t)
	ts, conn := wsListener(t, s)

	// A real published version with a real verdict, so "unchanged" is a claim
	// about a populated state rather than an empty one.
	postJSONTo(t, s, "/api/code", `{"code":"s(\"bd*2\")"}`)
	wsReadNextEvent(t, conn, EventCode)
	postJSONTo(t, s, "/api/eval-result", `{"version":1,"ok":true,"stats":{"haps":4}}`)
	wsReadNextEvent(t, conn, EventEvalResult)

	before := s.Conductor.Snapshot()

	verdict := dryRunAndAnswer(t, ts, conn, `s("bd"`, false, "Unexpected token '}'", 0)

	if verdict.OK {
		t.Fatal("dry-run reported ok for a deliberately broken candidate")
	}
	if !strings.Contains(verdict.Error, "Unexpected token") {
		t.Errorf("dry-run error = %q, want the browser's own message verbatim", verdict.Error)
	}

	after := s.Conductor.Snapshot()
	if after.Version != before.Version {
		t.Errorf("version moved %d -> %d; a dry-run must publish nothing", before.Version, after.Version)
	}
	if len(after.History) != len(before.History) {
		t.Errorf("history grew %d -> %d; a dry-run is not a revision", len(before.History), len(after.History))
	}
	if after.Code != before.Code {
		t.Errorf("the live document changed to %q; a dry-run must not touch what is playing", after.Code)
	}
	if after.LastEvalResult == nil || after.LastEvalResult.Version != 1 || !after.LastEvalResult.OK {
		t.Errorf("the stored verdict for the PUBLISHED version was disturbed by a dry-run: %+v", after.LastEvalResult)
	}
}

// TestDryRunTimeoutIsBounded covers the other half of "no hang": a listener IS
// connected but never answers, which is what a wedged or backgrounded tab looks
// like. The request must return a named timeout, and the registry must not keep
// the entry afterwards.
func TestDryRunTimeoutIsBounded(t *testing.T) {
	s := dryRunServer(t)
	ts, _ := wsListener(t, s)

	code, body := dryRunPost(t, ts, "/api/dry-run", `{"code":"s(\"bd\")"}`)
	if code != http.StatusGatewayTimeout {
		t.Fatalf("an unanswered dry-run = %d %q, want 504", code, body)
	}
	if !strings.Contains(body, "no listener answered") {
		t.Errorf("error %q does not say the dry-run went unanswered", body)
	}

	// A timed-out entry must not linger: it would make a later id resolve onto a
	// dead request and leak one goroutine per abandoned dry-run.
	if n := s.dryRuns.Len(); n != 0 {
		t.Errorf("the registry holds %d entries after a timeout; it must be emptied on every exit path", n)
	}
}

// TestDryRunVerdictDoesNotOverwriteTheStoredOne is the corruption case named in
// the bead, asserted directly: after a dry-run, the stored verdict must still
// be the browser's verdict about the PUBLISHED code, for the published version.
func TestDryRunVerdictDoesNotOverwriteTheStoredOne(t *testing.T) {
	s := dryRunServer(t)
	ts, conn := wsListener(t, s)

	postJSONTo(t, s, "/api/code", `{"code":"s(\"bd*2\")"}`)
	wsReadNextEvent(t, conn, EventCode)
	postJSONTo(t, s, "/api/eval-result", `{"version":1,"ok":true,"stats":{"haps":4}}`)
	wsReadNextEvent(t, conn, EventEvalResult)

	// A dry-run that SUCCEEDS, which is the more dangerous direction: a stored
	// ok:true from code that is not live would tell the agent its last change
	// worked when what worked was a candidate it never pushed.
	dryRunAndAnswer(t, ts, conn, `s("cp")`, true, "", 7)

	stored := s.Conductor.Snapshot().LastEvalResult
	if stored == nil {
		t.Fatal("the stored verdict disappeared")
	}
	if stored.Version != 1 {
		t.Errorf("stored verdict version = %d, want 1 (the published version)", stored.Version)
	}
	var stats map[string]any
	if err := json.Unmarshal(stored.Stats, &stats); err != nil {
		t.Fatalf("stored stats are not an object: %v", err)
	}
	if stats["haps"] != float64(4) {
		t.Errorf("stored stats = %v, want the published version's haps=4; a dry-run verdict overwrote them", stats)
	}
}

// TestDryRunsDoNotCrossVerdicts guards the correlation id. Two concurrent
// dry-runs each get their own id, and answering one must never satisfy the
// other -- otherwise an agent is told its candidate passed on the strength of a
// verdict about a different document.
func TestDryRunsDoNotCrossVerdicts(t *testing.T) {
	s := dryRunServer(t)
	ts, conn := wsListener(t, s)

	// Both requests are issued BEFORE either frame is read: they are concurrent
	// by construction, and reading a frame before the second request exists would
	// make this a sequential test wearing a concurrent test's name.
	type pending struct {
		code int
		body string
	}
	results := make(chan pending, 2)
	issue := func(code string) {
		go func() {
			c, b, err := dryRunPostQuiet(ts, "/api/dry-run", dryRunBody(code))
			if err != nil {
				results <- pending{0, err.Error()}
				return
			}
			results <- pending{c, b}
		}()
	}
	issue(`s("bd")`)
	issue(`s("cp")`)

	first := readDryRunRequest(t, conn)
	second := readDryRunRequest(t, conn)
	if first.ID == second.ID {
		t.Fatalf("two concurrent dry-runs share id %d; verdicts could cross", first.ID)
	}
	// Which candidate arrives first is NOT deterministic -- two goroutines race
	// into Register -- so the assertion is that BOTH arrived, not that a
	// particular one came first. Ordering is not part of the contract.
	if !((strings.Contains(first.Code, "bd") && strings.Contains(second.Code, "cp")) ||
		(strings.Contains(first.Code, "cp") && strings.Contains(second.Code, "bd"))) {
		t.Errorf("the frames carried the wrong candidates: %q and %q", first.Code, second.Code)
	}

	// Answer exactly ONE of them. The other must time out rather than receive a
	// verdict about a different document -- that is the whole claim here.
	dryRunPost(t, ts, "/api/dry-run-result",
		fmt.Sprintf(`{"dryRunId":%d,"ok":true,"stats":{"haps":3}}`, second.ID))

	var okCount, timeoutCount int
	for i := 0; i < 2; i++ {
		switch r := <-results; r.code {
		case http.StatusOK:
			okCount++
		case http.StatusGatewayTimeout:
			timeoutCount++
		default:
			t.Fatalf("unexpected dry-run outcome: %d %q", r.code, r.body)
		}
	}
	if okCount != 1 || timeoutCount != 1 {
		t.Errorf("one dry-run answered and one unanswered is what this proves; got %d answered and %d timed out", okCount, timeoutCount)
	}
}

// TestDryRunRejectsTheSameMalformedBodiesAsCode keeps dry-run on the same
// validate-first footing as every other endpoint: a blank candidate, an unknown
// field and an oversized body are all refused, and none of them reaches a
// browser.
func TestDryRunRejectsTheSameMalformedBodiesAsCode(t *testing.T) {
	s := dryRunServer(t)
	ts, _ := wsListener(t, s)

	for _, tc := range []struct {
		what   string
		body   string
		want   int
		needle string
	}{
		{"blank code", `{"code":"   "}`, http.StatusBadRequest, "code must not be empty"},
		{"unknown field", `{"code":"s(1)","dryrun":true}`, http.StatusBadRequest, "unknown field"},
		{"trailing JSON", `{"code":"s(1)"}{"code":"s(2)"}`, http.StatusBadRequest, "unexpected data after JSON body"},
		{"oversized", `{"code":"` + strings.Repeat("x", APIMaxBodyBytes+16) + `"}`, http.StatusRequestEntityTooLarge, "limit is"},
	} {
		code, body := dryRunPost(t, ts, "/api/dry-run", tc.body)
		if code != tc.want {
			t.Errorf("%s: %d %q, want %d", tc.what, code, body, tc.want)
			continue
		}
		if !strings.Contains(body, tc.needle) {
			t.Errorf("%s: error %q does not contain %q", tc.what, body, tc.needle)
		}
	}

	// A refused request must not have asked anybody to evaluate anything.
	if s.dryRuns.Len() != 0 {
		t.Errorf("%d dry-run(s) registered despite every body being refused", s.dryRuns.Len())
	}
}

// TestDryRunWrongMethodIsRefused keeps the route's verb fallback in place.
func TestDryRunWrongMethodIsRefused(t *testing.T) {
	s := New()
	req := httptest.NewRequest(http.MethodGet, "/api/dry-run", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/dry-run = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Errorf("Allow = %q, want %q", got, http.MethodPost)
	}
}

// TestDryRunRegistryIsEmptiedOnEveryExitPath is the leak assertion. Every exit
// from the waiting handler -- answered, timed out, client gone -- must remove
// its entry, or the registry grows one goroutine and one map key per abandoned
// dry-run.
func TestDryRunRegistryIsEmptiedOnEveryExitPath(t *testing.T) {
	t.Run("answered", func(t *testing.T) {
		s := dryRunServer(t)
		ts, conn := wsListener(t, s)
		issued := make(chan struct{})
		go func() {
			_, _, _ = dryRunPostQuiet(ts, "/api/dry-run", `{"code":"s(1)"}`)
			close(issued)
		}()
		req := readDryRunRequest(t, conn)
		dryRunPost(t, ts, "/api/dry-run-result", fmt.Sprintf(`{"dryRunId":%d,"ok":true}`, req.ID))
		<-issued
		waitFor(t, 2*time.Second, func() bool { return s.dryRuns.Len() == 0 },
			"an answered dry-run stayed in the registry")
	})

	t.Run("client gave up", func(t *testing.T) {
		s := dryRunServer(t)
		ts, _ := wsListener(t, s)

		// A request that is abandoned mid-wait, which is what an agent whose own
		// -timeout fires looks like from here.
		ctx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/dry-run", strings.NewReader(`{"code":"s(1)"}`))
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		if resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req); err == nil {
			resp.Body.Close()
		}

		waitFor(t, 2*time.Second, func() bool { return s.dryRuns.Len() == 0 },
			"an abandoned dry-run stayed in the registry")
	})
}

// ---------- dry-run test helpers ----------

// dryRunPost sends one POST to a real httptest server over the loopback socket
// and returns the status and body.
//
// It goes over HTTP rather than through postJSONTo's direct ServeHTTP call
// because dry-run is a BLOCKING round trip: the request must be genuinely in
// flight on a socket while the listener is asked to evaluate, which is the only
// way the ordering under test is the ordering in production.
func dryRunPost(t *testing.T, ts *httptest.Server, path, body string) (int, string) {
	t.Helper()
	code, raw, err := dryRunPostQuiet(ts, path, body)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return code, raw
}

// dryRunPostQuiet is dryRunPost without t.Fatalf, for the goroutines that a
// blocking dry-run requires.
//
// t.Fatalf calls FailNow, which must run on the test's OWN goroutine: from
// anywhere else it does not report a failure, it aborts the run. A dry-run
// request has to be in flight while the listener answers it, so the goroutine
// that issues it cannot be the one that reports on it — hence the error return.
func dryRunPostQuiet(ts *httptest.Server, path, body string) (int, string, error) {
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, "", err
	}
	// The RAW bytes, deliberately not bodyOf: these tests decode the payload, and
	// bodyOf returns a JSON-QUOTED rendering for error messages, which is not
	// valid input to json.Unmarshal.
	return resp.StatusCode, string(raw), nil
}

// dryRunAndAnswer performs one dry-run end to end: it starts the request,
// answers as the connected browser would once it has been asked, and returns
// the verdict the agent received.
//
// The answer is driven from the LISTENER SOCKET rather than from a second
// goroutine guessing, because the browser is a real participant in this
// protocol: reading the frame it was actually sent is what proves the candidate
// and the correlation id reached it.
func dryRunAndAnswer(t *testing.T, ts *httptest.Server, conn *websocket.Conn, code string, ok bool, evalErr string, haps int) DryRunVerdict {
	t.Helper()

	type result struct {
		rec *httptest.ResponseRecorder
	}
	type answer struct {
		code int
		body string
	}
	done := make(chan answer, 1)
	go func() {
		c, b, err := dryRunPostQuiet(ts, "/api/dry-run", dryRunBody(code))
		if err != nil {
			done <- answer{0, err.Error()}
			return
		}
		done <- answer{c, b}
	}()

	req := readDryRunRequest(t, conn)
	if req.Code != code {
		t.Errorf("the browser was asked to evaluate %q, want %q", req.Code, code)
	}
	dryRunPost(t, ts, "/api/dry-run-result", dryRunResultBody(req.ID, ok, evalErr, haps))

	res := <-done
	if res.code != http.StatusOK {
		t.Fatalf("POST /api/dry-run = %d %q, want 200", res.code, res.body)
	}
	var v DryRunVerdict
	if err := json.Unmarshal([]byte(res.body), &v); err != nil {
		t.Fatalf("dry-run verdict is not JSON: %v (%q)", err, res.body)
	}
	return v
}

// dryRunBody encodes the agent's request body for a candidate.
func dryRunBody(code string) string {
	b, _ := json.Marshal(map[string]string{"code": code})
	return string(b)
}

// dryRunResultBody encodes the browser's verdict for one dry-run.
func dryRunResultBody(id int64, ok bool, evalErr string, haps int) string {
	stats, _ := json.Marshal(map[string]int{"haps": haps})
	m := map[string]any{"dryRunId": id, "ok": ok, "stats": json.RawMessage(stats)}
	if !ok && evalErr != "" {
		m["error"] = evalErr
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// readDryRunRequest reads the dry-run frame a listener receives and returns it.
func readDryRunRequest(t *testing.T, conn *websocket.Conn) DryRunRequest {
	t.Helper()
	ev := wsReadNextEvent(t, conn, EventDryRun)
	if ev.DryRun == nil {
		t.Fatal("the dry-run frame carried no dryRun member")
	}
	return *ev.DryRun
}

// why.
func TestDryRunResultForAnUnknownIDIsRefused(t *testing.T) {
	s := dryRunServer(t)
	ts, _ := wsListener(t, s)

	code, body := dryRunPost(t, ts, "/api/dry-run-result", `{"dryRunId":9999,"ok":true}`)
	if code != http.StatusNotFound {
		t.Fatalf("a verdict for an unknown dry-run = %d %q, want 404", code, body)
	}
	if !strings.Contains(body, "unknown dry-run") {
		t.Errorf("error %q does not name the unknown dry-run", body)
	}
}

// TestDryRunResultIsIgnoredOnceAnswered covers the second report for an id that
// already resolved: every listener evaluates and every listener reports, so the
// losers must be refused rather than overwrite the winner's answer.
func TestDryRunResultIsIgnoredOnceAnswered(t *testing.T) {
	s := dryRunServer(t)
	ts, conn := wsListener(t, s)

	go func() {
		_, _, _ = dryRunPostQuiet(ts, "/api/dry-run", `{"code":"s(1)"}`)
	}()
	req := readDryRunRequest(t, conn)
	dryRunPost(t, ts, "/api/dry-run-result", fmt.Sprintf(`{"dryRunId":%d,"ok":true,"stats":{"haps":2}}`, req.ID))
	// A second listener answering the same id.
	code, body := dryRunPost(t, ts, "/api/dry-run-result", fmt.Sprintf(`{"dryRunId":%d,"ok":false,"error":"late"}`, req.ID))
	if code != http.StatusNotFound && code != http.StatusConflict {
		t.Fatalf("a duplicate verdict = %d %q, want it refused", code, body)
	}
}
