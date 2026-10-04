(function () {
  'use strict';

  var currentPattern = null;
  var latestSnapshot = null;
  var fallbackEpochMs = null;

  function setPattern(pattern) {
    currentPattern = pattern;
  }

  function onSnapshot(snapshot) {
    latestSnapshot = snapshot;
  }

  function getHapTime(hap, key) {
    if (!hap) return 0;
    var val = null;
    if (hap.whole && hap.whole[key] !== undefined) {
      val = hap.whole[key];
    } else if (hap.part && hap.part[key] !== undefined) {
      val = hap.part[key];
    } else if (hap[key] !== undefined) {
      val = hap[key];
    }
    if (val !== null && typeof val === 'object' && typeof val.valueOf === 'function') {
      return val.valueOf();
    }
    var num = Number(val);
    return isNaN(num) ? 0 : num;
  }

  // Resolve a hap's value into the lane it belongs to.
  //
  // Precedence is EXPLICIT here rather than first-match, because first-match
  // ordering silently decides this: whichever recognised field is tested first
  // wins for every hap carrying it, so a later field can never take precedence
  // and the semantics are accidental.
  //
  // The order is: a pitched hap is a lane per PITCH; an unpitched one is a lane
  // per SOUND. Pitch is resolved first (on `note`, then on `n` — the pinned
  // @strudel/web@1.3.0 bundle registers them as two names for the same control,
  // `{note:Fo}=w(["note","n"])`, and reading only one of them loses the pitch
  // lane for every pattern written with the other). Crucially `s` is only
  // consulted when there is NO pitch: a hap like `note("c4").sound("piano")` or
  // `n("0 2 4").s("saw")` carries both, and keying it on `s` collapsed every
  // pitch in the pattern onto one lane named after the shared instrument.
  //
  // The same resolver supplies the colour, so lane identity and colour cannot
  // disagree — deciding them separately is what let a {note, s} hap be filed
  // under its sound while painted as a note.
  function laneFor(value) {
    if (!value || typeof value !== 'object') {
      return { key: 'other', kind: 'other' };
    }
    var pitch = value.note !== undefined && value.note !== null && value.note !== '' ? value.note : value.n;
    if (pitch !== undefined && pitch !== null && pitch !== '') {
      return { key: String(pitch), kind: 'note' };
    }
    if (typeof value.s === 'string' && value.s.length > 0) {
      return { key: value.s, kind: 'sample' };
    }
    return { key: 'other', kind: 'other' };
  }

  function colourFor(kind) {
    if (kind === 'other') return '#8888aa';
    if (kind === 'note') return '#4f8cff';
    return '#00e5a3';
  }

  function draw() {
    var canvas = document.getElementById('canvas');
    if (!canvas) {
      return;
    }
    var ctx = canvas.getContext('2d');
    if (!ctx) {
      return;
    }

    var rect = canvas.getBoundingClientRect();
    var dpr = window.devicePixelRatio || 1;
    var w = rect.width;
    var h = rect.height;

    if (w <= 0 || h <= 0) {
      return;
    }

    var pixelW = Math.floor(w * dpr);
    var pixelH = Math.floor(h * dpr);
    if (canvas.width !== pixelW || canvas.height !== pixelH) {
      canvas.width = pixelW;
      canvas.height = pixelH;
    }

    ctx.save();
    ctx.scale(dpr, dpr);

    // Clear background
    ctx.fillStyle = '#121216';
    ctx.fillRect(0, 0, w, h);

    // Determine shared timeline anchor
    var snapshot = latestSnapshot;
    var anchor = (snapshot && snapshot.anchor) ? snapshot.anchor : null;
    if (!anchor) {
      if (!fallbackEpochMs) {
        fallbackEpochMs = Date.now();
      }
      anchor = { epochMs: fallbackEpochMs, cps: 0.5 };
    }

    var currentCycle = ((Date.now() - anchor.epochMs) / 1000) * anchor.cps;

    // The renderer is driven entirely by the pattern (queryArc) and the shared
    // anchor, so it needs no audio context. A waveform branch used to live here,
    // guarded by a window.getAudioContext / repl.audioContext lookup that the
    // pinned @strudel/web@1.3.0 bundle never exposes — so it could never run. It
    // was removed rather than wired to a context that would draw a flat,
    // silent line: getAnalyserById only creates an analyser, it never connects
    // one to the output graph. See srv/viz_browser_test.go.

    var playheadX = 40;
    var playhead = playheadX;
    var timelineWidth = Math.max(10, w - playheadX - 10);
    var windowCycles = 2;

    // Query upcoming haps for timeline window
    var haps = [];
    if (currentPattern && typeof currentPattern.queryArc === 'function') {
      try {
        var begin = currentCycle;
        var end = currentCycle + windowCycles;
        haps = currentPattern.queryArc(begin, end) || [];
      } catch (e) {
        haps = [];
      }
    }

    // Group haps into lanes
    var laneMap = {};
    var laneKeys = [];
    var laneKinds = {};

    for (var i = 0; i < haps.length; i++) {
      var hap = haps[i];
      if (!hap) continue;
      var lane = laneFor(hap.value);
      var key = lane.key;
      if (!laneMap[key]) {
        laneMap[key] = [];
        laneKeys.push(key);
      }
      // A lane keeps the kind of the first hap filed under it. Every hap with
      // the same key resolves to the same kind (the key IS derived from the
      // kind), so this cannot drift.
      laneKinds[key] = lane.kind;
      laneMap[key].push(hap);
    }

    if (laneKeys.length === 0) {
      laneKeys = ['other'];
      laneMap['other'] = [];
      laneKinds['other'] = 'other';
    }

    laneKeys.sort();

    var numLanes = laneKeys.length;
    var laneHeight = h / numLanes;

    for (var l = 0; l < numLanes; l++) {
      var lKey = laneKeys[l];
      var laneTop = l * laneHeight;

      // Lane line
      ctx.strokeStyle = '#23232e';
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(0, laneTop);
      ctx.lineTo(w, laneTop);
      ctx.stroke();

      // Lane label
      ctx.fillStyle = '#6e6e80';
      ctx.font = '11px sans-serif';
      ctx.textBaseline = 'middle';
      ctx.fillText(lKey, 6, laneTop + laneHeight / 2);

      // Draw haps in lane
      var laneHaps = laneMap[lKey] || [];
      for (var j = 0; j < laneHaps.length; j++) {
        var hItem = laneHaps[j];
        var b = getHapTime(hItem, 'begin');
        var e = getHapTime(hItem, 'end');
        if (e <= b) {
          e = b + 0.1;
        }

        var x1 = playheadX + ((b - currentCycle) / windowCycles) * timelineWidth;
        var x2 = playheadX + ((e - currentCycle) / windowCycles) * timelineWidth;
        var blockW = Math.max(3, x2 - x1);
        var blockY = laneTop + 4;
        var blockH = Math.max(4, laneHeight - 8);

        // The colour comes from the lane's resolved kind, not from re-inspecting the
        // hap: deriving it separately is what let a {note, s} hap be filed under
        // its sound while painted as a note (issue strudel-agent-uvj.10).
        ctx.fillStyle = colourFor(laneKinds[lKey]);
        ctx.fillRect(x1, blockY, blockW, blockH);
      }
    }

    // Playhead line
    ctx.strokeStyle = '#ff3366';
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.moveTo(playhead, 0);
    ctx.lineTo(playhead, h);
    ctx.stroke();

    ctx.restore();
  }

  function loop() {
    try {
      draw();
    } catch (e) {
      // Must not throw when rendering fails
    }
    requestAnimationFrame(loop);
  }

  requestAnimationFrame(loop);

  window.strudelViz = {
    setPattern: setPattern,
    onSnapshot: onSnapshot,
  };
})();
