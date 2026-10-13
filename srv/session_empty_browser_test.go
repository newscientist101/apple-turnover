package srv

import (
	"testing"
)

// The empty document reports nothing (strudel-agent-dbo.1.1), executed as
// served.
//
// Every page load against a freshly restarted (version-0) server drove a
// catch-up snapshot carrying version 0 / empty code through applyVersion,
// whose empty-document path POSTed a verdict for version 0 to
// /api/eval-result. There is no version 0 (versions start at 1) and nothing
// was evaluated, so Conductor.RecordEvalResult rightly refused it with 400 —
// once per listener on every fresh-boot page load.
//
// So these tests drive the SERVED session.js in goja and assert on the
// recorded fetches: NO /api/eval-result POST for a version-0/empty snapshot,
// while a real version still reports.
const emptyDocPrelude = sessionJSPrelude + `
window.__fetches = [];
var fetch = function (path, init) {
  window.__fetches.push({ path: path, body: init && init.body ? JSON.parse(init.body) : null });
  return apiStub();
};
window.__sandboxCalls = [];
window.__sandboxBehaviour = null;
function makeEmptySandbox() {
  return {
    evaluate: function (code) {
      window.__sandboxCalls.push(code);
      var b = window.__sandboxBehaviour;
      if (b && b.throws) { throw new Error(b.throws); }
      var pattern = { queryArc: function () { return (b && b.haps) || []; } };
      return Promise.resolve(pattern);
    },
    get state() {
      var b = window.__sandboxBehaviour;
      return b && b.evalError ? { evalError: new Error(b.evalError) } : {};
    },
  };
}
`

// newEmptyDocRuntime loads the served session.js with the recording fetch in
// place.
func newEmptyDocRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, emptyDocPrelude)
	rt.run(servedSyncJS(t))
	rt.run(servedSessionJS(t))
	rt.run(`window.__live = makeLive(); window.__sandbox = makeEmptySandbox();`)
	return rt
}

// TestEmptyDocumentReportsNoVerdict is the regression test for the
// version-0 verdict 400: a catch-up snapshot carrying version 0 / empty code
// (a fresh boot, where lastVersion is still -1) must produce NO POST to
// /api/eval-result, because nothing was evaluated and there is no version 0
// to report about.
func TestEmptyDocumentReportsNoVerdict(t *testing.T) {
	rt := newEmptyDocRuntime(t)

	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
		kind: "code",
		snapshot: { version: 0, code: "" },
	}), window.__sandbox, window.__live);`)
	rt.settle()

	evals := bodiesTo(t, rt, "/api/eval-result")
	if len(evals) != 0 {
		t.Fatalf("the browser made %d POST(s) to /api/eval-result for a version-0/empty snapshot, want none: nothing was evaluated and there is no version 0, so any verdict here is refused with 400 (%v)", len(evals), evals)
	}
	if got := rt.eval("window.__sandboxCalls.length").String(); got != "0" {
		t.Errorf("the sandbox evaluated %s snippet(s) for an empty document, want none: an empty document has nothing to validate", got)
	}
	if got := rt.eval("window.__setPatternCalls.length").String(); got != "0" {
		t.Errorf("the live repl was hot-swapped %s time(s) for an empty document, want none: the playing pattern keeps playing untouched", got)
	}
}

// TestRealVersionStillReportsVerdict guards the fix against overcorrection:
// skipping the empty-document post must not silence real verdicts. A genuine
// version with code still evaluates and reports exactly one verdict.
func TestRealVersionStillReportsVerdict(t *testing.T) {
	rt := newEmptyDocRuntime(t)

	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
		kind: "code",
		snapshot: { version: 1, code: 's("bd")' },
	}), window.__sandbox, window.__live);`)
	rt.settle()

	evals := bodiesTo(t, rt, "/api/eval-result")
	if len(evals) != 1 {
		t.Fatalf("the browser made %d POST(s) to /api/eval-result for version 1, want exactly 1: a real version was evaluated and its verdict is the agent's feedback loop (%v)", len(evals), evals)
	}
	if v, ok := evals[0]["version"].(float64); !ok || v != 1 {
		t.Errorf("reported version = %v (%T), want 1: the verdict must name the version that was actually evaluated", evals[0]["version"], evals[0]["version"])
	}
	if ok, present := evals[0]["ok"].(bool); !present || !ok {
		t.Errorf("reported ok = %v (%T), want true: code that evaluated cleanly must not come back failing", evals[0]["ok"], evals[0]["ok"])
	}
}
