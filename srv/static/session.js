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
  var DRY_RUN_RESULT_PATH = "/api/dry-run-result";
  // How a listener tells the server where its commit landed (strudel-agent-uvj.15).
  // It is NOT part of the eval-result POST: that one is sent before the boundary
  // timer fires, so the drift does not exist yet at the time it goes out. See
  // recordObservation.
  var SYNC_RESULT_PATH = "/api/sync-result";
  // How one listener reports what its OWN sample registry holds
  // (strudel-agent-f79). It is keyed by the connection id the server assigned
  // us, which is what makes the answer PER-LISTENER: lastEvalResult records
  // whichever listener reported last, so an agent reading it cannot tell whether
  // the other tabs can hear the code.
  var SAMPLES_PATH = "/api/samples";
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

  // The id the server assigned THIS connection, or null before it has told us.
  // It arrives on the `listener` frame, which the server sends before the
  // catch-up snapshot precisely so this is set before we can report anything: a
  // report keyed to an identity we do not have yet would be refused.
  var listenerId = null;

  // The signature of the sample state we last reported, so a poll that finds
  // nothing new stays silent. The server ignores an unchanged report anyway, but
  // not making the request at all is what keeps a periodic check from being a
  // periodic POST — and a request that is never made cannot fail.
  var lastSampleSignature = null;

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
      statusTimer = setInterval(function onTick() {
        updateSyncStatusUI();
        // Re-check the sample registry on the same tick that refreshes the status
        // line. A pack is loaded by a HUMAN pressing a button in welcome.html,
        // asynchronously and with no callback into this module — so nothing here
        // would otherwise notice that the audience just became able to hear the
        // code, and the agent would keep working from a stale answer.
        //
        // reportAudienceSamples is gated on the state having actually changed, so this
        // costs one registry read per tick and issues no request unless something
        // moved. That ordering matters: the alternative — hooking the button — is
        // what the uvj.18 work deliberately avoided, because that button's "off"
        // branch flips a label and unregisters nothing, so a hook would report a
        // state the registry does not agree with.
        reportAudienceSamples();
      }, 250);
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
  //
  // The haps are handed back through `out` rather than queried a second time by
  // the sample resolver. queryArc is a pure query, but running it twice to answer
  // two questions about the same pattern would make the hap count and the sample
  // finding describe two different evaluations if the pattern were anything
  // non-deterministic — two stats that disagree about one pattern, which is
  // exactly the kind of self-contradictory report an agent cannot act on.
  function collectStats(pattern, out) {
    var stats = { haps: 0 };
    try {
      var haps = pattern.queryArc(0, 1);
      if (out) {
        out.haps = haps;
      }
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

  // Sound names that are NOT sample lookups, so the resolver must not ask the
  // registry about them (strudel-agent-uvj.18).
  //
  // The first three are what Strudel itself checks for before resolving a name —
  // its own trigger path returns early on exactly this set — and they are common
  // in real patterns: `s("bd ~ cp")` is an ordinary bar of drums, and reporting a
  // rest as an unresolved sample would cry wolf on a melody anyone would write.
  //
  // wt_ names are registered through a different door (registerWaveTable) and a
  // waveform table is not a sample, so asking about it in the sample registry
  // would be asking the wrong map.
  var NON_SAMPLE_SOUNDS = ["-", "~", "_"];

  // maxReportedMissing bounds how many unresolved names a report names. An agent
  // needs the first of them to act on; it does not need all ten thousand.
  var maxReportedMissing = 20;

  // normaliseSoundName applies the ONE rule the pinned @strudel/web@1.3.0 bundle
  // applies before it stores or looks up a sound name:
  //
  //   registerSound (jt): name.toLowerCase().replace(/\s+/g, "_")
  //   playback     (Un): soundMap.get()[name.toLowerCase()]
  //
  // Matching the bundle exactly is the entire correctness property of this
  // resolver: a resolver that normalises differently would report a sample the
  // user loaded as missing, or — far worse — a sample they never loaded as
  // present. It would then disagree with the audio the user hears, which is the
  // precise failure this bead exists to stop. So the rule is copied from the
  // bundle, not reimplemented by intuition.
  function normaliseSoundName(name) {
    return String(name).toLowerCase().replace(/\s+/g, "_");
  }

  // soundRegistry returns the strudel sound map, or null when there is nothing to
  // ask.
  //
  // It reads window.strudel on every call rather than capturing it at load: the
  // bundle is a CDN script and may not have executed yet, and a page whose bundle
  // never arrives must degrade to UNKNOWN, not to a confident answer.
  function soundRegistry() {
    var scope = (typeof window !== "undefined" && window.strudel) || null;
    if (!scope || !scope.soundMap || typeof scope.soundMap.get !== "function") {
      return null;
    }
    try {
      return scope.soundMap.get() || null;
    } catch (err) {
      // A registry that throws is not a registry that says "nothing is loaded".
      return null;
    }
  }
    // collectSampleReport reports whether every sound the validated pattern NAMES
  // is present in the browser's sample registry (strudel-agent-uvj.18).
  //
  // Why this is a registry lookup and not playback: the validating sandbox
  // discards its output by design, and sample resolution in Strudel happens on
  // the output path. That stub is what makes validation free of audio, of the
  // scheduler and of the network, and it must stay. The registry is the very map
  // playback consults (`soundMap.get()[name.toLowerCase()]`, yielding nothing for
  // a miss), so reading it answers "would this name sound?" without playing
  // anything.
  //
  // It returns null when the answer is UNKNOWN — no registry, so nothing was
  // checked — and that null is what the caller turns into an ABSENT field rather
  // than a false. A resolver that could not look must say so: reporting true there
  // would be the original defect wearing a fix's clothes, a green light over a
  // check that never ran.
  function collectSampleReport(haps) {
    var registry = soundRegistry();
    if (!registry) {
      return null;
    }
    var checked = 0;
    var missing = {};
    for (var i = 0; i < haps.length; i++) {
      var value = haps[i] && haps[i].value;
      if (!value) continue;
      var name = value.s;
      // Only a plain string is a sound name. Numbers and undefined are not
      // lookups, and asking about them would invent a missing sample out of a hap
      // that never named one.
      if (typeof name !== "string" || name.length === 0) continue;
      if (NON_SAMPLE_SOUNDS.indexOf(name) !== -1) continue;
      if (name.indexOf("wt_") === 0) continue;
      checked++;
      if (!Object.prototype.hasOwnProperty.call(registry, normaliseSoundName(name))) {
        missing[name] = true;
      }
    }
    var names = Object.keys(missing).sort();
    return {
      checked: checked,
      resolved: names.length === 0,
      // Sorted so two browsers resolving the same pattern report the same list,
      // and capped so a pathological pattern cannot turn a verdict into an
      // unbounded payload. totalMissing keeps the true count visible after the cap.
      missing: names.length > maxReportedMissing ? names.slice(0, maxReportedMissing) : names,
      totalMissing: names.length,
    };
  }

  // reportSamples attaches a sample report to a verdict body.
  //
  // The tri-state is load-bearing and is why the field is set only when a report
  // exists: `samplesResolved` absent means UNKNOWN (no registry to ask), false
  // means at least one name did not resolve, and true means every name did. A
  // plain boolean could not tell "healthy" from "never looked", and an agent
  // optimising against a signal that cannot express its own blindness is
  // optimising against noise.
  //
  // Note what this does NOT do. It never rewrites `ok`, and the failure paths
  // never call it at all: a parse error proves nothing about samples, because the
  // pattern never got far enough to name one. Reporting an unresolved sound there
  // would invent a defect the agent did not commit, on top of an error that
  // already says what is wrong.
  function reportSamples(body, haps) {
    var report = collectSampleReport(haps || []);
    if (!report) {
      return body;
    }
    body.samplesResolved = report.resolved;
    body.stats = body.stats || {};
    body.stats.samples = report;
    if (!report.resolved) {
      console.warn("[session] " + report.totalMissing + " sound(s) did not resolve in this browser: " +
        report.missing.join(", ") + ". The pattern is valid and was committed, but it will be silent here.");
    }
    return body;
  }

  // postEvalResult sends the verdict to the server.
  //
  // haps is the hap list collectStats already queried. It is a parameter rather
  // than a re-query so that the hap count and the sample finding describe ONE
  // evaluation (see collectStats). It is omitted on the failure paths, which is
  // what keeps a syntax error from carrying a sample verdict it did not earn.
  function postEvalResult(version, ok, error, stats, haps) {
    var body = { version: version, ok: ok, stats: stats || { haps: 0 } };
    if (!ok && error) {
      body.error = String(error).slice(0, 500);
    }
    if (ok) {
      reportSamples(body, haps);
    }
    return fetch(EVAL_RESULT_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).catch(function (err) {
      console.warn("[session] failed to report eval-result:", err);
    });
  }

  // dryRunCandidate evaluates a candidate that was NEVER published and reports the
  // verdict to POST /api/dry-run-result (strudel-agent-uvj.16).
  //
  // The single most important property here is what it does NOT do: it never
  // calls live.setPattern, never touches showCode, and never advances
  // lastVersion. A dry-run exists so an unproven candidate cannot become the
  // playing document, and a handler that committed it would defeat the entire
  // feature while still reporting success. Only the SANDBOX sees this code.
  //
  // It evaluates in the sandbox for the same reason applyVersion does: the live
  // repl's own evaluate hushes before parsing, so bad code would silence every
  // listener even though it never became the new pattern.
  //
  // Every listener evaluates and reports, and the server keeps the FIRST answer,
  // so a report that fails here has usually lost a race rather than misfiring.
  async function dryRunCandidate(request, sandbox) {
    var body = { dryRunId: request.id, ok: false, stats: { haps: 0 } };
    try {
      var pattern = await sandbox.evaluate(request.code, false, false);
      var evalError = sandbox.state && sandbox.state.evalError;
      if (!pattern || evalError) {
        body.error = evalError
          ? String((evalError && evalError.message) || evalError)
          : "evaluation produced no pattern";
      } else {
        body.ok = true;
        // The same resolver, on the same evaluation, as the real verdict: an
        // agent that dry-runs a candidate and is told nothing, then pushes it and
        // is told "unresolved", has been given two answers to one question.
        var probed = {};
        body.stats = collectStats(pattern, probed);
        reportSamples(body, probed.haps);
      }
    } catch (err) {
      body.ok = false;
      body.error = String((err && err.message) || err);
    }
    return fetch(DRY_RUN_RESULT_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).catch(function (err) {
      // Losing the race, or the agent giving up, are ordinary outcomes when
      // every listener answers the same request — so this is informational.
      console.info("[session] dry-run " + request.id + " report: " + err);
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
    // The haps are kept so the sample resolver reports on the SAME evaluation
    // whose hap count this is; see collectStats.
    var probed = {};
    var stats = collectStats(pattern, probed);
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

    return postEvalResult(version, true, "", stats, probed.haps);
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
      recordObservation({ version: version, unscheduled: true });
      return commitNow(commit, version, stats, pattern);
    }
    var handle = sync().scheduleAtBoundary(function (at) {
      recordObservation({
        version: version,
        cycle: sync().cyclePosition(anchor, at.actualMs),
        targetMs: at.targetMs,
        actualMs: at.actualMs,
        driftMs: at.driftMs,
      });
      return commitNow(commit, version, stats, pattern);
    }, anchor, Date.now(), sync().LEAD_MS);
    // pendingCommit is assigned by the CALLER, which already owns it and
    // cancels it before scheduling. Setting it here as well gave one variable
    // two owners, and the countdown below depends on which write landed.
    return handle;
  }

  // recordObservation is the ONE place a drift observation is born
  // (strudel-agent-uvj.15).
  //
  // It exists so the three consumers of an observation cannot disagree. The
  // status line, sync.js's own record and the report to the server all read the
  // same object, which is the same discipline laneFor/colourFor follows for lane
  // identity: derive it once, feed everybody from that.
  //
  // It is also why the report lives on its own endpoint rather than inside the
  // eval-result body. This function runs when the boundary timer FIRES, which is
  // strictly after applyVersion has already POSTed the eval verdict -- at that
  // earlier instant the drift did not exist yet, so folding it into the verdict
  // would mean posting one version twice and letting the second post overwrite
  // the agent's verdict, or stalling that verdict behind a bar line. Drift is a
  // property of a commit at a bar; an evaluation is not.
  //
  // The unscheduled case is reported as unscheduled rather than as driftMs:0.
  // Those are different claims: one says the commit landed on the bar line it
  // targeted, the other says there was no bar line at all. Collapsing them would
  // make a listener that never aligned look perfectly aligned, and would stop an
  // agent re-anchoring a system that needs it.
  function recordObservation(observation) {
    if (sync()) {
      sync().observe(observation);
    }
    updateSyncStatusUI();
    postSyncResult(observation);
  }

  // postSyncResult sends one observation to POST /api/sync-result so an agent can
  // read it back from GET /api/state.
  //
  // Without it the drift was measured, shown to whoever was looking at the tab,
  // and discarded -- so the drift visibility the docs promised was reachable only
  // by a human, and an agent could not know when re-anchoring was needed.
  //
  // The body is built from the observation object itself rather than from
  // variables reconstructed here, so the number reported to the server is
  // provably the number the client measured and painted.
  //
  // A failure is warned about and swallowed for the same reason postEvalResult's
  // is: the report is feedback, not part of the audio path, and a server that is
  // briefly unreachable must not take the music down with it.
  function postSyncResult(observation) {
    if (!observation) {
      return undefined;
    }
    var body = { version: observation.version };
    if (observation.unscheduled) {
      body.unscheduled = true;
    } else {
      // Only a scheduled observation has a bar line to be late against. An
      // absent driftMs would be refused by the server as claiming nothing at
      // all, which is the correct answer to a measurement that did not happen.
      if (typeof observation.driftMs !== "number") {
        console.warn("[session] scheduled commit produced no drift measurement; not reporting it");
        return undefined;
      }
      body.driftMs = Math.round(observation.driftMs);
      if (typeof observation.targetMs === "number") {
        body.targetMs = Math.round(observation.targetMs);
        body.actualMs = Math.round(observation.actualMs);
      }
    }
    return fetch(SYNC_RESULT_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).catch(function (err) {
      console.warn("[session] failed to report sync result:", err);
    });
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

  // describeSamples reads the CURRENT sample state of this browser, for the
// per-listener audience report (strudel-agent-f79).
//
// It is the same registry the verdict-scoped resolver reads — soundRegistry() — so
// the two paths cannot disagree about what this browser can hear. That matters
// more than it looks: an agent comparing `lastEvalResult.samplesResolved` against
// `snapshot.samples` would be comparing two answers to one question, and a client
// that answered on one path and stayed silent on the other would give it two.
//
// The tri-state is load-bearing. A browser with no registry returns loaded=null
// (UNKNOWN) rather than false, because it learned nothing — and reporting false
// would tell an agent its samples are missing on the strength of a check that
// never ran, which is the defect strudel-agent-uvj.18 was filed for.
//
// `count` is how many sounds the registry holds. It distinguishes "no samples at
// all" from "some samples, but not the one this code names", which a boolean
// cannot.
function describeSamples() {
  var registry = soundRegistry();
  if (!registry) {
    return { loaded: null, count: 0 };
  }
  var names = [];
  for (var name in registry) {
    if (Object.prototype.hasOwnProperty.call(registry, name)) {
      names.push(name);
    }
  }
  // A registry that exists but is empty is genuinely "no samples loaded", which
  // is a finding and not the same as having no registry to ask.
  return { loaded: names.length > 0, count: names.length };
}

// reportAudienceSamples sends this listener's sample state to POST /api/samples, keyed by
// the connection id the server assigned us.
//
// It is a REPORT, like the eval verdict and the drift observation: the server
// never evaluates JavaScript and cannot look at a registry itself, so the browser
// is the only thing that can answer. Without this the audience question was
// unanswerable — and the failure mode this bead exists to prevent is precisely
// that: an agent pushing sample-dependent code to an audience that cannot hear it,
// with no signal that says so.
//
// A report is made only when the state actually CHANGED. The server ignores an
// unchanged one, but not issuing the request at all is what keeps a periodic check
// from being a periodic POST, and a request never made cannot fail.
//
// Before the server has told us our id there is nothing to report against, so this
// is a no-op rather than a POST with an empty key: the server refuses a report
// naming no listener, and firing one on every frame until the `listener` frame
// arrived would fill its error path with our own connect handshake.
//
// A failure is warned about and swallowed, for the reason postEvalResult's is: the
// report is feedback, not part of the audio path, and a server that is briefly
// unreachable must not take the music down with it.
function reportAudienceSamples(force) {
  if (!listenerId) {
    return undefined;
  }
  var state = describeSamples();
  var signature = state.loaded + ":" + state.count;
  if (!force && signature === lastSampleSignature) {
    return undefined;
  }
  lastSampleSignature = signature;

  var body = { listenerId: listenerId, count: state.count };
  // Omitted, not null, when unknown: the server's tri-state is a *bool, and a
  // JSON null there is not the same as an absent key.
  if (state.loaded !== null) {
    body.loaded = state.loaded;
  }
  return fetch(SAMPLES_PATH, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }).catch(function (err) {
    console.info("[session] samples report: " + err);
  });
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
    // The `listener` frame is this connection's identity, and it arrives BEFORE
    // the catch-up snapshot precisely so it is known before anything else can be
    // reported. Capturing it here — rather than in ws.onmessage — keeps the decode
    // seam the single place a frame is interpreted, which is what lets the harness
    // drive the real path instead of a reimplementation of it.
    //
    // The report is made HERE, immediately, rather than after the snapshot is
    // processed: a listener's sample state does not depend on the code it is
    // playing, and waiting would mean an audience answer missing for a frame.
    if (frame.kind === "listener" && frame.listener && typeof frame.listener.id === "string" && frame.listener.id) {
      // A RECONNECT gets a different id, because ids are never reused. The
      // signature gate is cleared here for that reason: the previous connection's
      // id is gone, so an unchanged registry is still a report the new connection
      // has never made, and without this a reconnected tab would be absent from
      // the audience summary until its registry happened to change.
      //
      // The force flag carries the same weight for the first report: it must go
      // out even if nothing about the registry has moved.
      if (listenerId !== frame.listener.id) {
        lastSampleSignature = null;
      }
      listenerId = frame.listener.id;
      reportAudienceSamples(true);
      return;
    }
    // A `samples` frame describes what the AUDIENCE can hear. It is not rendered
    // here and deliberately does not become a listener-count-like badge: it is
    // evidence for the agent, which reads it from the snapshot, and painting it
    // would be the same mistake as the drift indicator — measured, stored, and
    // displayed to a human who cannot act on it while the agent never sees it.
    if (frame.kind === "samples") {
      return;
    }
    // Transport / message / eval-result / listener-count frames carry the
    // same snapshot shape: keep panels fresh, but only new code versions
    // go through validate-then-commit. A failing eval-result verdict for
    // the version on screen also highlights the view, so a failure
    // reported by another listener is visible here too.
    showMessage(snapshot);
    if (window.strudelViz && typeof window.strudelViz.onSnapshot === "function") { window.strudelViz.onSnapshot(snapshot); }
    // A dry-run is a REQUEST to evaluate something that is not the document, so
    // it returns before any of the version handling below. Two things matter
    // about the ordering: lastVersion must NOT be advanced (the frame is not a
    // new document, and advancing it would make the next real code frame look
    // stale and be skipped), and the candidate must not reach applyVersion.
    if (frame.kind === "dry-run") {
      if (frame.dryRun && typeof frame.dryRun.id === "number" && typeof frame.dryRun.code === "string") {
        dryRunCandidate(frame.dryRun, sandbox);
      } else {
        console.warn("[session] dry-run frame carried no usable candidate", frame);
      }
      return;
    }
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
    // collectSampleReport is exposed alongside collectStats so the headless
    // harness can drive the resolver directly and assert on its tri-state —
    // including the UNKNOWN (absent registry) case, which is unreachable through
    // the report path once a registry exists.
    collectSampleReport: collectSampleReport,
    isNewCodeVersion: isNewCodeVersion,
    applyVersion: applyVersion,
    postEvalResult: postEvalResult,
    // dryRunCandidate is exposed so the headless harness can prove a dry-run is
    // evaluated in the SANDBOX and reported to /api/dry-run-result, and — the
    // property that matters most — that it never commits to the live repl.
    dryRunCandidate: dryRunCandidate,
    scheduleCommit: scheduleCommit,
    // recordObservation and postSyncResult are exposed so the headless harness can
    // prove an observation actually REACHES the server. Calling the renderer and
    // asserting it looked right is the mistake this bead exists to correct: drift
    // was painted into #sync-status for months and no test noticed it never left
    // the browser, because every test of it stopped at the display.
    recordObservation: recordObservation,
    postSyncResult: postSyncResult,
    // The audience report (strudel-agent-f79) is exposed for the same reason, and
    // the same discipline: the assertion must be on the body that would have been
    // POSTed, not on what this module computed. A browser that resolves samples
    // correctly and never tells the server would pass every test that stopped at
    // describeSamples.
    describeSamples: describeSamples,
    reportAudienceSamples: reportAudienceSamples,
    getListenerId: function () { return listenerId; },
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
