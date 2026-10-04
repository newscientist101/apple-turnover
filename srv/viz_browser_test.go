package srv

// The pattern visualization, executed as served (issue strudel-agent-uvj.2).
//
// The vis drove its hap lanes and playhead from currentPattern.queryArc() and
// that half works. Alongside it lived a waveform branch guarded by an
// audio-context lookup that could never resolve: viz.js reached for
// window.getAudioContext (a name the pinned @strudel/web@1.3.0 bundle never
// defines) and for repl.audioContext / repl.ctx / repl.webaudio (none of which
// the bundle's repl, class ZD, has). The branch was therefore dead, and the
// audio-context feature was never observed working — it is not a regression.
//
// The bundle does export drawTimeScope (so the FUNCTION was reachable), but its
// real signature is drawTimeScope(analyserNode, {ctx, id}): it reads an
// AnalyserNode registered by strudel.getAnalyserById(id), not a bare
// AudioContext. getAnalyserById only CREATES the analyser, never connects it to
// the output graph, so even a correctly wired call would draw a flat, silent
// line. Rather than trade a dead branch for a misleadingly-alive one, the
// unreachable waveform path is removed and the module is proven to render the
// pattern from the DOM alone.
//
// These tests EXECUTE the served viz.js in goja against a fake DOM, which is
// the only way to prove the renderer still works. Grepping the source for a
// marker would pass on a module that draws nothing — the same reason AGENTS.md
// requires browser timing to be proved by running the served JavaScript rather
// than re-deriving its arithmetic in Go.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// vizJSPrelude is the host surface a browser would give viz.js: a canvas in
// the DOM, a 2D context that records what it is asked to draw, a device pixel
// ratio, and a requestAnimationFrame that CAPTURES its callback instead of
// scheduling it (a real rAF re-arms forever; a headless harness must run
// exactly one frame on demand, and that also keeps the loop bounded).
//
// It deliberately models NOTHING about audio: the removed waveform path must
// not need any, and a missing name here would surface the moment the renderer
// reached for one.
const vizJSPrelude = `
// console/document/devicePixelRatio are NOT part of syncJSPrelude (they live in
// sessionJSPrelude because session.js needs them). viz.js needs them too, so it
// gets its own host here rather than inheriting one.
var console = { info: function () {}, warn: function () {}, error: function () {}, debug: function () {}, log: function () {} };
window.devicePixelRatio = 1;

var __ctxCalls = { fillRect: [], stroke: [], fillText: [] };
var __ctx2d = {
  fillStyle: "", strokeStyle: "", lineWidth: 1, font: "", textBaseline: "",
  canvas: null,
  save: function () {}, restore: function () {}, scale: function () {},
  beginPath: function () {}, moveTo: function () {}, lineTo: function () {},
  fillRect: function (x, y, w, h) { __ctxCalls.fillRect.push({ x: x, y: y, w: w, h: h }); },
  stroke: function () { __ctxCalls.stroke.push(1); },
  fillText: function (text, x, y) { __ctxCalls.fillText.push({ text: text, x: x, y: y }); },
};
var __canvas = {
  width: 0, height: 0,
  getContext: function () { return __ctx2d; },
  getBoundingClientRect: function () { return { width: 400, height: 160, top: 0, left: 0 }; },
};
__ctx2d.canvas = __canvas;

// viz.js reads the canvas from the document. Unlike sessionJSPrelude, this
// harness declares document itself; the module must not depend on a canvas
// that does not exist.
var document = {
  readyState: "complete",
  addEventListener: function () {},
  getElementById: function (id) {
    if (id === "canvas") { return __canvas; }
    return null;
  },
  activeElement: null,
};

window.__raf = null;
var requestAnimationFrame = function (cb) { window.__raf = cb; return 1; };
window.requestAnimationFrame = requestAnimationFrame;

window.__framesDrawn = 0;
window.__runOneFrame = function () {
  var cb = window.__raf;
  window.__raf = null;
  window.__framesDrawn += 1;
  cb();
};
window.__ctxCalls = __ctxCalls;
`

// servedVizJS fetches the bytes the server actually serves for viz.js, through
// the real route tree, so a module that is not served cannot pass.
func servedVizJS(t *testing.T) string {
	t.Helper()
	server := New()
	req := httptest.NewRequest(http.MethodGet, "/static/viz.js", nil)
	w := httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/viz.js status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET /static/viz.js served as JSON (%q): the API layer swallowed a static asset", ct)
	}
	js := w.Body.String()
	if len(js) == 0 {
		t.Fatal("GET /static/viz.js returned an empty body")
	}
	return js
}

// newVizRuntime loads the fake DOM, then the REAL served viz.js on top of it.
func newVizRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, vizJSPrelude)
	rt.run(servedVizJS(t))
	if got := rt.eval("window.__framesDrawn").ToInteger(); got != 0 {
		t.Fatalf("viz.js drew %d frame(s) at load: the loop must be armed, not run, until the harness steps it", got)
	}
	return rt
}

// TestVisualizationRendersHapLanesFromTheServedJS executes the served viz.js and
// proves the ACTUAL feature still works after the waveform branch is removed: a
// pattern with a sample hap and a note hap must produce two labelled lanes and
// drawn blocks, plus the shared playhead.
func TestVisualizationRendersHapLanesFromTheServedJS(t *testing.T) {
	rt := newVizRuntime(t)

	// The module's public hooks must exist and be callable, or session.js's
	// strudelViz.setPattern/onSnapshot would be inert.
	for _, name := range []string{"setPattern", "onSnapshot"} {
		if got := rt.eval("typeof window.strudelViz." + name).String(); got != "function" {
			t.Fatalf("window.strudelViz.%s = %s, want function: the visualization hooks are missing", name, got)
		}
	}

	// One sample hap and one note hap, both inside the two-cycle window the
	// renderer queries. They must land in SEPARATE lanes (value.s and
	// value.note respectively), which is what exercises the lane grouping.
	rt.run(`
	  window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } });
	  window.strudelViz.setPattern({
	    queryArc: function (begin, end) {
	      return [
	        { whole: { begin: 0.0, end: 0.5 }, part: { begin: 0.0, end: 0.5 }, value: { s: "bd" } },
	        { whole: { begin: 0.5, end: 1.0 }, part: { begin: 0.5, end: 1.0 }, value: { note: "c4" } },
	      ];
	    },
	  });
	  window.__runOneFrame();
	`)

	if got := rt.eval("window.__framesDrawn").ToInteger(); got != 1 {
		t.Fatalf("drew %d frame(s), want exactly 1: the harness stepped one frame", got)
	}

	// Lane labels come from value.s and value.note; both must be drawn.
	labels := rt.eval("JSON.stringify(window.__ctxCalls.fillText.map(function (t) { return t.text; }))").String()
	for _, want := range []string{"bd", "c4"} {
		if !strings.Contains(labels, want) {
			t.Errorf("lane labels = %s, want it to include %q: lanes are not grouped by value.s/value.note", labels, want)
		}
	}

	// The lane blocks are narrower and shorter than the full-canvas background
	// fill, so counting only those proves real blocks were painted rather than
	// the background alone.
	laneBlocks := rt.eval(`(function () {
	  var rect = __canvas.getBoundingClientRect();
	  return window.__ctxCalls.fillRect.filter(function (r) { return r.w < rect.width && r.h < rect.height; }).length;
	})()`).ToInteger()
	if laneBlocks < 2 {
		t.Errorf("painted %d lane block(s), want at least 2 (one per hap): the pattern is not being rendered", laneBlocks)
	}

	// Two lane separator lines plus the playhead.
	if strokes := rt.eval("window.__ctxCalls.stroke.length").ToInteger(); strokes < 3 {
		t.Errorf("stroked %d time(s), want at least 3 (lane separators + playhead)", strokes)
	}
}

// TestVisualizationNeverReachesForAnAudioContext proves the unreachable
// waveform path is gone BEHAVIOURALLY: with a bundle-shaped window.strudel and
// a repl whose audio context is running, a rendered frame must still call
// nothing. A regression that re-introduces the lookup would draw (and thus
// increment the counter) here.
func TestVisualizationNeverReachesForAnAudioContext(t *testing.T) {
	rt := newVizRuntime(t)

	rt.run(`
	  window.__scopeCalls = 0;
	  window.strudel = {
	    getAudioContext: function () { return { state: "running" }; },
	    getAnalyserById: function () { return { frequencyBinCount: 1024 }; },
	    drawTimeScope: function () { window.__scopeCalls += 1; },
	  };
	  window.repl = { audio: { audioCtx: { state: "running" } } };
	  window.strudelViz.setPattern({ queryArc: function () { return []; } });
	  window.__runOneFrame();
	`)

	if got := rt.eval("window.__scopeCalls").ToInteger(); got != 0 {
		t.Errorf("drawTimeScope was called %d time(s): the renderer must not reach for audio", got)
	}
}

// stripJSComments removes // line comments and /* ... */ block comments,
// while leaving string literals intact, so prose that legitimately names a
// removed API cannot be mistaken for a live reference to it. AGENTS.md calls
// out exactly this trap for CSS (stripCSSComments): the file explains the old
// behaviour in prose that contains the very text a naive search looks for.
func stripJSComments(js string) string {
	var b strings.Builder
	for i := 0; i < len(js); {
		c := js[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			quote := c
			b.WriteByte(c)
			i++
			for i < len(js) {
				b.WriteByte(js[i])
				if js[i] == '\\' && i+1 < len(js) {
					i++
					b.WriteByte(js[i])
				} else if js[i] == quote {
					i++
					break
				}
				i++
			}
		case c == '/' && i+1 < len(js) && js[i+1] == '/':
			for i < len(js) && js[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(js) && js[i+1] == '*':
			i += 2
			for i+1 < len(js) && !(js[i] == '*' && js[i+1] == '/') {
				i++
			}
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// TestVisualizationServedBytesCarryNoAudioLookup is the static half: the served
// code must not mention the removed audio surface at all, and must still carry
// the pattern-rendering markers. This assertion fails while the dead branch is
// present — which is exactly the point. It is NOT the only guard: the two tests
// above execute the module.
func TestVisualizationServedBytesCarryNoAudioLookup(t *testing.T) {
	js := servedVizJS(t)
	code := stripJSComments(js)

	for _, banned := range []string{"getAudioContext", "audioContext", "webaudio", "drawTimeScope"} {
		if strings.Contains(code, banned) {
			t.Errorf("viz.js still references %q in code: the unreachable audio-context waveform path was not removed", banned)
		}
	}

	for _, want := range []string{".queryArc(", "value.s", "value.note", "requestAnimationFrame", "playhead"} {
		if !strings.Contains(js, want) {
			t.Errorf("viz.js is missing load-bearing marker %q: the pattern rendering was damaged", want)
		}
	}
}
