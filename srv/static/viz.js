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

    // Feature-detect drawTimeScope and draw waveform if live audio context is running
    var drawTimeScopeFn = (typeof drawTimeScope === "function" ? drawTimeScope : ((window.strudel && window.strudel.drawTimeScope) || window.drawTimeScope));
    var repl = window.repl;
    var audioCtx = repl && (repl.audioContext || repl.ctx || (repl.webaudio && repl.webaudio.ctx));
    if (!audioCtx && typeof window.getAudioContext === 'function') {
      try { audioCtx = window.getAudioContext(); } catch (e) {}
    }
    if (drawTimeScopeFn && typeof drawTimeScopeFn === 'function' && audioCtx && audioCtx.state === 'running') {
      try {
        drawTimeScopeFn(ctx, w, h);
      } catch (e) {
        // Graceful degradation when audio is suspended or waveform fails
      }
    }

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

    for (var i = 0; i < haps.length; i++) {
      var hap = haps[i];
      if (!hap) continue;
      var value = hap.value;
      var key = 'other';
      if (value && typeof value === 'object') {
        if (typeof value.s === 'string' && value.s.length > 0) {
          key = value.s;
        } else if (value.note !== undefined && value.note !== null && value.note !== '') {
          key = String(value.note);
        }
      }
      if (!laneMap[key]) {
        laneMap[key] = [];
        laneKeys.push(key);
      }
      laneMap[key].push(hap);
    }

    if (laneKeys.length === 0) {
      laneKeys = ['other'];
      laneMap['other'] = [];
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

        var valItem = hItem.value;
        var isNote = valItem && typeof valItem === 'object' && valItem.note !== undefined;
        ctx.fillStyle = (lKey === 'other' ? '#8888aa' : (isNote ? '#4f8cff' : '#00e5a3'));
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
