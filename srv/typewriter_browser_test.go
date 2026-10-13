package srv

// Typewriter edit simulation (issue strudel-agent-3f2.3), executed as served.
//
// When a code frame changes the displayed document the change is SIMULATED
// as a human edit: the visible cursor moves to the edit site, old text is
// deleted and new text is inserted in bounded steps, ending with the
// displayed document EXACTLY equal to the new version's code. The mechanics
// are fixed by the design leaf strudel-agent-3f2.2 and implemented in the
// served editor.js: single prefix/suffix edit region (UTF-16 offsets),
// pacing via the fake-clock setTimeout chain (STEP_MS=80, DEL_CHUNK=1,
// INS_CHUNK=1, TOTAL_BUDGET_MS=2400, MAX_STEPS=30), instant clamp for big
// diffs, churn retarget single-flight, textarea instant no-op, and a
// visible caret (cursorBlinkRate 0 at init plus a marker class on the CM
// wrapper while animating, so the cursor tracks the edit without
// stealing focus).
//
// These tests EXECUTE the served session.js + editor.js in goja against the
// fake clock and a fake CodeMirror 5 (which records replaceRange/setValue
// calls, tracks the caret, and counts 'changes' events), for the same
// reason viz_browser_test.go runs the served viz.js: asserting on the
// served bytes is the only way to prove the animation actually runs.

import (
	"encoding/json"
	"strings"
	"testing"
)

// typewriterEditorPrelude is the host surface editor.js needs on top of
// sessionJSPrelude (which already brings window, the fake clock, console
// and the recording-fetch collaborators): a fake textarea under id
// "editor" plus a fake CodeMirror 5 whose programmatic edits behave like
// the real one — replaceRange splices the document and fires one
// 'changes' event per operation() (per the live verification in 3f2.1),
// setValue drops all marks with the document, and the caret/scroll targets
// are recorded for assertion.
const typewriterEditorPrelude = `
window.__twEdits = [];
window.__twClampedLine = -1;
window.__cmChanges = 0;
window.__cmChangeHandlers = [];
window.__twCursorLog = [];
window.__twScrollLog = [];
window.__twSetValueCalls = 0;
// Option recording (issue strudel-agent-3f2.6): init must request a steady
// caret via setOption('cursorBlinkRate', 0) when supported.
window.__twSetOptionLog = [];
// Wrapper class recording (issue strudel-agent-3f2.6): the fake wrapper
// element persists per instance and records every classList mutation, so
// the cursor-visibility test can pin the animating marker class across
// start and convergence.
window.__twWrapperClassLog = [];
window.__twWrapper = {
  __classes: {},
  classList: {
    add: function (cls) {
      window.__twWrapper.__classes[cls] = true;
      window.__twWrapperClassLog.push({ op: "add", cls: String(cls) });
    },
    remove: function (cls) {
      delete window.__twWrapper.__classes[cls];
      window.__twWrapperClassLog.push({ op: "remove", cls: String(cls) });
    },
    contains: function (cls) { return !!window.__twWrapper.__classes[cls]; },
  },
};
// Mark recording (issue strudel-agent-3f2.4): the same record shape as
// highlightEditorPrelude, so liveHighlightMarks works on this harness too.
// highlightEditorPrelude already defined window.__liveMarks; this fake only
// feeds its window.__cmMarks backing store.
window.__cmMarks = [];
window.__liveMarks = function () {
  return window.__cmMarks.filter(function (m) { return !m.cleared; });
};
window.CodeMirror = {
  version: "5.65.16",
  fromTextArea: function (el, opts) {
    var cm = {
      __doc: (el && typeof el.value === "string") ? el.value : "",
      __cursor: { line: 0, ch: 0 },
      __inOp: false,
      __opDirty: false,
      getValue: function () { return cm.__doc; },
      setValue: function (v) {
        window.__twSetValueCalls++;
        cm.__doc = String(v);
        // Like the real CodeMirror, a document swap drops all marks.
        window.__cmMarks.forEach(function (m) { m.cleared = true; });
        window.__cmMarks.length = 0;
        cm.__noteChange();
      },
      __noteChange: function () {
        if (cm.__inOp) { cm.__opDirty = true; return; }
        window.__cmChanges++;
        window.__cmChangeHandlers.forEach(function (h) { h(); });
      },
      __idx: function (pos) {
        if (typeof pos === "number") { return Math.max(0, Math.min(cm.__doc.length, pos)); }
        return Math.max(0, Math.min(cm.__doc.length, pos && typeof pos.ch === "number" ? pos.ch : 0));
      },
      posFromIndex: function (i) { return { line: 0, ch: i }; },
      indexFromPos: function (pos) { return cm.__idx(pos); },
      replaceRange: function (text, from, to) {
        var a = cm.__idx(from);
        var b = (to === undefined || to === null) ? a : cm.__idx(to);
        if (b < a) { var t = a; a = b; b = t; }
        window.__twEdits.push({ text: String(text), from: a, to: b, docBefore: cm.__doc });
        cm.__doc = cm.__doc.slice(0, a) + String(text) + cm.__doc.slice(b);
        cm.__noteChange();
      },
      operation: function (fn) {
        cm.__inOp = true; cm.__opDirty = false;
        try { fn(); } finally {
          cm.__inOp = false;
          if (cm.__opDirty) {
            window.__cmChanges++;
            window.__cmChangeHandlers.forEach(function (h) { h(); });
            cm.__opDirty = false;
          }
        }
      },
      markText: function (from, to, options) {
        var rec = { from: from, to: to, options: options || {}, cleared: false };
        window.__cmMarks.push(rec);
        return { clear: function () { rec.cleared = true; } };
      },
      scrollIntoView: function (pos) { window.__twScrollLog.push(cm.__idx(pos)); },
      getCursor: function () { return { line: cm.__cursor.line, ch: cm.__cursor.ch }; },
      setCursor: function (pos) {
        var i = cm.__idx(pos);
        cm.__cursor = { line: 0, ch: i };
        window.__twCursorLog.push(i);
      },
      hasFocus: function () { return false; },
      lineCount: function () { return 1; },
      addLineClass: function (line, where, cls) { window.__twClampedLine = line; },
      removeLineClass: function () {},
      getWrapperElement: function () { return window.__twWrapper; },
      setOption: function (name, value) { window.__twSetOptionLog.push({ name: String(name), value: value }); },
      on: function (ev, h) { if (ev === "changes") { window.__cmChangeHandlers.push(h); } },
    };
    window.__cm = cm;
    return cm;
  },
};
window.__fakeTextarea = { value: "" };
document.getElementById = function (id) {
  if (id === "editor") { return window.__fakeTextarea; }
  return null;
};
`

// newTypewriterRuntime loads the full served client stack the animation
// spans: session.js under the session prelude, then the typewriter editor
// prelude, then the REAL served editor.js. viz.js is deliberately absent:
// the animation core (3f2.3) must not depend on the highlight frame loop
// (3f2.4 owns that interplay).
func newTypewriterRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, sessionJSPrelude)
	rt.run(servedSyncJS(t))
	rt.run(servedSessionJS(t))
	rt.run(vizJSPrelude)
	rt.run(highlightEditorPrelude)
	rt.run(typewriterEditorPrelude)
	rt.run(servedEditorJS(t))
	if got := rt.eval("typeof window.strudelEditor").String(); got != "object" {
		t.Fatalf("window.strudelEditor = %s, want object: served editor.js did not install its API", got)
	}
	return rt
}

// twDoc reads the fake CodeMirror document.
func twDoc(rt *syncRuntime) string {
	rt.t.Helper()
	raw := rt.eval("JSON.stringify(window.__cm.getValue())").String()
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		rt.t.Fatalf("decoding cm document %s: %v", raw, err)
	}
	return s
}

// twSetDoc seeds the fake document directly (the "already on screen" state).
func twSetDoc(rt *syncRuntime, doc string) {
	rt.t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		rt.t.Fatalf("marshalling seed doc: %v", err)
	}
	rt.run("window.__cm.__doc = " + string(b))
	rt.run("window.__twEdits = []; window.__twCursorLog = []; window.__twScrollLog = [];")
}

// twConverge advances the fake clock one STEP_MS (80ms) at a time until
// no animation timer remains, failing fast if the design budget is
// exceeded. It returns the number of advances used.
func twConverge(rt *syncRuntime, maxAdvances int) int {
	rt.t.Helper()
	used := 0
	for {
		if rt.eval("window.__pendingTimers()").ToInteger() == 0 {
			return used
		}
		if used >= maxAdvances {
			rt.t.Fatalf("animation did not converge after %d advances: the pacing has no bound", used)
		}
		rt.run("window.__advance(80)")
		used++
	}
}

// twPendingTimers reports the fake clock's outstanding timer count.
func twPendingTimers(rt *syncRuntime) int64 {
	rt.t.Helper()
	return rt.eval("window.__pendingTimers()").ToInteger()
}

// TestTypewriterBoundedAnimation drives s(bd)->s(bd cp) one STEP_MS at a
// time: the animation must land exactly on the new code within the
// design's MAX_STEPS=30 budget, leave zero timers outstanding, move the
// caret to the edit frontier, and scroll the frontier into view. The
// 3-char insert types one char per 80ms tick (DEL_CHUNK=INS_CHUNK=1), so
// the visible steps are slow enough to read as human edits.
func TestTypewriterBoundedAnimation(t *testing.T) {
	rt := newTypewriterRuntime(t)
	twSetDoc(rt, `s("bd")`)
	rt.run(`window.strudelEditor.setCode('s("bd cp")');`)
	if got := twDoc(rt); got == `s("bd cp")` {
		t.Fatalf("setCode landed instantly: the edit was not simulated as a bounded animation")
	}
	used := twConverge(rt, 40)
	if used > 30 {
		t.Errorf("converged in %d advances, want <= 30 (MAX_STEPS): the pacing budget is violated", used)
	}
	if got := twDoc(rt); got != `s("bd cp")` {
		t.Errorf("final document = %q, want %q: the animation must end exactly on the new code", got, `s("bd cp")`)
	}
	if n := twPendingTimers(rt); n != 0 {
		t.Errorf("__pendingTimers() = %d, want 0: the animation left a timer armed", n)
	}
	if got := rt.eval("window.__twCursorLog.length").ToInteger(); got == 0 {
		t.Error("no setCursor call recorded: the visible cursor never moved to the edit site")
	}
	if got := rt.eval("window.__twScrollLog.length").ToInteger(); got == 0 {
		t.Error("no scrollIntoView call recorded: the edit frontier was never scrolled into view")
	}
	last := rt.eval("window.__twCursorLog[window.__twCursorLog.length-1]").ToInteger()
	if last < 4 || last > 9 {
		t.Errorf("final caret = %d, want within the 4..9 edit region: the cursor must park at the frontier", last)
	}

	// Phase ordering on a two-phase diff: the delete region drains first
	// (one char/tick, tiled contiguously from the region end), then the
	// insert types (one char/tick) at the cursor — each tick collapsing
	// to exactly one 'changes' event. A 6-delete/5-insert script needs
	// ceil(6/1)+ceil(5/1)=11 ticks, which also proves the old 8/4 chunk
	// sizes are gone: the first delete tick removes exactly one char.
	rt2 := newTypewriterRuntime(t)
	oldDoc := `s("` + strings.Repeat("d", 6) + `")`
	newDoc := `s("` + strings.Repeat("i", 5) + `")`
	twSetDoc(rt2, oldDoc)
	b, _ := json.Marshal(newDoc)
	rt2.run("window.strudelEditor.setCode(" + string(b) + ");")
	if got := twDoc(rt2); got == newDoc {
		t.Fatalf("two-phase setCode landed instantly: the edit was not simulated")
	}
	for i := 0; i < 11; i++ {
		if n := twPendingTimers(rt2); n != 1 {
			t.Fatalf("before tick %d: __pendingTimers() = %d, want exactly 1: at most one animation timer may be outstanding", i, n)
		}
		rt2.run("window.__advance(80)")
	}
	if n := twPendingTimers(rt2); n != 0 {
		t.Fatalf("__pendingTimers() = %d after 11 ticks, want 0: the 6-del/5-ins script needs ceil(6/1)+ceil(5/1)=11 ticks", n)
	}
	if got := twDoc(rt2); got != newDoc {
		t.Fatalf("final document = %q, want %q", got, newDoc)
	}
	raw := rt2.eval(`JSON.stringify(window.__twEdits.map(function (e) {
	  return { t: e.text, d: e.to - e.from, f: e.from, o: e.to };
	}))`).String()
	var edits []struct {
		Text string `json:"t"`
		Del  int    `json:"d"`
		From int    `json:"f"`
		To   int    `json:"o"`
	}
	if err := json.Unmarshal([]byte(raw), &edits); err != nil {
		t.Fatalf("decoding edit log %s: %v", raw, err)
	}
	// Region [3,9): prefix s(" is 3 chars, suffix ") is 2.
	want := []struct {
		Text string
		Del  int
		From int
		To   int
	}{
		{"", 1, 8, 9}, {"", 1, 7, 8}, {"", 1, 6, 7},
		{"", 1, 5, 6}, {"", 1, 4, 5}, {"", 1, 3, 4},
		{"i", 0, 3, 3}, {"i", 0, 4, 4}, {"i", 0, 5, 5},
		{"i", 0, 6, 6}, {"i", 0, 7, 7},
	}
	if len(edits) != len(want) {
		t.Fatalf("edit log has %d entries, want %d (one replaceRange per tick): %s", len(edits), len(want), raw)
	}
	for i, w := range want {
		if edits[i].Text != w.Text || edits[i].Del != w.Del || edits[i].From != w.From || edits[i].To != w.To {
			t.Errorf("tick %d edit = %+v, want text=%q del=%d [%d,%d): deletes tile from the region end first, then inserts type at the cursor", i, edits[i], w.Text, w.Del, w.From, w.To)
		}
	}
	if got := rt2.eval("window.__cmChanges").ToInteger(); got != 11 {
		t.Errorf("__cmChanges = %d, want 11: each tick's operation() must collapse to exactly one changes-event", got)
	}
	if got := rt2.eval("JSON.stringify(window.__twCursorLog)").String(); got != "[8,7,6,5,4,3,4,5,6,7,8]" {
		t.Errorf("caret log = %s, want [8,7,6,5,4,3,4,5,6,7,8]: the cursor parks at the shrinking delete edge, then rides the typed edge", got)
	}
	if got := rt2.eval("JSON.stringify(window.__twScrollLog)").String(); got != "[8,7,6,5,4,3,4,5,6,7,8]" {
		t.Errorf("scroll log = %s, want the caret trail: every tick scrolls the frontier into view", got)
	}
}

// TestTypewriterExactFinalState pins the derivation edges: empty->code,
// code->empty, the identical no-op (synchronous, zero timers), unicode
// offsets in UTF-16 code units, the 64 KiB clamp path (instant setValue +
// zero animation timers), and the clamp boundary (an estimate-35 diff
// jumps instantly while an estimate-30 diff still animates).
func TestTypewriterExactFinalState(t *testing.T) {
	t.Run("empty-to-code", func(t *testing.T) {
		rt := newTypewriterRuntime(t)
		twSetDoc(rt, "")
		rt.run(`window.strudelEditor.setCode('s("bd")');`)
		twConverge(rt, 40)
		if got := twDoc(rt); got != `s("bd")` {
			t.Errorf("final document = %q, want %q", got, `s("bd")`)
		}
	})
	t.Run("code-to-empty", func(t *testing.T) {
		rt := newTypewriterRuntime(t)
		twSetDoc(rt, `s("bd")`)
		rt.run(`window.strudelEditor.setCode('');`)
		twConverge(rt, 40)
		if got := twDoc(rt); got != "" {
			t.Errorf("final document = %q, want empty", got)
		}
	})
	t.Run("identical-noop", func(t *testing.T) {
		rt := newTypewriterRuntime(t)
		twSetDoc(rt, `s("bd")`)
		rt.run(`window.strudelEditor.setCode('s("bd")');`)
		if n := twPendingTimers(rt); n != 0 {
			t.Errorf("__pendingTimers() = %d after identical setCode, want 0: a no-op edit armed a timer", n)
		}
		if got := twDoc(rt); got != `s("bd")` {
			t.Errorf("document = %q after identical setCode, want unchanged", got)
		}
	})
	t.Run("unicode-offsets", func(t *testing.T) {
		rt := newTypewriterRuntime(t)
		twSetDoc(rt, "s(\"bd \U0001F920\")")
		// The target travels JSON-encoded (a JSON string is a valid JS
		// string literal): a raw JS '...\U...' escape would not decode
		// the emoji, and the test would assert on its own mangling.
		target := "s(\"bd \U0001F920 cp\")"
		b, _ := json.Marshal(target)
		rt.run("window.strudelEditor.setCode(" + string(b) + ");")
		twConverge(rt, 40)
		if got := twDoc(rt); got != target {
			t.Errorf("final document = %q, want the unicode target byte-exactly: offsets are UTF-16 code units", got)
		}
	})
	t.Run("clamp-64k", func(t *testing.T) {
		rt := newTypewriterRuntime(t)
		twSetDoc(rt, "")
		big := strings.Repeat("x", 65536)
		b, _ := json.Marshal(big)
		rt.run("window.__twSetValueCalls = 0; window.strudelEditor.setCode(" + string(b) + ");")
		if got := twDoc(rt); got != big {
			t.Errorf("clamp path document length = %d, want 65536 instantly: big diffs must jump, not type", len(got))
		}
		if n := twPendingTimers(rt); n != 0 {
			t.Errorf("__pendingTimers() = %d after 64 KiB setCode, want 0: the clamp path armed an animation timer", n)
		}
		if got := rt.eval("window.__twSetValueCalls").ToInteger(); got == 0 {
			t.Error("setValue was never called on the clamp path: big diffs must jump via instant setValue")
		}
	})
	t.Run("clamp-boundary", func(t *testing.T) {
		rt := newTypewriterRuntime(t)
		// 20 deletes + 21 inserts: ceil(20/1)+ceil(21/1)=41 steps, which
		// exceeds MAX_STEPS=30 and must clamp to the instant setValue
		// path with zero animation timers.
		twSetDoc(rt, `s("`+strings.Repeat("d", 20)+`")`)
		big := `s("` + strings.Repeat("i", 15) + ` extra")`
		b, _ := json.Marshal(big)
		rt.run("window.__twSetValueCalls = 0; window.strudelEditor.setCode(" + string(b) + ");")
		if got := twDoc(rt); got != big {
			t.Errorf("clamp-boundary document = %q, want the jump target: an estimate-41 diff must land instantly", got)
		}
		if n := twPendingTimers(rt); n != 0 {
			t.Errorf("__pendingTimers() = %d on the clamp boundary, want 0: the clamp path armed an animation timer", n)
		}
		if got := rt.eval("window.__twSetValueCalls").ToInteger(); got == 0 {
			t.Error("setValue was never called on the clamp boundary: over-budget diffs must jump via instant setValue")
		}
	})
}

// TestTypewriterCursorVisible proves the caret is VISIBLE while the edit
// animates, without stealing focus (a focus steal would trip setCode's
// hasFocus guard on the next frame). It pins three legs of the
// strudel-agent-3f2.6 mechanism: init calls setOption('cursorBlinkRate',
// 0) when supported (steady caret per the 3f2.1 live finding); every tick
// calls setCursor at the frontier (the per-tick caret trail); and the
// 'strudel-animating' marker class is added to the CM wrapper at
// animation start and removed at convergence on the stepped landing path.
func TestTypewriterCursorVisible(t *testing.T) {
	rt := newTypewriterRuntime(t)
	// Init (via the served editor.js boot at load) must have requested
	// a steady caret through the supported setOption seam.
	blink := rt.eval(`JSON.stringify(window.__twSetOptionLog.filter(function (e) {
	  return e.name === 'cursorBlinkRate';
	}))`).String()
	if !strings.Contains(blink, "0") {
		t.Errorf("setOption log = %s, want a cursorBlinkRate 0 entry: init must steady the caret when supported", rt.eval("JSON.stringify(window.__twSetOptionLog)").String())
	}
	twSetDoc(rt, `s("bd")`)
	rt.run(`window.strudelEditor.setCode('s("bd cp")');`)
	if got := twDoc(rt); got == `s("bd cp")` {
		t.Fatalf("setCode landed instantly: the animation never started, so caret visibility races nothing")
	}
	// The marker class must be present mid-animation.
	if got := rt.eval("window.__twWrapper.classList.contains('strudel-animating')").String(); got != "true" {
		t.Errorf("wrapper marker class mid-animation = %s, want true: the animating caret rule must apply while stepping", got)
	}
	// The caret trail: one setCursor per tick, riding the frontier.
	rt.run("window.__advance(80)")
	if got := rt.eval("window.__twCursorLog.length").ToInteger(); got == 0 {
		t.Fatalf("no setCursor call after one tick: the caret never moved to the edit site")
	}
	used := twConverge(rt, 40)
	if used > 30 {
		t.Errorf("converged in %d advances, want <= 30 (MAX_STEPS)", used)
	}
	if got := twDoc(rt); got != `s("bd cp")` {
		t.Errorf("final document = %q, want %q", got, `s("bd cp")`)
	}
	// The marker class must be gone after the stepped landing.
	if got := rt.eval("window.__twWrapper.classList.contains('strudel-animating')").String(); got != "false" {
		t.Errorf("wrapper marker class after convergence = %s, want false: typewriterFinish must remove it on every landing path", got)
	}
	// The add/remove pairing: exactly one add (at animation start) and a
	// trailing remove (the convergence landing). The log opens with one
	// more remove: setCode unconditionally clears the abandoned script's
	// marker before deriving the new script, so a superseded animation
	// can never leak its class — the no-op remove on a fresh start is
	// that same discipline, not an imbalance.
	log := rt.eval("JSON.stringify(window.__twWrapperClassLog)").String()
	adds := strings.Count(log, `"op":"add","cls":"strudel-animating"`)
	if adds != 1 {
		t.Errorf("wrapper class log = %s, want exactly one add of strudel-animating: the marker must be added once at animation start", log)
	}
	if !strings.HasSuffix(log[:len(log)-1], `"op":"remove","cls":"strudel-animating"}`) {
		t.Errorf("wrapper class log = %s, want a trailing remove of strudel-animating: the marker must be removed at convergence", log)
	}
	// Focus was never stolen: the fake CM reports unfocused throughout,
	// so the next setCode's hasFocus guard still passes.
	if got := rt.eval("window.__cm.hasFocus()").String(); got != "false" {
		t.Errorf("cm.hasFocus() = %s, want false: the animation must not steal focus", got)
	}
	// The served stylesheet must carry the caret rule the marker
	// references — a marker class with no rule is an invisible feature
	// (the same served-bytes discipline as TestHighlightStyleRuleServed).
	css := servedStyleCSS(t)
	for _, want := range []string{"strudel-animating", ".CodeMirror-cursor"} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css carries no %q under the animating marker: the cursor would stay hidden mid-edit", want)
		}
	}
}

// TestTypewriterSupersede drives two versions into the animation window:
// the final document must be exactly the LATEST code, at most one timer
// may be outstanding at any moment, and convergence after the LAST setCode
// must stay within the design budget.
func TestTypewriterSupersede(t *testing.T) {
	rt := newTypewriterRuntime(t)
	twSetDoc(rt, `s("bd")`)
	// The first version needs 21 ticks (21 inserts at INS_CHUNK=1), so it
	// is provably still in flight when the second version lands: a
	// supersede of an already-converged script would prove nothing about
	// cancellation (and let the no-cancel mutation survive).
	first := `s("bd ` + strings.Repeat("h", 21) + `")`
	b, _ := json.Marshal(first)
	rt.run("window.strudelEditor.setCode(" + string(b) + ");")
	rt.run(`window.__advance(80); window.__advance(80);`)
	if got := twDoc(rt); got == first {
		t.Fatalf("first animation already converged after 2 advances: the supersede would race nothing")
	}
	if n := twPendingTimers(rt); n != 1 {
		t.Fatalf("__pendingTimers() = %d mid-animation, want exactly 1", n)
	}
	rt.run(`window.strudelEditor.setCode('s("bd cp")');`)
	if n := twPendingTimers(rt); n > 1 {
		t.Fatalf("__pendingTimers() = %d after supersede, want <= 1: the stale script was not cancelled", n)
	}
	used := twConverge(rt, 40)
	if used > 30 {
		t.Errorf("converged in %d advances after the last setCode, want <= 30: retarget must restart within budget", used)
	}
	if got := twDoc(rt); got != `s("bd cp")` {
		t.Errorf("final document = %q, want the LATEST code %q: the stale animation won", got, `s("bd cp")`)
	}
}

// TestTypewriterVerdictImmediate proves the feedback loop is untouched by
// the cosmetics: the verdict POST fires at frame arrival, not after the
// animation converges. The fetch is recorded with its fake-clock
// timestamp; the animation is then driven to convergence and the verdict
// must already have been recorded at the arrival instant.
func TestTypewriterVerdictImmediate(t *testing.T) {
	rt := newTypewriterRuntime(t)
	rt.run(`
window.__fetchLog = [];
var fetch = function (path, init) {
  window.__fetchLog.push({ path: path, at: Date.now(), body: init && init.body ? JSON.parse(init.body) : null });
  return apiStub();
};
window.__live = makeLive();
window.__sandbox = {
  evaluate: function (code) {
    return Promise.resolve({ queryArc: function () { return []; } });
  },
  state: {},
};
window.__setNow(1000);
window.strudelSession.handleFrame(JSON.stringify({
  kind: "code",
  snapshot: { version: 1, code: 's("bd cp")', anchor: null },
}), window.__sandbox, window.__live);
`)
	rt.settle()
	raw := rt.eval(`JSON.stringify(window.__fetchLog.filter(function (f) { return f.path === "/api/eval-result"; }))`).String()
	if !strings.Contains(raw, `"version":1`) {
		t.Fatalf("no /api/eval-result verdict recorded at frame arrival (before any __advance): %s", raw)
	}
	if got := rt.eval(`window.__fetchLog.filter(function (f) { return f.path === "/api/eval-result"; })[0].at`).ToInteger(); got != 1000 {
		t.Errorf("verdict recorded at fake-clock %d, want 1000 (frame arrival): the verdict waited for the animation", got)
	}
	twConverge(rt, 40)
	if got := twDoc(rt); got != `s("bd cp")` {
		t.Errorf("final document = %q after convergence, want the version code: the view must still land", got)
	}
}

// TestTypewriterHighlightSuppressed proves the choice-(a) highlight
// interplay (issue strudel-agent-3f2.4, design D3): while the typewriter
// animation is in flight the displayed document is NOT the committed
// document, so setActiveSpans must SUPPRESS marks — it buffers the latest
// span set (one slot, newer overwrites) and places zero live marks — and
// re-apply them document-true against the FINAL document at convergence.
// The pnn.5 invariant survives: marks never appear on text that does not
// exist yet, and once the animation lands they are the FINAL document's
// [3,5). An empty active set still clears, preserving the clear contract.
func TestTypewriterHighlightSuppressed(t *testing.T) {
	rt := newTypewriterRuntime(t)
	twSetDoc(rt, `s("bd")`)
	target := `s("bd cp")`
	b, _ := json.Marshal(target)
	rt.run("window.strudelEditor.setCode(" + string(b) + ");")
	if got := twDoc(rt); got == target {
		t.Fatalf("setCode landed instantly: the animation never started, so suppression races nothing")
	}
	if n := twPendingTimers(rt); n != 1 {
		t.Fatalf("__pendingTimers() = %d mid-animation, want exactly 1", n)
	}
	// Mid-animation marks must be suppressed, not placed: the live
	// document is mid-edit, so any [3,5) mark would sit on text that
	// does not exist yet.
	rt.run(`window.strudelEditor.setActiveSpans([{ start: 3, end: 5 }]);`)
	if marks := liveHighlightMarks(rt); len(marks) != 0 {
		t.Fatalf("live marks mid-animation = %+v, want none: marks must be suppressed while the document is mid-edit", marks)
	}
	// A newer span set overwrites the buffered one: the buffer holds at
	// most one span set, never appends.
	rt.run(`window.strudelEditor.setActiveSpans([{ start: 6, end: 8 }]); window.strudelEditor.setActiveSpans([{ start: 3, end: 5 }]);`)
	if marks := liveHighlightMarks(rt); len(marks) != 0 {
		t.Fatalf("live marks after re-buffering = %+v, want none: the buffer must hold, not place", marks)
	}
	// Convergence re-applies the LATEST buffered set document-true
	// against the final document: exactly the [3,5) the spanframe tests
	// pin, never the overwritten [6,8).
	twConverge(rt, 40)
	if got := twDoc(rt); got != target {
		t.Fatalf("final document = %q, want %q: the animation must land before marks are asserted", got, target)
	}
	marks := liveHighlightMarks(rt)
	if len(marks) != 1 {
		t.Fatalf("live marks after convergence = %+v, want exactly 1: the buffered spans must re-apply document-true", marks)
	}
	if marks[0].From.Ch != 3 || marks[0].To.Ch != 5 {
		t.Errorf("marked range after convergence = %d..%d, want 3..5: re-applied marks must sit on the FINAL document's token", marks[0].From.Ch, marks[0].To.Ch)
	}
	// An empty active set still clears: the clear contract survives the
	// suppression path.
	rt.run(`window.strudelEditor.setActiveSpans([]);`)
	if marks := liveHighlightMarks(rt); len(marks) != 0 {
		t.Errorf("live marks after empty setActiveSpans = %+v, want none: an empty active set must clear all marks", marks)
	}
	// A span set arriving after convergence applies immediately: the
	// window only lasts while animating (<=2400ms by the pacing budget).
	rt.run(`window.strudelEditor.setActiveSpans([{ start: 3, end: 5 }]);`)
	if marks := liveHighlightMarks(rt); len(marks) != 1 {
		t.Errorf("live marks after post-convergence setActiveSpans = %+v, want exactly 1: suppression must end at convergence", marks)
	}
}

// TestTypewriterTextareaFallback proves the no-CM path is a documented
// no-op: setCode swaps instantly, arms zero timers, and leaves the exact
// value in the textarea.
func TestTypewriterTextareaFallback(t *testing.T) {
	rt := newSyncRuntime(t, sessionJSPrelude)
	rt.run(servedSyncJS(t))
	rt.run(servedSessionJS(t))
	rt.run(vizJSPrelude)
	rt.run(`
document.getElementById = function (id) {
  if (id === "canvas") { return __canvas; }
  if (id === "editor") { return window.__plainTextarea || (window.__plainTextarea = { value: "" }); }
  return null;
};
`)
	rt.run(servedEditorJS(t))
	if got := rt.eval("typeof window.strudelEditor").String(); got != "object" {
		t.Fatalf("window.strudelEditor = %s, want object", got)
	}
	rt.run(`window.strudelEditor.setCode('s("bd cp")');`)
	if got := rt.eval("JSON.stringify(window.__plainTextarea.value)").String(); got != `"s(\"bd cp\")"` {
		t.Errorf("textarea value = %s, want the code instantly: the fallback must swap, not animate", got)
	}
	if n := rt.eval("window.__pendingTimers()").ToInteger(); n != 0 {
		t.Errorf("__pendingTimers() = %d on the textarea path, want 0: the fallback armed a timer", n)
	}
	// Combined highlight + fallback (issue strudel-agent-3f2.4): the
	// textarea path is a documented no-op for highlighting too — the
	// actuator stays silent and leaves the value alone, before and after
	// the swap.
	before := rt.eval("JSON.stringify(window.__plainTextarea.value)").String()
	rt.run(`window.strudelEditor.setActiveSpans([{ start: 3, end: 5 }]);`)
	if got := rt.eval("JSON.stringify(window.__plainTextarea.value)").String(); got != before {
		t.Errorf("textarea value after setActiveSpans = %s, want %s: the highlight must not touch the fallback document", got, before)
	}
	if n := rt.eval("window.__pendingTimers()").ToInteger(); n != 0 {
		t.Errorf("__pendingTimers() = %d after fallback setActiveSpans, want 0: highlighting must arm no timers", n)
	}
	rt.run(`window.strudelEditor.setCode('s("hh")'); window.strudelEditor.setActiveSpans([]);`)
	if got := rt.eval("JSON.stringify(window.__plainTextarea.value)").String(); got != `"s(\"hh\")"` {
		t.Errorf("textarea value = %s, want the swapped code: an empty highlight set must not disturb the fallback document", got)
	}
}
