// Strudel Agent browser session client: validate-then-commit (issue .3vo.5).
//
// The heart of live-change safety. The server never evaluates JavaScript; the
// browser is the only thing that runs strudel. This client subscribes to /ws
// and on each new code version:
//
//  1. validates in a SEPARATE sandbox repl built with
//     strudel.repl({getTime:()=>0, defaultOutput:async()=>{}}) whose
//     evaluate() returns the Pattern on success and sets state.evalError for
//     BOTH syntax and runtime errors;
//  2. only on success commits to the live repl via
//     live.setPattern(pattern, false) so audio hot-swaps without a gap;
//  3. on failure leaves the currently playing pattern untouched and reports
//     the error to POST /api/eval-result.
//
// This order exists because repl.evaluate() calls hush() BEFORE parsing, so
// pushing unvalidated code to the live repl would silence every listener on
// bad code. The sandbox absorbs that hush: its scheduler never starts, so
// hushing it is a no-op while the live repl keeps playing.
//
// Also reports per-event stats gathered from queryArc so the agent can see
// what its code actually produced: haps (event count in the first cycle),
// plus the first event value shape as a sanity sample.
//
// Frame protocol: exactly ONE frame shape {kind, snapshot}; the snapshot is
// replaced wholesale, never merged. See AGENT_API.md.
(function () {
  'use strict';

  var WS_PATH = "/ws";
  var EVAL_RESULT_PATH = "/api/eval-result";
  var RECONNECT_BASE_MS = 1000;
  var RECONNECT_MAX_MS = 15000;

  // Last committed version. Guards against re-processing the same version:
  // reconnects replay the snapshot frame for the current version, and
  // listener-count frames carry snapshots too -- neither is new music.
  var lastVersion = -1;
  // Latest live Pattern committed via setPattern (for viz hooks).
  var currentPattern = null;

  // Build the sandbox repl: a SEPARATE repl whose scheduler never starts, so
  // evaluate() parses and compiles without touching audio. getTime:()=>0
  // freezes its clock; defaultOutput:async()=>{} discards triggers.
  // evaluate() returns the Pattern on success and sets state.evalError for
  // BOTH syntax and runtime errors -- that split is what makes
  // validate-then-commit possible.
  function buildSandbox(strudel) {
    return strudel.repl({
      getTime: function () { return 0; },
      defaultOutput: async function () {},
    });
  }

  // Gather per-event stats from queryArc: how many haps the validated
  // pattern produces in its first cycle, plus a small sample of the first
  // hap value so the agent can see what its code actually produced.
  function collectStats(pattern) {
    var stats = { haps: 0 };
    try {
      var haps = pattern.queryArc(0, 1);
      stats.haps = haps.length;
      if (haps.length > 0 && haps[0] && haps[0].value !== undefined) {
        var sample;
        try {
          sample = JSON.parse(JSON.stringify(haps[0].value));
        } catch (e) {
          sample = String(haps[0].value).slice(0, 120);
        }
        if (typeof sample === "string" && sample.length > 120) {
          sample = sample.slice(0, 120);
        }
        stats.sample = sample;
      }
    } catch (err) {
      // queryArc itself failed after a successful evaluate: report the
      // pattern as valid with zero countable haps plus the probe error.
      stats.haps = 0;
      stats.queryError = String((err && err.message) || err).slice(0, 200);
    }
    return stats;
  }

  function postEvalResult(version, ok, error, stats) {
    var body = { version: version, ok: ok, stats: stats || { haps: 0 } };
    if (!ok && error) {
      body.error = String(error).slice(0, 500);
    }
    return fetch(EVAL_RESULT_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).catch(function (err) {
      console.warn("[session] failed to report eval-result:", err);
    });
  }

  function showMessage(snapshot) {
    var el = document.getElementById("agent-message");
    if (el && snapshot && snapshot.lastAgentMessage) {
      el.textContent = snapshot.lastAgentMessage;
    }
  }

  // showCode routes every version through the live code view adapter
  // (window.strudelEditor, issue .3vo.6) when it exists, falling back to the
  // plain textarea when the editor module has not loaded. The adapter owns
  // the CodeMirror instance or the textarea value; nothing here touches
  // either directly, so the human-edit path can be added inside the adapter
  // without reworking this flow.
  //
  // A version that has just arrived replaces what is on screen, so any
  // eval-error highlight left from the PREVIOUS version no longer describes
  // the document in view: it is cleared here, and re-applied by
  // showEvalError only if this version fails in turn. The landed-version
  // flash is emitted here too, so the cue is tied to the arrival rather than
  // to the verdict.
  function showCode(snapshot) {
    if (!snapshot || typeof snapshot.code !== 'string') {
      return;
    }
    var ed = (typeof window !== 'undefined' && window.strudelEditor) || null;
    if (ed && typeof ed.setCode === 'function') {
      ed.setCode(snapshot.code);
      if (typeof ed.flashUpdate === 'function') {
        ed.flashUpdate();
      }
      clearEvalError();
      return;
    }
    var editor = document.getElementById('editor');
    if (editor) {
      if (document.activeElement !== editor) {
        editor.value = snapshot.code;
      }
    }
  }

  function showEvalError(message) {
    var ed = (typeof window !== 'undefined' && window.strudelEditor) || null;
    if (ed && typeof ed.markError === 'function') {
      ed.markError(message);
    }
  }

  function clearEvalError() {
    var ed = (typeof window !== 'undefined' && window.strudelEditor) || null;
    if (ed && typeof ed.clearError === 'function') {
      ed.clearError();
    }
  }

  // Validate-then-commit for one code version. Resolves when reported.
  async function applyVersion(snapshot, sandbox, live) {
    var version = snapshot.version;
    var code = snapshot.code;
    lastVersion = version;

    showCode(snapshot);
    showMessage(snapshot);

    if (!code || !code.trim()) {
      // Empty document: nothing to validate, nothing to commit. The live
      // pattern keeps playing untouched.
      return postEvalResult(version, true, "", { haps: 0 });
    }

    // (1) Validate in the sandbox repl. autostart=false keeps the sandbox
    // scheduler stopped; hush=false skips even the sandbox hush -- belt and
    // braces, the sandbox has no audio either way.
    var pattern = null;
    try {
      pattern = await sandbox.evaluate(code, false, false);
    } catch (err) {
      pattern = null;
    }
    var evalError = sandbox.state && sandbox.state.evalError;
    if (!pattern || evalError) {
      // (3) Failure: leave the playing pattern untouched, report the error.
      // state.evalError covers BOTH syntax and runtime errors, which is why
      // this checks it rather than only the throw.
      var message = evalError
        ? String((evalError && evalError.message) || evalError)
        : "evaluation produced no pattern";
      console.warn("[session] version " + version + " failed validation:", message);
      showEvalError(message);
      return postEvalResult(version, false, message, { haps: 0 });
    }

    // (2) Success: commit to the live repl. setPattern(pattern, false)
    // hot-swaps audio without a gap; false means "do not autostart", so a
    // listener that has not clicked play yet is not force-started.
    var stats = collectStats(pattern);
    try {
      await live.setPattern(pattern, false);
    } catch (err) {
      var commitMessage = String((err && err.message) || err);
      console.warn("[session] version " + version + " failed to commit:", commitMessage);
      showEvalError(commitMessage);
      return postEvalResult(version, false, commitMessage, { haps: 0 });
    }
    currentPattern = pattern;
    console.info("[session] version " + version + " live (" + stats.haps + " haps)");
    return postEvalResult(version, true, "", stats);
  }

  function isNewCodeVersion(snapshot) {
    return (
      snapshot &&
      typeof snapshot.version === "number" &&
      typeof snapshot.code === "string" &&
      snapshot.version !== lastVersion
    );
  }

  function connect(sandbox, live, attempt) {
    var protocol = location.protocol === "https:" ? "wss:" : "ws:";
    var ws = new WebSocket(protocol + "//" + location.host + WS_PATH);

    ws.onopen = function () {
      attempt = 0;
      console.info("[session] subscribed to " + WS_PATH);
    };

    ws.onmessage = function (event) {
      var frame;
      try {
        frame = JSON.parse(event.data);
      } catch (err) {
        console.warn("[session] ignoring unparseable frame:", err);
        return;
      }
      var snapshot = frame && frame.snapshot;
      if (!snapshot) {
        return;
      }
      // Transport / message / eval-result / listener-count frames carry the
      // same snapshot shape: keep panels fresh, but only new code versions
      // go through validate-then-commit. A failing eval-result verdict for
      // the version on screen also highlights the view, so a failure
      // reported by another listener is visible here too.
      showMessage(snapshot);
      if (frame.kind !== "code" && frame.kind !== "snapshot") {
        if (typeof snapshot.version === "number") {
          lastVersion = Math.max(lastVersion, snapshot.version);
        }
        if (frame.kind === "eval-result" && snapshot.lastEvalResult &&
            !snapshot.lastEvalResult.ok && snapshot.lastEvalResult.error) {
          showEvalError(snapshot.lastEvalResult.error);
        }
        return;
      }
      if (!isNewCodeVersion(snapshot)) {
        return;
      }
      applyVersion(snapshot, sandbox, live);
    };

    ws.onclose = function () {
      var delay = Math.min(RECONNECT_BASE_MS * Math.pow(2, attempt || 0), RECONNECT_MAX_MS);
      console.warn("[session] disconnected; reconnecting in " + delay + "ms");
      setTimeout(function () {
        connect(sandbox, live, (attempt || 0) + 1);
      }, delay);
    };

    ws.onerror = function (err) {
      console.warn("[session] websocket error:", err);
    };
  }

  // Boot: wait for initStrudel (live repl with audio), build the sandbox
  // repl beside it, then subscribe. Both repls come from the same strudel
  // bundle globals so they parse the same language.
  function boot() {
    var ready = window.strudelInitPromise;
    if (!ready) {
      console.error("[session] strudelInitPromise missing: is initStrudel wired?");
      return;
    }
    ready.then(function (live) {
      var strudel = window.strudel || window.strudelScope || null;
      if (!strudel || typeof strudel.repl !== "function") {
        console.error("[session] strudel.repl unavailable: cannot build the sandbox repl");
        return;
      }
      var sandbox = buildSandbox(strudel);
      window.strudelSandbox = sandbox;
      console.info("[session] sandbox repl ready; subscribing");
      connect(sandbox, live, 0);
    }).catch(function (err) {
      console.error("[session] live repl failed to start:", err);
    });
  }

  // Exposed for testing / debugging without a socket. applyVersion and
  // postEvalResult are exposed so the headless harness can drive the full
  // validate-then-commit path (success commits, failure leaves live alone)
  // with stub repls and assert on the POST /api/eval-result bodies.
  window.strudelSession = {
    buildSandbox: buildSandbox,
    collectStats: collectStats,
    isNewCodeVersion: isNewCodeVersion,
    applyVersion: applyVersion,
    postEvalResult: postEvalResult,
    getLastVersion: function () { return lastVersion; },
    getCurrentPattern: function () { return currentPattern; },
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
