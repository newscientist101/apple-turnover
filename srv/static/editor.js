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

  // setCode replaces the whole document in place. Versions arrive wholesale
  // from the server (never merged), so the view does too. Skipped while the
  // element has focus so a future editable mode never clobbers typing; in the
  // current read-only mode focus cannot edit, but the guard keeps the future
  // path from needing a rewrite.
  function setCode(code) {
    if (typeof code !== 'string') {
      return;
    }
    if (cm) {
      if (cm.hasFocus && cm.hasFocus()) {
        return;
      }
      if (cm.getValue() !== code) {
        cm.setValue(code);
      }
      return;
    }
    var el = findTextarea();
    if (el && typeof document !== 'undefined' &&
        document.activeElement !== el && el.value !== code) {
      el.value = code;
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
