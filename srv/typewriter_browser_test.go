package srv

// Typewriter edit simulation (issue strudel-agent-3f2.3), executed as served.
//
// When a code frame changes the displayed document the change is SIMULATED
// as a human edit: the visible cursor moves to the edit site, old text is
// deleted and new text is inserted in bounded steps, ending with the
// displayed document EXACTLY equal to the new version's code. The mechanics
// are fixed by the design leaf strudel-agent-3f2.2 and implemented in the
// served editor.js: single prefix/suffix edit region (UTF-16 offsets),
// pacing via the fake-clock setTimeout chain (STEP_MS=30, DEL_CHUNK=8,
// INS_CHUNK=4, TOTAL_BUDGET_MS=1500, MAX_STEPS=50), instant clamp for big
// diffs, churn retarget single-flight, textarea instant no-op.
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
        return { clear: function () {} };
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
      getWrapperElement: function () { return null; },
      setOption: function () {},
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

// twConverge advances the fake clock STEP_MS at a time until no animation
// timer remains, failing fast if the design budget is exceeded. It returns
// the number of advances used.
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
		rt.run("window.__advance(30)")
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
// design's MAX_STEPS=50 budget, leave zero timers outstanding, move the
// caret to the edit frontier, and scroll the frontier into view.
func TestTypewriterBoundedAnimation(t *testing.T) {
	rt := newTypewriterRuntime(t)
	twSetDoc(rt, `s("bd")`)
	rt.run(`window.strudelEditor.setCode('s("bd cp")');`)
	if got := twDoc(rt); got == `s("bd cp")` {
		t.Fatalf("setCode landed instantly: the edit was not simulated as a bounded animation")
	}
	used := twConverge(rt, 60)
	if used > 50 {
		t.Errorf("converged in %d advances, want <= 50 (MAX_STEPS): the pacing budget is violated", used)
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
	// (up to DEL_CHUNK=8 chars/tick, tiled contiguously from the region
	// end), then the insert types (up to INS_CHUNK=4 chars/tick) at the
	// cursor — each tick collapsing to exactly one 'changes' event.
	rt2 := newTypewriterRuntime(t)
	oldDoc := `s("` + strings.Repeat("d", 20) + `")`
	newDoc := `s("` + strings.Repeat("i", 9) + `")`
	twSetDoc(rt2, oldDoc)
	b, _ := json.Marshal(newDoc)
	rt2.run("window.strudelEditor.setCode(" + string(b) + ");")
	if got := twDoc(rt2); got == newDoc {
		t.Fatalf("two-phase setCode landed instantly: the edit was not simulated")
	}
	for i := 0; i < 6; i++ {
		if n := twPendingTimers(rt2); n != 1 {
			t.Fatalf("before tick %d: __pendingTimers() = %d, want exactly 1: at most one animation timer may be outstanding", i, n)
		}
		rt2.run("window.__advance(30)")
	}
	if n := twPendingTimers(rt2); n != 0 {
		t.Fatalf("__pendingTimers() = %d after 6 ticks, want 0: the 20-del/9-ins script needs ceil(20/8)+ceil(9/4)=6 ticks", n)
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
	// Region [3,23): prefix s(" is 3 chars, suffix ") is 2.
	want := []struct {
		Text string
		Del  int
		From int
		To   int
	}{
		{"", 8, 15, 23}, {"", 8, 7, 15}, {"", 4, 3, 7},
		{"iiii", 0, 3, 3}, {"iiii", 0, 7, 7}, {"i", 0, 11, 11},
	}
	if len(edits) != len(want) {
		t.Fatalf("edit log has %d entries, want %d (one replaceRange per tick): %s", len(edits), len(want), raw)
	}
	for i, w := range want {
		if edits[i].Text != w.Text || edits[i].Del != w.Del || edits[i].From != w.From || edits[i].To != w.To {
			t.Errorf("tick %d edit = %+v, want text=%q del=%d [%d,%d): deletes tile from the region end first, then inserts type at the cursor", i, edits[i], w.Text, w.Del, w.From, w.To)
		}
	}
	if got := rt2.eval("window.__cmChanges").ToInteger(); got != 6 {
		t.Errorf("__cmChanges = %d, want 6: each tick's operation() must collapse to exactly one changes-event", got)
	}
	if got := rt2.eval("JSON.stringify(window.__twCursorLog)").String(); got != "[15,7,3,7,11,12]" {
		t.Errorf("caret log = %s, want [15,7,3,7,11,12]: the cursor parks at the shrinking delete edge, then rides the typed edge", got)
	}
	if got := rt2.eval("JSON.stringify(window.__twScrollLog)").String(); got != "[15,7,3,7,11,12]" {
		t.Errorf("scroll log = %s, want the caret trail: every tick scrolls the frontier into view", got)
	}
}

// TestTypewriterExactFinalState pins the derivation edges: empty->code,
// code->empty, the identical no-op (synchronous, zero timers), unicode
// offsets in UTF-16 code units, and the 64 KiB clamp path (instant setValue
// + zero animation timers).
func TestTypewriterExactFinalState(t *testing.T) {
	t.Run("empty-to-code", func(t *testing.T) {
		rt := newTypewriterRuntime(t)
		twSetDoc(rt, "")
		rt.run(`window.strudelEditor.setCode('s("bd")');`)
		twConverge(rt, 60)
		if got := twDoc(rt); got != `s("bd")` {
			t.Errorf("final document = %q, want %q", got, `s("bd")`)
		}
	})
	t.Run("code-to-empty", func(t *testing.T) {
		rt := newTypewriterRuntime(t)
		twSetDoc(rt, `s("bd")`)
		rt.run(`window.strudelEditor.setCode('');`)
		twConverge(rt, 60)
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
		twConverge(rt, 60)
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
}

// TestTypewriterSupersede drives two versions into the animation window:
// the final document must be exactly the LATEST code, at most one timer
// may be outstanding at any moment, and convergence after the LAST setCode
// must stay within the design budget.
func TestTypewriterSupersede(t *testing.T) {
	rt := newTypewriterRuntime(t)
	twSetDoc(rt, `s("bd")`)
	// The first version needs 5 ticks (20 inserts at INS_CHUNK=4), so it
	// is provably still in flight when the second version lands: a
	// supersede of an already-converged script would prove nothing about
	// cancellation (and let the no-cancel mutation survive).
	first := `s("bd ` + strings.Repeat("h", 20) + `")`
	b, _ := json.Marshal(first)
	rt.run("window.strudelEditor.setCode(" + string(b) + ");")
	rt.run(`window.__advance(30); window.__advance(30);`)
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
	used := twConverge(rt, 60)
	if used > 50 {
		t.Errorf("converged in %d advances after the last setCode, want <= 50: retarget must restart within budget", used)
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
	twConverge(rt, 60)
	if got := twDoc(rt); got != `s("bd cp")` {
		t.Errorf("final document = %q after convergence, want the version code: the view must still land", got)
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
}
