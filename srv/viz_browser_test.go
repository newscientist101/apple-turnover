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
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
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

var __ctxCalls = { fillRect: [], stroke: [], fillText: [], order: [] };
var __ctx2d = {
  fillStyle: "", strokeStyle: "", lineWidth: 1, font: "", textBaseline: "",
  canvas: null,
  save: function () {}, restore: function () {}, scale: function () {},
  beginPath: function () {}, moveTo: function () {}, lineTo: function () {},
  fillRect: function (x, y, w, h) { var rec = { x: x, y: y, w: w, h: h, fillStyle: __ctx2d.fillStyle }; __ctxCalls.fillRect.push(rec); __ctxCalls.order.push({ op: "fillRect", w: w, h: h, fillStyle: __ctx2d.fillStyle }); },
  stroke: function () { __ctxCalls.stroke.push(1); },
  fillText: function (text, x, y) { __ctxCalls.fillText.push({ text: text, x: x, y: y }); __ctxCalls.order.push({ op: "fillText", text: text }); },
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

// vizLaneLabels returns the lane labels the real renderer drew, in draw order.
func vizLaneLabels(rt *syncRuntime) []string {
	rt.t.Helper()
	raw := rt.eval(`JSON.stringify(window.__ctxCalls.fillText.map(function (t) { return t.text; }))`).String()
	var labels []string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		rt.t.Fatalf("decoding lane labels %s: %v", raw, err)
	}
	return labels
}

func containsLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func laneNames(styles map[string][]string) []string {
	names := make([]string, 0, len(styles))
	for k := range styles {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// vizLaneBlockStyles returns, per lane label, the fillStyles the real renderer
// actually assigned when it painted that lane's blocks.
//
// The colour must be read at PAINT time, from the recorded call, not from
// ctx.fillStyle after the frame: the background fill, every lane label and the
// playhead overwrite fillStyle, so reading it late reports whatever the last
// painter left behind. This is the trap AGENTS.md calls out for the CSS pulse —
// asserting a value the code merely mentions proves nothing about the value it
// used. Likewise the label/block pairing is recovered from the interleaved call
// trace rather than assumed, because fillText and fillRect are recorded in
// separate arrays.
func vizLaneBlockStyles(rt *syncRuntime) map[string][]string {
	rt.t.Helper()
	raw := rt.eval(`(function () {
  var rect = __canvas.getBoundingClientRect();
  var out = [];
  var current = null;
  for (var i = 0; i < window.__ctxCalls.order.length; i++) {
    var c = window.__ctxCalls.order[i];
    if (c.op === "fillText") {
      current = c.text;
    } else if (c.op === "fillRect" && c.w < rect.width && c.h < rect.height) {
      out.push({ lane: current, style: c.fillStyle });
    }
  }
  return JSON.stringify(out);
})()`).String()

	var blocks []struct {
		Lane  string `json:"lane"`
		Style string `json:"style"`
	}
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		rt.t.Fatalf("decoding lane block styles %s: %v", raw, err)
	}
	byLane := map[string][]string{}
	for _, b := range blocks {
		byLane[b.Lane] = append(byLane[b.Lane], b.Style)
	}
	return byLane
}

// TestVisualizationLanesNotesByPitchNotBySound (issue strudel-agent-uvj.10)
//
// A pitched hap carries its pitch on `note`/`n` AND its instrument on `s`, as in
// `note("c4").sound("piano")` or `n("0 2 4").s("saw")`. Those are ordinary
// Strudel idioms, and the pinned @strudel/web@1.3.0 bundle registers `note` and
// `n` as two names for the SAME control (`{note:Fo}=w(["note","n"])`) while `s`
// is a separate sound control.
//
// viz.js used to key lanes first-match with `value.s` tested FIRST, so a shared
// instrument became the lane key and every pitch in the pattern collapsed onto
// ONE lane. The sound is not a lane's identity: a pitched hap is a lane per
// PITCH.
func TestVisualizationLanesNotesByPitchNotBySound(t *testing.T) {
	rt := newVizRuntime(t)

	rt.run(`
	  window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } });
	  window.strudelViz.setPattern({
	    queryArc: function (begin, end) {
	      return [
	        { whole: { begin: 0.0, end: 0.5 }, value: { note: "c4", s: "piano" } },
	        { whole: { begin: 0.5, end: 1.0 }, value: { note: "e4", s: "piano" } },
	        { whole: { begin: 0.0, end: 0.5 }, value: { note: "g4", s: "piano" } },
	        { whole: { begin: 0.5, end: 1.0 }, value: { note: "b4", s: "piano" } },
	      ];
	    },
	  });
	  window.__runOneFrame();
	`)

	labels := vizLaneLabels(rt)
	for _, l := range labels {
		if l == "piano" {
			t.Fatalf("lane labels = %v: a shared sound became the lane key, so all four pitches collapsed onto one lane", labels)
		}
	}
	for _, want := range []string{"c4", "e4", "g4", "b4"} {
		if !containsLabel(labels, want) {
			t.Errorf("lane labels = %v, want one lane per pitch including %q", labels, want)
		}
	}
	if len(labels) != 4 {
		t.Errorf("drew %d lane(s) (%v), want 4: one per pitch", len(labels), labels)
	}
}

// TestVisualizationLanesNKeyedHaps (issue strudel-agent-uvj.10) proves the `n`
// spelling of the note control is treated as a pitch. viz.js only ever read
// `value.note`, so a pattern written with `n` — very common, it is the SAME
// control — put every hap into the single `other` lane.
func TestVisualizationLanesNKeyedHaps(t *testing.T) {
	rt := newVizRuntime(t)

	rt.run(`
	  window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } });
	  window.strudelViz.setPattern({
	    queryArc: function (begin, end) {
	      return [
	        { whole: { begin: 0.0, end: 0.5 }, value: { n: "c4" } },
	        { whole: { begin: 0.5, end: 1.0 }, value: { n: "e4" } },
	      ];
	    },
	  });
	  window.__runOneFrame();
	`)

	labels := vizLaneLabels(rt)
	if len(labels) != 2 {
		t.Fatalf("lane labels = %v, want 2 lanes: `n` is an alias of `note`, so each n-keyed hap is its own lane", labels)
	}
	for _, want := range []string{"c4", "e4"} {
		if !containsLabel(labels, want) {
			t.Errorf("lane labels = %v, want %q: n-keyed haps lose their pitch lane", labels, want)
		}
	}
}

// TestVisualizationLaneColourMatchesLaneIdentity (issue strudel-agent-uvj.10)
// proves the note colour branch is actually REACHED, and that colour agrees with
// the lane the hap was filed under.
//
// The exact hex pins were moved to family membership because colour is now
// per-lane (issue strudel-agent-uvj.9): a lane's colour varies within its
// family, so pinning one literal per family would break on every legitimate
// palette change.
//
// The expected colour is NOT read back out of colourFor. Doing that would be
// tautological — it asserts only that the renderer calls the function the test
// itself just called, and a colourFor that ignored its arguments and returned
// one constant would satisfy it.
//
// Family is decided by MEMBERSHIP of the served palette table instead: the
// colour a lane painted must be one of ITS family's entries. Membership is used
// rather than a hue band because hue does not discriminate here — the 'other'
// family is grey-violet by design and 11 of its 16 entries sit inside the note
// family's blue band, so a hue check happily accepts an 'other' colour as a
// note colour. That is exactly the uvj.10 defect (a lane filed under one thing
// and painted as another) arriving through the palette instead of the resolver,
// and a hue-banded version of this test let mutation
// 130-viz-lane-colour-decoupled-from-lane SURVIVE. The palettes are proven
// disjoint by TestVisualizationFamilyPalettesAreDisjoint, which is what makes
// membership a sound discriminator rather than another loose hint.
func TestVisualizationLaneColourMatchesLaneIdentity(t *testing.T) {
	rt := newVizRuntime(t)

	pals := vizPalettes(t, rt)
	memberOf := func(colour, family string) bool {
		for _, c := range pals[family] {
			if c == colour {
				return true
			}
		}
		return false
	}

	rt.run(`
	  window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } });
	  window.strudelViz.setPattern({
	    queryArc: function (begin, end) {
	      return [
	        { whole: { begin: begin, end: begin + 0.5 }, value: { note: "c4", s: "piano" } },
	        { whole: { begin: begin + 0.5, end: begin + 1.0 }, value: { s: "bd" } },
	      ];
	    },
	  });
	  window.__runOneFrame();
	`)

	styles := vizLaneBlockStyles(rt)

	for _, c := range []struct {
		lane   string
		family string
	}{
		// A {note, s} hap is filed under its PITCH, so the lane named "c4"
		// must be painted from the note palette.
		{"c4", "note"},
		{"bd", "sample"},
	} {
		got := styles[c.lane]
		if len(got) == 0 {
			t.Fatalf("no block painted in lane %q (lanes seen: %v)", c.lane, laneNames(styles))
		}
		for _, s := range got {
			if !isWellFormedHex(s) {
				t.Errorf("block in lane %q painted %q, which is not a #rrggbb hex", c.lane, s)
				continue
			}
			if !memberOf(s, c.family) {
				t.Errorf("block in lane %q painted %s, which is not in the %s palette: lane identity and colour disagree",
					c.lane, s, c.family)
			}
			// Name the family it wrongly came from, when it came from one:
			// "not the note palette" alone leaves the reader guessing.
			for _, other := range []string{"note", "sample", "other"} {
				if other != c.family && memberOf(s, other) {
					t.Errorf("block in lane %q painted %s, an %s-family colour: the resolver and the palette disagree", c.lane, s, other)
				}
			}
		}
	}

	// Exactly two lanes. Before the fix the pitched hap was keyed under "piano",
	// so the lanes were "bd" and "piano" — no lane named after a pitch existed
	// to carry the note colour at all.
	if len(styles) != 2 {
		t.Errorf("lanes seen = %v, want exactly [bd c4]: a sound or an ungrouped value is leaking in as a lane", laneNames(styles))
	}
}

// vizPalettes reads the three family palettes out of the SERVED viz.js.
//
// It reads the palette TABLE, never colourFor, so using it to check a painted
// colour stays independent of the resolver under test.
func vizPalettes(t *testing.T, rt *syncRuntime) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, fam := range []string{"note", "sample", "other"} {
		raw := rt.eval(`JSON.stringify((window.strudelViz.PALETTES || {})["` + fam + `"] || null)`).String()
		var entries []string
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			t.Fatalf("%s palette is not a JSON array of colours: %s (%v)", fam, raw, err)
		}
		out[fam] = entries
	}
	return out
}

// TestVisualizationFamilyPalettesAreDisjoint proves no colour appears in two
// families.
//
// This is the invariant that lets a lane's family be decided by MEMBERSHIP —
// "the painted colour is one of the note palette's entries" — instead of by hue.
// Hue cannot do the job: the 'other' family is grey-violet by design, and
// 11 of its 16 entries have a hue inside the note family's blue band. A
// hue-only check therefore accepts an 'other' colour as a note colour, which is
// precisely how a {note, s} hap filed under its sound could be painted as a
// note (strudel-agent-uvj.10) re-entering through the palette.
func TestVisualizationFamilyPalettesAreDisjoint(t *testing.T) {
	rt := newVizRuntime(t)
	pals := vizPalettes(t, rt)

	for _, pair := range [][2]string{{"note", "sample"}, {"note", "other"}, {"sample", "other"}} {
		a, b := pair[0], pair[1]
		inA := map[string]bool{}
		for _, c := range pals[a] {
			inA[c] = true
		}
		for _, c := range pals[b] {
			if inA[c] {
				t.Errorf("colour %s is in both the %s and %s palettes: a painted colour would no longer identify its family", c, a, b)
			}
		}
	}
}

// The per-lane palette is built by varying lightness and saturation WITHIN a
// base hue, so an entry that drifts into another family's hue is invisible to
// any test that checks a couple of specific lanes: the lane simply borrows the
// wrong family's colour depending on where its key happens to hash. That is the
// same defect uvj.10 was about — colour disagreeing with identity — arriving
// through the palette instead of the resolver. The check is on the served bytes
// so the palette table itself is what is graded.
func TestVisualizationEveryPaletteEntryStaysInItsFamily(t *testing.T) {
	rt := newVizRuntime(t)

	for _, fam := range []struct {
		name    string
		hueFrom float64
		hueTo   float64
		maxSat  float64
		wantHue bool
		wantSat bool
	}{
		// The 'other' family is grey-violet by design and is bounded by
		// saturation instead: several of its entries are plain blue-greys, and
		// a hue band wide enough to hold them would swallow the note band.
		{name: "note", hueFrom: 195, hueTo: 262, wantHue: true},
		{name: "sample", hueFrom: 140, hueTo: 175, wantHue: true},
		{name: "other", maxSat: 0.70, wantSat: true},
	} {
		raw := rt.eval(`JSON.stringify((window.strudelViz.PALETTES || {})["` + fam.name + `"] || null)`).String()
		var entries []string
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			t.Fatalf("%s palette is not a JSON array of colours: %s (%v)", fam.name, raw, err)
		}
		if len(entries) < 4 {
			t.Errorf("%s palette has %d entries, want at least 4 so realistic lane counts stay distinguishable", fam.name, len(entries))
		}
		seen := map[string]bool{}
		for _, c := range entries {
			if !isWellFormedHex(c) {
				t.Errorf("%s palette entry %q is not a #rrggbb hex", fam.name, c)
				continue
			}
			if seen[c] {
				t.Errorf("%s palette repeats %q: the variation within a family is doing nothing", fam.name, c)
			}
			seen[c] = true
			if fam.wantHue {
				h, _ := hexHueDegrees(c)
				if h < fam.hueFrom || h > fam.hueTo {
					t.Errorf("%s palette entry %s has hue %.1f, outside its %.0f..%.0f family band: it would paint a lane in another family's colour", fam.name, c, h, fam.hueFrom, fam.hueTo)
				}
			}
			if fam.wantSat {
				if s, _ := hexSaturation(c); s > fam.maxSat {
					t.Errorf("%s palette entry %s has saturation %.2f, above the %.2f ceiling for that family", fam.name, c, s, fam.maxSat)
				}
			}
		}
	}
}

// TestVisualizationPerLanePalette (issue strudel-agent-uvj.9) proves:
// 1. Four distinct sample lanes receive four distinguishable colours.
// 2. Note, sample, and 'other' lanes are semantically distinguishable and receive well-formed hexes.
// 3. Colours are stable across re-renders and lane count/sort order changes (flicker regression).
func TestVisualizationPerLanePalette(t *testing.T) {
	rt := newVizRuntime(t)

	// 1. Four distinct sample lanes receive four distinguishable colours.
	rt.run(`
	  window.strudelViz.onSnapshot({ anchor: { epochMs: 0, cps: 0.5 } });
	  window.strudelViz.setPattern({
	    queryArc: function (begin, end) {
	      return [
	        { whole: { begin: begin, end: begin + 0.25 }, value: { s: "bd" } },
	        { whole: { begin: begin + 0.25, end: begin + 0.50 }, value: { s: "sd" } },
	        { whole: { begin: begin + 0.50, end: begin + 0.75 }, value: { s: "cp" } },
	        { whole: { begin: begin + 0.75, end: begin + 1.00 }, value: { s: "hh" } },
	      ];
	    },
	  });
	  window.__runOneFrame();
	`)

	sampleStyles := vizLaneBlockStyles(rt)
	sampleLanes := []string{"bd", "sd", "cp", "hh"}
	seenSampleColours := make(map[string]string)

	for _, lane := range sampleLanes {
		got := sampleStyles[lane]
		if len(got) == 0 {
			t.Fatalf("no blocks painted for lane %q", lane)
		}
		colour := got[0]
		if !isWellFormedHex(colour) {
			t.Errorf("lane %q colour %q is not a well-formed hex #rrggbb", lane, colour)
		}
		if prevLane, dup := seenSampleColours[colour]; dup {
			t.Errorf("lane %q and lane %q share the exact same colour %q: sample lanes must be distinguishable", lane, prevLane, colour)
		}
		seenSampleColours[colour] = lane
	}

	// 2. Semantic distinction across families (note vs sample vs other).
	rt.run(`
	  window.__ctxCalls.fillRect.length = 0;
	  window.__ctxCalls.stroke.length = 0;
	  window.__ctxCalls.fillText.length = 0;
	  window.__ctxCalls.order.length = 0;
	  window.strudelViz.setPattern({
	    queryArc: function (begin, end) {
	      return [
	        { whole: { begin: begin, end: begin + 0.33 }, value: { note: "c4" } },
	        { whole: { begin: begin + 0.33, end: begin + 0.66 }, value: { s: "bd" } },
	        { whole: { begin: begin + 0.66, end: begin + 1.00 }, value: { gain: 0.8 } }, // 'other'
	      ];
	    },
	  });
	  window.__runOneFrame();
	`)

	familyStyles := vizLaneBlockStyles(rt)
	noteColour := familyStyles["c4"][0]
	sampleColour := familyStyles["bd"][0]
	otherColour := familyStyles["other"][0]

	for lane, colour := range map[string]string{"c4": noteColour, "bd": sampleColour, "other": otherColour} {
		if !isWellFormedHex(colour) {
			t.Errorf("lane %q colour %q is not a well-formed hex #rrggbb", lane, colour)
		}
	}

	if noteColour == sampleColour {
		t.Errorf("note colour %q and sample colour %q are identical: families must be semantically distinguishable", noteColour, sampleColour)
	}
	if noteColour == otherColour {
		t.Errorf("note colour %q and other colour %q are identical: families must be semantically distinguishable", noteColour, otherColour)
	}
	if sampleColour == otherColour {
		t.Errorf("sample colour %q and other colour %q are identical: families must be semantically distinguishable", sampleColour, otherColour)
	}

	// 3. Stability across re-renders and lane count / sort order changes.
	// Initial pattern: bd, sd, cp
	rt.run(`
	  window.__ctxCalls.fillRect.length = 0;
	  window.__ctxCalls.stroke.length = 0;
	  window.__ctxCalls.fillText.length = 0;
	  window.__ctxCalls.order.length = 0;
	  window.strudelViz.setPattern({
	    queryArc: function (begin, end) {
	      return [
	        { whole: { begin: begin, end: begin + 0.33 }, value: { s: "bd" } },
	        { whole: { begin: begin + 0.33, end: begin + 0.66 }, value: { s: "sd" } },
	        { whole: { begin: begin + 0.66, end: begin + 1.00 }, value: { s: "cp" } },
	      ];
	    },
	  });
	  window.__runOneFrame();
	`)
	p1Styles := vizLaneBlockStyles(rt)
	bdColourP1 := p1Styles["bd"][0]
	sdColourP1 := p1Styles["sd"][0]
	cpColourP1 := p1Styles["cp"][0]

	// Re-render same pattern
	rt.run(`
	  window.__ctxCalls.fillRect.length = 0;
	  window.__ctxCalls.stroke.length = 0;
	  window.__ctxCalls.fillText.length = 0;
	  window.__ctxCalls.order.length = 0;
	  window.__runOneFrame();
	`)
	p1ReStyles := vizLaneBlockStyles(rt)
	if p1ReStyles["bd"][0] != bdColourP1 || p1ReStyles["sd"][0] != sdColourP1 || p1ReStyles["cp"][0] != cpColourP1 {
		t.Errorf("re-render changed colours: got [%s %s %s], want [%s %s %s]",
			p1ReStyles["bd"][0], p1ReStyles["sd"][0], p1ReStyles["cp"][0],
			bdColourP1, sdColourP1, cpColourP1)
	}

	// Render pattern with extra lane added and different sort order ("hh" added)
	rt.run(`
	  window.__ctxCalls.fillRect.length = 0;
	  window.__ctxCalls.stroke.length = 0;
	  window.__ctxCalls.fillText.length = 0;
	  window.__ctxCalls.order.length = 0;
	  window.strudelViz.setPattern({
	    queryArc: function (begin, end) {
	      return [
	        { whole: { begin: begin, end: begin + 0.25 }, value: { s: "hh" } },
	        { whole: { begin: begin + 0.25, end: begin + 0.50 }, value: { s: "bd" } },
	        { whole: { begin: begin + 0.50, end: begin + 0.75 }, value: { s: "sd" } },
	        { whole: { begin: begin + 0.75, end: begin + 1.00 }, value: { s: "cp" } },
	      ];
	    },
	  });
	  window.__runOneFrame();
	`)
	p2Styles := vizLaneBlockStyles(rt)
	if p2Styles["bd"][0] != bdColourP1 {
		t.Errorf("lane 'bd' colour changed when lane count/order changed: got %q, want %q", p2Styles["bd"][0], bdColourP1)
	}
	if p2Styles["sd"][0] != sdColourP1 {
		t.Errorf("lane 'sd' colour changed when lane count/order changed: got %q, want %q", p2Styles["sd"][0], sdColourP1)
	}
	if p2Styles["cp"][0] != cpColourP1 {
		t.Errorf("lane 'cp' colour changed when lane count/order changed: got %q, want %q", p2Styles["cp"][0], cpColourP1)
	}
}

// hexChannels returns the r/g/b components of a #rrggbb colour in [0,1], and
// whether the string was a well-formed hex at all.
//
// It exists so a lane's family can be asserted from the COLOUR THE RENDERER
// PAINTED rather than from the palette table in viz.js: reading the expectation
// back out of the implementation would make the assertion tautological.
func hexChannels(hex string) ([3]float64, bool) {
	var rgb [3]float64
	if len(hex) != 7 || hex[0] != '#' {
		return rgb, false
	}
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return rgb, false
		}
		rgb[i] = float64(v) / 255
	}
	return rgb, true
}

// hexHueDegrees returns the hue of a #rrggbb colour in degrees [0,360), and
// whether the string was a well-formed hex at all.
func hexHueDegrees(hex string) (float64, bool) {
	rgb, ok := hexChannels(hex)
	if !ok {
		return 0, false
	}
	maxC := math.Max(rgb[0], math.Max(rgb[1], rgb[2]))
	minC := math.Min(rgb[0], math.Min(rgb[1], rgb[2]))
	if maxC == minC {
		return 0, true // achromatic: no meaningful hue
	}
	d := maxC - minC
	var h float64
	switch maxC {
	case rgb[0]:
		h = (rgb[1] - rgb[2]) / d
		if rgb[1] < rgb[2] {
			h += 6
		}
	case rgb[1]:
		h = (rgb[2]-rgb[0])/d + 2
	default:
		h = (rgb[0]-rgb[1])/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, true
}

// hexSaturation returns the HSL saturation of a #rrggbb colour in [0,1], and
// whether the string was a well-formed hex at all.
//
// The 'other' (ungrouped) family is grey-violet BY DESIGN, so it is
// identified by LOW saturation rather than by hue: several of its entries sit
// near the blue part of the wheel (plain blue-greys like #78909c), and a hue
// band wide enough to contain them would overlap the note family's band and
// stop discriminating anything.
func hexSaturation(hex string) (float64, bool) {
	rgb, ok := hexChannels(hex)
	if !ok {
		return 0, false
	}
	maxC := math.Max(rgb[0], math.Max(rgb[1], rgb[2]))
	minC := math.Min(rgb[0], math.Min(rgb[1], rgb[2]))
	if maxC == minC {
		return 0, true
	}
	l := (maxC + minC) / 2
	d := maxC - minC
	if l > 0.5 {
		return d / (2 - maxC - minC), true
	}
	return d / (maxC + minC), true
}

func isWellFormedHex(hex string) bool {
	_, ok := hexChannels(hex)
	return ok
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
