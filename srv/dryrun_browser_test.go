package srv

import (
	"strings"
	"testing"
)

// Dry-run in the browser (strudel-agent-uvj.16), executed as served.
//
// The server half of dry-run is proved in dryrun_test.go, but the half that
// makes it SAFE lives here: the browser is the only evaluator, so this client
// decides whether a candidate that was never published reaches the live repl.
// A handler that committed it would pass every server test — the version, the
// history and the stored verdict would all still be correct, because the server
// never saw the commit — while quietly defeating the entire feature.
//
// So these tests drive the SERVED session.js in goja, the same way coherence_test
// does, and assert on what the live repl was asked to do.

// dryRunPrelude extends the session harness with a recording fetch, so a test can
// assert on the body that would have been POSTed rather than on a console line.
//
// The base prelude's fetch is a no-op thenable; this replaces it with one that
// records the request and resolves, which is the minimum a report needs to look
// like a successful one to the client.
const dryRunPrelude = sessionJSPrelude + `
window.__fetches = [];
var fetch = function (path, init) {
  window.__fetches.push({ path: path, body: init && init.body ? JSON.parse(init.body) : null });
  return apiStub();
};
// A sandbox whose evaluate outcome the test chooses, and which RECORDS that it
// was called — the two things a dry-run test needs to assert on.
window.__sandboxCalls = [];
window.__sandboxBehaviour = null;
function makeSandbox() {
  return {
    evaluate: function (code) {
      window.__sandboxCalls.push(code);
      var b = window.__sandboxBehaviour;
      if (b && b.throws) { throw new Error(b.throws); }
      var pattern = { queryArc: function () { return (b && b.haps) || []; } };
      return Promise.resolve(pattern);
    },
    // A GETTER, not a fixed value: session.js reads sandbox.state AFTER
    // evaluate returns, so a state computed once at construction would describe
    // whatever the test had configured before the call rather than the outcome
    // of the call itself. That would make the failure path untestable.
    get state() {
      var b = window.__sandboxBehaviour;
      return b && b.evalError ? { evalError: new Error(b.evalError) } : {};
    },
  };
}
`

// newDryRunRuntime loads the served session.js with the recording fetch in place.
func newDryRunRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, dryRunPrelude)
	rt.run(servedSyncJS(t))
	rt.run(servedSessionJS(t))
	rt.run(`window.__live = makeLive(); window.__sandbox = makeSandbox();`)
	return rt
}

// TestDryRunEvaluatesInTheSandboxAndReports proves the happy path end to end
// through the real decode seam: a dry-run frame is evaluated and reported to
// /api/dry-run-result with the id the server asked about.
func TestDryRunEvaluatesInTheSandboxAndReports(t *testing.T) {
	rt := newDryRunRuntime(t)
	rt.run(`window.__sandboxBehaviour = { haps: [1, 2, 3] };`)

	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
		kind: "dry-run",
		snapshot: { version: 1, code: 's("bd")' },
		dryRun: { id: 7, code: 's("cp*4")' },
	}), window.__sandbox, window.__live);`)
	rt.settle()

	if got := rt.eval("JSON.stringify(window.__sandboxCalls)").String(); got != `["s(\"cp*4\")"]` {
		t.Errorf("sandbox evaluated %s, want the candidate from the frame", got)
	}
	if got := rt.eval("JSON.stringify(window.__fetches)").String(); !strings.Contains(got, "/api/dry-run-result") {
		t.Errorf("the verdict was not reported to /api/dry-run-result: %s", got)
	}
	if got := rt.eval("window.__fetches.length ? window.__fetches[0].body.dryRunId : -1").String(); got != "7" {
		t.Errorf("reported dryRunId = %v, want 7: a verdict about a different dry-run is worse than none", got)
	}
	if got := rt.eval("window.__fetches[0].body.ok").String(); got != "true" {
		t.Errorf("a candidate that evaluated cleanly reported ok=%v", got)
	}
	if got := rt.eval("window.__fetches[0].body.stats.haps").String(); got != "3" {
		t.Errorf("reported haps = %v, want 3 collected from the validated pattern", got)
	}
}

// TestDryRunReportsAFailingCandidate is the case the feature exists for: a
// broken candidate must come back ok:false with the browser's own message, so an
// agent can refuse to publish it.
func TestDryRunReportsAFailingCandidate(t *testing.T) {
	rt := newDryRunRuntime(t)
	rt.run(`window.__sandboxBehaviour = { evalError: "Unexpected token '}'" };`)

	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
		kind: "dry-run",
		snapshot: { version: 1, code: 's("bd")' },
		dryRun: { id: 9, code: 's("bd"' },
	}), window.__sandbox, window.__live);`)
	rt.settle()

	if got := rt.eval("window.__fetches[0].body.ok").String(); got != "false" {
		t.Errorf("a syntax-error candidate reported ok=%v, want false", got)
	}
	if got := rt.eval("window.__fetches[0].body.error").String(); !strings.Contains(got, "Unexpected token") {
		t.Errorf("reported error = %v, want the browser's own message", got)
	}
}

// TestDryRunNeverCommitsToTheLiveRepl is THE safety property. The candidate is
// unproven by definition, so it must never be hot-swapped into the live repl, the
// editor must not be overwritten with it, and the tracked version must not move.
//
// Each is separately observable: setPatternCalls records an audio commit, and a
// lastVersion that advanced would make the NEXT real code frame look stale.
func TestDryRunNeverCommitsToTheLiveRepl(t *testing.T) {
	rt := newDryRunRuntime(t)
	rt.run(`window.__sandboxBehaviour = { haps: [1, 2, 3] };`)

	// Establish a known version first, so "unchanged" means something.
	rt.run(`window.strudelSession.applyVersion({ version: 4, code: 's("bd")', anchor: null }, window.__sandbox, window.__live);`)
	rt.settle()
	before := rt.eval("String(window.strudelSession.getLastVersion())").String()
	if before != "4" {
		t.Fatalf("harness setup: lastVersion = %v, want 4", before)
	}
	rt.run(`window.__setPatternCalls = [];`)

	// A candidate that evaluates perfectly. If anything here reaches the live
	// repl, it is this one.
	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
		kind: "dry-run",
		snapshot: { version: 4, code: 's("bd")' },
		dryRun: { id: 11, code: 's("cp*8").gain(0.9)' },
	}), window.__sandbox, window.__live);`)
	rt.settle()

	if got := rt.eval("String(window.__setPatternCalls.length)").String(); got != "0" {
		t.Errorf("the live repl was hot-swapped %v time(s) for a dry-run candidate; the music changed for code that was never published", got)
	}
	if got := rt.eval("String(window.strudelSession.getLastVersion())").String(); got != before {
		t.Errorf("lastVersion moved %s -> %s on a dry-run frame; the next real code frame would then be skipped as stale", before, got)
	}
	if got := rt.eval("String(window.strudelSession.getCurrentPattern() === null)").String(); got != "true" {
		t.Errorf("a dry-run candidate became the current pattern; the unproven document is now what the visualiser shows")
	}
	// The candidate must still have been evaluated — otherwise "nothing happened"
	// would pass for the wrong reason, which is the defect this test could hide.
	// The count is read AFTER the setup applyVersion above, so it includes that
	// one call: the claim is that exactly one MORE evaluation happened.
	if got := rt.eval("String(window.__sandboxCalls.length)").String(); got != "2" {
		t.Errorf("the sandbox evaluated %v candidates in total, want 2 (the setup version plus the dry-run): nothing was validated at all", got)
	}
}

// TestDryRunDoesNotStealTheNextCodeFrame is the regression the guard above
// protects. A dry-run frame in between must not make the real code frame that
// follows look already-seen, because then a genuine new version would be skipped
// and never reach the repl at all.
func TestDryRunDoesNotStealTheNextCodeFrame(t *testing.T) {
	rt := newDryRunRuntime(t)
	rt.run(`window.__sandboxBehaviour = { haps: [] };`)

	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
		kind: "dry-run",
		snapshot: { version: 1, code: 's("bd")' },
		dryRun: { id: 1, code: 's("cp")' },
	}), window.__sandbox, window.__live);`)
	rt.settle()

	// Now a REAL new version arrives. It must be applied.
	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
		kind: "code",
		snapshot: { version: 2, code: 's("bd*2")', anchor: null },
	}), window.__sandbox, window.__live);`)
	rt.settle()

	if got := rt.eval("String(window.__fetches.filter(function (f) { return f.path === '/api/eval-result'; }).length)").String(); got != "1" {
		t.Errorf("the real code frame after a dry-run produced %v eval-result reports, want 1: the dry-run consumed it", got)
	}
	if got := rt.eval("String(window.strudelSession.getLastVersion())").String(); got != "2" {
		t.Errorf("lastVersion = %v after the real code frame, want 2", got)
	}
}
