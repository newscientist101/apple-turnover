// Drift reporting in the browser (strudel-agent-uvj.15), executed as served.
//
// The defect this file exists for is the "claimed but not verified" family. The
// browser computes drift honestly (sync.js), paints it honestly (session.js's
// #sync-status line) and then DROPS it: the only outbound call in any browser
// asset was POST /api/eval-result. So README's "residual drift is visible" and
// AGENT_API.md's "the browser reports observed drift" were true only to a human
// looking at the tab, and unavailable to the agent the docs address. An agent
// could not tell when listeners had drifted apart and so could not know when
// re-anchoring -- the DOCUMENTED recovery mechanism -- was needed.
//
// That is why this is a goja test over the served bytes rather than a Go test
// asserting the server accepts a `driftMs` field. A server-only test passes
// against exactly today's code: the field would validate, be stored, be echoed
// back, and the browser would still send nothing. The assertion that has
// evidence is the one on the wire the browser actually produces.
//
// The call ORDER is what forces this design, and it is worth stating because it
// is also why the obvious cheap fix is wrong. applyVersion arms the boundary
// timer (session.js:608) and POSTs the eval verdict immediately (:620); the
// observation does not exist until sync().observe fires at the bar line (:642).
// Folding drift into the eval-result body would mean either stalling the agent's
// verdict behind a bar line or POSTing one version twice and letting the second
// POST overwrite the verdict. So drift gets its own report.

package srv

import (
	"encoding/json"
	"fmt"
	"testing"
)

// syncReportPrelude extends the session harness with a RECORDING fetch, so the
// tests assert on the body that would have gone over the wire rather than on a
// console line nobody reads.
//
// It declares its own fetch rather than reusing the sessionJSPrelude's silent
// no-op: a stub that swallows the call makes "the browser reports drift" and
// "the browser discards it" indistinguishable, which is the entire defect.
const syncReportPrelude = sessionJSPrelude + `
window.__fetches = [];
var fetch = function (path, init) {
  window.__fetches.push({ path: path, body: init && init.body ? JSON.parse(init.body) : null });
  return apiStub();
};
`

// newSyncReportRuntime loads the served session.js over the recording prelude.
func newSyncReportRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, syncReportPrelude)
	rt.run(servedSyncJS(t))
	rt.run(servedSessionJS(t))
	rt.run(`window.__live = makeLive();
	  window.__sandbox = {
	    evaluate: function () { return { queryArc: function () { return []; } }; },
	    state: {},
	  };`)
	return rt
}

// syncReports returns the recorded bodies POSTed to the sync-result endpoint,
// which is the only place a drift observation is allowed to travel.
func syncReports(t *testing.T, rt *syncRuntime) []map[string]any {
	t.Helper()
	raw, err := rt.vm.RunString(`JSON.stringify(window.__fetches.filter(function (f) {
	  return f.path === "/api/sync-result";
	}))`)
	if err != nil {
		t.Fatalf("reading recorded fetches: %v", err)
	}
	var out []map[string]any
	if s := raw.String(); s != "[]" {
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			t.Fatalf("decoding recorded sync reports %s: %v", s, err)
		}
	}
	// The recorder stores {path, body} envelopes; these helpers hand back the
	// BODIES, so a test reads the fields an agent would actually receive rather
	// than the harness's own bookkeeping. Returning the envelope would make
	// every assertion look like a missing field, which is misleading in the
	// dangerous direction: it looks like the browser sent nothing.
	bodies := make([]map[string]any, 0, len(out))
	for _, rec := range out {
		if body, ok := rec["body"].(map[string]any); ok {
			bodies = append(bodies, body)
		}
	}
	return bodies
}

// bodiesTo returns the recorded bodies for one path.
func bodiesTo(t *testing.T, rt *syncRuntime, path string) []map[string]any {
	t.Helper()
	quoted := jsString(path)
	raw, err := rt.vm.RunString(`JSON.stringify(window.__fetches.filter(function (f) {
	  return f.path === ` + quoted + `;
	}))`)
	if err != nil {
		t.Fatalf("reading recorded bodies for %s: %v", path, err)
	}
	var out []map[string]any
	if s := raw.String(); s != "[]" {
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			t.Fatalf("decoding %s bodies %s: %v", path, s, err)
		}
	}
	bodies := make([]map[string]any, 0, len(out))
	for _, rec := range out {
		if body, ok := rec["body"].(map[string]any); ok {
			bodies = append(bodies, body)
		}
	}
	return bodies
}

// TestDriftReachesTheServerOverTheWire is the acceptance-critical test for this
// bead: a real code frame arrives, the client's clock runs to the shared
// boundary, and a report carrying the MEASURED drift reaches the server.
//
// The lateness is deliberate. The fake harness supports __lateBy precisely so a
// timer can run late the way a browser event loop does, and a report of
// driftMs:0 would pass against a client that posted a constant. The assertion
// names the number the CLIENT measured, so a client that computed drift wrongly
// cannot agree with this test by accident.
func TestDriftReachesTheServerOverTheWire(t *testing.T) {
	rt := newSyncReportRuntime(t)

	// A frame lands inside cycle 1 (1002000..1004000), so the commit belongs on
	// the 1004000 boundary plus the 25ms lead. The commit timer then runs 12ms
	// late, which is the drift this report must carry.
	const frameAt = int64(1002100)
	const lateBy = 12
	const wantTarget = 1004000.0 + 25
	const wantDrift = float64(lateBy)

	rt.set("__lateBy", lateBy)
	rt.run(fmt.Sprintf("window.__setNow(%d)", frameAt))
	rt.run(`window.strudelSession.applyVersion(
	  { version: 3, code: 's("bd*2")', anchor: { epochMs: 1000000, cps: 0.5 } },
	  window.__sandbox, window.__live);`)
	rt.settle()

	// Nothing yet, and that is the point: the drift is not knowable until the
	// timer fires, so a report sent here would have to be a guess.
	if got := syncReports(t, rt); len(got) != 0 {
		t.Fatalf("the browser reported drift %d time(s) on ARRIVAL (%v): drift is not knowable before the boundary, so this report could only be invented", len(got), got)
	}

	rt.advance(int64(wantTarget-frameAt) + lateBy)
	rt.settle()

	reports := syncReports(t, rt)
	if len(reports) != 1 {
		t.Fatalf("the browser made %d POST(s) to /api/sync-result after the commit, want exactly 1: a drift observation that never leaves the browser cannot inform an agent when to re-anchor (%v)", len(reports), reports)
	}
	got := reports[0]

	if v, ok := got["version"].(float64); !ok || v != 3 {
		t.Errorf("reported version = %v (%T), want 3: the observation must name the version whose commit it describes", got["version"], got["version"])
	}
	if d, ok := got["driftMs"].(float64); !ok || d != wantDrift {
		t.Errorf("reported driftMs = %v (%T), want the measured %v: an agent reading this number decides whether to re-anchor, so a wrong or absent one is the whole defect", got["driftMs"], got["driftMs"], wantDrift)
	}
	if u, ok := got["unscheduled"].(bool); ok && u {
		t.Error("a commit made ON the shared boundary reported unscheduled: the two are mutually exclusive claims and the server refuses both together")
	}
	if target, ok := got["targetMs"].(float64); !ok || target != wantTarget {
		t.Errorf("reported targetMs = %v (%T), want the shared target %v: the agent cannot place the observation without knowing which bar line it was measured against", got["targetMs"], got["targetMs"], wantTarget)
	}
}

// TestUnscheduledCommitIsReportedNotFlattened covers the case AGENT_API.md
// promises explicitly: with no usable anchor the browser reports `unscheduled`
// "rather than inventing a bar position".
//
// The trap this guards is a plausible-looking refactor that reports driftMs:0
// instead, because a zero drift and an unscheduled commit are both "not
// drifting". They are not: one says the commit landed on the bar line it
// targeted, the other says there was no bar line at all. Collapsing them makes
// a listener that never aligned look perfectly aligned, which is the exact
// inverse of the truth and would stop an agent re-anchoring a system that needs
// it.
func TestUnscheduledCommitIsReportedNotFlattened(t *testing.T) {
	rt := newSyncReportRuntime(t)

	rt.run("window.__setNow(1002100)")
	rt.run(`window.strudelSession.applyVersion(
	  { version: 4, code: 's("bd*2")', anchor: null },
	  window.__sandbox, window.__live);`)
	rt.settle()

	reports := syncReports(t, rt)
	if len(reports) != 1 {
		t.Fatalf("the browser made %d POST(s) to /api/sync-result for an unscheduled commit, want exactly 1: the unscheduled case is as important as the numeric drift, because it is the one that says alignment could not be attempted (%v)", len(reports), reports)
	}
	got := reports[0]

	if u, ok := got["unscheduled"].(bool); !ok || !u {
		t.Errorf("reported unscheduled = %v (%T), want true: with no usable anchor there is no bar line to claim", got["unscheduled"], got["unscheduled"])
	}
	if _, present := got["driftMs"]; present {
		t.Errorf("an unscheduled commit reported driftMs = %v: that is an invented bar position, and the server rejects a report carrying both", got["driftMs"])
	}
}

// TestOnlyACommitThatHappenedIsReported pins the negative case, which is the one
// a naive implementation gets wrong in the other direction: a commit SUPERSEDED
// by a newer version is cancelled and never reaches the live repl, so no
// observation of it exists.
//
// Reporting it anyway would put a drift number on the wire describing a commit
// that did not happen, and an agent would read it as the state of the audience.
func TestOnlyACommitThatHappenedIsReported(t *testing.T) {
	rt := newSyncReportRuntime(t)

	rt.run("window.__setNow(1002100)")
	rt.run(`window.strudelSession.applyVersion(
	  { version: 5, code: 's("bd*2")', anchor: { epochMs: 1000000, cps: 0.5 } },
	  window.__sandbox, window.__live);`)
	rt.settle()

	// A newer version arrives inside the same cycle and cancels the pending
	// commit before it fires.
	rt.run("window.__setNow(1002120)")
	rt.run(`window.strudelSession.applyVersion(
	  { version: 6, code: 's("cp*2")', anchor: { epochMs: 1000000, cps: 0.5 } },
	  window.__sandbox, window.__live);`)
	rt.settle()

	rt.advance(4000)
	rt.settle()

	reports := syncReports(t, rt)
	if len(reports) != 1 {
		t.Fatalf("the browser made %d POST(s) to /api/sync-result, want exactly 1: only version 6 committed, so a second report would describe a commit that was cancelled (%v)", len(reports), reports)
	}
	if v, ok := reports[0]["version"].(float64); !ok || v != 6 {
		t.Errorf("reported version = %v (%T), want 6 (the version that actually committed)", reports[0]["version"], reports[0]["version"])
	}
}

// TestTheSyncReportIsNotSmuggledOntoAnotherEndpoint guards the decision this
// bead makes rather than merely implements.
//
// Drift is a property of a COMMIT at a bar, not of an evaluation, so it must
// not appear folded into the eval-result verdict: the two are measured at
// different instants and stored under different lifetimes. This asserts the
// separation positively -- the eval-result body carries no drift -- so a later
// "simplification" that merges the two has to break a test rather than pass
// quietly.
func TestTheSyncReportIsNotSmuggledOntoAnotherEndpoint(t *testing.T) {
	rt := newSyncReportRuntime(t)

	rt.run("window.__setNow(1002100)")
	rt.run(`window.strudelSession.applyVersion(
	  { version: 7, code: 's("bd*2")', anchor: { epochMs: 1000000, cps: 0.5 } },
	  window.__sandbox, window.__live);`)
	rt.settle()
	rt.advance(2500)
	rt.settle()

	evals := bodiesTo(t, rt, "/api/eval-result")
	if len(evals) != 1 {
		t.Fatalf("the browser made %d POST(s) to /api/eval-result, want 1", len(evals))
	}
	for _, forbidden := range []string{"driftMs", "unscheduled", "sync"} {
		if _, present := evals[0][forbidden]; present {
			t.Errorf("the eval-result verdict carries %q: drift is a property of a commit at a bar, not of an evaluation, and merging them would make the stored verdict describe two instants at once", forbidden)
		}
	}
	// And the sync report is genuinely a separate call, not an absent one.
	if got := syncReports(t, rt); len(got) != 1 {
		t.Errorf("the browser made %d POST(s) to /api/sync-result, want 1 (%v)", len(got), got)
	}
}
