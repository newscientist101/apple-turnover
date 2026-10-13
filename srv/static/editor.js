// Strudel Agent live code view: CodeMirror adapter (issue .3vo.6).
//
// The agent is the sole writer of the single performer instance, so the view
// is read-only by default. This module owns the ONLY CodeMirror touchpoint:
// it upgrades the shell's <textarea id="editor"> in place (fromTextArea) when
// the pinned CDN bundle loaded, and otherwise operates the textarea directly,
// so a blocked CDN degrades to today's plain-text view instead of a blank one.
//
// Future human-edit path: call window.strudelEditor.setReadOnly(false) and add
// a push path beside it. No caller outside this file touches the CodeMirror
// instance or the textarea value; session.js drives versions through setCode,
// flashUpdate, markError and clearError only.
(function () {
  'use strict';

  // Pinned CDN bundle (see srv/templates/welcome.html): CodeMirror 5.65.16.
  // The version is pinned so the view cannot drift under a floating tag.
  var EXPECTED_VERSION = '5.65.16';

  var textarea = null;
  var cm = null;
  var readOnly = true;
  var errorLine = null;
  var flashTimer = null;
  // Handles of the currently outlined sounding-token marks (CodeMirror
  // TextMarkers). Recomputed from scratch every viz frame; no cross-frame
  // state survives beyond this list.
  var activeMarks = [];

  // Typewriter edit simulation (issue strudel-agent-3f2, design leaf
  // strudel-agent-3f2.2). When a code frame changes the displayed document
  // the change is SIMULATED as a human edit: the visible cursor moves to
  // the edit site, old text is deleted and new text is inserted in bounded
  // steps, ending with the displayed document EXACTLY equal to the new
  // version's code. Normative constants (tests pin them):
  // STEP_MS=162, DEL_CHUNK=1 char/tick (delete phase first), INS_CHUNK=1
  // char/tick, TOTAL_BUDGET_MS=4860, MAX_STEPS=30 (=4860/162). A typical
  // 1-3 character change therefore takes ~162-486ms per visible step,
  // slow enough to read; very large changes still jump.
  var TYPEWRITER_STEP_MS = 162;
  var TYPEWRITER_DEL_CHUNK = 1;
  var TYPEWRITER_INS_CHUNK = 1;
  var TYPEWRITER_MAX_STEPS = 30;
  // At most ONE animation script is pending: a newer setCode cancels the
  // in-flight setTimeout and re-derives from the live buffer (churn
  // RETARGET, single-flight). The handle is the only cross-tick state.
  var typewriterTimer = null;
  // Highlight suppression (issue strudel-agent-3f2.4, design D3 choice
  // (a) SUPPRESS/FREEZE). While the displayed document is mid-edit it is
  // NOT the committed document, so no span can be document-true yet.
  // typewriterAnimating is true from animation start to convergence (at
  // most the TOTAL_BUDGET 4860ms pacing window); pendingSpans holds at
  // most ONE buffered span set (newer overwrites, never appends),
  // re-applied document-true against the final document at convergence.
  var typewriterAnimating = false;
  var pendingSpans = null;

  function findTextarea() {
    if (textarea) {
      return textarea;
    }
    if (typeof document === 'undefined' || !document.getElementById) {
      return null;
    }
    textarea = document.getElementById('editor');
    return textarea;
  }

  // init upgrades the textarea when CodeMirror is available. Idempotent: safe
  // to call on every boot path. Returns true when a CodeMirror instance owns
  // the view, false when the textarea remains the view.
  function init() {
    var el = findTextarea();
    if (!el || cm) {
      return !!cm;
    }
    if (typeof window !== 'undefined' && window.CodeMirror &&
        typeof window.CodeMirror.fromTextArea === 'function') {
      try {
        cm = window.CodeMirror.fromTextArea(el, {
          mode: 'javascript',
          lineNumbers: true,
          readOnly: readOnly,
          viewportMargin: Infinity,
        });
        // Steady caret (issue strudel-agent-3f2.6; the 3f2.1 live finding
        // is that the fake caret is visible only when focused, and
        // blinkRate 0 makes it steady). Requested once at init when the
        // instance supports setOption; focusing the editor is NOT an
        // option (it would trip setCode's hasFocus guard on the next
        // frame), so the caret is steadied without stealing focus.
        if (cm && typeof cm.setOption === 'function') {
          try {
            cm.setOption('cursorBlinkRate', 0);
          } catch (err) {
            // Caret steadiness is cosmetic best-effort, never load-bearing.
          }
        }
      } catch (err) {
        cm = null;
      }
    }
    return !!cm;
  }

  function currentCode() {
    if (cm) {
      return cm.getValue();
    }
    var el = findTextarea();
    return el ? el.value : '';
  }

  // cancelTypewriter drops the in-flight animation tick, if any. Called
  // by every setCode before deriving the new script, so a superseding
  // version never replays stale steps and at most one timer is ever
  // outstanding (the session.js:689-692 supersede-cancel discipline).
  function cancelTypewriter() {
    if (typewriterTimer !== null) {
      clearTimeout(typewriterTimer);
      typewriterTimer = null;
    }
  }

  // typewriterCanAnimate reports whether the animated path is available:
  // a CodeMirror instance with the programmatic edit surface the research
  // leaf verified live (replaceRange + operation). Otherwise setCode takes
  // the instant path (textarea fallback: documented no-op, zero timers).
  function typewriterCanAnimate() {
    return !!cm && typeof cm.replaceRange === 'function' &&
        typeof cm.operation === 'function';
  }

  // typewriterDerive computes the single prefix/suffix edit region from
  // the CURRENT DISPLAYED text O to the new version code N, in UTF-16 code
  // units (matching the CM5 posFromIndex/indexFromPos round-trip). Multi-
  // region diffs collapse into the one spanning region, never staged.
  // Returns { start, delEnd, insert } where [start, delEnd) is deleted
  // from O and insert is typed at start, or null when both are empty
  // (synchronous no-op).
  function typewriterDerive(oldText, newText) {
    var oldLen = oldText.length;
    var newLen = newText.length;
    var p = 0;
    while (p < oldLen && p < newLen && oldText.charAt(p) === newText.charAt(p)) {
      p++;
    }
    var s = 0;
    while (s < oldLen - p && s < newLen - p &&
        oldText.charAt(oldLen - 1 - s) === newText.charAt(newLen - 1 - s)) {
      s++;
    }
    var delLen = oldLen - s - p;
    var insert = newText.slice(p, newLen - s);
    if (delLen <= 0 && insert.length === 0) {
      return null;
    }
    return { start: p, delEnd: oldLen - s, insert: insert };
  }

  // typewriterEstimate counts setTimeout ticks for one script: deletion
  // ticks first, then insertion ticks.
  function typewriterEstimate(delLen, insLen) {
    return Math.ceil(delLen / TYPEWRITER_DEL_CHUNK) +
        Math.ceil(insLen / TYPEWRITER_INS_CHUNK);
  }

  // typewriterMarker toggles the 'strudel-animating' class on the
  // CodeMirror wrapper while an animation runs (issue strudel-agent-3f2.6).
  // The stylesheet keeps .CodeMirror-cursor visible under that marker, so
  // the caret tracks the edit WITHOUT stealing focus (a focus steal would
  // trip setCode's hasFocus guard on the next frame). Feature-detected via
  // getWrapperElement: no wrapper, no classList, no marker — and never
  // throws, so a hostile host cannot break the animation.
  function typewriterMarker(on) {
    if (!cm || typeof cm.getWrapperElement !== 'function') {
      return;
    }
    var root = null;
    try {
      root = cm.getWrapperElement();
    } catch (err) {
      return;
    }
    if (!root || !root.classList) {
      return;
    }
    try {
      if (on) {
        root.classList.add('strudel-animating');
      } else {
        root.classList.remove('strudel-animating');
      }
    } catch (err) {
      // The marker is cosmetic; it must never break the animation.
    }
  }

  // typewriterFinish lands the script exactly and disarms. Every path
  // through the animation — stepped convergence, instant clamp, supersede
  // landing — funnels here so the end state is provably exact. Landing
  // also ends highlight suppression: the buffered span set, if any, is
  // re-applied via the real setActiveSpans against the final document,
  // document-true by construction (marks never appear on text that does
  // not exist yet).
  function typewriterFinish(target) {
    typewriterTimer = null;
    // The marker leaves on EVERY landing path: stepped convergence, the
    // instant clamp, and the drift-guard landing all funnel here. (The
    // supersede reset in setCode removes it too, for the abandoned script
    // that never lands.)
    typewriterMarker(false);
    if (cm.getValue() !== target) {
      cm.setValue(target);
    }
    typewriterAnimating = false;
    var buffered = pendingSpans;
    pendingSpans = null;
    if (buffered && buffered.length > 0) {
      setActiveSpans(buffered);
    }
  }

  // typewriterStep runs one tick of the in-flight script: delete phase
  // first (shrink the delete region by one char from its END, cursor
  // parked at the frontier), then insertion phase (one char at the
  // cursor). Each tick is wrapped in
  // cm.operation() (one changes-event + one layout pass per tick) and
  // followed by setCursor(frontier) + scrollIntoView(frontier).
  function typewriterStep(script) {
    typewriterTimer = null;
    // Phase counters live on the script, never derived from the document
    // length: after the delete phase the typed prefix would otherwise read
    // as delete remainder and the animation would eat its own typing. The
    // length invariant below is the drift guard — a superseding setCode
    // cancels this script first, so a mismatch can only come from an
    // outside edit, and landing exactly beats typing stale text.
    var cur = cm.getValue();
    if (cur.length !== script.start + script.delLeft + script.insAt + script.suffixLen ||
        cur.slice(0, script.start) !== script.prefix) {
      typewriterFinish(script.target);
      return;
    }
    cm.operation(function () {
      if (script.delLeft > 0) {
        var take = Math.min(TYPEWRITER_DEL_CHUNK, script.delLeft);
        var from = cm.posFromIndex(script.start + script.delLeft - take);
        var to = cm.posFromIndex(script.start + script.delLeft);
        script.delLeft -= take;
        cm.replaceRange('', from, to);
      } else if (script.insAt < script.insert.length) {
        var chunk = script.insert.substr(script.insAt, TYPEWRITER_INS_CHUNK);
        cm.replaceRange(chunk, cm.posFromIndex(script.start + script.insAt));
        script.insAt += chunk.length;
      }
    });
    // Cursor parks at the frontier: the shrinking delete edge during the
    // delete phase, the growing typed edge during insertion.
    var caret = script.delLeft > 0 ? script.start + script.delLeft : script.start + script.insAt;
    if (typeof cm.setCursor === 'function') {
      try {
        cm.setCursor(cm.posFromIndex(caret));
      } catch (err) {
        // Caret placement is cosmetic best-effort, never load-bearing.
      }
    }
    if (typeof cm.scrollIntoView === 'function') {
      try {
        cm.scrollIntoView(cm.posFromIndex(caret));
      } catch (err) {
        // Scrolling is cosmetic best-effort, never load-bearing.
      }
    }
    if (cm.getValue() === script.target) {
      typewriterFinish(script.target);
      return;
    }
    typewriterTimer = setTimeout(function () { typewriterStep(script); }, TYPEWRITER_STEP_MS);
  }

  // typewriterAnimate arms the bounded animation for one script, or
  // clamps to the instant path when the estimate exceeds MAX_STEPS.
  // CLAMP (big changes jump): est > 30 -> instant cm.setValue(N) +
  // flashUpdate exactly as today, zero animation timers.
  function typewriterAnimate(script) {
    if (typewriterEstimate(script.delLeft, script.insert.length) > TYPEWRITER_MAX_STEPS) {
      typewriterFinish(script.target);
      return;
    }
    typewriterAnimating = true;
    typewriterMarker(true);
    typewriterTimer = setTimeout(function () { typewriterStep(script); }, TYPEWRITER_STEP_MS);
  }

  // setCode replaces the whole document in place. Versions arrive wholesale
  // from the server (never merged), so the view does too. Skipped while the
  // element has focus so a future editable mode never clobbers typing; in the
  // current read-only mode focus cannot edit, but the guard keeps the future
  // path from needing a rewrite.
  function setCode(code) {
    if (typeof code !== 'string') {
      return;
    }
    // Dynamic outlines belong to the old document: drop them even when the
    // text is unchanged (setValue drops marks with the document, but the
    // equal-text and textarea paths never reach it, and a mid-frame
    // staleness must resolve to "dark, re-mark next frame").
    clearActiveMarks();
    cancelTypewriter();
    // The abandoned script's buffered spans describe a document that will
    // never land; drop them with the script. The new animation (or the
    // instant path, which converges synchronously below) starts clean.
    typewriterAnimating = false;
    pendingSpans = null;
    // The abandoned script's marker class leaves with the script (it
    // never lands, so typewriterFinish never runs for it); the new
    // animation below re-adds the marker if it animates.
    typewriterMarker(false);
    if (cm) {
      if (cm.hasFocus && cm.hasFocus()) {
        return;
      }
      var current = cm.getValue();
      if (current === code) {
        return;
      }
      // The animation is view-only, behind this synchronous return: it
      // arms at most one fake-clock timer and never delays validation,
      // the verdict POST, or the bar-aligned commit (feedback-loop
      // immunity: setCode adds 0ms to time-to-verdict by construction).
      if (typewriterCanAnimate()) {
        var region = typewriterDerive(current, code);
        if (region === null) {
          return;
        }
        typewriterAnimate({
          start: region.start,
          delLeft: region.delEnd - region.start,
          insert: region.insert,
          insAt: 0,
          // Script-fixed witnesses, captured once at derivation: the
          // kept prefix proves the buffer still matches this script on
          // every tick, and the kept suffix length bounds the delete
          // phase. A superseding setCode cancels this script first, so
          // these are guards, not versioning.
          prefix: current.slice(0, region.start),
          suffixLen: current.length - region.delEnd,
          target: code,
        });
        return;
      }
      cm.setValue(code);
      return;
    }
    var el = findTextarea();
    if (el && typeof document !== 'undefined' &&
        document.activeElement !== el && el.value !== code) {
      el.value = code;
    }
  }

  // clearActiveMarks drops every sounding-token outline. Called by
  // setActiveSpans, by setCode, and by viz when there is no pattern.
  function clearActiveMarks() {
    for (var i = 0; i < activeMarks.length; i++) {
      try {
        if (activeMarks[i] && typeof activeMarks[i].clear === 'function') {
          activeMarks[i].clear();
        }
      } catch (err) {
        // A stale handle must never break the next version update.
      }
    }
    activeMarks = [];
  }

  // setActiveSpans replaces all dynamic outlines with exactly the given
  // spans: absolute character offsets [{start, end}] into the evaluated
  // code, as collected from hap.context.locations by viz.js. Invalid spans
  // (non-numbers, end <= start, entirely out of range) are skipped;
  // partially out-of-range spans clamp to the document length. A dumb
  // actuator by design: dedup lives in the caller, and the error-line path
  // (addLineClass) stays untouched so the two highlights cannot share a
  // handle. A no-op when CodeMirror is absent: the plain textarea shows no
  // highlight but keeps working.
  function setActiveSpans(spans) {
    // While the typewriter animation is in flight the displayed document
    // is mid-edit, so no span can be document-true: buffer the latest set
    // (one slot, newer overwrites) and place nothing. An empty set still
    // clears the buffer, preserving the clear contract. The buffer is
    // re-applied at convergence via this same function.
    if (typewriterAnimating) {
      clearActiveMarks();
      if (spans && typeof spans.length === 'number' && spans.length > 0) {
        pendingSpans = spans;
      } else {
        pendingSpans = null;
      }
      return;
    }
    clearActiveMarks();
    if (!cm) {
      return;
    }
    if (!spans || typeof spans.length !== 'number') {
      return;
    }
    try {
      var docLen = cm.getValue().length;
      for (var i = 0; i < spans.length; i++) {
        var span = spans[i];
        if (!span || typeof span.start !== 'number' || typeof span.end !== 'number') {
          continue;
        }
        if (isNaN(span.start) || isNaN(span.end)) {
          continue;
        }
        var start = Math.max(0, Math.min(docLen, Math.floor(span.start)));
        var end = Math.max(0, Math.min(docLen, Math.ceil(span.end)));
        if (end <= start) {
          continue;
        }
        try {
          activeMarks.push(cm.markText(cm.posFromIndex(start), cm.posFromIndex(end), {
            className: 'editor-active-token',
          }));
        } catch (err) {
          // One bad range must not take down the frame loop.
        }
      }
    } catch (err) {
      // The rAF loop must never throw.
    }
  }

  function editorRoot() {
    if (cm && cm.getWrapperElement) {
      return cm.getWrapperElement();
    }
    return findTextarea();
  }

  // flashUpdate emits the subtle "a new version landed" cue. Class removal +
  // forced reflow + re-add restarts the CSS animation on every version, even
  // when versions land back-to-back.
  function flashUpdate() {
    var root = editorRoot();
    if (!root || !root.classList) {
      return;
    }
    root.classList.remove('editor-flash');
    // Force reflow so re-adding the class restarts the animation.
    void root.offsetWidth;
    root.classList.add('editor-flash');
    if (flashTimer) {
      clearTimeout(flashTimer);
    }
    flashTimer = setTimeout(function () {
      root.classList.remove('editor-flash');
    }, 700);
  }

  function clearError() {
    if (cm && errorLine !== null) {
      // CodeMirror 5's addLineClass returns the LINE, not a clearable handle,
      // so the class is taken back off by line number. Guarded: a document
      // replaced since the error was marked can leave the line out of range.
      try {
        cm.removeLineClass(errorLine, 'background', 'editor-error-line');
      } catch (err) {
        // A stale line number must never break the next version update.
      }
    }
    errorLine = null;
    var root = editorRoot();
    if (root && root.classList) {
      root.classList.remove('editor-error');
    }
    setStatus('');
  }

  function setStatus(text) {
    if (typeof document === 'undefined' || !document.getElementById) {
      return;
    }
    var el = document.getElementById('editor-status');
    if (el) {
      el.textContent = text;
    }
  }

  // markError highlights the failing region when a version fails to evaluate.
  // Server verdicts carry only a message string, never a range, so this is
  // best-effort: line 0 is marked when no line number can be parsed from the
  // message. The previously playing document stays in the view; only the
  // highlight + status line change.
  function markError(message) {
    clearError();
    var text = message ? String(message).slice(0, 300) : 'evaluation failed';
    var line = 0;
    var m = /line\s+(\d+)/i.exec(text);
    if (m) {
      line = Math.max(0, parseInt(m[1], 10) - 1 || 0);
    }
    if (cm) {
      try {
        var lastLine = cm.lineCount() - 1;
        if (line > lastLine) {
          line = lastLine;
        }
        cm.addLineClass(line, 'background', 'editor-error-line');
        errorLine = line;
      } catch (err) {
        errorLine = null;
      }
    }
    var root = editorRoot();
    if (root && root.classList) {
      root.classList.add('editor-error');
    }
    setStatus('Last version failed to evaluate: ' + text);
  }

  // setReadOnly is the seam for the future human-edit path: flip to false and
  // add a push path beside it. Today the view boots read-only and nothing
  // calls this with false.
  function setReadOnly(ro) {
    readOnly = !!ro;
    if (cm && typeof cm.setOption === 'function') {
      cm.setOption('readOnly', readOnly);
    }
    var el = findTextarea();
    if (el) {
      if (readOnly) {
        el.setAttribute('readonly', 'readonly');
      } else {
        el.removeAttribute('readonly');
      }
    }
  }

  function isReadOnly() {
    return readOnly;
  }

  function version() {
    if (typeof window !== 'undefined' && window.CodeMirror &&
        window.CodeMirror.version) {
      return window.CodeMirror.version;
    }
    return null;
  }

  function boot() {
    init();
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', boot);
    } else {
      boot();
    }
  }

  var api = {
    expectedVersion: EXPECTED_VERSION,
    init: init,
    setCode: setCode,
    currentCode: currentCode,
    flashUpdate: flashUpdate,
    markError: markError,
    clearError: clearError,
    setActiveSpans: setActiveSpans,
    clearActiveMarks: clearActiveMarks,
    setReadOnly: setReadOnly,
    isReadOnly: isReadOnly,
    version: version,
  };

  if (typeof window !== 'undefined') {
    window.strudelEditor = api;
  }
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = api;
  }
})();
