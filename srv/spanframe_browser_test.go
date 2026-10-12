package srv

// Coordinate frame of the sounding-token highlight (issue strudel-agent-pnn.5).
//
// strudel-agent-pnn.4 proved on the served page that the highlight lights the
// WRONG characters: for s("bd sd hh cp") the live haps carry locations
// [[1,3],[4,6],[7,9],[10,12]] while the document-true spans are
// [[3,5],[6,8],[9,11],[12,14]] — a constant string-literal quote offset. The
// mechanism (per-frame activeSpans, the markText actuator, clear-on-empty) is
// correct; the spans sit in the wrong coordinate frame because buildSandbox
// evaluates WITHOUT the bundle's transpiler, and only the transpiled
// evaluation path reports document-relative locations.
//
// These tests EXECUTE the served session.js + editor.js + viz.js in goja. The
// only stub is the @strudel/web bundle itself, which cannot load headless (the
// page takes it from a CDN): the fake models the bundle's two evaluation paths
// exactly as the pinned bundle's source behaves — with a transpiler the
// pattern's locations are document-relative and state.miniLocations is
// populated; without one the locations are mini-relative and miniLocations is
// empty. A test that only asserted "a mark appeared" would pass on the
// defect; every assertion below names the SPAN, so red means "wrong frame",
// not "absent feature".

import (
	"encoding/json"
	"strings"
	"testing"
)

// spanFrameBundlePrelude is a faithful two-path model of the pinned bundle's
// repl factory: the SAME evaluate entry point reports document-relative
// locations when the repl was constructed with a transpiler and mini-relative
// ones when it was not — which is what `this.transpiler ? ... : {output: t}`
// plus `miniLocations: Le?.miniLocations || []` do in the real source.
//
// The document is s("bd"): the token sits at document offsets [3,5) and at
// mini-relative offsets [1,3), so the two frames are distinguishable by value.
const spanFrameBundlePrelude = `
window.__replOpts = null;
function __makeSpanHap(s, e) {
  return {
    whole: { begin: 0, end: 1 },
    part: { begin: 0, end: 1 },
    value: { s: "bd" },
    context: { locations: [{ start: s, end: e }] },
    isActive: function (t) { return 0 <= t && t < 1; },
  };
}
window.__fakeStrudel = {
  transpiler: function (code) {
    return { output: code, miniLocations: [[3, 5]], widgets: [] };
  },
  repl: function (opts) {
    window.__replOpts = opts;
    var st = {};
    return {
      evaluate: function (code) {
        if (opts.transpiler) {
          st.miniLocations = opts.transpiler(code).miniLocations;
          return Promise.resolve({ queryArc: function () { return [__makeSpanHap(3, 5)]; } });
        }
        st.miniLocations = [];
        return Promise.resolve({ queryArc: function () { return [__makeSpanHap(1, 3)]; } });
      },
      state: st,
    };
  },
};
`

// newSpanFrameRuntime loads the full served client stack the highlight spans:
// session.js (validate-then-commit) under the session prelude, then the fake
// DOM/canvas surface viz.js needs, then the REAL served editor.js and viz.js.
// The viz prelude redeclares console/document (legal var redeclaration); the
// session module looks both up dynamically, so nothing it captured goes stale.
func newSpanFrameRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, sessionJSPrelude)
	rt.run(servedSyncJS(t))
	rt.run(servedSessionJS(t))
	rt.run(vizJSPrelude)
	rt.run(highlightEditorPrelude)
	rt.run(servedEditorJS(t))
	rt.run(servedVizJS(t))
	if got := rt.eval("typeof window.strudelEditor").String(); got != "object" {
		t.Fatalf("window.strudelEditor = %s, want object: served editor.js did not install its API", got)
	}
	if got := rt.eval("typeof window.strudelSession").String(); got != "object" {
		t.Fatalf("window.strudelSession = %s, want object: served session.js did not install its API", got)
	}
	return rt
}

// commitDocumentTruePattern builds the sandbox through the SERVED buildSandbox
// and commits version 1 ('s("bd")', no usable anchor so the commit lands
// immediately) with a live repl whose setPattern returns a real promise, so
// commitNow's await drains under settle and the live pattern is actually set.
func commitDocumentTruePattern(rt *syncRuntime) {
	rt.t.Helper()
	rt.run(spanFrameBundlePrelude)
	rt.run(`
window.__live = {
  setPattern: function (p, autostart) {
    window.__setPatternCalls.push({ at: Date.now(), autostart: autostart });
    return Promise.resolve(p);
  },
};
window.__sandbox = window.strudelSession.buildSandbox(window.__fakeStrudel);
window.strudelSession.applyVersion(
  { version: 1, code: 's("bd")', anchor: null },
  window.__sandbox, window.__live);
`)
	rt.settle()
	rt.settle()
}

// spanFrameLocations reads the live pattern's hap locations back through the
// served getCurrentPattern accessor: the spans viz.js will mark.
func spanFrameLocations(rt *syncRuntime) [][]map[string]float64 {
	rt.t.Helper()
	raw := rt.eval(`JSON.stringify(window.strudelSession.getCurrentPattern().queryArc(0, 1).map(function (h) {
  return h.context.locations;
}))`).String()
	var out [][]map[string]float64
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		rt.t.Fatalf("decoding live hap locations %s: %v", raw, err)
	}
	return out
}

// TestSandboxBuildPassesTranspiler: the sandbox must be constructed WITH the
// bundle's transpiler (and keep its silent clock and discarded output), or it
// evaluates on the raw path and every span it produces is mini-relative.
func TestSandboxBuildPassesTranspiler(t *testing.T) {
	rt := newSyncRuntime(t, sessionJSPrelude)
	rt.run(servedSessionJS(t))
	rt.run(spanFrameBundlePrelude)
	rt.run(`window.__sandbox = window.strudelSession.buildSandbox(window.__fakeStrudel);`)

	if got := rt.eval("window.__replOpts.transpiler === window.__fakeStrudel.transpiler").String(); got != "true" {
		t.Errorf("sandbox repl was not constructed with the bundle transpiler: without it evaluate takes the raw path and every hap location is mini-relative, not document-true")
	}
	for _, name := range []string{"getTime", "defaultOutput"} {
		if got := rt.eval("typeof window.__replOpts." + name).String(); got != "function" {
			t.Errorf("sandbox repl option %s = %s, want function: the transpiler must extend the silent sandbox, not replace it", name, got)
		}
	}
}

// TestSandboxBuildWithoutTranspilerWarns: on a bundle shape without a
// transpiler the sandbox still builds (boot must not crash), but it must say
// so loudly — a silent fallback reintroduces the misplacement silently.
func TestSandboxBuildWithoutTranspilerWarns(t *testing.T) {
	rt := newSyncRuntime(t, sessionJSPrelude)
	rt.run(servedSessionJS(t))
	rt.run(spanFrameBundlePrelude)
	rt.run(`
window.__warns = [];
console.warn = function (m) { window.__warns.push(String(m)); };
window.__sandbox = window.strudelSession.buildSandbox({ repl: window.__fakeStrudel.repl });
`)

	if got := rt.eval("typeof window.__sandbox.evaluate").String(); got != "function" {
		t.Fatalf("sandbox without transpiler did not build: the fallback must degrade, not throw")
	}
	if got := rt.eval(`"transpiler" in window.__replOpts`).String(); got != "false" {
		t.Errorf("sandbox repl options carry a transpiler key on a bundle without one: the fallback must evaluate raw, not pass an undefined transpiler")
	}
	if got := rt.eval("JSON.stringify(window.__warns)").String(); !strings.Contains(got, "transpiler") {
		t.Errorf("no transpiler warning recorded (%s): a sandbox evaluating raw while claiming nothing is the silent misplacement", got)
	}
}

// TestCommittedPatternCarriesDocumentTrueSpans: the pattern commitNow
// hot-swaps into the live repl must carry the DOCUMENT spans [3,5) — the same
// spans the sandbox reports in state.miniLocations — not the mini-relative
// [1,3) the raw path produces.
func TestCommittedPatternCarriesDocumentTrueSpans(t *testing.T) {
	rt := newSpanFrameRuntime(t)
	commitDocumentTruePattern(rt)

	locs := spanFrameLocations(rt)
	if len(locs) != 1 || len(locs[0]) != 1 || locs[0][0]["start"] != 3 || locs[0][0]["end"] != 5 {
		t.Errorf("live hap locations = %v, want [[{3 5}]]: the committed pattern sits in the sandbox-relative frame, so viz would mark as though mini offsets were document offsets", locs)
	}
	if got := rt.eval("JSON.stringify(window.__sandbox.state.miniLocations)").String(); got != "[[3,5]]" {
		t.Errorf("sandbox state.miniLocations = %s, want [[3,5]]: only the transpiled path populates document-true spans", got)
	}
}

// TestActiveMarkLandsOnDocumentSpan: one full frame against the committed
// pattern must outline the DOCUMENT token [3,5) — the mark the user sees. A
// mark at [1,3) is the defect: the outline on the wrong characters.
func TestActiveMarkLandsOnDocumentSpan(t *testing.T) {
	rt := newSpanFrameRuntime(t)
	requireHighlightActuator(rt)
	commitDocumentTruePattern(rt)

	rt.run(`window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } }); window.__setNow(200); window.__runOneFrame();`)
	marks := liveHighlightMarks(rt)
	if len(marks) != 1 {
		t.Fatalf("live marks = %+v, want exactly 1: the sounding token must be outlined", marks)
	}
	if marks[0].From.Ch != 3 || marks[0].To.Ch != 5 {
		t.Errorf("marked range = %d..%d, want 3..5: the outline sits in the sandbox-relative frame instead of on the document token", marks[0].From.Ch, marks[0].To.Ch)
	}
}
