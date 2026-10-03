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
  // Handle for the commit scheduled at the next cycle boundary, so a NEWER
  // version can supersede a PENDING one (issue .3vo.8.2). Null when nothing is
  // pending.
  var pendingCommit = null;

  // Sync status UI state (issue .3vo.8.5).
  var connectionState = "reconnecting";
  var latestAnchor = null;
  var statusTimer = null;

  // AGENT_STATES is the whole badge vocabulary: three states the wire can
  // actually express, each with its own class and its own words.
  //
  // "never" is kept distinct from "absent" deliberately. They are different
  // situations and an operator acts on them differently: NEVER means no agent
  // has ever run and one should be started; LOST means one was here and has
  // stopped, which is the case worth investigating. Collapsing them would lose
  // exactly the distinction this badge was added to provide.
  var AGENT_STATES = {
    connected: { cls: "agent-connected", label: "AGENT LIVE" },
    absent: { cls: "agent-absent", label: "AGENT LOST" },
    never: { cls: "agent-never", label: "AGENT NEVER" },
  };

  // agentStateOf reads the three wire states out of a snapshot.
  //
  // It is defensive about the agent field on purpose. A browser can legitimately
  // hold a frame encoded before the server knew about presence — a reconnect
  // replaying an old frame, or a server upgraded underneath a live page. Throwing
  // here would take down the whole onmessage handler and with it the music, so a
  // missing field degrades to "never" instead.
  function agentStateOf(snapshot) {
    var agent = snapshot && snapshot.agent;
    if (!agent || typeof agent !== "object") {
      return "never";
    }
    if (agent.active === true) {
      return "connected";
    }
    // lastSeenMs === 0 is the server's "no agent has EVER connected" marker,
    // distinct from a heartbeat that landed at the epoch.
    return agent.lastSeenMs ? "absent" : "never";
  }

  // renderAgentStatus paints the badge from a snapshot.
  //
  // The presence decision comes ENTIRELY from the server's snapshot.agent. The
  // client deliberately does not compare agent.lastSeenMs against its own
  // Date.now() to decide whether the lease has expired: that would inherit
  // clock skew between browser and server and the two would disagree near the
  // boundary. The server owns the TTL; the client reports what it was told. The
  // expiry arrives as an "agent" frame when the server decides it.
  //
  // Every write is guarded by comparing against what the element already
  // holds, for the same reason updateSyncStatusUI guards its own region: this
  // one is aria-live, and every frame carries the full snapshot, so an
  // unconditional write would re-announce the badge on every message and turn a
  // status line into a screen-reader chatterbox. Comparing per element — rather
  // than caching a "last state" and skipping the whole render — means a
  // partially-applied update still repairs itself.
  function renderAgentStatus(snapshot) {
    var state = AGENT_STATES[agentStateOf(snapshot)];
    var indicator = document.getElementById("status-indicator");
    var dot = document.getElementById("agent-status-dot");
    var text = document.getElementById("agent-status-text");

    if (indicator) {
      var cls = "status-indicator " + state.cls;
      // Replace rather than append, so a re-render cannot accumulate duplicate
      // state classes.
      if (indicator.className !== cls) {
        indicator.className = cls;
      }
    }
    if (dot && dot.className !== "status-dot") {
      dot.className = "status-dot";
    }
    if (text && text.innerHTML !== state.label) {
      text.innerHTML = state.label;
    }
  }

  // handleFrame is the /ws onmessage body, exposed as a seam so a test can drive
  // the REAL decode-to-badge path rather than calling renderAgentStatus
  // directly.
  //
  // The distinction matters: a badge that renders correctly but is never wired
  // to incoming frames looks identical to a working one when tested by calling
  // its renderer, and is exactly the defect this feature was raised about — the
  // old badge was decoration precisely because nothing called it.
  //
  // It returns whether the frame was usable. Throwing in an onmessage handler
  // would take the music down with it, so a corrupt frame is logged and dropped —
  // but a silent drop means a caller passing the wrong type fails quietly, which
  // is how a test can go green while asserting nothing. Returning the verdict
  // makes "handled" and "ignored" distinguishable.
  function handleFrame(raw, sandbox, live) {
    var frame;
    try {
      frame = JSON.parse(raw);
    } catch (err) {
      console.warn("[session] unparseable frame:", err);
      return false;
    }
    var snapshot = frame && frame.snapshot;
    renderAgentStatus(snapshot);
    applyFrame(frame, snapshot, sandbox, live);
    return true;
  }

  function updateSyncStatusUI() {
    var el = document.getElementById("sync-status");
    if (!el) {
      return;
    }

    var parts = [];

    // a. connection state
    parts.push(connectionState);

    var nowMs = Date.now();

    // b. current cycle and bar
    if (sync() && sync().usableAnchor(latestAnchor)) {
      var cyclePos = sync().cyclePosition(latestAnchor, nowMs);
      var bar = Math.floor(cyclePos);
      parts.push("cycle " + cyclePos.toFixed(1) + " (bar " + bar + ")");
    }

    // c. ms to the next commit WHILE one is pending
    if (pendingCommit && typeof pendingCommit.targetMs === "function" &&
        (!pendingCommit.isCancelled || !pendingCommit.isCancelled())) {
      var targetMs = pendingCommit.targetMs();
      var remainingMs = Math.max(0, Math.round(targetMs - nowMs));
      parts.push("next in " + remainingMs + "ms");
    }

    // d & e. last commit bar & observed drift
    var lastObs = sync() ? sync().lastObservation() : null;
    if (lastObs) {
      if (lastObs.unscheduled) {
        parts.push("last: unscheduled");
      } else {
        var lastBar = typeof lastObs.cycle === "number" ? Math.floor(lastObs.cycle) : "?";
        var driftVal = lastObs.driftMs;
        var driftText = typeof driftVal === "number"
          ? (driftVal >= 0 ? "+" : "") + Math.round(driftVal) + "ms"
          : String(driftVal);
        parts.push("last bar " + lastBar + " (<span class=\"sync-drift\">drift " + driftText + "</span>)");
      }
    }

    // The countdown makes this text change several times a second, and the
    // region is aria-live="polite" — so writing unconditionally would announce
    // every tick to a screen reader and turn a status line into a chatterbox.
    // aria-live announces on a MUTATION of the region, so an identical write is
    // what stays quiet. Comparing before writing is what buys that: the
    // countdown still updates, but only the values that actually moved speak.
    var next = parts.join(" | ");
    if (el.innerHTML === next) {
      return;
    }
    el.innerHTML = next;
  }

  function startStatusTimer() {
    if (statusTimer === null) {
      statusTimer = setInterval(updateSyncStatusUI, 250);
    }
  }

  function stopStatusTimer() {
    if (statusTimer !== null) {
      clearInterval(statusTimer);
      statusTimer = null;
    }
  }

  // The countdown only needs to tick while the page is on screen. pagehide
  // (not unload) is the right hook because it also fires when the page enters
  // the back/forward cache, where the interval would otherwise keep running
  // against a frozen clock. stopStatusTimer is idempotent, so a restored page
  // that also re-runs start is harmless.
  window.addEventListener("pagehide", stopStatusTimer);

  // window.strudelSync (sync.js, issue .3vo.8.2) owns the shared bar grid.
  // Resolved per call rather than captured at load: the module is loaded with
  // defer alongside this one, and a client served a cached session.js must still
  // commit immediately rather than throw if sync.js 404s.
  function sync() {
    return window.strudelSync || null;
  }

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

    // (2) Success: the commit is DEFERRED to the shared cycle boundary
    // (issue .3vo.8.2), not applied here. Committing the instant a frame lands
    // is what made two listeners on the same anchor diverge: each committed at
    // whatever moment its own frame arrived, so they landed on different bars.
    // strudelSync maps snapshot.anchor onto the shared bar grid and runs this at
    // the next boundary, so the commit lands on the SAME bar everywhere.
    //
    // Everything above stays immediate on purpose: validation and the
    // POST /api/eval-result report are the agent's feedback loop, and delaying
    // them behind a bar line would stall the loop for no coherence gain. Only
    // the audio commit waits for the bar.
    var stats = collectStats(pattern);
    var anchor = snapshot.anchor || null;
    if (anchor) {
      latestAnchor = anchor;
    }

    // A newer version supersedes a pending commit: cancel before scheduling, so
    // a superseded pattern can never land after the version that replaced it.
    if (pendingCommit) {
      pendingCommit.cancel();
      pendingCommit = null;
    }

    pendingCommit = scheduleCommit(function () {
      pendingCommit = null;
      // setPattern(pattern, false) hot-swaps audio without a gap; false means
      // "do not autostart", so a listener that has not clicked play yet is not
      // force-started.
      return live.setPattern(pattern, false);
    }, anchor, version, stats, pattern);

    // Refresh now that this caller owns the new handle, so the countdown
    // describes the commit that is actually pending rather than the previous one.
    updateSyncStatusUI();

    return postEvalResult(version, true, "", stats);
  }

  // Schedule one validated pattern for the next shared cycle boundary and
  // record the resulting drift for the sync status UI (.8.5).
  //
  // With no usable anchor there is no grid to align to, so the commit happens
  // immediately rather than at a NaN boundary: drifting is strictly better than
  // silence, and the observation is marked unscheduled so the UI can say so
  // honestly instead of reporting a fake aligned commit.
  function scheduleCommit(commit, anchor, version, stats, pattern) {
    if (anchor) {
      latestAnchor = anchor;
    }
    if (!sync() || !sync().usableAnchor(anchor)) {
      if (sync()) {
        sync().observe({ version: version, unscheduled: true });
      }
      updateSyncStatusUI();
      return commitNow(commit, version, stats, pattern);
    }
    var handle = sync().scheduleAtBoundary(function (at) {
      sync().observe({
        version: version,
        cycle: sync().cyclePosition(anchor, at.actualMs),
        targetMs: at.targetMs,
        actualMs: at.actualMs,
        driftMs: at.driftMs,
      });
      updateSyncStatusUI();
      return commitNow(commit, version, stats, pattern);
    }, anchor, Date.now(), sync().LEAD_MS);
    // pendingCommit is assigned by the CALLER, which already owns it and
    // cancels it before scheduling. Setting it here as well gave one variable
    // two owners, and the countdown below depends on which write landed.
    return handle;
  }

  // The commit itself, plus everything that describes it: the viz hook, the log
  // line, and the failure report. setPattern is the only call here that can
  // fail now that it is deferred, and a failure must still reach the agent as
  // an eval-result for THIS version rather than being swallowed at the boundary.
  async function commitNow(commit, version, stats, pattern) {
    try {
      await commit();
    } catch (err) {
      var commitMessage = String((err && err.message) || err);
      console.warn("[session] version " + version + " failed to commit:", commitMessage);
      showEvalError(commitMessage);
      return postEvalResult(version, false, commitMessage, { haps: 0 });
    }
    currentPattern = pattern;
    if (window.strudelViz && typeof window.strudelViz.setPattern === "function") { window.strudelViz.setPattern(pattern); }
    console.info("[session] version " + version + " live (" + stats.haps + " haps)");
    return undefined;
  }

  function isNewCodeVersion(snapshot) {
    return (
      snapshot &&
      typeof snapshot.version === "number" &&
      typeof snapshot.code === "string" &&
      snapshot.version !== lastVersion
    );
  }

  // applyFrame is everything a decoded frame does apart from parsing it and
  // painting the presence badge. It is split out of handleFrame so the decode
  // seam and the badge can be exercised together, and so ws.onmessage stays a
  // one-liner that routes through the same path the harness drives.
  function applyFrame(frame, snapshot, sandbox, live) {
    if (snapshot && snapshot.anchor) {
      latestAnchor = snapshot.anchor;
    }
    updateSyncStatusUI();
    if (!snapshot) {
      return;
    }
    // Transport / message / eval-result / listener-count frames carry the
    // same snapshot shape: keep panels fresh, but only new code versions
    // go through validate-then-commit. A failing eval-result verdict for
    // the version on screen also highlights the view, so a failure
    // reported by another listener is visible here too.
    showMessage(snapshot);
    if (window.strudelViz && typeof window.strudelViz.onSnapshot === "function") { window.strudelViz.onSnapshot(snapshot); }
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
  }

  function connect(sandbox, live, attempt) {
    var protocol = location.protocol === "https:" ? "wss:" : "ws:";
    var ws = new WebSocket(protocol + "//" + location.host + WS_PATH);

    ws.onopen = function () {
      attempt = 0;
      connectionState = "connected";
      updateSyncStatusUI();
      console.info("[session] subscribed to " + WS_PATH);
    };

    // Every frame carries the full snapshot, so the badge is refreshed from all
    // of them — including the "agent" frame that reports a lapsed lease, which
    // is the only way this client learns an agent has gone.
    ws.onmessage = function (event) {
      handleFrame(event.data, sandbox, live);
    };

    ws.onclose = function () {
      connectionState = "reconnecting";
      updateSyncStatusUI();
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
      startStatusTimer();
      connect(sandbox, live, 0);
    }).catch(function (err) {
      console.error("[session] live repl failed to start:", err);
    });
  }

  // Exposed for testing / debugging without a socket. applyVersion and
  // postEvalResult are exposed so the headless harness can drive the full
  // validate-then-commit path (success commits, failure leaves live alone)
  // with stub repls and assert on the POST /api/eval-result bodies.
  // scheduleCommit is exposed for the same reason (issue .3vo.8.2): bar
  // alignment is only observable from outside if the scheduling step is.
  window.strudelSession = {
    buildSandbox: buildSandbox,
    collectStats: collectStats,
    isNewCodeVersion: isNewCodeVersion,
    applyVersion: applyVersion,
    postEvalResult: postEvalResult,
    scheduleCommit: scheduleCommit,
    getLastVersion: function () { return lastVersion; },
    getCurrentPattern: function () { return currentPattern; },
    getPendingCommit: function () { return pendingCommit; },
    updateSyncStatusUI: updateSyncStatusUI,
    // The agent badge (strudel-agent-4jk). Exposed so the headless harness can
    // drive the real rendering path against real /api/state bytes, rather than
    // reimplementing the state mapping in Go and proving nothing about what a
    // browser would display.
    agentStateOf: agentStateOf,
    renderAgentStatus: renderAgentStatus,
    // handleFrame is the real /ws onmessage path, exposed so a test proves the
    // badge is WIRED to incoming frames rather than only that its renderer
    // works when called directly.
    handleFrame: handleFrame,
    getConnectionState: function () { return connectionState; },
    // Test/debug seam: startStatusTimer is normally called once from boot, but a
    // test needs to observe the interval actually ticking and actually stopping.
    startStatusTimer: startStatusTimer,
    stopStatusTimer: stopStatusTimer,
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
