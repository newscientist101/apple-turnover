package srv

// The agent status indicator (strudel-agent-4jk, part of uvj.6).
//
// The badge in the top-left used to be pure decoration: no element ids, nothing
// in any served module touching it, and a CSS @keyframes pulse that ran
// unconditionally. It read "LIVE" whether an agent was driving, dead, or had
// never existed — which is worse than showing nothing, because it looks like a
// health light.
//
// These tests EXECUTE the served session.js in goja against a fake DOM, which
// is the only way to prove the badge renders. Asserting on the CSS, or grepping
// session.js for a class name, would pass against an indicator that renders
// nothing — the same reason AGENTS.md requires the served JavaScript to be
// executed for timing behaviour rather than its arithmetic reimplemented in Go.
//
// The state under test comes from REAL /api/state bytes rather than a
// hand-written object, so a renamed json tag cannot leave the test passing.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// servedSessionJS fetches the bytes the server actually serves for session.js,
// through the real route tree.
func servedSessionJS(t *testing.T) string {
	t.Helper()
	server := New()
	req := httptest.NewRequest(http.MethodGet, "/static/session.js", nil)
	w := httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/session.js status = %d, want 200", w.Code)
	}
	return w.Body.String()
}

// agentIndicatorRuntime loads the served session.js with a fake DOM holding the
// badge elements the template renders, and exposes a way to read what the badge
// currently says.
func agentIndicatorRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSessionRuntime(t, servedSessionJS(t))

	rt.run(`
	  // The three elements welcome.html gives ids to. Recording innerHTML and
	  // className is what lets a test assert what a viewer would actually see,
	  // not merely that some property was assigned.
	  var __dot = { className: "" };
	  var __text = { innerHTML: "" };
	  var __indicator = { className: "" };
	  document.getElementById = function (id) {
	    if (id === "agent-status-dot") { return __dot; }
	    if (id === "agent-status-text") { return __text; }
	    if (id === "status-indicator") { return __indicator; }
	    return null;
	  };
	  window.__dotClass = function () { return __dot.className; };
	  window.__labelText = function () { return __text.innerHTML; };
	  window.__indicatorClass = function () { return __indicator.className; };
	`)
	return rt
}

// snapshotWithAgent fetches a live snapshot from a real server, overriding the
// agent fields with the given presence.
//
// It starts from real bytes so the surrounding snapshot shape is the server's,
// and overrides only the two presence fields so the test can pin a decay that
// has not happened yet.
func snapshotWithAgent(t *testing.T, base string, active bool, lastSeenMS int64) map[string]any {
	t.Helper()
	code, raw := fanoutGet(t, base, "/api/state")
	if code != http.StatusOK {
		t.Fatalf("GET /api/state = %d", code)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	snap["agent"] = map[string]any{"active": active, "lastSeenMs": lastSeenMS}
	return snap
}

// jsJSON marshals v as a JavaScript object literal, so a Go value can be handed
// to the runtime without hand-writing JSON into a string literal (and without a
// quoting bug quietly turning a test into a no-op).
func jsJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(b)
}

// TestAgentIndicatorDistinguishesPresentFromAbsent is the behaviour the feature
// exists for: the badge must VISIBLY differ between an agent that is connected
// and one that is not.
//
// It drives real /api/state bytes through the served client and asserts on the
// rendered label and dot class, so a badge that renders nothing — or renders
// the same thing in both states — fails here.
func TestAgentIndicatorDistinguishesPresentFromAbsent(t *testing.T) {
	_, base, _ := fanoutServer(t)
	rt := agentIndicatorRuntime(t)

	// 1. Absent: no agent has ever connected.
	rt.run("window.strudelSession.renderAgentStatus(" +
		jsJSON(t, snapshotWithAgent(t, base, false, 0)) + ")")
	absentLabel := rt.eval("window.__labelText()").String()
	absentDot := rt.eval("window.__indicatorClass()").String()

	if absentLabel == "" {
		t.Fatal("the badge rendered nothing for an absent agent: the region exists but is never written")
	}

	// 2. Present.
	rt.run("window.strudelSession.renderAgentStatus(" +
		jsJSON(t, snapshotWithAgent(t, base, true, 1000)) + ")")
	presentLabel := rt.eval("window.__labelText()").String()
	presentDot := rt.eval("window.__indicatorClass()").String()

	// The two states must be distinguishable on BOTH the words and the state
	// class, so the difference survives a screen reader and a glance at the
	// colour. The class is what style.css keys its colour and its pulse off.
	if presentLabel == absentLabel {
		t.Errorf("the badge says %q whether the agent is present or absent: it reports a state it cannot distinguish", presentLabel)
	}
	if presentDot == absentDot {
		t.Errorf("the indicator class is %q in both states: the badge is visually identical either way", presentDot)
	}
	if !strings.Contains(presentDot, "agent-connected") {
		t.Errorf("present indicator class = %q, want the agent-connected state class the stylesheet keys on", presentDot)
	}
	if !strings.Contains(presentLabel, "AGENT") {
		t.Errorf("present label = %q, want it to name the agent so the badge says WHAT is connected", presentLabel)
	}
}

// TestAgentIndicatorDistinguishesNeverSeenFromDecayed covers the three states
// the wire can actually express.
//
// Collapsing "never connected" into "disconnected" would be a lie about
// history: a server that has never seen an agent is in a different situation
// from one whose agent crashed, and an operator deciding whether to start an
// agent needs to tell them apart.
func TestAgentIndicatorDistinguishesNeverSeenFromDecayed(t *testing.T) {
	_, base, _ := fanoutServer(t)
	rt := agentIndicatorRuntime(t)

	rt.run("window.strudelSession.renderAgentStatus(" +
		jsJSON(t, snapshotWithAgent(t, base, false, 0)) + ")")
	neverSeen := rt.eval("window.__labelText()").String()

	rt.run("window.strudelSession.renderAgentStatus(" +
		jsJSON(t, snapshotWithAgent(t, base, false, 1000)) + ")")
	decayed := rt.eval("window.__labelText()").String()

	if neverSeen == decayed {
		t.Errorf("never-seen and decayed both render as %q: an operator cannot tell 'no agent has ever run' from 'the agent died'", neverSeen)
	}
	if !strings.Contains(neverSeen, "NEVER") {
		t.Errorf("never-seen label = %q, want it to say an agent has never connected", neverSeen)
	}
	if !strings.Contains(decayed, "LOST") {
		t.Errorf("decayed label = %q, want it to say a previously-seen agent has gone", decayed)
	}
}

// TestAgentIndicatorSuppressesIdenticalWrites proves the same well-behavedness
// #sync-status has: the badge region is aria-live, so re-assigning identical
// content still announces.
//
// It counts writes, because "the text ended up right" cannot distinguish a
// suppressed write from a performed one.
func TestAgentIndicatorSuppressesIdenticalWrites(t *testing.T) {
	_, base, _ := fanoutServer(t)
	rt := agentIndicatorRuntime(t)

	rt.run(`
	  var __writes = 0;
	  var __text = { _html: "", get innerHTML() { return this._html; },
	                 set innerHTML(v) { __writes++; this._html = v; } };
	  document.getElementById = function (id) {
	    if (id === "agent-status-text") { return __text; }
	    if (id === "agent-status-dot") { return { className: "" }; }
	    if (id === "status-indicator") { return { className: "" }; }
	    return null;
	  };
	  window.__writes = function () { return __writes; };
	`)

	snap := jsJSON(t, snapshotWithAgent(t, base, true, 1000))
	rt.run("window.strudelSession.renderAgentStatus(" + snap + ")")
	first := rt.eval("window.__writes()").ToInteger()
	if first == 0 {
		t.Fatal("renderAgentStatus wrote nothing for a present agent")
	}

	// Same state three more times: every one must be suppressed.
	for i := 0; i < 3; i++ {
		rt.run("window.strudelSession.renderAgentStatus(" + snap + ")")
	}
	if got := rt.eval("window.__writes()").ToInteger(); got != first {
		t.Errorf("writes after 4 renders of identical state = %d, want %d: an identical write still mutates the aria-live region", got, first)
	}

	// A REAL change must still be written, or the suppression above would be
	// suppressing everything.
	rt.run("window.strudelSession.renderAgentStatus(" +
		jsJSON(t, snapshotWithAgent(t, base, false, 1000)) + ")")
	if got := rt.eval("window.__writes()").ToInteger(); got <= first {
		t.Errorf("writes after a real state change = %d, want MORE than %d: suppression swallowed a real change", got, first)
	}
}

// TestAgentIndicatorHandlesMissingAgentField proves the client tolerates a
// snapshot with no agent field at all.
//
// This is a REAL case, not a hypothetical: a browser can hold a frame encoded
// before the server learned about presence, and a client that threw on a missing
// field would take down the whole frame handler — including the music. The badge
// must degrade to a state that does not claim an agent is live.
func TestAgentIndicatorHandlesMissingAgentField(t *testing.T) {
	rt := agentIndicatorRuntime(t)

	rt.run(`window.strudelSession.renderAgentStatus({ version: 3, code: "s(\"bd\")" });`)

	label := rt.eval("window.__labelText()").String()
	if label == "" {
		t.Fatal("a snapshot with no agent field left the badge blank: the client must degrade, not throw or vanish")
	}
	if strings.Contains(strings.ToUpper(label), "LIVE") {
		t.Errorf("label = %q: with no presence information the badge must not claim an agent is connected", label)
	}
}

// TestAgentIndicatorTemplateAndStyleAreWired proves the page and stylesheet
// actually carry the ids and state classes the client writes.
//
// This is the one place a source check is right rather than a compromise: the
// harness above can only prove session.js writes to ids that EXIST. Without
// this, renaming an id in welcome.html would leave every behavioural test
// passing while the real page silently stopped rendering a badge.
func TestAgentIndicatorTemplateAndStyleAreWired(t *testing.T) {
	s := New()
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	page := rec.Body.String()

	for _, id := range []string{"status-indicator", "agent-status-dot", "agent-status-text"} {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("welcome.html has no element with id %q: the client writes to it, so the badge would never render in a real browser", id)
		}
	}
	if strings.Contains(page, ">LIVE<") {
		t.Error("welcome.html still ships a hardcoded \"LIVE\" label: the badge must start from a state it can actually observe")
	}

	// The pulse must be conditional on a state class. An unconditional
	// animation is exactly the defect: it animates identically whether an agent
	// is alive or dead.
	cssRec := httptest.NewRecorder()
	s.routes().ServeHTTP(cssRec, httptest.NewRequest(http.MethodGet, "/static/style.css", nil))
	if cssRec.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css = %d, want 200", cssRec.Code)
	}
	css := cssRec.Body.String()
	for _, cls := range []string{".agent-connected", ".agent-absent", ".agent-never"} {
		if !strings.Contains(css, cls) {
			t.Errorf("style.css has no %s rule: the client sets that state class and nothing styles it, so states look identical", cls)
		}
	}

	// The pulse must be SCOPED to the connected state, not merely present. A
	// stylesheet containing "agent-connected" somewhere still satisfies the loop
	// above while animating every state identically if the animation itself sits
	// on an unscoped selector — which is exactly the original defect, so the
	// check has to be about where the animation is declared.
	code := stripCSSComments(css)
	i := strings.Index(code, "animation: pulse")
	if i < 0 {
		t.Fatal("style.css declares no pulse animation at all")
	}
	// The selector owning that declaration is the text back to the previous "}".
	selector := code[:i]
	if j := strings.LastIndex(selector, "}"); j >= 0 {
		selector = selector[j+1:]
	}
	if !strings.Contains(selector, "agent-connected") {
		t.Errorf("the pulse is declared on %q, which is not scoped to .agent-connected: it animates again whether an agent is alive or dead", strings.TrimSpace(selector))
	}
}

// TestAgentIndicatorFollowsARealExpiryEndToEnd closes the loop the unit tests
// above only simulate: a real server, a real heartbeat, a real lease expiry
// pushed over a real WebSocket, and the served client rendering it.
//
// The other tests inject a snapshot to pin a state. This one proves the wiring
// BETWEEN the pieces exists — that handleAPIHeartbeat stores the lease, the
// sweeper announces the lapse, and renderAgentStatus paints it — which is the
// only place a defect could hide in all three seams at once.
func TestAgentIndicatorFollowsARealExpiryEndToEnd(t *testing.T) {
	s := presenceSweepingServer(t)

	_, _, wsBase := fanoutServerWith(t, s)
	conn := dialListener(t, wsBase)
	defer conn.Close(websocket.StatusNormalClosure, "")
	l := fanoutAttach(t, "listener", conn, s.Conductor.Snapshot())

	rt := agentIndicatorRuntime(t)

	// The live connect snapshot says no agent has ever been seen. fanoutAttach
	// has already consumed that frame, so the server's own state is the
	// authority for what the page was just told.
	rt.run("window.strudelSession.handleFrame(" +
		jsJSONString(t, map[string]any{"kind": "snapshot", "snapshot": s.Conductor.Snapshot()}) +
		", window.__sandbox, window.__live)")
	if got := rt.eval("window.__labelText()").String(); !strings.Contains(got, "NEVER") {
		t.Fatalf("initial badge = %q, want the never-seen state from the connect snapshot", got)
	}

	// A real heartbeat, then let the lease lapse for real.
	if rec := postJSONTo(t, s, "/api/heartbeat", ""); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/heartbeat = %d: %s", rec.Code, snapBody(rec))
	}
	activeSnap := s.Conductor.Snapshot()
	if !activeSnap.Agent.Active {
		t.Fatal("the server does not consider the agent present right after its heartbeat")
	}
	rt.run("window.strudelSession.handleFrame(" +
		jsJSONString(t, map[string]any{"kind": "code", "snapshot": activeSnap}) +
		", window.__sandbox, window.__live)")
	if got := rt.eval("window.__labelText()").String(); !strings.Contains(got, "AGENT") || strings.Contains(got, "NEVER") {
		t.Fatalf("badge after a heartbeat = %q, want the connected state", got)
	}

	s.expireAgentLeaseForTest()

	ev := l.next(t, "the real lease expiry")
	if ev.Kind != EventAgent {
		t.Fatalf("frame kind = %q, want %q", ev.Kind, EventAgent)
	}
	rt.run("window.strudelSession.handleFrame(" +
		jsJSONString(t, map[string]any{"kind": ev.Kind, "snapshot": ev.Snapshot}) +
		", window.__sandbox, window.__live)")
	got := rt.eval("window.__labelText()").String()
	if !strings.Contains(got, "LOST") {
		t.Errorf("badge after the real expiry = %q, want the LOST state: the indicator is not reporting that the agent went away", got)
	}
	if cls := rt.eval("window.__indicatorClass()").String(); !strings.Contains(cls, "agent-absent") {
		t.Errorf("indicator class after the expiry = %q, want agent-absent", cls)
	}
}

// stripCSSComments removes /* ... */ blocks so a comment that merely MENTIONS a
// declaration cannot be mistaken for the declaration itself.
//
// The stylesheet explains in prose that the pulse used to be unconditional, and
// that sentence contains the very text a naive search looks for. Searching the
// raw file would therefore find the explanation and report it as the rule.
func stripCSSComments(css string) string {
	var b strings.Builder
	for {
		start := strings.Index(css, "/*")
		if start < 0 {
			b.WriteString(css)
			return b.String()
		}
		b.WriteString(css[:start])
		end := strings.Index(css[start:], "*/")
		if end < 0 {
			return b.String()
		}
		css = css[start+end+2:]
	}
}

// jsJSONString marshals v and returns it as a QUOTED JavaScript string literal.
//
// handleFrame takes the raw frame text, exactly as a WebSocket message delivers
// it, so a caller must pass a string. Passing the bare object instead makes
// JSON.parse throw — and handleFrame deliberately swallows that, logging and
// returning, exactly as it must for a genuinely corrupt frame. The result is a
// test that silently asserts nothing, which is the worst outcome available, so
// the two helpers are kept distinct and separately named.
func jsJSONString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	quoted, err := json.Marshal(string(b))
	if err != nil {
		t.Fatalf("quote %s: %v", b, err)
	}
	return string(quoted)
}

// TestHandleFrameRejectsAnUnparseableFrameWithoutClaimingSuccess proves the
// swallow is a deliberate, bounded behaviour rather than a way to make a broken
// test look green.
//
// handleFrame catches a parse failure, logs, and returns — which is exactly what
// it must do for a corrupt frame arriving on a real socket, because throwing in
// an onmessage handler would kill the music too. The cost is that a caller which
// hands it the wrong TYPE fails silently, and that is how this very test file
// shipped a moment: passing an object where a string was expected produced a
// green suite asserting nothing.
//
// So the swallow must return a value a caller can check. It returns false on a
// frame it could not use and true otherwise, which is what lets a test tell
// "handled" from "quietly ignored" without reaching into a console.
func TestHandleFrameRejectsAnUnparseableFrameWithoutClaimingSuccess(t *testing.T) {
	rt := agentIndicatorRuntime(t)

	for _, tc := range []struct {
		name string
		expr string
		want bool
	}{
		{"not json at all", `"}{ not json"`, false},
		{"an object instead of the raw text", `{kind:"snapshot"}`, false},
		{"valid frame text", `JSON.stringify({kind:"snapshot",snapshot:{agent:{active:false,lastSeenMs:0}}})`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rt.eval(
				"window.strudelSession.handleFrame(" + tc.expr + ", window.__sandbox, window.__live)").ToBoolean()
			if got != tc.want {
				t.Errorf("handleFrame(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
