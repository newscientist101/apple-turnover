package srv

// Active-element highlight, executed as served (issue strudel-agent-pnn.3).
//
// During a performance the read-only CodeMirror view must outline every
// mini-notation token that is sounding at the current instant, and clear the
// outline the instant it stops sounding. The data source is the same `haps`
// array viz.js already queries per frame, filtered by `hap.isActive(t)` with
// spans from `hap.context.locations`; the clock is the anchor-derived cycle
// the lanes and playhead already use, so the three can never disagree.
//
// These tests EXECUTE the served editor.js + viz.js in goja against a fake
// DOM/CM/clock, for the same reason viz_browser_test.go runs the served
// viz.js: grepping the source for a class name would pass on a module that
// marks nothing. A fake CodeMirror 5 records markText calls (range + class
// string) and returns clearable handles, so every assertion below is about
// marks actually placed and actually cleared.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// highlightEditorPrelude is the host surface editor.js needs on top of
// vizJSPrelude (which already brings window, the fake clock, the canvas and
// the captured rAF): a fake textarea under id "editor" and a fake
// CodeMirror 5 whose markText records every mark and whose setValue drops
// all marks with the document, like the real one.
const highlightEditorPrelude = `
window.__cmMarks = [];
window.__lineClasses = [];
window.__liveMarks = function () {
  return window.__cmMarks.filter(function (m) { return !m.cleared; });
};
window.CodeMirror = {
  version: "5.65.16",
  fromTextArea: function (el, opts) {
    var cm = {
      __doc: (el && typeof el.value === "string") ? el.value : "",
      getValue: function () { return cm.__doc; },
      setValue: function (v) {
        cm.__doc = String(v);
        window.__cmMarks.forEach(function (m) { m.cleared = true; });
        window.__cmMarks.length = 0;
      },
      posFromIndex: function (i) { return { line: 0, ch: i }; },
      markText: function (from, to, options) {
        var rec = { from: from, to: to, options: options || {}, cleared: false };
        window.__cmMarks.push(rec);
        return { clear: function () { rec.cleared = true; } };
      },
      hasFocus: function () { return false; },
      lineCount: function () { return 1; },
      addLineClass: function (line, where, cls) { window.__lineClasses.push({ line: line, cls: cls }); },
      removeLineClass: function () {},
      getWrapperElement: function () { return null; },
      setOption: function () {},
    };
    window.__cm = cm;
    return cm;
  },
};
window.__fakeTextarea = { value: 's("bd sd")' };
document.getElementById = function (id) {
  if (id === "canvas") { return __canvas; }
  if (id === "editor") { return window.__fakeTextarea; }
  return null;
};
`

// servedEditorJS fetches the bytes the server actually serves for editor.js,
// through the real route tree, so a module that is not served cannot pass.
func servedEditorJS(t *testing.T) string {
	t.Helper()
	server := New()
	req := httptest.NewRequest(http.MethodGet, "/static/editor.js", nil)
	w := httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/editor.js status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET /static/editor.js served as JSON (%q): the API layer swallowed a static asset", ct)
	}
	js := w.Body.String()
	if len(js) == 0 {
		t.Fatal("GET /static/editor.js returned an empty body")
	}
	return js
}

// servedStyleCSS fetches the bytes the server actually serves for style.css.
func servedStyleCSS(t *testing.T) string {
	t.Helper()
	server := New()
	req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	w := httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css status = %d, want 200", w.Code)
	}
	return w.Body.String()
}

// newHighlightRuntime loads the fake DOM, then the REAL served editor.js
// (which boots into CodeMirror mode against the fake), then the REAL served
// viz.js on top of it — the two modules the highlight spans.
func newHighlightRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, vizJSPrelude)
	rt.run(highlightEditorPrelude)
	rt.run(servedEditorJS(t))
	rt.run(servedVizJS(t))
	if got := rt.eval("typeof window.strudelEditor").String(); got != "object" {
		t.Fatalf("window.strudelEditor = %s, want object: served editor.js did not install its API", got)
	}
	return rt
}

// highlightMark is one recorded CodeMirror mark: the placed range plus the
// class string it was marked with, plus whether its handle was cleared.
type highlightMark struct {
	From struct {
		Line int `json:"line"`
		Ch   int `json:"ch"`
	} `json:"from"`
	To struct {
		Line int `json:"line"`
		Ch   int `json:"ch"`
	} `json:"to"`
	Class   string `json:"cls"`
	Cleared bool   `json:"cleared"`
}

// liveHighlightMarks returns every mark the fake CodeMirror currently holds
// that has NOT been cleared — i.e. what the user would see outlined.
func liveHighlightMarks(rt *syncRuntime) []highlightMark {
	rt.t.Helper()
	raw := rt.eval(`JSON.stringify(window.__liveMarks().map(function (m) {
  return { from: m.from, to: m.to, cls: m.options.className, cleared: m.cleared };
}))`).String()
	var marks []highlightMark
	if err := json.Unmarshal([]byte(raw), &marks); err != nil {
		rt.t.Fatalf("decoding live marks %s: %v", raw, err)
	}
	if marks == nil {
		marks = []highlightMark{}
	}
	return marks
}

// allHighlightMarks returns every mark ever placed, cleared or not, so a
// test can prove a stale handle's clear() was actually invoked.
func allHighlightMarks(rt *syncRuntime) []highlightMark {
	rt.t.Helper()
	raw := rt.eval(`JSON.stringify(window.__cmMarks.map(function (m) {
  return { from: m.from, to: m.to, cls: m.options.className, cleared: m.cleared };
}))`).String()
	var marks []highlightMark
	if err := json.Unmarshal([]byte(raw), &marks); err != nil {
		rt.t.Fatalf("decoding all marks %s: %v", raw, err)
	}
	if marks == nil {
		marks = []highlightMark{}
	}
	return marks
}

// requireHighlightActuator fails unless the served editor.js exposes the
// highlight actuator the design names. Every test that drives marks goes
// through here so the red run fails with the reason, not a goja TypeError.
func requireHighlightActuator(rt *syncRuntime) {
	rt.t.Helper()
	for _, name := range []string{"setActiveSpans", "clearActiveMarks"} {
		if got := rt.eval("typeof window.strudelEditor." + name).String(); got != "function" {
			rt.t.Fatalf("window.strudelEditor.%s = %s, want function: served editor.js has no highlight actuator", name, got)
		}
	}
}

// installTwoHapPattern installs the design's canonical fake: hap A sounding
// on whole=[0,0.5) at code offsets {3,5}, hap B on whole=[0.5,1) at {6,8}.
// The window query returns BOTH haps regardless of time; only the isActive
// filter decides which one lights. Time is the anchor-derived cycle with
// anchor {epochMs:0, cps:0.5}, so __now=200 is cycle 0.1 and __now=1000 is
// cycle 0.5.
func installTwoHapPattern(rt *syncRuntime) {
	rt.t.Helper()
	rt.run(`
  window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } });
  window.strudelViz.setPattern({
    queryArc: function (begin, end) {
      function hap(b, e, s, e2) {
        return {
          whole: { begin: b, end: e },
          part: { begin: b, end: e },
          value: { s: "bd" },
          context: { locations: [{ start: s, end: e2 }] },
          isActive: function (t) { return b <= t && t < e; },
        };
      }
      return [hap(0, 0.5, 3, 5), hap(0.5, 1, 6, 8)];
    },
  });
`)
}

// TestHighlightActiveHapMarksItsToken: at cycle 0.1 hap A sounds, so exactly
// its token offsets must be outlined with the design's class string.
func TestHighlightActiveHapMarksItsToken(t *testing.T) {
	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	installTwoHapPattern(rt)
	rt.run(`window.__setNow(200); window.__runOneFrame();`)

	marks := liveHighlightMarks(rt)
	if len(marks) != 1 {
		t.Fatalf("live marks = %+v, want exactly 1: the sounding hap's token must be outlined", marks)
	}
	m := marks[0]
	if m.From.Ch != 3 || m.To.Ch != 5 {
		t.Errorf("marked range = %d..%d, want 3..5: the outline must cover hap A's locations, not a fixed window", m.From.Ch, m.To.Ch)
	}
	if m.Class != "editor-active-token" {
		t.Errorf("marked class = %q, want %q: the outline must carry the design's class", m.Class, "editor-active-token")
	}
}

// TestHighlightOnlyTheActiveTokenMarks: the window query returns both haps
// but only A is active at 0.1, so one mark — not two — must exist. At the
// exact boundary 0.5 the half-open window flips: A goes dark, B lights.
func TestHighlightOnlyTheActiveTokenMarks(t *testing.T) {
	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	installTwoHapPattern(rt)
	rt.run(`window.__setNow(200); window.__runOneFrame();`)

	if marks := liveHighlightMarks(rt); len(marks) != 1 {
		t.Fatalf("live marks at cycle 0.1 = %+v, want exactly 1: every window hap must not light", marks)
	}

	rt.run(`window.__setNow(1000); window.__runOneFrame();`)
	marks := liveHighlightMarks(rt)
	if len(marks) != 1 {
		t.Fatalf("live marks at cycle 0.5 = %+v, want exactly 1 (hap B): the half-open window must flip at the boundary", marks)
	}
	if marks[0].From.Ch != 6 || marks[0].To.Ch != 8 {
		t.Errorf("marked range at cycle 0.5 = %d..%d, want 6..8: hap A must already be dark while B lights", marks[0].From.Ch, marks[0].To.Ch)
	}
}

// TestHighlightMarkClearsWhenHapEnds: advancing past A's end must clear A's
// handle and leave only B marked — marks are recomputed per frame, never
// appended.
func TestHighlightMarkClearsWhenHapEnds(t *testing.T) {
	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	installTwoHapPattern(rt)
	rt.run(`window.__setNow(200); window.__runOneFrame();`)

	before := liveHighlightMarks(rt)
	if len(before) != 1 {
		t.Fatalf("live marks at cycle 0.1 = %+v, want exactly 1 before advancing", before)
	}

	rt.run(`window.__setNow(1200); window.__runOneFrame();`)
	after := allHighlightMarks(rt)
	clearedA := false
	for _, m := range after {
		if m.From.Ch == 3 && m.To.Ch == 5 && m.Cleared {
			clearedA = true
		}
	}
	if !clearedA {
		t.Errorf("hap A's mark was never cleared after its hap ended: marks would accumulate forever (marks = %+v)", after)
	}
	live := liveHighlightMarks(rt)
	if len(live) != 1 || live[0].From.Ch != 6 || live[0].To.Ch != 8 {
		t.Errorf("live marks at cycle 0.6 = %+v, want exactly hap B's 6..8", live)
	}
}

// TestHighlightNoPatternClears: with no pattern (and with a throwing
// queryArc) the frame must clear stale marks, draw lanes as today, and never
// throw.
func TestHighlightNoPatternClears(t *testing.T) {
	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	installTwoHapPattern(rt)
	rt.run(`window.__setNow(200); window.__runOneFrame();`)
	if marks := liveHighlightMarks(rt); len(marks) != 1 {
		t.Fatalf("live marks at cycle 0.1 = %+v, want exactly 1 before removing the pattern", marks)
	}

	rt.run(`window.strudelViz.setPattern(null); window.__runOneFrame();`)
	if marks := liveHighlightMarks(rt); len(marks) != 0 {
		t.Errorf("live marks with no pattern = %+v, want none: an empty active set must clear all marks", marks)
	}
	if labels := rt.eval("JSON.stringify(window.__ctxCalls.fillText.map(function (t) { return t.text; }))").String(); !strings.Contains(labels, "other") {
		t.Errorf("lane labels with no pattern = %s, want the fallback lane: lanes must still draw", labels)
	}

	rt.run(`window.strudelViz.setPattern({ queryArc: function () { throw new Error("boom"); } }); window.__runOneFrame();`)
	if marks := liveHighlightMarks(rt); len(marks) != 0 {
		t.Errorf("live marks with throwing queryArc = %+v, want none", marks)
	}
}

// TestHighlightNoAnchorFallbackFollowsLanes: with no snapshot the highlight
// must follow the same fallback-epoch path the lanes use, so the two stay
// mutually consistent.
func TestHighlightNoAnchorFallbackFollowsLanes(t *testing.T) {
	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	// No onSnapshot call: the first frame seeds the fallback epoch from
	// Date.now, and with __now starting at 0 the fallback cycle is 0 —
	// inside hap A.
	rt.run(`
  window.strudelViz.setPattern({
    queryArc: function (begin, end) {
      return [{
        whole: { begin: 0, end: 0.5 },
        part: { begin: 0, end: 0.5 },
        value: { s: "bd" },
        context: { locations: [{ start: 3, end: 5 }] },
        isActive: function (t) { return 0 <= t && t < 0.5; },
      }];
    },
  });
  window.__runOneFrame();
`)
	marks := liveHighlightMarks(rt)
	if len(marks) != 1 || marks[0].From.Ch != 3 || marks[0].To.Ch != 5 {
		t.Errorf("live marks on the fallback clock = %+v, want hap A's 3..5: highlight and lanes must share the clock", marks)
	}
}

// TestHighlightTextareaFallbackStaysQuiet: with no CodeMirror the actuator
// and the clearer must be silent no-ops that leave the textarea alone.
func TestHighlightTextareaFallbackStaysQuiet(t *testing.T) {
	rt := newSyncRuntime(t, vizJSPrelude)
	rt.run(`
document.getElementById = function (id) {
  if (id === "canvas") { return __canvas; }
  if (id === "editor") { return window.__plainTextarea || (window.__plainTextarea = { value: 's("bd sd")' }); }
  return null;
};
`)
	rt.run(servedEditorJS(t))
	if got := rt.eval("typeof window.strudelEditor").String(); got != "object" {
		t.Fatalf("window.strudelEditor = %s, want object", got)
	}
	for _, name := range []string{"setActiveSpans", "clearActiveMarks"} {
		if got := rt.eval("typeof window.strudelEditor." + name).String(); got != "function" {
			t.Fatalf("window.strudelEditor.%s = %s, want function even in textarea mode", name, got)
		}
	}
	rt.run(`window.strudelEditor.setActiveSpans([{ start: 3, end: 5 }]); window.strudelEditor.clearActiveMarks();`)
	if got := rt.eval("window.__plainTextarea.value").String(); got != `s("bd sd")` {
		t.Errorf("textarea value = %q, want it untouched: the fallback shows no highlight", got)
	}
}

// TestHighlightLocationlessHapSkipped: an active hap with no locations
// (non-mini JS structure) is not an element — it must never mark, never
// throw, and never hide the markable hap beside it.
func TestHighlightLocationlessHapSkipped(t *testing.T) {
	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	rt.run(`
  window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } });
  window.strudelViz.setPattern({
    queryArc: function (begin, end) {
      return [
        {
          whole: { begin: 0, end: 0.5 },
          value: { gain: 0.8 },
          context: {},
          isActive: function (t) { return 0 <= t && t < 0.5; },
        },
        {
          whole: { begin: 0, end: 0.5 },
          value: { s: "bd" },
          context: { locations: [{ start: 3, end: 5 }] },
          isActive: function (t) { return 0 <= t && t < 0.5; },
        },
      ];
    },
  });
  window.__setNow(200); window.__runOneFrame();
`)
	marks := liveHighlightMarks(rt)
	if len(marks) != 1 || marks[0].From.Ch != 3 || marks[0].To.Ch != 5 {
		t.Errorf("live marks with a location-less active hap = %+v, want only hap A's 3..5", marks)
	}
}

// TestHighlightStackedHapsAllLight: two co-sounding haps light two tokens;
// two co-sounding haps sharing one span dedup to a single mark.
func TestHighlightStackedHapsAllLight(t *testing.T) {
	stacked := func(locs string) string {
		return `
  window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } });
  window.strudelViz.setPattern({
    queryArc: function (begin, end) {
      function hap(locs) {
        return {
          whole: { begin: 0, end: 1 },
          part: { begin: 0, end: 1 },
          value: { s: "bd" },
          context: { locations: locs },
          isActive: function (t) { return 0 <= t && t < 1; },
        };
      }
      return [hap(` + locs + `), hap(` + locs + `)];
    },
  });
  window.__setNow(200); window.__runOneFrame();
`
	}

	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	rt.run(stacked(`[{ start: 3, end: 5 }]`))
	if marks := liveHighlightMarks(rt); len(marks) != 1 {
		t.Errorf("live marks for two haps sharing one span = %+v, want exactly 1: colliding spans dedup", marks)
	}

	rt2 := newHighlightRuntime(t)
	requireHighlightActuator(rt2)
	rt2.run(`
  window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } });
  window.strudelViz.setPattern({
    queryArc: function (begin, end) {
      function hap(b, e, s, e2) {
        return {
          whole: { begin: 0, end: 1 },
          part: { begin: 0, end: 1 },
          value: { s: "bd" },
          context: { locations: [{ start: s, end: e2 }] },
          isActive: function (t) { return 0 <= t && t < 1; },
        };
      }
      return [hap(0, 1, 3, 5), hap(0, 1, 6, 8)];
    },
  });
  window.__setNow(200); window.__runOneFrame();
`)
	if marks := liveHighlightMarks(rt2); len(marks) != 2 {
		t.Errorf("live marks for two stacked haps = %+v, want 2: every sounding token lights", marks)
	}
}

// TestHighlightVersionChangeClears: replacing the document must clear
// already-placed marks so no stale outline survives onto the new version.
func TestHighlightVersionChangeClears(t *testing.T) {
	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	installTwoHapPattern(rt)
	rt.run(`window.__setNow(200); window.__runOneFrame();`)
	if marks := liveHighlightMarks(rt); len(marks) != 1 {
		t.Fatalf("live marks at cycle 0.1 = %+v, want exactly 1 before replacing the document", marks)
	}

	rt.run(`window.strudelEditor.setCode('s("hh")');`)
	for _, m := range allHighlightMarks(rt) {
		if !m.Cleared {
			t.Errorf("mark %+v survived setCode: a replaced document must drop its outlines", m)
		}
	}
	if marks := liveHighlightMarks(rt); len(marks) != 0 {
		t.Errorf("live marks after setCode = %+v, want none", marks)
	}
	// The equal-text path never reaches setValue (the fake CM drops marks
	// with the document, like the real one), so only the accessor's
	// explicit clear can darken it: re-mark, then setCode the identical
	// document and require darkness.
	rt.run(`window.__setNow(200); window.__runOneFrame();`)
	if marks := liveHighlightMarks(rt); len(marks) != 1 {
		t.Fatalf("live marks re-lit at cycle 0.1 = %+v, want exactly 1 before the equal-text setCode", marks)
	}
	rt.run(`window.strudelEditor.setCode(window.strudelEditor.currentCode());`)
	if marks := liveHighlightMarks(rt); len(marks) != 0 {
		t.Errorf("live marks after equal-text setCode = %+v, want none: the accessor must clear explicitly, not rely on setValue", marks)
	}
}

// TestHighlightMalformedSpansNeverThrow: garbage spans must never kill the
// frame loop — invalid ones are skipped, partially out-of-range ones clamp
// to the document.
func TestHighlightMalformedSpansNeverThrow(t *testing.T) {
	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	// The fake document is s("bd sd"), length 10: only the clamped span
	// survives, pinned to 0..10.
	rt.run(`window.strudelEditor.setActiveSpans([{ start: 99, end: 2 }, { start: -5, end: 5000 }, null, "x"]);`)
	marks := liveHighlightMarks(rt)
	if len(marks) != 1 {
		t.Fatalf("live marks for malformed spans = %+v, want exactly the one clamped mark", marks)
	}
	if marks[0].From.Ch != 0 || marks[0].To.Ch != 10 {
		t.Errorf("clamped mark = %d..%d, want 0..10: partially out-of-range spans clamp to the document", marks[0].From.Ch, marks[0].To.Ch)
	}
}

// TestHighlightOlderEditorWithoutAccessor: viz must feature-detect the
// actuator, so a frame against an editor.js that predates it draws lanes
// and never throws.
func TestHighlightOlderEditorWithoutAccessor(t *testing.T) {
	rt := newHighlightRuntime(t)
	installTwoHapPattern(rt)
	rt.run(`window.strudelEditor.setActiveSpans = undefined; window.__setNow(200); window.__runOneFrame();`)
	if labels := rt.eval("JSON.stringify(window.__ctxCalls.fillText.map(function (t) { return t.text; }))").String(); !strings.Contains(labels, "bd") {
		t.Errorf("lane labels = %s, want the bd lane: the frame must still draw without the actuator", labels)
	}
}

// TestHighlightErrorPathKeepsOwnClass: the eval-error highlight and the
// active outline are orthogonal — markError must keep its own line class
// and must not disturb active marks.
func TestHighlightErrorPathKeepsOwnClass(t *testing.T) {
	rt := newHighlightRuntime(t)
	requireHighlightActuator(rt)
	rt.run(`window.strudelEditor.markError("boom");`)
	if got := rt.eval("JSON.stringify(window.__lineClasses)").String(); !strings.Contains(got, "editor-error-line") {
		t.Errorf("line classes after markError = %s, want editor-error-line: the error path must keep its own class", got)
	}
	if got := rt.eval("JSON.stringify(window.__lineClasses)").String(); strings.Contains(got, "editor-active-token") {
		t.Errorf("line classes after markError = %s: the error path must not reuse the active class", got)
	}

	installTwoHapPattern(rt)
	rt.run(`window.__setNow(200); window.__runOneFrame();`)
	marks := liveHighlightMarks(rt)
	if len(marks) != 1 || marks[0].Class != "editor-active-token" {
		t.Errorf("live marks after markError + frame = %+v, want the active mark under its own class", marks)
	}

	// The error path must not steal the active handles either: marking an
	// error over a live outline leaves the outline in place (it is
	// recomputed next frame anyway; clearError must not clear it early).
	rt.run(`window.strudelEditor.markError("line 3 boom");`)
	if marks := liveHighlightMarks(rt); len(marks) != 1 || marks[0].From.Ch != 3 {
		t.Errorf("live marks after markError over a live outline = %+v, want hap A's mark undisturbed", marks)
	}
	rt.run(`window.strudelEditor.clearError();`)
	if marks := liveHighlightMarks(rt); len(marks) != 1 || marks[0].From.Ch != 3 {
		t.Errorf("live marks after clearError = %+v, want hap A's mark undisturbed: the error path owns no active handles", marks)
	}
}

// TestHighlightActiveSpansHelperPure: the served viz.js must expose the
// pure activeSpans(haps, now) helper the design names, so the derivation is
// unit-testable without stepping a frame.
func TestHighlightActiveSpansHelperPure(t *testing.T) {
	rt := newHighlightRuntime(t)
	if got := rt.eval("typeof window.strudelViz.activeSpans").String(); got != "function" {
		t.Fatalf("window.strudelViz.activeSpans = %s, want function: the served viz.js must expose the pure helper", got)
	}
	raw := rt.eval(`JSON.stringify(window.strudelViz.activeSpans([
  { whole: { begin: 0, end: 0.5 }, context: { locations: [{ start: 3, end: 5 }] }, isActive: function (t) { return 0 <= t && t < 0.5; } },
  { whole: { begin: 0.5, end: 1 }, context: { locations: [{ start: 6, end: 8 }] }, isActive: function (t) { return 0.5 <= t && t < 1; } },
  { whole: { begin: 0, end: 0.5 }, context: {}, isActive: function (t) { return 0 <= t && t < 0.5; } },
], 0.1))`).String()
	if raw != `[{"start":3,"end":5}]` {
		t.Errorf("activeSpans(haps, 0.1) = %s, want only hap A's span: inactive and location-less haps contribute nothing", raw)
	}
	// Without isActive the half-open whole window decides: a hap on
	// [0,0.5) is active at 0.1 and dark at 0.5.
	for _, tc := range []struct {
		now  string
		want string
	}{
		{"0.1", `[{"start":3,"end":5}]`},
		{"0.5", `[]`},
	} {
		got := rt.eval(`JSON.stringify(window.strudelViz.activeSpans([
  { whole: { begin: 0, end: 0.5 }, context: { locations: [{ start: 3, end: 5 }] } },
], ` + tc.now + `))`).String()
		if got != tc.want {
			t.Errorf("activeSpans(fallback hap, %s) = %s, want %s: the half-open fallback window decides without isActive", tc.now, got, tc.want)
		}
	}
	// isActive is authoritative when present: a hap whose method reports
	// sounding at 0.1 while its whole window sits at [5,6) must still
	// light. (Catches: ignoring isActive and keying off the whole window
	// or the lane/value instead of the sounding time.)
	got := rt.eval(`JSON.stringify(window.strudelViz.activeSpans([
  { whole: { begin: 5, end: 6 }, context: { locations: [{ start: 3, end: 5 }] }, isActive: function () { return true; } },
], 0.1))`).String()
	if got != `[{"start":3,"end":5}]` {
		t.Errorf("activeSpans(isActive-true hap, 0.1) = %s, want hap A's span: isActive decides, not the whole window", got)
	}
}

// TestHighlightStyleRuleServed: the served stylesheet must carry the
// outline rule the marks reference — a class string with no rule is an
// invisible feature.
func TestHighlightStyleRuleServed(t *testing.T) {
	css := servedStyleCSS(t)
	if !strings.Contains(css, "editor-active-token") {
		t.Errorf("style.css carries no editor-active-token rule: the outline would be invisible")
	}
}
