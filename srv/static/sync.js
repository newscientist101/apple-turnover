// Strudel Agent cycle-aligned commit (issue strudel-agent-3vo.8.2).
//
// THE DEFECT THIS FIXES
// session.js used to commit to the live repl the instant a frame landed:
// live.setPattern(pattern, false) inside the websocket handler. Every client
// therefore committed mid-cycle, at whatever moment its OWN frame arrived, so
// two listeners on the same anchor landed on different bars and drifted apart
// from then on. Bar alignment is the whole goal here — strudel has no
// cross-machine clock to align to (NeoCyclist shares a clock only between
// instances in the SAME browser), so the app layer owns coherence.
//
// THE MECHANISM
// The server publishes one anchor (epochMs + cps, srv/conductor.go) on every
// snapshot. That pair is a shared bar grid: cycle i begins at
// epochMs + i * 1000/cps wall-clock ms. A client that commits exactly ON a
// boundary commits on the same bar as every other client, whatever order the
// frames arrived in.
//
// THE LEAD IS LOAD-BEARING
// nextCycleBoundary returns the next boundary PLUS a lead, not the raw
// boundary. Without it, a client whose frame arrives 20ms late computes
// floor(cycle)+1, lands on the boundary that is 20ms in the past, rounds it
// down to the CURRENT bar, and commits a bar behind everyone else — which is
// precisely the drift being fixed. The lead buys the late client the same
// answer the early one got.
//
// This module is deliberately dependency-free and exposes its clock and timers
// through optional arguments. That is what lets the Go harness (goja,
// srv/coherence_test.go) execute the REAL served bytes against a fake clock and
// assert that two clients handed the same anchor commit on the same bar,
// instead of grepping the source for a marker and hoping.
(function () {
  'use strict';

  // LEAD_MS is how far ahead of a cycle boundary a commit is scheduled. It has
  // to cover the worst frame-arrival jitter between clients sharing an anchor;
  // it is named and exported so it is one assertable thing rather than a literal
  // buried in an expression.
  var LEAD_MS = 25;

  // Fractional cycles elapsed since the anchor's epoch. Negative before the
  // epoch (a re-anchor into the future), which callers must tolerate rather
  // than treat as an error.
  function cyclePosition(anchor, nowMs) {
    return ((nowMs - anchor.epochMs) / 1000) * anchor.cps;
  }

  // An anchor a client can actually schedule against: a positive rate and a
  // finite epoch. A malformed anchor is not worth throwing over mid-performance
  // — the caller's fallback (commit immediately) beats silence.
  function usableAnchor(anchor) {
    return !!anchor &&
      typeof anchor.epochMs === 'number' && isFinite(anchor.epochMs) &&
      typeof anchor.cps === 'number' && isFinite(anchor.cps) && anchor.cps > 0;
  }

  // The first cycle boundary at least leadMs in the future.
  //
  // Mathematically: the next boundary strictly after now, plus the lead. The
  // index comes from Math.floor on elapsed/cycleMs, so a client anywhere inside
  // cycle k targets the start of cycle k+1 — never the bar it is currently
  // playing, which would swap the pattern under the listener mid-bar.
  function nextCycleBoundary(anchor, nowMs, leadMs) {
    if (!usableAnchor(anchor)) {
      return nowMs; // unschedulable: caller should commit now, not at NaN
    }
    var lead = typeof leadMs === 'number' && isFinite(leadMs) ? leadMs : LEAD_MS;
    var cycleMs = 1000 / anchor.cps;
    var elapsed = nowMs - anchor.epochMs;
    var nextIndex = Math.floor(elapsed / cycleMs) + 1;
    // THE LEAD, added to the boundary. Removing it is mutation row 88.
    return anchor.epochMs + nextIndex * cycleMs + lead;
  }


  // Run fn at the next cycle boundary. deps: {setTimeout, clearTimeout, now},
  // defaulting to the host globals.
  //
  // The timer is SELF-CORRECTING: setTimeout may fire EARLY (a busy event loop
  // can land it fractionally early), and a timer that fires early must not
  // commit early — that would be the same off-by-one-bar bug in a new place. So
  // on every tick the target is recomputed against the clock, and a tick that
  // arrives before the target re-arms instead of firing.
  //
  // The returned handle is what lets a NEWER version supersede a PENDING
  // commit: cancelling is idempotent, so a handle cancelled twice (a late frame
  // after an unsubscribe, say) is harmless.
  function scheduleAtBoundary(fn, anchor, nowMs, leadMs, deps) {
    var d = deps || {};
    var setT = d.setTimeout || setTimeout;
    var clearT = d.clearTimeout || clearTimeout;
    var clock = d.now || function () { return Date.now(); };

    var target = nextCycleBoundary(anchor, nowMs, leadMs);
    var cancelled = false;
    var handle = null;

    if (!usableAnchor(anchor)) {
      // No usable grid: defer nothing. fn still runs, just not on a boundary.
      var t0 = clock();
      fn({ targetMs: t0, actualMs: t0, driftMs: 0, unscheduled: true });
      return {
        cancel: function () { cancelled = true; },
        isCancelled: function () { return cancelled; },
        targetMs: function () { return t0; },
      };
    }

    function arm() {
      handle = setT(function onTick() {
        if (cancelled) {
          return;
        }
        var remaining = target - clock();
        if (remaining > 0) {
          arm(); // fired early: wait out the rest rather than commit off-grid
          return;
        }
        var actual = clock();
        fn({ targetMs: target, actualMs: actual, driftMs: actual - target });
      }, Math.max(0, target - clock()));
    }

    arm();

    return {
      cancel: function () {
        cancelled = true;
        if (handle !== null) {
          clearT(handle);
          handle = null;
        }
      },
      isCancelled: function () { return cancelled; },
      // Exposed so the status UI (.8.5) can count down to a pending commit
      // rather than guessing at it.
      targetMs: function () { return target; },
    };
  }

  // Drift observations. Bar alignment REDUCES drift, it cannot eliminate it:
  // different machines, different audio clocks, no sample-accurate sync. So the
  // drift is recorded and exposed rather than assumed away — the status UI in
  // .8.5 renders this, and nothing here renders anything.
  var MAX_OBSERVATIONS = 32;
  var observations = [];

  function observe(record) {
    observations.push(record);
    while (observations.length > MAX_OBSERVATIONS) {
      observations.shift();
    }
    return record;
  }

  function lastObservation() {
    return observations.length ? observations[observations.length - 1] : null;
  }

  window.strudelSync = {
    LEAD_MS: LEAD_MS,
    cyclePosition: cyclePosition,
    usableAnchor: usableAnchor,
    nextCycleBoundary: nextCycleBoundary,
    scheduleAtBoundary: scheduleAtBoundary,
    observe: observe,
    observations: function () { return observations.slice(); },
    lastObservation: lastObservation,
    // Test/debug seam, same spirit as window.strudelSession in session.js.
    _reset: function () { observations = []; },
  };
})();
