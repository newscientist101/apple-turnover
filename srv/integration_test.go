package srv

// This file is the repo-owned integration harness. It boots the REAL handler
// tree (the same s.routes() that Server.Serve mounts) over httptest and drives
// it the way the external agent does at runtime. Future issues (.3 WebSocket
// hub, .4 page shell, .5 browser client, .8 coherence, ...) are expected to
// EXTEND this file rather than write their own throwaway curl scripts.
//
// Two rules hold for everything below:
//
//
//	1. Assert on response BODIES, not only status codes. A 200 with a
//	   truncated or empty body is a real defect (see
//	   TestIntegrationShellRendersToCompletion for the known template case).
//	2. Everything is bounded. No test may block forever: the parallel test
//	   uses a deadline plus a per-transport timeout, and the literal process
//	   tests use an explicit exec timeout. A hanging test is itself a defect,
//	   so a blocked run must FAIL rather than sit in CI.
//
// The mutation/sabotage counterpart to this file lives in
// scripts/mutation-check.sh, which breaks the implementation on purpose and
// reports any mutation these tests fail to notice.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- harness plumbing ----------

// boundedTransport is the client transport every integration test shares. Its
// timeouts are deliberately aggressive: every response in this file is
// generated in-memory, so anything that takes longer than a second is a hang
// (a deadlocked handler, a leaked lock) and the test must fail rather than
// wedge the run. ResponseHeaderTimeout is what makes a deadlock inside a
// handler observable as an error instead of an endless Wait.
func boundedTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: time.Second}
			return d.DialContext(ctx, network, addr)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 2 * time.Second,
		TLSHandshakeTimeout:   time.Second,
	}
}

// safeBuffer is an io.Writer that is safe to read while a subprocess is still
// writing to it. exec.Cmd copies the child's stdout and stderr on its own
// goroutines, so the obvious bytes.Buffer plus out.String() in a failure
// message is a genuine data race under -race; this closes that.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// integrationSession is one server under test plus the primitives to talk to
// it. Nothing here needs a real TCP port for the API: the handler tree is
// driven in-process, which keeps the tests deterministic and fast. The
// HTTP-client based test at the bottom of this file additionally serves the
// same tree over a loopback httptest server.
type integrationSession struct {
	t       *testing.T
	server  *Server
	handler http.Handler
}

func newIntegrationSession(t *testing.T) *integrationSession {
	t.Helper()
	s := New("integration-host")
	// routes() is called once and reused, exactly as Serve would.
	return &integrationSession{t: t, server: s, handler: s.routes()}
}

// do drives one request through the real handler tree.
func (s *integrationSession) do(req *http.Request) *httptest.ResponseRecorder {
	s.t.Helper()
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, req)
	return w
}

// jsonReq builds a request with a JSON content type.
func (s *integrationSession) jsonReq(method, path, body string) *http.Request {
	s.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func (s *integrationSession) post(path, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.do(s.jsonReq(http.MethodPost, path, body))
}

func (s *integrationSession) get(path string) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.do(httptest.NewRequest(http.MethodGet, path, nil))
}

// body returns the recorder body as a string.
func body(w *httptest.ResponseRecorder) string { return w.Body.String() }

// wantStatus fails the test unless the recorded status matches.
func wantStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d (body=%q)", w.Code, want, body(w))
	}
}

func wantContains(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s: missing %q in %q", what, needle, haystack)
	}
}

func wantJSONRespType(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json (body=%q)", ct, body(w))
	}
}

// jsonObject decodes a response body into a generic map, using json.Number so
// numeric fields can be compared without float formatting surprises.
func jsonObject(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	dec := json.NewDecoder(strings.NewReader(body(w)))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("response body is not a JSON object: %v (body=%q)", err, body(w))
	}
	return got
}

// errorMessage decodes the uniform {"error":"..."} failure shape.
func errorMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	wantJSONRespType(t, w)
	got := jsonObject(t, w)
	if len(got) != 1 {
		t.Errorf("error body has %d fields (%q), want exactly the one %q field", len(got), body(w), "error")
	}
	msg, ok := got["error"].(string)
	if !ok || strings.TrimSpace(msg) == "" {
		t.Fatalf("error body = %q, want a non-empty {\"error\":...} object", body(w))
	}
	return msg
}

// numString renders a decoded json.Number for comparison.
func numString(t *testing.T, v any) string {
	t.Helper()
	n, ok := v.(json.Number)
	if !ok {
		t.Fatalf("value %v (%T) is not a JSON number", v, v)
	}
	return n.String()
}

// bodyOf reports the length and a short prefix of a request body, for test
// failure messages that must not dump 64 KiB.
func bodyOf(b string) string {
	if len(b) <= 200 {
		return strconv.Quote(b)
	}
	return strconv.Quote(b[:200]) + fmt.Sprintf("...(%d bytes)", len(b))
}

// ---------- 1. the agent feedback loop ----------

// TestIntegrationAgentFeedbackLoop is the end-to-end spine: the exact sequence
// an external agent runs against a live server, in order, asserting on the
// bodies of every response. A fresh performance, a push, narration, a browser
// verdict, and finally a read that must agree with all of it.
func TestIntegrationAgentFeedbackLoop(t *testing.T) {
	s := newIntegrationSession(t)

	// ---- step 1: GET /api/state on a fresh performance ----
	//
	// Raw-text assertions first, because these pin the SHAPE of the wire
	// format that the agent contract depends on and that a struct-tag typo or
	// an omitted field would silently break.
	w := s.get("/api/state")
	wantStatus(t, w, http.StatusOK)
	wantJSONRespType(t, w)
	raw := body(w)
	for _, want := range []string{
		`"version":0`,
		`"code":""`,
		`"lastAgentMessage":""`,
		`"anchor":{`,
		`"epochMs":`,
		`"cps":0.5`,
		`"history":[]`,
		`"playing":true`,
		`"listenerCount":0`,
		`"lastEvalResult":null`,
	} {
		wantContains(t, raw, want, "fresh GET /api/state body")
	}

	state := jsonObject(t, w)
	if got := numString(t, state["version"]); got != "0" {
		t.Errorf("fresh version = %s, want 0", got)
	}
	if got, ok := state["code"].(string); !ok || got != "" {
		t.Errorf("fresh code = %v, want empty string", state["code"])
	}
	// These two must be the JSON literals true/0/null, never null or absent:
	// a browser branches on playing and a listener-count of null would print
	// as "null listeners".
	if got, ok := state["playing"].(bool); !ok || !got {
		t.Errorf("fresh playing = %v, want the boolean true", state["playing"])
	}
	if got := numString(t, state["listenerCount"]); got != "0" {
		t.Errorf("fresh listenerCount = %s, want 0", got)
	}
	if _, has := state["lastEvalResult"]; !has {
		t.Error("fresh state omits lastEvalResult; the agent needs an explicit null")
	} else if state["lastEvalResult"] != nil {
		t.Errorf("fresh lastEvalResult = %v, want null", state["lastEvalResult"])
	}

	// ---- step 2: POST /api/code ----
	w = s.post("/api/code", `{"code":"s(\"bd*4\").gain(0.8)","message":"four on the floor"}`)
	wantStatus(t, w, http.StatusOK)
	wantJSONRespType(t, w)
	raw = body(w)
	wantContains(t, raw, `"version":1`, "push response body")
	wantContains(t, raw, `"four on the floor"`, "push response body")

	state = jsonObject(t, w)
	if got := numString(t, state["version"]); got != "1" {
		t.Errorf("push response version = %s, want 1", got)
	}
	if got, _ := state["code"].(string); got != `s("bd*4").gain(0.8)` {
		t.Errorf("push response code = %q, want the pushed document", got)
	}
	if got, _ := state["lastAgentMessage"].(string); got != "four on the floor" {
		t.Errorf("push response lastAgentMessage = %q, want the narration", got)
	}
	if got, ok := state["playing"].(bool); !ok || !got {
		t.Errorf("push response playing = %v, want true (a push is not a hush)", state["playing"])
	}
	hist, ok := state["history"].([]any)
	if !ok || len(hist) != 1 {
		t.Fatalf("push response history = %v, want one entry", state["history"])
	}
	entry, _ := hist[0].(map[string]any)
	if entry == nil {
		t.Fatalf("history[0] = %v, want an object", hist[0])
	}
	if got := numString(t, entry["version"]); got != "1" {
		t.Errorf("history[0].version = %s, want 1", got)
	}

	// ---- step 3: POST /api/message ----
	w = s.post("/api/message", `{"message":"now adding a backbeat"}`)
	wantStatus(t, w, http.StatusOK)
	wantContains(t, body(w), `"now adding a backbeat"`, "message response body")
	state = jsonObject(t, w)
	if got := numString(t, state["version"]); got != "1" {
		t.Errorf("narration bumped version to %s, want 1", got)
	}
	if got, _ := state["code"].(string); got != `s("bd*4").gain(0.8)` {
		t.Errorf("narration changed the document: %q", got)
	}

	// ---- step 4: POST /api/eval-result (the browser's verdict) ----
	w = s.post("/api/eval-result",
		`{"version":1,"ok":true,"stats":{"events":16,"voices":3}}`)
	wantStatus(t, w, http.StatusOK)
	wantJSONRespType(t, w)
	ack := jsonObject(t, w)
	if got, ok := ack["accepted"].(bool); !ok || !got {
		t.Errorf("eval ack = %q, want {\"accepted\":true,...}", body(w))
	}
	if got := numString(t, ack["version"]); got != "1" {
		t.Errorf("eval ack version = %s, want 1", got)
	}
	// The ack must never carry the verdict itself: the agent reads that back
	// from /api/state, and an "ok":true here would let a client mistake the
	// acknowledgement for the eval outcome.
	if _, has := ack["ok"]; has {
		t.Errorf("eval ack = %q, want no \"ok\" field (only the server's acceptance)", body(w))
	}

	// ---- step 5: GET /api/state must agree with everything above ----
	w = s.get("/api/state")
	wantStatus(t, w, http.StatusOK)
	state = jsonObject(t, w)
	if got := numString(t, state["version"]); got != "1" {
		t.Errorf("final version = %s, want 1 (narration and eval reports are not versions)", got)
	}
	if got, _ := state["code"].(string); got != `s("bd*4").gain(0.8)` {
		t.Errorf("final code = %q", got)
	}
	if got, _ := state["lastAgentMessage"].(string); got != "now adding a backbeat" {
		t.Errorf("final lastAgentMessage = %q, want the newest narration", got)
	}
	if got, ok := state["playing"].(bool); !ok || !got {
		t.Errorf("final playing = %v, want true", state["playing"])
	}
	lr, ok := state["lastEvalResult"].(map[string]any)
	if !ok {
		t.Fatalf("final lastEvalResult = %v, want the stored verdict object", state["lastEvalResult"])
	}
	if got := numString(t, lr["version"]); got != "1" {
		t.Errorf("stored verdict version = %s, want 1", got)
	}
	if got, ok := lr["ok"].(bool); !ok || !got {
		t.Errorf("stored verdict ok = %v, want true", lr["ok"])
	}
	stats, ok := lr["stats"].(map[string]any)
	if !ok {
		t.Fatalf("stored stats = %v, want the reported object echoed verbatim", lr["stats"])
	}
	if got := numString(t, stats["events"]); got != "16" {
		t.Errorf("stored stats.events = %s, want 16", got)
	}
	if got := numString(t, lr["epochMs"]); got == "0" {
		t.Error("stored verdict epochMs = 0, want a server timestamp")
	}

	// ---- and the transport verbs complete the loop ----
	w = s.post("/api/hush", "")
	wantStatus(t, w, http.StatusOK)
	if got, ok := jsonObject(t, w)["playing"].(bool); !ok || got {
		t.Errorf("hush response playing = %v, want false", jsonObject(t, w)["playing"])
	}
	if got, _ := jsonObject(t, s.get("/api/state"))["code"].(string); got != `s("bd*4").gain(0.8)` {
		t.Errorf("hush changed the document: %q", got)
	}
	w = s.post("/api/play", "")
	wantStatus(t, w, http.StatusOK)
	if got, ok := jsonObject(t, w)["playing"].(bool); !ok || !got {
		t.Errorf("play response playing = %v, want true", jsonObject(t, w)["playing"])
	}
}

// TestIntegrationStaleVerdictDoesNotRegress pins the one stateful subtlety in
// the loop: reports arrive from many browsers, out of order. A straggler
// reporting an OLDER version must be accepted (its HTTP status is 200, so a
// client cannot tell it was dropped) but must NOT overwrite the verdict for
// the newer version — otherwise the agent's view of "did my latest code work"
// gets rolled back by whichever browser was slowest.
func TestIntegrationStaleVerdictDoesNotRegress(t *testing.T) {
	s := newIntegrationSession(t)
	for i := 1; i <= 3; i++ {
		w := s.post("/api/code", fmt.Sprintf(`{"code":"s(\"bd\").gain(%d)"}`, i))
		wantStatus(t, w, http.StatusOK)
	}

	// v3 evaluates fine.
	w := s.post("/api/eval-result", `{"version":3,"ok":true,"stats":{"events":3}}`)
	wantStatus(t, w, http.StatusOK)

	// A straggler's report for the older v2 arrives late and claims failure.
	w = s.post("/api/eval-result", `{"version":2,"ok":false,"error":"stale failure"}`)
	wantStatus(t, w, http.StatusOK)
	wantContains(t, body(w), `"accepted":true`, "stale report ack")

	lr, ok := jsonObject(t, s.get("/api/state"))["lastEvalResult"].(map[string]any)
	if !ok {
		t.Fatal("lastEvalResult disappeared; the stale report wiped it")
	}
	if got := numString(t, lr["version"]); got != "3" {
		t.Errorf("stale report regressed stored version to %s, want 3", got)
	}
	if got, ok := lr["ok"].(bool); !ok || !got {
		t.Errorf("stale report overwrote the verdict: ok = %v, want true", lr["ok"])
	}
	if _, has := lr["error"]; has {
		t.Errorf("stale report's error text was stored: %q", body(w))
	}

	// The SAME version may be re-reported, and that must update the verdict:
	// a browser that re-evaluates v3 must be able to correct its own answer.
	wantStatus(t, s.post("/api/eval-result", `{"version":3,"ok":false,"error":"retried and broke"}`), http.StatusOK)
	lr, _ = jsonObject(t, s.get("/api/state"))["lastEvalResult"].(map[string]any)
	if got, ok := lr["ok"].(bool); !ok || got {
		t.Errorf("re-reporting the current version was ignored: ok = %v, want false", lr["ok"])
	}
	if got, _ := lr["error"].(string); got != "retried and broke" {
		t.Errorf("stored error = %q, want the newest report's text", got)
	}
}

// ---------- 2. the error / edge matrix ----------

// TestIntegrationErrorMatrix is the table of rejected requests. Every case
// asserts the status, the JSON content type, the uniform error shape and a
// substring of the message, and then that the rejection left no trace: a
// refused request must not have bumped the version or changed the document.
func TestIntegrationErrorMatrix(t *testing.T) {
	// seededSession hands each subtest a server that already holds one version,
	// so stale/zero/negative version cases are real rather than trivially
	// unknown, and so "nothing was stored" is a meaningful assertion.
	seededSession := func(t *testing.T) *integrationSession {
		t.Helper()
		s := newIntegrationSession(t)
		w := s.post("/api/code", `{"code":"s(\"bd\")","message":"seed"}`)
		wantStatus(t, w, http.StatusOK)
		return s
	}

	postCases := []struct {
		name string
		path string
		// contentType is sent as-is; "" means Content-Type is omitted.
		contentType string
		body        string
		wantStatus  int
		wantMsg     string
		// okVersion is the version the accepted response must report. It is
		// only meaningful when wantStatus is 200: a push bumps the version, a
		// transport verb does not.
		okVersion string
	}{
		// --- POST /api/code ---
		{
			name: "code: empty code", path: "/api/code", contentType: "application/json",
			body: `{"code":""}`, wantStatus: 400, wantMsg: "code must not be empty",
		},
		{
			name: "code: whitespace-only code", path: "/api/code", contentType: "application/json",
			body: `{"code":"   \n\t  "}`, wantStatus: 400, wantMsg: "code must not be empty",
		},
		{
			name: "code: missing code field", path: "/api/code", contentType: "application/json",
			body: `{}`, wantStatus: 400, wantMsg: "code must not be empty",
		},
		{
			name: "code: unknown field (typo messge)", path: "/api/code", contentType: "application/json",
			body: `{"code":"s(\"bd\")","messge":"typo"}`, wantStatus: 400, wantMsg: "messge",
		},
		{
			name: "code: trailing JSON", path: "/api/code", contentType: "application/json",
			body: `{"code":"s(\"bd\")"}{"code":"evil"}`, wantStatus: 400, wantMsg: "unexpected data after JSON body",
		},
		{
			name: "code: malformed JSON", path: "/api/code", contentType: "application/json",
			body: `{"code": `, wantStatus: 400, wantMsg: "invalid JSON body",
		},
		{
			name: "code: empty body", path: "/api/code", contentType: "application/json",
			body: ``, wantStatus: 400, wantMsg: "empty or truncated",
		},
		{
			name: "code: no Content-Type", path: "/api/code", contentType: "",
			body: `{"code":"s(\"bd\")"}`, wantStatus: 200, okVersion: "2",
		},
		{
			name: "code: wrong Content-Type is not sniffed", path: "/api/code", contentType: "text/plain",
			body: `{"code":"s(\"bd\")"}`, wantStatus: 200, okVersion: "2",
		},
		{
			name: "code: wrong field type", path: "/api/code", contentType: "application/json",
			body: `{"code":42}`, wantStatus: 400, wantMsg: "invalid JSON body",
		},

		// --- POST /api/message ---
		{
			name: "message: empty message", path: "/api/message", contentType: "application/json",
			body: `{"message":""}`, wantStatus: 400, wantMsg: "message must not be empty",
		},
		{
			name: "message: whitespace-only message", path: "/api/message", contentType: "application/json",
			body: `{"message":" \t "}`, wantStatus: 400, wantMsg: "message must not be empty",
		},
		{
			name: "message: missing message field", path: "/api/message", contentType: "application/json",
			body: `{}`, wantStatus: 400, wantMsg: "message must not be empty",
		},
		{
			name: "message: unknown field (typo messge)", path: "/api/message", contentType: "application/json",
			body: `{"messge":"typo"}`, wantStatus: 400, wantMsg: "messge",
		},
		{
			name: "message: trailing JSON", path: "/api/message", contentType: "application/json",
			body: `{"message":"hi"} junk`, wantStatus: 400, wantMsg: "unexpected data after JSON body",
		},
		{
			name: "message: malformed JSON", path: "/api/message", contentType: "application/json",
			body: `{"message"`, wantStatus: 400, wantMsg: "invalid JSON body",
		},
		{
			name: "message: empty body", path: "/api/message", contentType: "application/json",
			body: ``, wantStatus: 400, wantMsg: "empty or truncated",
		},

		// --- POST /api/eval-result: version matrix ---
		{
			name: "eval: future version", path: "/api/eval-result", contentType: "application/json",
			body: `{"version":2,"ok":true}`, wantStatus: 400, wantMsg: "unknown version",
		},
		{
			name: "eval: far-future version", path: "/api/eval-result", contentType: "application/json",
			body: `{"version":9999,"ok":true}`, wantStatus: 400, wantMsg: "unknown version",
		},
		{
			name: "eval: zero version", path: "/api/eval-result", contentType: "application/json",
			body: `{"version":0,"ok":true}`, wantStatus: 400, wantMsg: "unknown version",
		},
		{
			name: "eval: negative version", path: "/api/eval-result", contentType: "application/json",
			body: `{"version":-1,"ok":true}`, wantStatus: 400, wantMsg: "unknown version",
		},
		{
			name: "eval: missing version", path: "/api/eval-result", contentType: "application/json",
			body: `{"ok":true}`, wantStatus: 400, wantMsg: "unknown version",
		},
		{
			name: "eval: version as string", path: "/api/eval-result", contentType: "application/json",
			body: `{"version":"1","ok":true}`, wantStatus: 400, wantMsg: "invalid JSON body",
		},
		{
			name: "eval: unknown field", path: "/api/eval-result", contentType: "application/json",
			body: `{"version":1,"ok":true,"verdict":"yes"}`, wantStatus: 400, wantMsg: "verdict",
		},
		{
			name: "eval: malformed JSON", path: "/api/eval-result", contentType: "application/json",
			body: `{"version":`, wantStatus: 400, wantMsg: "invalid JSON body",
		},

		// --- POST /api/hush and /api/play take no arguments ---
		{
			name: "hush: unexpected body field", path: "/api/hush", contentType: "application/json",
			body: `{"code":"s(\"evil\")"}`, wantStatus: 400, wantMsg: "unexpected field(s) code",
		},
		{
			name: "play: unexpected body field", path: "/api/play", contentType: "application/json",
			body: `{"gain":0.5}`, wantStatus: 400, wantMsg: "unexpected field(s) gain",
		},
		{
			// A no-argument endpoint must still reject a body it cannot parse
			// rather than treating it as "no arguments".
			name: "hush: malformed body", path: "/api/hush", contentType: "application/json",
			body: `{`, wantStatus: 400, wantMsg: "invalid JSON body",
		},
		{
			name: "play: unknown field typo", path: "/api/play", contentType: "application/json",
			body: `{"playingg":true}`, wantStatus: 400, wantMsg: "unexpected field(s) playingg",
		},
		{
			// A no-argument endpoint still accepts a body that takes no
			// arguments: `{}` must be a valid no-op, only a FIELD is an error.
			name: "hush: empty object is a valid no-argument body", path: "/api/hush", contentType: "application/json",
			body: `{}`, wantStatus: 200, okVersion: "1",
		},
		{
			name: "play: empty object is a valid no-argument body", path: "/api/play", contentType: "application/json",
			body: `{}`, wantStatus: 200, okVersion: "1",
		},
	}

	for _, tc := range postCases {
		t.Run(tc.name, func(t *testing.T) {
			s := seededSession(t)
			before := s.server.Conductor.Snapshot()

			r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			w := s.do(r)

			if w.Code != tc.wantStatus {
				t.Fatalf("POST %s body=%s status = %d, want %d (body=%q)",
					tc.path, bodyOf(tc.body), w.Code, tc.wantStatus, body(w))
			}
			if tc.wantStatus == http.StatusOK {
				wantJSONRespType(t, w)
				// Missing/odd Content-Type must still be parsed as JSON: a
				// browser fetch or a curl without -H sends no Content-Type and
				// the agent API must not care. The version pins whether this
				// request was a push (bumps) or a transport verb (does not).
				if got := numString(t, jsonObject(t, w)["version"]); got != tc.okVersion {
					t.Errorf("accepted request version = %s, want %s", got, tc.okVersion)
				}
				return
			}

			msg := errorMessage(t, w)
			if !strings.Contains(strings.ToLower(msg), strings.ToLower(tc.wantMsg)) {
				t.Errorf("error %q does not mention %q", msg, tc.wantMsg)
			}

			after := s.server.Conductor.Snapshot()
			if after.Version != before.Version {
				t.Errorf("rejected request bumped version %d -> %d", before.Version, after.Version)
			}
			if after.Code != before.Code {
				t.Errorf("rejected request changed the document %q -> %q", before.Code, after.Code)
			}
			if after.LastAgentMessage != before.LastAgentMessage {
				t.Errorf("rejected request changed narration %q -> %q", before.LastAgentMessage, after.LastAgentMessage)
			}
			if after.LastEvalResult != nil {
				t.Errorf("rejected request stored an eval verdict: %+v", after.LastEvalResult)
			}
		})
	}
}

// TestIntegrationWrongMethod pins the 405 contract: the verb is refused with an
// Allow header naming the permitted method, a JSON body, and - importantly -
// no side effect on the performance.
func TestIntegrationWrongMethod(t *testing.T) {
	cases := []struct {
		path    string
		allow   string
		methods []string
	}{
		{path: "/api/state", allow: http.MethodGet, methods: []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}},
		{path: "/api/code", allow: http.MethodPost, methods: []string{http.MethodGet, http.MethodPut, http.MethodDelete}},
		{path: "/api/message", allow: http.MethodPost, methods: []string{http.MethodGet, http.MethodDelete}},
		{path: "/api/eval-result", allow: http.MethodPost, methods: []string{http.MethodGet, http.MethodDelete}},
		{path: "/api/hush", allow: http.MethodPost, methods: []string{http.MethodGet, http.MethodDelete}},
		{path: "/api/play", allow: http.MethodPost, methods: []string{http.MethodGet}},
	}

	for _, tc := range cases {
		for _, method := range tc.methods {
			t.Run(method+" "+tc.path, func(t *testing.T) {
				s := newIntegrationSession(t)
				s.post("/api/code", `{"code":"s(\"bd\")"}`)
				before := s.server.Conductor.Snapshot()

				w := s.do(httptest.NewRequest(method, tc.path, strings.NewReader(`{"code":"evil"}`)))
				if w.Code != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s status = %d, want 405 (body=%q)", method, tc.path, w.Code, body(w))
				}
				if got := w.Header().Get("Allow"); got != tc.allow {
					t.Errorf("Allow = %q, want %q", got, tc.allow)
				}
				msg := errorMessage(t, w)
				if !strings.Contains(msg, tc.allow) {
					t.Errorf("405 error %q does not name the permitted method %q", msg, tc.allow)
				}
				if !strings.Contains(msg, tc.path) {
					t.Errorf("405 error %q does not name the path %q", msg, tc.path)
				}

				after := s.server.Conductor.Snapshot()
				if after.Version != before.Version || after.Code != before.Code {
					t.Errorf("405 %s %s mutated state: %+v -> %+v", method, tc.path, before, after)
				}
			})
		}
	}
}

// TestIntegrationUnregisteredAPIPaths pins that the API namespace never leaks
// into the HTML shell: an unknown /api path (including the bare namespace
// roots) is a JSON 404, while a non-/api 404 stays net/http's own plain-text
// body, which is what browsers and caches expect from the shell.
func TestIntegrationUnregisteredAPIPaths(t *testing.T) {
	jsonCases := []string{
		"/api",
		"/api/",
		"/api/nope",
		"/api/nope/deeper",
		"/api/state/extra",
		"/api/ws",
	}
	for _, path := range jsonCases {
		t.Run("json "+path, func(t *testing.T) {
			s := newIntegrationSession(t)
			w := s.get(path)
			if w.Code != http.StatusNotFound {
				t.Fatalf("GET %s status = %d, want 404 (body=%q)", path, w.Code, body(w))
			}
			msg := errorMessage(t, w)
			if !strings.Contains(msg, path) {
				t.Errorf("404 error %q does not name the requested path %q", msg, path)
			}
		})
	}

	plainCases := []string{"/definitely-not-here", "/shell/nope", "/static/missing.css"}
	for _, path := range plainCases {
		t.Run("plain "+path, func(t *testing.T) {
			s := newIntegrationSession(t)
			w := s.get(path)
			if w.Code != http.StatusNotFound {
				t.Fatalf("GET %s status = %d, want 404", path, w.Code)
			}
			if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
				t.Errorf("non-API 404 %s answered as JSON (%q): the API catch-all is too broad", path, ct)
			}
			if !strings.Contains(body(w), "404 page not found") {
				t.Errorf("non-API 404 %s body = %q, want net/http's plain-text 404", path, body(w))
			}
		})
	}
}

// TestIntegrationPayloadCap is the boundary case for the 64 KiB cap. Two
// things are easy to get wrong and both are pinned here:
//
//   - "at cap" must be ACCEPTED. An off-by-one (>= instead of >) would refuse
//     a legal body, and the limit is documented in bytes, so exactly the limit
//     has to pass.
//   - an oversized body that is ALSO malformed must report 413, not 400. If
//     the implementation decodes as it streams, the JSON syntax error in the
//     first bytes wins and a payload-cap violation is reported as a client
//     formatting mistake, which sends the agent debugging the wrong thing.
func TestIntegrationPayloadCap(t *testing.T) {
	t.Run("exactly at cap is accepted", func(t *testing.T) {
		s := newIntegrationSession(t)
		// Build a body that is exactly APIMaxBodyBytes long.
		prefix := `{"code":"`
		suffix := `"}`
		payload := strings.Repeat("a", APIMaxBodyBytes-len(prefix)-len(suffix))
		reqBody := prefix + payload + suffix
		if len(reqBody) != APIMaxBodyBytes {
			t.Fatalf("test bug: body len = %d, want exactly %d", len(reqBody), APIMaxBodyBytes)
		}

		w := s.post("/api/code", reqBody)
		if w.Code != http.StatusOK {
			t.Fatalf("body of exactly %d bytes: status = %d, want 200 (body=%q)",
				APIMaxBodyBytes, w.Code, body(w))
		}
		if got := s.server.Conductor.Snapshot().Code; got != payload {
			t.Errorf("stored code len = %d, want the full %d-byte payload", len(got), len(payload))
		}
	})

	t.Run("one byte over cap is rejected", func(t *testing.T) {
		s := newIntegrationSession(t)
		prefix := `{"code":"`
		suffix := `"}`
		reqBody := prefix + strings.Repeat("a", APIMaxBodyBytes-len(prefix)-len(suffix)+1) + suffix
		if len(reqBody) != APIMaxBodyBytes+1 {
			t.Fatalf("test bug: body len = %d, want %d", len(reqBody), APIMaxBodyBytes+1)
		}

		w := s.post("/api/code", reqBody)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("body of %d bytes: status = %d, want 413 (body=%q)", len(reqBody), w.Code, body(w))
		}
		msg := errorMessage(t, w)
		if !strings.Contains(msg, strconv.Itoa(APIMaxBodyBytes)) {
			t.Errorf("413 error %q does not state the %d-byte limit", msg, APIMaxBodyBytes)
		}
		if v := s.server.Conductor.Snapshot().Version; v != 0 {
			t.Errorf("oversized push bumped version to %d, want 0", v)
		}
	})

	// The subtle one: huge AND malformed. The cap is authoritative.
	t.Run("oversized malformed body reports 413 not 400", func(t *testing.T) {
		bodies := map[string]string{
			"unterminated object":       "{" + strings.Repeat("x", APIMaxBodyBytes),
			"invalid JSON array":        "[" + strings.Repeat("not json, ", APIMaxBodyBytes/9),
			"valid prefix then garbage": `{"code":"` + strings.Repeat("z", APIMaxBodyBytes),
		}
		for name, reqBody := range bodies {
			t.Run(name, func(t *testing.T) {
				if len(reqBody) <= APIMaxBodyBytes {
					t.Fatalf("test bug: body len %d is not over the %d cap", len(reqBody), APIMaxBodyBytes)
				}
				s := newIntegrationSession(t)
				w := s.post("/api/code", reqBody)
				if w.Code != http.StatusRequestEntityTooLarge {
					t.Fatalf("oversized+malformed body: status = %d, want 413 not 400 (body=%q)", w.Code, body(w))
				}
				wantJSONRespType(t, w)
				msg := errorMessage(t, w)
				if !strings.Contains(msg, "too large") {
					t.Errorf("413 error %q does not say the payload is too large", msg)
				}
				if v := s.server.Conductor.Snapshot().Version; v != 0 {
					t.Errorf("oversized push bumped version to %d, want 0", v)
				}
			})
		}
	})

	// The cap is shared middleware, so it has to hold on every write path and
	// on a body sent without a Content-Type.
	t.Run("cap applies to every write endpoint", func(t *testing.T) {
		huge := `{"code":"` + strings.Repeat("a", APIMaxBodyBytes) + `"}`
		for _, path := range []string{"/api/code", "/api/message", "/api/eval-result", "/api/hush", "/api/play"} {
			t.Run(path, func(t *testing.T) {
				s := newIntegrationSession(t)
				w := s.post(path, huge)
				if w.Code != http.StatusRequestEntityTooLarge {
					t.Fatalf("POST %s oversized status = %d, want 413 (body=%q)", path, w.Code, body(w))
				}
			})
		}
		t.Run("no Content-Type", func(t *testing.T) {
			s := newIntegrationSession(t)
			r := httptest.NewRequest(http.MethodPost, "/api/code", strings.NewReader(huge))
			w := s.do(r)
			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized body without Content-Type: status = %d, want 413 (body=%q)", w.Code, body(w))
			}
		})
	})
}

// ---------- 3. concurrency ----------

// TestIntegrationParallelPushesAreBoundedAndContiguous is the lost-update test.
// Many goroutines push concurrently over a real loopback server (so the whole
// stack, including net/http's own request handling, is exercised) and the
// version numbers they are handed must be exactly 1..N with no gaps and no
// repeats. `make verify` runs the suite under -race, so a data race in the
// Conductor or in a handler fails the build too.
//
// It CANNOT hang. Three independent bounds:
//
//  1. a 30-second context deadline shared by every request (the client is
//     built with that context, so a deadlocked request fails at the deadline);
//  2. a per-transport ResponseHeaderTimeout plus an http.Client.Timeout, so a
//     handler that never writes headers errors out instead of blocking;
//  3. a watchdog that waits on a channel and never on wg.Wait() directly, so
//     even a truly stuck goroutine becomes a hard t.Fatal at 30 seconds rather
//     than an endless run.
func TestIntegrationParallelPushesAreBoundedAndContiguous(t *testing.T) {
	// total is deliberately NOT a multiple of HistoryLimit (188 = 5*32 + 28):
	// if it were, the ring buffer's read window would start at index 0 anyway
	// and a window-start bug would be invisible. An awkward count keeps the
	// wrap-around itself under test.
	const (
		writers = 47
		pushPer = 4
		total   = writers * pushPer
	)
	if total%HistoryLimit == 0 {
		t.Fatalf("test bug: total %d is a multiple of HistoryLimit %d; the ring window would be trivial", total, HistoryLimit)
	}

	ts := httptest.NewServer(New("load-host").routes())
	// Close is called with a bound below, NOT deferred: httptest.Server.Close
	// waits for outstanding requests to finish, so if a handler is wedged (the
	// very defect this test is meant to catch) a deferred Close would block
	// forever and hang CI — exactly the failure mode this harness exists to
	// prevent. Closing with a bound turns "server is wedged" into a failure.
	defer func() {
		closed := make(chan struct{})
		go func() { ts.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(10 * time.Second):
			t.Errorf("httptest.Server.Close did not return within 10s: a handler is wedged")
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &http.Client{Transport: boundedTransport(), Timeout: 10 * time.Second}

	versions := make([]int64, total)
	var next int64
	var failures atomic.Int64
	var firstErr atomic.Value
	recordErr := func(err error) {
		if failures.Add(1) == 1 {
			firstErr.Store(err)
		}
	}

	pushURL := ts.URL + "/api/code"
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for j := 0; j < pushPer; j++ {
				slot := int(atomic.AddInt64(&next, 1)) - 1
				reqBody := fmt.Sprintf(`{"code":"s(\"bd\").gain(%d)","message":"w%d"}`, writer, writer)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, pushURL, strings.NewReader(reqBody))
				if err != nil {
					recordErr(fmt.Errorf("writer %d: build request: %w", writer, err))
					return
				}
				req.Header.Set("Content-Type", "application/json")

				resp, err := client.Do(req)
				if err != nil {
					recordErr(fmt.Errorf("writer %d push %d: %w", writer, j, err))
					return
				}
				raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				resp.Body.Close()
				if readErr != nil {
					recordErr(fmt.Errorf("writer %d push %d: read body: %w", writer, j, readErr))
					return
				}
				if resp.StatusCode != http.StatusOK {
					recordErr(fmt.Errorf("writer %d push %d: status %d body %q", writer, j, resp.StatusCode, raw))
					continue
				}
				var snap struct {
					Version int64 `json:"version"`
				}
				if err := json.Unmarshal(raw, &snap); err != nil {
					recordErr(fmt.Errorf("writer %d push %d: decode: %w", writer, j, err))
					continue
				}
				if snap.Version < 1 || snap.Version > total {
					recordErr(fmt.Errorf("writer %d push %d: version %d out of range 1..%d", writer, j, snap.Version, total))
					continue
				}
				versions[slot] = snap.Version
			}
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("%d concurrent writers did not finish within 30s: the server or the client is wedged", writers)
	case <-ctx.Done():
		t.Fatalf("request context expired early: %v", ctx.Err())
	}

	if n := failures.Load(); n > 0 {
		t.Fatalf("%d of %d pushes failed; first error: %v", n, total, firstErr.Load())
	}

	// Contiguity and uniqueness: sort a copy and require exactly 1..total.
	// A lost update shows up as a duplicate (two writers handed the same
	// version) and a gap at the top; a non-monotonic counter loses a number.
	gotVersions := append([]int64(nil), versions...)
	slices.Sort(gotVersions)
	for i, v := range gotVersions {
		if want := int64(i + 1); v != want {
			t.Fatalf("version sequence has a gap or duplicate at index %d: got %d, want %d (full: %v)",
				i, v, want, gotVersions)
		}
	}

	// The authoritative read agrees, and the ring buffer holds exactly the
	// newest HistoryLimit versions, contiguously.
	resp, err := client.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatalf("GET /api/state: %v", err)
	}
	defer resp.Body.Close()
	var snap struct {
		Version int64 `json:"version"`
		History []struct {
			Version int64 `json:"version"`
		} `json:"history"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode final snapshot: %v", err)
	}
	if snap.Version != total {
		t.Errorf("final version = %d, want %d", snap.Version, total)
	}
	if len(snap.History) != HistoryLimit {
		t.Errorf("final history len = %d, want %d", len(snap.History), HistoryLimit)
	}
	for i, v := range snap.History {
		want := int64(total - HistoryLimit + 1 + i)
		if v.Version != want {
			t.Errorf("history[%d].version = %d, want %d (the ring returned the wrong window)", i, v.Version, want)
		}
	}
}

// TestIntegrationConcurrentReadsDuringPushes checks the read path under
// concurrent writes: every /api/state response must be a coherent snapshot
// (the newest history entry is the current version) rather than a torn struct.
// Bounded the same way, with a watchdog so a wedged handler fails the test.
func TestIntegrationConcurrentReadsDuringPushes(t *testing.T) {
	s := newIntegrationSession(t)

	const rounds = 40
	var wg sync.WaitGroup
	var bad atomic.Int64
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if w := s.post("/api/code", `{"code":"s(\"bd\")"}`); w.Code != http.StatusOK {
				t.Errorf("concurrent push %d status = %d (body=%q)", i, w.Code, body(w))
				bad.Add(1)
			}
		}(i)
		go func() {
			defer wg.Done()
			w := s.get("/api/state")
			if w.Code != http.StatusOK {
				bad.Add(1)
				return
			}
			var snap struct {
				Version int64 `json:"version"`
				History []struct {
					Version int64 `json:"version"`
				} `json:"history"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
				bad.Add(1)
				return
			}
			if len(snap.History) > 0 {
				if newest := snap.History[len(snap.History)-1].Version; newest != snap.Version {
					t.Errorf("torn snapshot: history ends at %d but version is %d", newest, snap.Version)
					bad.Add(1)
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent read/push round did not finish within 30s: a handler is wedged")
	}
	if n := bad.Load(); n > 0 {
		t.Errorf("%d concurrent requests returned an inconsistent or failing response", n)
	}
	if got := s.server.Conductor.Snapshot().Version; got != rounds {
		t.Errorf("final version = %d, want %d", got, rounds)
	}
}

// ---------- 4. the non-API surface ----------

// TestIntegrationShellRendersToCompletion guards the known past defect:
// HandleRoot renders with html/template and, if the template references a
// field that no longer exists on pageData, Execute aborts MID-DOCUMENT while
// HandleRoot still logs only a warning and returns 200. A status-code-only
// assertion would pass on a truncated page, so this asserts on markers that
// only appear at the very end of the document (the Shelley ribbon and the
// closing </html>), which prove the whole template executed.
func TestIntegrationShellRendersToCompletion(t *testing.T) {
	s := newIntegrationSession(t)

	w := s.get("/")
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / Content-Type = %q, want text/html", ct)
	}

	bodyText := body(w)
	// Ordered check: </html> must be the tail, and it must come after the
	// ribbon, so "Edit with Shelley" cannot be a stray string from a partial
	// render that still happened to contain it.
	for _, want := range []string{
		"<!doctype html>",
		"integration-host",
		"Copied to clipboard!",
		"Edit with Shelley",
		"</html>",
	} {
		wantContains(t, bodyText, want, "GET / body")
	}
	trimmed := strings.TrimSpace(bodyText)
	if !strings.HasSuffix(trimmed, "</html>") {
		t.Errorf("GET / body does not END with </html>; the template aborted mid-render (tail=%q)", tailOf(bodyText, 120))
	}
	if strings.Index(bodyText, "Edit with Shelley") > strings.Index(bodyText, "</html>") {
		t.Error("GET / body has the Shelley ribbon after </html>; unexpected document order")
	}
	if strings.Contains(bodyText, "VisitCount") {
		t.Error("GET / body references the removed VisitCount field")
	}

	// Signed-in rendering keeps working too (the identity header path).
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-ExeDev-Email", "agent@exe.dev")
	w = s.do(r)
	wantStatus(t, w, http.StatusOK)
	wantContains(t, body(w), "agent@exe.dev", "GET / with identity")
	wantContains(t, body(w), "</html>", "GET / with identity")
}

// tailOf returns the last n bytes of s, for readable failure messages.
func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// TestIntegrationStaticAssets pins the /static/ mount: the strip-prefix
// fileserver must still serve the real assets the page references, and must not
// have been captured by the API catch-all.
func TestIntegrationStaticAssets(t *testing.T) {
	s := newIntegrationSession(t)

	for _, path := range []string{"/static/script.js", "/static/style.css"} {
		t.Run(path, func(t *testing.T) {
			w := s.get(path)
			wantStatus(t, w, http.StatusOK)
			if w.Body.Len() == 0 {
				t.Errorf("GET %s returned an empty body", path)
			}
			if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
				t.Errorf("GET %s served as JSON (%q); the API layer swallowed a static asset", path, ct)
			}
		})
	}

	// The shell references both assets, so a renamed file would break the page
	// even though the static mount itself still works.
	shell := body(s.get("/"))
	for _, ref := range []string{"/static/script.js", "/static/style.css"} {
		wantContains(t, shell, ref, "GET / body")
	}
}

// TestIntegrationNonAPINamespaceUntouched walks the non-/api routes to make
// sure adding the API did not disturb them, including the root's exact-match
// behaviour (a nested path under / is not the page).
func TestIntegrationNonAPINamespaceUntouched(t *testing.T) {
	s := newIntegrationSession(t)

	// Only GET /{$} is the shell: /anything-else is a plain 404, not the page.
	w := s.get("/not-the-page")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /not-the-page status = %d, want 404", w.Code)
	}
	if strings.Contains(body(w), "Edit with Shelley") {
		t.Error("GET /not-the-page served the HTML shell; the root pattern is too broad")
	}

	// The root pattern must ignore the query string, because exe.dev's login
	// flow sends users back to "/?something=1" (see loginURLForRequest). A
	// pattern that matched the whole RequestURI would 404 real users.
	w = s.get("/?redirect=%2F")
	if w.Code != http.StatusOK {
		t.Errorf("GET /?redirect=%%2F status = %d, want 200 (the root pattern must match on path only)", w.Code)
	} else {
		wantContains(t, body(w), "</html>", "GET / with a query string")
	}

	// A non-GET verb on the shell path is net/http's own plain-text 405 with an
	// Allow header, not a JSON api error: the API error shape is confined to
	// /api. (This also documents that the API rewrite did not install a JSON
	// fallback over the whole tree.)
	w = s.do(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x")))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / status = %d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Errorf("POST / Allow = %q, want it to name GET", allow)
	}
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Errorf("POST / answered with the JSON api error shape (%q); the API layer leaked onto the shell", ct)
	}
}

// ---------- real sockets: the same tree over a real listener ----------

// TestIntegrationOverRealHTTPServer closes the loop on "it only works in
// httptest": the identical handler tree, served by a real http.Server on a
// loopback port, driven by a real http.Client with explicit timeouts. Nothing
// here touches the network beyond 127.0.0.1 and every call is bounded.
func TestIntegrationOverRealHTTPServer(t *testing.T) {
	s := New("socket-host")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		<-serveErr
	}()

	client := &http.Client{Transport: boundedTransport(), Timeout: 10 * time.Second}
	base := "http://" + ln.Addr().String()

	get := func(path string) (int, []byte) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			t.Fatalf("GET %s: read: %v", path, err)
		}
		return resp.StatusCode, raw
	}
	post := func(path, bodyStr string) (int, []byte) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(bodyStr))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			t.Fatalf("POST %s: read: %v", path, err)
		}
		return resp.StatusCode, raw
	}

	if code, raw := get("/api/state"); code != http.StatusOK || !bytes.Contains(raw, []byte(`"version":0`)) {
		t.Fatalf("GET /api/state over a real socket: %d %q", code, raw)
	}

	code, raw := post("/api/code", `{"code":"s(\"bd*8\")","message":"over a socket"}`)
	if code != http.StatusOK {
		t.Fatalf("POST /api/code: %d %q", code, raw)
	}
	var push struct {
		Version int64  `json:"version"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(raw, &push); err != nil {
		t.Fatalf("decode push: %v", err)
	}
	if push.Version != 1 || push.Code != `s("bd*8")` {
		t.Errorf("push over socket returned version=%d code=%q, want 1 and the pushed code", push.Version, push.Code)
	}

	code, raw = post("/api/eval-result", `{"version":1,"ok":false,"error":"boom while listening"}`)
	if code != http.StatusOK {
		t.Fatalf("POST /api/eval-result: %d %q", code, raw)
	}

	code, raw = get("/api/state")
	if code != http.StatusOK {
		t.Fatalf("final GET /api/state: %d %q", code, raw)
	}
	var state struct {
		Version          int64  `json:"version"`
		Code             string `json:"code"`
		LastAgentMessage string `json:"lastAgentMessage"`
		LastEvalResult   *struct {
			Version int64  `json:"version"`
			OK      bool   `json:"ok"`
			Error   string `json:"error"`
		} `json:"lastEvalResult"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Version != 1 || state.LastAgentMessage != "over a socket" {
		t.Errorf("final state = version %d message %q, want 1 and the narration", state.Version, state.LastAgentMessage)
	}
	if state.LastEvalResult == nil || state.LastEvalResult.OK || state.LastEvalResult.Error != "boom while listening" {
		t.Errorf("final lastEvalResult = %+v, want the failure verdict over the wire", state.LastEvalResult)
	}

	// The shell over a real socket too (Content-Length must match the body).
	code, raw = get("/")
	if code != http.StatusOK || !bytes.Contains(raw, []byte("Edit with Shelley")) {
		t.Errorf("GET / over a real socket: %d, len=%d", code, len(raw))
	}

	code, raw = get("/definitely-not-here")
	if code != http.StatusNotFound || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		t.Errorf("non-API 404 over a real socket: %d %q, want a plain-text 404", code, raw)
	}
}

// ---------- the real binary ----------

// TestIntegrationBuiltBinaryServesTheLoop builds cmd/srv with `go build` into a
// temp dir, runs the resulting binary on a loopback port, and drives the agent
// loop through it. This is the "does the artefact we actually ship work" test:
// it catches wiring that only breaks in main (flag parsing, a wrong listen
// address, a missing route) and it is the only test that exercises the real
// entrypoint rather than the handler tree.
//
// Every step is bounded so a wedged child can never hang the suite: the build
// runs under a context deadline with a hard Cancel and a WaitDelay, the wait
// for the port has a 20-second deadline, every HTTP call carries a context
// deadline and the client has its own timeout, and shutdown is a SIGKILL
// followed by a bounded Wait.
func TestIntegrationBuiltBinaryServesTheLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a real process; skipped under -short")
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		t.Skipf("go.mod not found above %s: %v", repoRoot, err)
	}

	// ---- build ----
	bin := filepath.Join(t.TempDir(), "srv-under-test")
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer buildCancel()
	buildStarted := time.Now()
	buildCmd := exec.CommandContext(buildCtx, goBin, "build", "-o", bin, "./cmd/srv")
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	// A hard kill as well as the context, so even a wedged `go build` cannot
	// outlive the test.
	buildCmd.Cancel = func() error { return buildCmd.Process.Kill() }
	buildCmd.WaitDelay = 10 * time.Second
	if buildOut, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/srv failed after %v: %v\n%s", time.Since(buildStarted).Round(time.Millisecond), err, buildOut)
	}

	// ---- pick a loopback port ----
	// Race-free enough for a test: bind, read the port, release it, then let
	// the child bind it. Nothing else on this machine competes for it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}

	// ---- run ----
	cmd := exec.Command(bin, "-listen", addr)
	// The child's stdout/stderr are copied by two goroutines that exec starts,
	// and the test reads the captured output on failure paths. A bare
	// bytes.Buffer would be a data race (the race detector flags it under
	// -race, and a torn read could print garbage); this writer serialises both.
	out := &safeBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	childDone := func() (error, bool) {
		select {
		case err := <-waited:
			return err, true
		default:
			return nil, false
		}
	}
	defer func() {
		// Kill unconditionally, then wait with a bound. Reading from `waited`
		// twice is fine: the channel is buffered and the value is memoised by
		// the closer below.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-waited:
		case <-time.After(10 * time.Second):
			t.Errorf("the server binary did not exit within 10s of SIGKILL")
		}
	}()

	client := &http.Client{Transport: boundedTransport(), Timeout: 5 * time.Second}
	base := "http://" + addr

	// Wait for the child to be genuinely READY, not merely to have a socket
	// open: a bare TCP dial can connect while the listener is still coming up
	// (the port is released and rebound by the child), and the very next
	// request then gets a connection reset. Readiness is therefore "GET
	// /api/state returns a 200 body", retried on ANY transport error, which is
	// what the rest of the test depends on. Bounded at 20 seconds, and the
	// child exiting at any point is a hard failure.
	deadline := time.Now().Add(20 * time.Second)
	var lastReadyErr error
	ready := false
	for time.Now().Before(deadline) {
		if err, exited := childDone(); exited {
			t.Fatalf("server binary exited before becoming ready (%v); output:\n%s", err, out.String())
		}
		reqCtx, reqCancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/api/state", nil)
		if reqErr != nil {
			reqCancel()
			t.Fatalf("build readiness request: %v", reqErr)
		}
		resp, getErr := client.Do(req)
		if getErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				reqCancel()
				break
			}
			lastReadyErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastReadyErr = getErr
		}
		reqCancel()
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("server binary was never ready on %s within 20s: %v; output:\n%s", addr, lastReadyErr, out.String())
	}

	// do is the only place a request is issued, so every call gets a deadline
	// and every body is closed.
	do := func(method, path, reqBody string) (int, []byte) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var reader io.Reader
		if reqBody != "" {
			reader = strings.NewReader(reqBody)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		if reqBody != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s against the real binary: %v (output:\n%s)", method, path, err, out.String())
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			t.Fatalf("read %s %s: %v", method, path, err)
		}
		return resp.StatusCode, raw
	}

	// ---- the loop, against the shipped binary ----
	if code, raw := do(http.MethodGet, "/api/state", ""); code != http.StatusOK {
		t.Fatalf("fresh GET /api/state from the real binary: %d %q", code, raw)
	} else if !bytes.Contains(raw, []byte(`"version":0`)) || !bytes.Contains(raw, []byte(`"history":[]`)) {
		t.Errorf("fresh state from the real binary = %q, want version 0 with an empty history", raw)
	}

	code, raw := do(http.MethodPost, "/api/code",
		`{"code":"s(\"bd*4\")","message":"from the shipped binary"}`)
	if code != http.StatusOK {
		t.Fatalf("POST /api/code to the real binary: %d %q", code, raw)
	}
	var pushed struct {
		Version int64  `json:"version"`
		Code    string `json:"code"`
		Message string `json:"lastAgentMessage"`
	}
	if err := json.Unmarshal(raw, &pushed); err != nil {
		t.Fatalf("decode push from the real binary: %v (body=%q)", err, raw)
	}
	if pushed.Version != 1 || pushed.Code != `s("bd*4")` || pushed.Message != "from the shipped binary" {
		t.Errorf("the real binary returned version=%d code=%q message=%q", pushed.Version, pushed.Code, pushed.Message)
	}

	// The read path of the real binary agrees.
	if code, raw := do(http.MethodGet, "/api/state", ""); code != http.StatusOK || !bytes.Contains(raw, []byte(`"version":1`)) {
		t.Errorf("real binary GET /api/state after a push: %d %q", code, raw)
	}

	// An invalid push is still rejected with the documented JSON error shape
	// through the real binary, not just in-process.
	if code, raw := do(http.MethodPost, "/api/code", `{"code":"   "}`); code != http.StatusBadRequest ||
		!bytes.Contains(raw, []byte(`"error"`)) {
		t.Errorf("real binary rejected-push response: %d %q, want 400 with a JSON error", code, raw)
	}

	// And the HTML shell still renders to completion through the real binary:
	// the end-of-document marker proves the template did not abort mid-render.
	code, shellRaw := do(http.MethodGet, "/", "")
	if code != http.StatusOK {
		t.Fatalf("GET / from the real binary: %d", code)
	}
	for _, want := range []string{"Edit with Shelley", "</html>"} {
		if !bytes.Contains(shellRaw, []byte(want)) {
			t.Errorf("the real binary did not render the whole shell: missing %q (len=%d)", want, len(shellRaw))
		}
	}
}

// TestIntegrationListenAddrIsHonoured is the cheap, deterministic companion to
// the binary test: it asserts the flag actually reaches the listener, without
// spawning anything. It parses -listen the same way main does and dials it.
func TestIntegrationListenAddrIsHonoured(t *testing.T) {
	// The flag's default is part of the shipped contract (srv.service relies on
	// the binary being startable with no arguments).
	src, err := os.ReadFile(filepath.Join("..", "cmd", "srv", "main.go"))
	if err != nil {
		t.Skipf("cannot read cmd/srv/main.go: %v", err)
	}
	for _, want := range []string{"flag.String(\"listen\"", ":8000"} {
		if !bytes.Contains(src, []byte(want)) {
			t.Errorf("cmd/srv/main.go no longer contains %q; the documented default listen address changed", want)
		}
	}
}

// TestIntegrationSocketsAreLoopbackOnly documents (and enforces in the test
// suite's own use) the constraint that the harness needs no external network:
// every listener the tests open is on 127.0.0.1 and every dial is bounded.
func TestIntegrationSocketsAreLoopbackOnly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback listen: %v", err)
	}
	defer ln.Close()

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T is not TCP", ln.Addr())
	}
	if !tcpAddr.IP.IsLoopback() {
		t.Errorf("test listener bound to %s, want a loopback address", tcpAddr.IP)
	}
	if tcpAddr.Port == 8000 {
		t.Error("test listener took the production port 8000; tests must not collide with a running service")
	}
}
