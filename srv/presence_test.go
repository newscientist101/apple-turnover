package srv

// Agent presence over the wire (strudel-agent-sfu, part of uvj.6).
//
// The Conductor slice proved the lease is correct. These tests prove the three
// things that make it usable by an agent and observable by a listener:
//
//  1. POST /api/heartbeat exists in the REAL route tree, is argument-free like
//     hush/play, and returns the resulting snapshot.
//  2. It never bumps the version, however many times it is called.
//  3. Listeners are told when the lease EXPIRES — and crucially are NOT told
//     anything on a routine heartbeat, because a heartbeat changes nothing an
//     existing listener could act on.
//
// (3) is the property that distinguishes design option (a) from a client-side
// timer, and it is the one a source-reading check cannot prove: it is about what
// does and does not arrive on a socket.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestHeartbeatEndpointIsRegistered proves the endpoint is reachable through
// routes() and not merely implemented — a handler nothing routes to is dead
// code that answers 404.
func TestHeartbeatEndpointIsRegistered(t *testing.T) {
	s := New()
	rec := postJSONTo(t, s, "/api/heartbeat", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/heartbeat = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var snap Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("response is not a Snapshot: %v (%q)", err, snapBody(rec))
	}
	if !snap.Agent.Active {
		t.Error("agent is not active in the snapshot returned by its own heartbeat: the response does not describe the state it just wrote")
	}
	if snap.Agent.LastSeenMS == 0 {
		t.Error("LastSeenMS = 0 after a heartbeat: 0 is reserved for never-seen")
	}
}

// TestHeartbeatIsArgumentFree proves the endpoint follows the hush/play idiom:
// an empty body and {} are fine, any field is refused.
//
// This matters because an argument-free endpoint that silently ignored a body
// would let a client believe it had changed something — the exact defect
// rejectUnexpectedBody exists to prevent.
func TestHeartbeatIsArgumentFree(t *testing.T) {
	s := New()

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"empty body", "", http.StatusOK},
		{"empty object", `{}`, http.StatusOK},
		{"unknown field", `{"agentId":"a1"}`, http.StatusBadRequest},
		{"smuggled code", `{"code":"s(\"bd\")"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSONTo(t, s, "/api/heartbeat", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("POST /api/heartbeat %q = %d, want %d: %s", tc.body, rec.Code, tc.want, snapBody(rec))
			}
		})
	}
}

// TestHeartbeatRejectsOtherMethods keeps the API's uniform JSON-error contract:
// the wrong verb is a 405 with an Allow header, not net/http's plain text.
func TestHeartbeatRejectsOtherMethods(t *testing.T) {
	s := New()

	req, err := http.NewRequest(http.MethodGet, "/api/heartbeat", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/heartbeat = %d, want 405: %s", rec.Code, snapBody(rec))
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Errorf("Allow = %q, want %q", got, http.MethodPost)
	}
	var apiErr apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil || apiErr.Error == "" {
		t.Errorf("405 body is not the documented error object: %v (%q)", err, snapBody(rec))
	}
}

// TestHeartbeatNeverBumpsVersion is the wire-level half of the Conductor's
// version invariant. It goes through the real route tree rather than calling the
// Conductor, because "the handler incremented something after the Conductor
// returned" is a defect the Conductor test cannot see.
func TestHeartbeatNeverBumpsVersion(t *testing.T) {
	s := New()

	// Publish first, so a heartbeat bumping the version would be visible as a
	// version of 2 rather than being confused with the initial 0.
	if rec := postJSONTo(t, s, "/api/code", `{"code":"s(\"bd\")"}`); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/code = %d: %s", rec.Code, snapBody(rec))
	}

	var last string
	for i := 0; i < 5; i++ {
		rec := postJSONTo(t, s, "/api/heartbeat", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/heartbeat #%d = %d: %s", i, rec.Code, snapBody(rec))
		}
		last = rec.Body.String()
	}

	var snap Snapshot
	if err := json.Unmarshal([]byte(last), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snap.Version != 1 {
		t.Errorf("version after one publish and five heartbeats = %d, want 1: presence must not bump the version", snap.Version)
	}
}

// TestHeartbeatDoesNotBroadcast proves a routine heartbeat is SILENT on the
// listener socket.
//
// The contract in event.go is that broadcast happens only after a mutation an
// existing listener could act on. A heartbeat is already covered by the active
// flag every listener holds, so it must not generate a frame: otherwise a
// listener whose queue is bounded at 64 could be evicted by heartbeats alone.
//
// It uses the shared fanoutListener rather than a raw Read, because a WebSocket
// read that times out leaves the connection unusable — the exact trap
// fanoutListener documents and exists to avoid. Here the FIRST read is expected
// to find nothing, so a bare conn.Read would poison every later assertion.
func TestHeartbeatDoesNotBroadcast(t *testing.T) {
	s, _, wsBase := fanoutServer(t)

	conn := dialListener(t, wsBase)
	defer conn.Close(websocket.StatusNormalClosure, "")
	l := fanoutAttach(t, "listener", conn, s.Conductor.Snapshot())

	for i := 0; i < 3; i++ {
		if rec := postJSONTo(t, s, "/api/heartbeat", ""); rec.Code != http.StatusOK {
			t.Fatalf("POST /api/heartbeat #%d = %d: %s", i, rec.Code, snapBody(rec))
		}
	}

	l.expectNone(t, "three heartbeats")
}

// TestAgentFrameFiresOnLeaseExpiry is the property that makes the indicator
// honest: when the lease lapses, listeners are told, exactly once.
//
// "Exactly once" is the load-bearing half. A sweeper that re-announces on every
// tick after expiry is indistinguishable, on the wire, from one that fires once
// — until a client counts frames, which is exactly what this does.
func TestAgentFrameFiresOnLeaseExpiry(t *testing.T) {
	s := presenceSweepingServer(t)

	_, _, wsBase := fanoutServerWith(t, s)
	conn := dialListener(t, wsBase)
	defer conn.Close(websocket.StatusNormalClosure, "")
	l := fanoutAttach(t, "listener", conn, s.Conductor.Snapshot())

	if rec := postJSONTo(t, s, "/api/heartbeat", ""); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/heartbeat = %d: %s", rec.Code, snapBody(rec))
	}

	// The agent is present; no expiry frame may arrive while the lease holds.
	l.expectNone(t, "a heartbeat with a live lease")

	// Expire the lease by rewinding the stored timestamp rather than sleeping
	// out the real 15s TTL. This keeps the test honest about WHICH side it is
	// proving: the sweeper's reaction, not the passage of time.
	s.expireAgentLeaseForTest()

	ev := l.next(t, "the lease expiry")
	if ev.Kind != EventAgent {
		t.Fatalf("frame kind = %q, want %q", ev.Kind, EventAgent)
	}
	if ev.Snapshot.Agent.Active {
		t.Error("the expiry frame still reports the agent active: a listener would be told nothing changed")
	}
	if ev.Snapshot.Agent.LastSeenMS == 0 {
		t.Error("the expiry frame reports LastSeenMS 0: the last heartbeat is a fact the client still needs, even after the lease lapses")
	}

	// And only once: the sweeper keeps ticking for the rest of this test, and
	// must not re-announce the same lapse on every tick after it.
	l.expectNone(t, "the same expiry, re-announced")
}

// TestAgentFrameDoesNotFireWithoutAnAgent is the converse: a server that has
// never seen an agent must not announce an agent going away.
//
// The never-seen state is not a transition INTO inactive — it is the initial
// state — so there is nothing to report. A sweeper that fires here would tell
// every listener that an agent disconnected from a performance that never had
// one.
func TestAgentFrameDoesNotFireWithoutAnAgent(t *testing.T) {
	s := presenceSweepingServer(t)

	_, _, wsBase := fanoutServerWith(t, s)
	conn := dialListener(t, wsBase)
	defer conn.Close(websocket.StatusNormalClosure, "")
	l := fanoutAttach(t, "listener", conn, s.Conductor.Snapshot())

	// Prove the sweeper really was running and really had the chance to fire,
	// so this cannot pass merely because nothing was watching.
	waitFor(t, time.Second, func() bool { return s.agentSweepTicks.Load() > 1 },
		"the sweeper never ticked twice: it was not running, so 'it stayed quiet' proves nothing")

	l.expectNone(t, "a server that never saw an agent")
}

// TestAgentSweeperStopsPromptly proves the goroutine is actually reaped, because
// a sweeper that outlives its server is a leak that accumulates one goroutine
// per Server — and the test suite builds a Server per test.
//
// The stop is asserted to be FAST as well as eventual: the assertion that proves
// teardown works is that the sweeper stopped, not merely that we eventually
// noticed it had not.
func TestAgentSweeperStopsPromptly(t *testing.T) {
	s := New()
	s.agentSweepInterval = 5 * time.Millisecond
	s.startAgentSweeper()
	t.Cleanup(s.stopAgentSweeper)

	// Let it prove it is running before stopping it, or "stopped" is vacuous.
	waitFor(t, time.Second, func() bool { return s.agentSweepTicks.Load() > 0 },
		"the sweeper never ticked: it is not running, so stopping it proves nothing")

	start := time.Now()
	s.stopAgentSweeper()
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("stopAgentSweeper took %s: a shutdown that blocks is a wedged server", elapsed)
	}

	// And it must STAY stopped: a sweeper that re-arms after teardown keeps
	// broadcasting into a closed hub.
	ticksAtStop := s.agentSweepTicks.Load()
	time.Sleep(50 * time.Millisecond)
	if got := s.agentSweepTicks.Load(); got > ticksAtStop {
		t.Errorf("the sweeper ticked %d more times after stopAgentSweeper: it re-armed after teardown", got-ticksAtStop)
	}
}

// TestStopAgentSweeperIsIdempotent covers the double-stop path: New and the test
// cleanup may both stop it, and closing an already-closed channel panics.
func TestStopAgentSweeperIsIdempotent(t *testing.T) {
	s := New()
	s.agentSweepInterval = 5 * time.Millisecond
	s.startAgentSweeper()
	s.stopAgentSweeper()
	s.stopAgentSweeper() // must not panic
}

// presenceSweepingServer returns a Server whose lease sweeper runs fast enough
// to observe in a test, with the sweeper already started and registered for
// teardown.
//
// The sweeper is started explicitly rather than by New so a test can shorten the
// interval first; New's interval is the production one, and a test that spent the
// real AgentTTL proving a decay would be both slow and no clearer.
func presenceSweepingServer(t *testing.T) *Server {
	t.Helper()
	s := New()
	s.agentSweepInterval = 10 * time.Millisecond
	s.startAgentSweeper()
	t.Cleanup(s.stopAgentSweeper)
	return s
}

// postJSONTo drives one POST through the real route tree and returns the
// recorder, so these tests exercise routing and the body-cap middleware exactly
// as an agent would rather than calling handlers directly.
func postJSONTo(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(http.MethodPost, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

func snapBody(rec *httptest.ResponseRecorder) string {
	return bodyOf(rec.Body.String())
}

// dialListener opens one listener connection and requires it to succeed.
func dialListener(t *testing.T, wsBase string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), wsBase, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsBase, err)
	}
	return conn
}

// wsReadNextEvent reads one frame and requires it to carry wantKind, so a test
// asserting a specific transition cannot silently pass on some other frame that
// happened to arrive first.
func wsReadNextEvent(t *testing.T, conn *websocket.Conn, wantKind string) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()

	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("no %q frame within %s: %v", wantKind, wsReadTimeout, err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("%q frame: type = %v, want %v", wantKind, typ, websocket.MessageText)
	}
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("%q frame is not a JSON Event: %v (%q)", wantKind, err, data)
	}
	if ev.Kind != wantKind {
		t.Fatalf("frame kind = %q, want %q", ev.Kind, wantKind)
	}
	return ev
}

// waitFor polls cond until it holds or the bound expires.
//
// A bound may make a hang fast; it must never make one pass. That is why this
// FAILS on expiry rather than returning quietly: a condition that never became
// true is a test failure, not a skip.
func waitFor(t *testing.T, bound time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition never held within %s: %s", bound, msg)
}

// TestAgentFrameFiresWhenTheLeaseDecaysWithTheTimestampUnchanged is the
// regression test for a defect the first implementation of the sweeper had.
//
// It tracked "have I seen this lease held?" with a boolean, which only a tick
// that CAUGHT the lease inside its window could set. A lease that was taken and
// then decayed without any tick observing it alive was never marked for
// announcement, and its expiry was silently dropped — every listener kept
// showing an agent the server already considered gone. That is exactly the stale
// green badge this feature exists to remove, reappearing through the back door.
//
// The subtlety is HOW the lease is expired here. It advances the Conductor's
// CLOCK and leaves lastSeenMS untouched, because that is what real decay does:
// time passes and the stored heartbeat gets older. Rewinding the timestamp
// instead would change lastSeenMS, and a sweeper keyed on lastSeenMS would then
// look correct while still being wrong for an agent that simply stopped beating
// — so a test that used that shortcut would pass against the very bug it claims
// to cover. TestAgentFrameFiresForARewoundLeaseExists guards that distinction.
func TestAgentFrameFiresWhenTheLeaseDecaysWithTheTimestampUnchanged(t *testing.T) {
	s := presenceSweepingServer(t)

	conn := dialListener(t, fanoutWSBase(t, s))
	defer conn.Close(websocket.StatusNormalClosure, "")
	l := fanoutAttach(t, "listener", conn, s.Conductor.Snapshot())

	// A real heartbeat, recorded against the Conductor's clock.
	if rec := postJSONTo(t, s, "/api/heartbeat", ""); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/heartbeat = %d: %s", rec.Code, snapBody(rec))
	}
	hbMS := s.Conductor.Snapshot().Agent.LastSeenMS
	if hbMS == 0 {
		t.Fatal("the heartbeat recorded no timestamp")
	}

	// Let the sweeper tick several times while the lease is VALID, so a
	// correct implementation records the heartbeat and a boolean-based one also
	// gets a chance to look right.
	waitFor(t, time.Second, func() bool { return s.agentSweepTicks.Load() >= 3 },
		"the sweeper did not tick three times while the lease was valid")

	// Now let the lease decay WITHOUT touching the stored timestamp: move the
	// clock forward, exactly as waiting would. The clock is an atomic, so this is
	// safe while the sweeper is running — which it must be, or the test would be
	// exercising a sweeper nobody started.
	s.Conductor.advanceClock(AgentTTLMS + 1)
	if s.Conductor.Snapshot().Agent.Active {
		t.Fatal("the lease is still active after the clock passed the TTL: the test is not exercising decay")
	}
	// The invariant this whole test turns on: the stored heartbeat has not moved.
	seenAt := hbMS
	if got := s.Conductor.Snapshot().Agent.LastSeenMS; got != seenAt {
		t.Fatalf("lastSeenMs = %d, want the heartbeat time %d unchanged: real decay must not move it", got, seenAt)
	}

	ev := l.next(t, "an expiry with the timestamp unchanged")
	if ev.Kind != EventAgent {
		t.Fatalf("frame kind = %q, want %q", ev.Kind, EventAgent)
	}
	if ev.Snapshot.Agent.Active {
		t.Error("the expiry frame reports the agent active")
	}
}

// TestAgentFrameFiresForALeaseThatNeverSpansATick covers the other decay
// ordering: the lease is taken and lapsed so fast that no tick ever observes it
// alive at all.
//
// It exists because the two orderings fail differently. A sweeper that only
// reacts to values it has not seen before handles this one; a sweeper that only
// reacts to a lease it watched go stale misses it. Both must work.
func TestAgentFrameFiresForALeaseThatNeverSpansATick(t *testing.T) {
	s := presenceSweepingServer(t)

	conn := dialListener(t, fanoutWSBase(t, s))
	defer conn.Close(websocket.StatusNormalClosure, "")
	l := fanoutAttach(t, "listener", conn, s.Conductor.Snapshot())

	// Take the lease and drop it immediately. With the sweep interval at 10ms,
	// heartbeat + expiry complete well inside one tick, so the sweeper can only
	// pass this by reading the stored value rather than by having observed the
	// lease while it was live.
	if rec := postJSONTo(t, s, "/api/heartbeat", ""); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/heartbeat = %d: %s", rec.Code, snapBody(rec))
	}
	s.expireAgentLeaseForTest()

	ev := l.next(t, "an expiry the sweeper never saw coming")
	if ev.Kind != EventAgent {
		t.Fatalf("frame kind = %q, want %q", ev.Kind, EventAgent)
	}
	if ev.Snapshot.Agent.Active {
		t.Error("the expiry frame reports the agent active")
	}
}

// TestAgentFrameFiresForARewoundLeaseExists guards the test shortcut itself.
//
// expireAgentLeaseForTest rewinds the stored timestamp, which is a different
// thing from decay: it CHANGES lastSeenMS rather than leaving it alone. This
// asserts that distinction explicitly, because if the two ever became the same
// thing then TestAgentFrameFiresWhenTheLeaseDecaysWithTheTimestampUnchanged
// would silently stop testing what it claims to test.
func TestAgentFrameFiresForARewoundLeaseExists(t *testing.T) {
	s := presenceSweepingServer(t)
	s.expireAgentLeaseForTest()
	// Never-seen: a rewind is a no-op, so the marker stays 0.
	if got := s.Conductor.Snapshot().Agent.LastSeenMS; got != 0 {
		t.Errorf("lastSeenMs = %d on a server that never saw an agent, want 0: the rewind invented a heartbeat", got)
	}

	if rec := postJSONTo(t, s, "/api/heartbeat", ""); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/heartbeat = %d: %s", rec.Code, snapBody(rec))
	}
	before := s.Conductor.Snapshot().Agent.LastSeenMS
	s.expireAgentLeaseForTest()
	after := s.Conductor.Snapshot().Agent.LastSeenMS

	if after == before {
		t.Fatal("expireAgentLeaseForTest left lastSeenMs unchanged, so it is no longer distinguishable from real decay and the decay test proves nothing")
	}
	if after >= before {
		t.Errorf("the rewind moved lastSeenMs forward (%d -> %d); it must move it BACK in time", before, after)
	}
}

// fanoutWSBase serves s on loopback and returns just the WebSocket URL, for the
// tests here that need the URL and nothing else.
func fanoutWSBase(t *testing.T, s *Server) string {
	t.Helper()
	_, _, wsBase := fanoutServerWith(t, s)
	return wsBase
}

// TestAgentSweeperStartsInServeNotNew pins WHERE the sweeper is started, which
// is a structural invariant rather than a behaviour one.
//
// Starting it in New would mean the goroutine is already reading
// agentSweepInterval while a caller configures it, so every field a test or a
// future option sets would be a data race rather than a compile error. The
// failure is invisible until -race happens to run the right interleaving, which
// is the worst time to discover it.
//
// It is asserted by observable state rather than by reading the source: after
// New the sweeper must not be running, and Serve is what starts it. Serve
// itself blocks on ListenAndServe, so this checks the construction side, which
// is the side that can be got wrong.
func TestAgentSweeperStartsInServeNotNew(t *testing.T) {
	s := New()
	t.Cleanup(s.stopAgentSweeper)

	// New must not have started it.
	time.Sleep(50 * time.Millisecond)
	if got := s.agentSweepTicks.Load(); got != 0 {
		t.Errorf("New started the lease sweeper (it ticked %d times): a caller cannot then configure the Server without racing that goroutine", got)
	}
	if s.agentSweepDone != nil {
		t.Error("New created the sweeper's channels: the sweeper belongs to Serve, which runs the server")
	}

	// And starting it explicitly is what makes it run, which is what Serve does.
	s.agentSweepInterval = 5 * time.Millisecond
	s.startAgentSweeper()
	waitFor(t, time.Second, func() bool { return s.agentSweepTicks.Load() > 0 },
		"the sweeper did not run after being started: Serve's start path is broken")
}

// TestStopAgentSweeperWaitsForTheGoroutine is the assertion that makes a
// non-joining stop detectable.
//
// The other sweeper test checks that ticking STOPS after stopAgentSweeper
// returns. That is not the same claim as the goroutine having EXITED: a stop
// which closes the done channel and returns immediately still stops the ticking,
// just a moment later, so a test watching ticks alone passes against exactly the
// leak it exists to prevent. A goroutine parked in NewTicker survives its
// Server, and the suite builds a Server per test.
//
// The direct evidence is the stopped channel the sweeper closes on its way out,
// which stopAgentSweeper must have observed. Checking it non-blockingly after
// the call returns is what distinguishes "joined" from "signalled and hoped".
func TestStopAgentSweeperWaitsForTheGoroutine(t *testing.T) {
	s := New()
	s.agentSweepInterval = 5 * time.Millisecond
	s.startAgentSweeper()

	// Let it prove it is running, or "it exited" is vacuous.
	waitFor(t, time.Second, func() bool { return s.agentSweepTicks.Load() > 0 },
		"the sweeper never ticked: it is not running, so its exit proves nothing")

	s.stopAgentSweeper()

	// stopAgentSweeper returns only after the goroutine closes this. If it
	// returned early, the channel is still open at this instant.
	select {
	case <-s.agentSweepStopped:
	default:
		t.Error("stopAgentSweeper returned with the sweeper still running: it signalled without joining, leaking one goroutine per Server")
	}
}
