package srv

// Autoplay transport with local mute (strudel-agent-3q0.4), executed as
// served.
//
// The page boots into playing mode: the client starts with
// `desiredPlaying=true` and applies `setMutedState(false)` once the live
// repl is ready, so a fresh tab is audible without a click and the button
// reads Mute. The button is a LOCAL mute gate only: it toggles
// per-browser `localMuted`, drives `liveRepl.stop()/start()` directly,
// never writes `desiredPlaying`, and never fetches `/api/hush` or
// `/api/play`. A `transport` frame records `desiredPlaying` and re-applies
// effective audio (`desiredPlaying && !localMuted`) without ever clearing
// `localMuted`; `snapshot`/`code` frames never drive audio.
//
// These tests drive the SERVED session.js bytes in goja — the same seam
// the coherence tests use — never a Go reimplementation. The stub live
// repl records start/stop calls, the fake document carries the #play-btn
// stub, and a recording fetch proves the mute path never touches the
// transport endpoints.

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// mutePrelude extends the session harness with the collaborators the mute
// behaviour needs: a #play-btn stub recording label and audibility class, a
// stub live repl recording start/stop, a controllable strudelInitPromise plus
// a strudel.repl sandbox stub so boot() can run headless, location/WebSocket
// stubs so connect() can run headless, and a recording fetch so any transport
// POST would be caught.
const mutePrelude = sessionJSPrelude + `
window.__fetches = [];
var fetch = function (path, init) {
  window.__fetches.push({ path: path, body: init && init.body ? JSON.parse(init.body) : null });
  return apiStub();
};
var location = { protocol: "http:", host: "example.test" };
var __wsInstances = [];
var WebSocket = function (url) {
  var ws = { url: url, onopen: null, onmessage: null, onclose: null, onerror: null };
  __wsInstances.push(ws);
  return ws;
};
window.__playBtn = {
  textContent: "",
  __playingClass: false,
  classList: {
    toggle: function (cls, force) {
      if (cls === "playing") { window.__playBtn.__playingClass = !!force; }
    },
    add: function (cls) {
      if (cls === "playing") { window.__playBtn.__playingClass = true; }
    },
    remove: function (cls) {
      if (cls === "playing") { window.__playBtn.__playingClass = false; }
    },
  },
  __handlers: {},
  addEventListener: function (name, fn) {
    this.__handlers[name] = this.__handlers[name] || [];
    this.__handlers[name].push(fn);
  },
};
window.__liveStarts = 0;
window.__liveStops = 0;
window.__liveStub = {
  start: function () { window.__liveStarts++; },
  stop: function () { window.__liveStops++; },
  setPattern: function () { return apiStub(); },
};
window.__resolveBoot = null;
window.__rejectBoot = null;
window.strudelInitPromise = new Promise(function (res, rej) {
  window.__resolveBoot = res;
  window.__rejectBoot = rej;
});
window.strudel = {
  repl: function () {
    return {
      evaluate: function () { return { queryArc: function () { return []; } }; },
      state: {},
    };
  },
};
document.getElementById = function (id) {
  if (id === "play-btn") { return window.__playBtn; }
  return null;
};
window.__btnLabel = function () { return window.__playBtn.textContent; };
window.__btnPlaying = function () { return !!window.__playBtn.__playingClass; };
window.__fetchPaths = function () {
  return window.__fetches.map(function (f) { return f.path; }).join(",");
};
`

// newMuteRuntime loads the served session.js over the mute prelude. Boot
// runs at load (document.readyState is "complete" in the harness) and parks
// on the controllable strudelInitPromise, so each test resolves it with the
// stub live repl when it is ready to observe boot.
func newMuteRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, mutePrelude)
	rt.run(servedSyncJS(t))
	rt.run(servedSessionJS(t))
	rt.run(`window.__live = makeLive();
	  window.__sandbox = {
	    evaluate: function () { return { queryArc: function () { return []; } }; },
	    state: {},
	  };`)
	return rt
}

// bootSettle resolves the boot promise with the stub live repl and drains
// the microtask queue so boot() runs to connect(). Eight pumps bound the
// wait without sleeping, matching the autoload harness.
func bootSettle(rt *syncRuntime) {
	rt.run(`window.__resolveBoot(window.__liveStub)`)
	for i := 0; i < 8; i++ {
		rt.run("0")
	}
}

func muteLabel(t *testing.T, rt *syncRuntime) string {
	t.Helper()
	return rt.eval(`window.__btnLabel()`).String()
}

func mutePlaying(t *testing.T, rt *syncRuntime) bool {
	t.Helper()
	return rt.eval(`window.__btnPlaying()`).ToBoolean()
}

func liveStarts(t *testing.T, rt *syncRuntime) int64 {
	t.Helper()
	return rt.eval(`window.__liveStarts`).ToInteger()
}

func liveStops(t *testing.T, rt *syncRuntime) int64 {
	t.Helper()
	return rt.eval(`window.__liveStops`).ToInteger()
}

func fetchPaths(t *testing.T, rt *syncRuntime) string {
	t.Helper()
	return rt.eval(`window.__fetchPaths()`).String()
}

func driveFrame(t *testing.T, rt *syncRuntime, frame string) {
	t.Helper()
	rt.run(`window.strudelSession.handleFrame(` + frame + `, window.__sandbox, window.__live);`)
	rt.settle()
}

func transportFrame(playing bool) string {
	if playing {
		return `JSON.stringify({ kind: "transport", snapshot: { playing: true } })`
	}
	return `JSON.stringify({ kind: "transport", snapshot: { playing: false } })`
}

// TestBootStartsPlayingWithMuteLabel is the behaviour the feature exists
// for: a fresh tab applies the boot intent without a click, so the stub
// live repl is started exactly once and the button reads Mute.
func TestBootStartsPlayingWithMuteLabel(t *testing.T) {
	rt := newMuteRuntime(t)
	bootSettle(rt)
	if got := liveStarts(t, rt); got != 1 {
		t.Errorf("live repl starts after boot = %d, want 1: a fresh tab must be audible without a click", got)
	}
	if got := liveStops(t, rt); got != 0 {
		t.Errorf("live repl stops after boot = %d, want 0", got)
	}
	if got := muteLabel(t, rt); got != "Mute" {
		t.Errorf("button label after boot = %q, want %q", got, "Mute")
	}
	if !mutePlaying(t, rt) {
		t.Error("button lacks the playing class after boot: the pre-JS paint and post-boot state disagree")
	}
	if got := rt.eval(`window.strudelSession.getMutedState()`).String(); got != "false" {
		t.Errorf("getMutedState() after boot = %s, want false", got)
	}
}

// TestMuteToggleStopsAndUnmuteRestores pins the local gate: muting stops
// THIS repl and flips the label to Unmute, unmuting starts it again and
// flips the label back — all without touching shared intent or the wire.
func TestMuteToggleStopsAndUnmuteRestores(t *testing.T) {
	rt := newMuteRuntime(t)
	bootSettle(rt)

	rt.run(`window.strudelSession.toggleMute()`)
	if got := liveStops(t, rt); got != 1 {
		t.Errorf("live repl stops after mute = %d, want 1: muting must hush this listener", got)
	}
	if got := muteLabel(t, rt); got != "Unmute" {
		t.Errorf("button label while muted = %q, want %q", got, "Unmute")
	}
	if mutePlaying(t, rt) {
		t.Error("button keeps the playing class while muted: the class tracks effective audibility, not the gate")
	}

	rt.run(`window.strudelSession.toggleMute()`)
	if got := liveStarts(t, rt); got != 2 {
		t.Errorf("live repl starts after unmute = %d, want 2 (boot + unmute)", got)
	}
	if got := muteLabel(t, rt); got != "Mute" {
		t.Errorf("button label after unmute = %q, want %q", got, "Mute")
	}
	if !mutePlaying(t, rt) {
		t.Error("button lacks the playing class after unmute with playing intent: audibility must be re-asserted")
	}
	if got := fetchPaths(t, rt); got != "" {
		t.Errorf("mute toggles fetched %q, want no requests: mute is local to this browser", got)
	}
}

// TestMuteButtonNeverTouchesTransportEndpoints proves the user decision at
// the narrowest seam: every mute transition must leave the fetch recorder
// empty, so no mute click can ever become a shared hush or play.
func TestMuteButtonNeverTouchesTransportEndpoints(t *testing.T) {
	rt := newMuteRuntime(t)
	bootSettle(rt)
	rt.run(`window.strudelSession.setMutedState(true)`)
	rt.run(`window.strudelSession.setMutedState(false)`)
	rt.run(`window.strudelSession.toggleMute()`)
	rt.run(`window.strudelSession.toggleMute()`)
	if got := fetchPaths(t, rt); got != "" {
		t.Errorf("mute transitions fetched %q, want no requests: the Mute button must never POST /api/hush or /api/play", got)
	}
	js := servedSessionJS(t)
	if strings.Contains(js, "/api/hush") || strings.Contains(js, "/api/play") {
		t.Error("served session.js references a transport endpoint: mute has a wire path to the shared transport")
	}
}

// TestTransportFrameNeverClearsLocalMute pins stickiness: a muted listener
// stays stopped with label Unmute across transport frames in both
// directions — no remote play can unmute it.
func TestTransportFrameNeverClearsLocalMute(t *testing.T) {
	rt := newMuteRuntime(t)
	bootSettle(rt)
	rt.run(`window.strudelSession.setMutedState(true)`)
	starts := liveStarts(t, rt)

	driveFrame(t, rt, transportFrame(true))
	driveFrame(t, rt, transportFrame(false))
	driveFrame(t, rt, transportFrame(true))

	if got := rt.eval(`window.strudelSession.getMutedState()`).String(); got != "true" {
		t.Errorf("getMutedState() after transport frames = %s, want true: transport must never clear a local mute", got)
	}
	// refreshAudio re-applies the (stopped) state on every frame by design —
	// what must not happen is a start. So the start count, not the stop
	// count, is the stickiness assertion.
	if got := liveStarts(t, rt); got != starts {
		t.Errorf("live repl starts moved %d -> %d across transport frames: a muted listener must stay stopped", starts, got)
	}
	if got := muteLabel(t, rt); got != "Unmute" {
		t.Errorf("button label after transport frames = %q, want %q", got, "Unmute")
	}
}

// TestUnmutedListenerStillFollowsTransport proves the other half of the
// contract: without a local mute, remote hush/play still drive this
// listener's audio, so agent transport keeps working and the server is
// untouched.
func TestUnmutedListenerStillFollowsTransport(t *testing.T) {
	rt := newMuteRuntime(t)
	bootSettle(rt)

	driveFrame(t, rt, transportFrame(false))
	if got := liveStops(t, rt); got != 1 {
		t.Errorf("live repl stops after remote hush = %d, want 1: an unmuted listener still follows transport", got)
	}
	if mutePlaying(t, rt) {
		t.Error("button keeps the playing class after a remote hush: the class tracks effective audibility")
	}

	driveFrame(t, rt, transportFrame(true))
	if got := liveStarts(t, rt); got != 2 {
		t.Errorf("live repl starts after remote play = %d, want 2 (boot + play)", got)
	}
	if !mutePlaying(t, rt) {
		t.Error("button lacks the playing class after a remote play")
	}
	// The label is a pure function of the mute gate, not of audibility.
	if got := muteLabel(t, rt); got != "Mute" {
		t.Errorf("button label after transport frames = %q, want %q: the label reflects the gate, not audibility", got, "Mute")
	}
}

// TestSnapshotFramesNeverDriveAudio pins reconnect safety: catch-up
// snapshot and code frames must not start, stop, or relabel anything, so a
// reconnect can never force-start a muted listener (and never hush a
// sounding one either).
func TestSnapshotFramesNeverDriveAudio(t *testing.T) {
	for _, tc := range []struct {
		name  string
		muted bool
	}{
		{"muted listener stays muted", true},
		{"sounding listener keeps sounding", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newMuteRuntime(t)
			bootSettle(rt)
			if tc.muted {
				rt.run(`window.strudelSession.setMutedState(true)`)
			}
			starts, stops := liveStarts(t, rt), liveStops(t, rt)
			label := muteLabel(t, rt)

			driveFrame(t, rt, `JSON.stringify({ kind: "snapshot", snapshot: { version: 3, code: "x", playing: true } })`)
			driveFrame(t, rt, `JSON.stringify({ kind: "code", snapshot: { version: 4, code: "y", playing: true } })`)

			if got := liveStarts(t, rt); got != starts {
				t.Errorf("live repl starts moved %d -> %d on catch-up frames: snapshot/code must never drive audio", starts, got)
			}
			if got := liveStops(t, rt); got != stops {
				t.Errorf("live repl stops moved %d -> %d on catch-up frames: snapshot/code must never drive audio", stops, got)
			}
			if got := muteLabel(t, rt); got != label {
				t.Errorf("button label moved %q -> %q on catch-up frames", label, got)
			}
		})
	}
}

// TestUnmuteIntoHushedServerStaysSilent pins the gate-vs-audibility split:
// unmuting while shared intent is stopped flips the label back to Mute but
// leaves the repl stopped — the label reflects the gate, not audibility.
func TestUnmuteIntoHushedServerStaysSilent(t *testing.T) {
	rt := newMuteRuntime(t)
	bootSettle(rt)

	driveFrame(t, rt, transportFrame(false))
	rt.run(`window.strudelSession.setMutedState(true)`)
	starts := liveStarts(t, rt)

	rt.run(`window.strudelSession.setMutedState(false)`)
	if got := liveStarts(t, rt); got != starts {
		t.Errorf("live repl starts moved %d -> %d on unmute into hush: unmuting must not start audio the shared intent stopped", starts, got)
	}
	if got := muteLabel(t, rt); got != "Mute" {
		t.Errorf("button label after unmute into hush = %q, want %q: the label reflects the gate, not audibility", got, "Mute")
	}
	if mutePlaying(t, rt) {
		t.Error("button carries the playing class while shared intent is stopped: the class tracks effective audibility")
	}
}

// TestPlayButtonClickCallsToggleMuteOnly drives the SERVED welcome.html
// inline script with a stub strudelSession: DOMContentLoaded registration
// followed by a click must call toggleMute exactly once and record no
// transport fetch. The recording fetch stub is installed BEFORE the served
// script runs, so a click handler that POSTs to a transport endpoint fails
// here on the recorded path — never on a ReferenceError from a missing
// fetch in this harness.
func TestPlayButtonClickCallsToggleMuteOnly(t *testing.T) {
	vm := goja.New()
	runJS(t, vm, autoloadPrelude, "the autoload prelude")
	runJS(t, vm, `
  window.__fetches = [];
  var fetch = function (path, init) {
    window.__fetches.push(String(path));
    return Promise.resolve({ ok: true });
  };
  window.__fetchPaths = function () { return window.__fetches.join(","); };
`, "the recording fetch stub")
	runJS(t, vm, servedWelcomeInlineScript(t), "the served inline loader")
	runJS(t, vm, `
	  window.__toggleMuteCalls = 0;
	  window.strudelSession = {
	    toggleMute: function () { window.__toggleMuteCalls++; },
	  };
	  window.__clickPlay = null;
	  var __playStub = {
	    addEventListener: function (name, fn) {
	      if (name === "click") { window.__clickPlay = fn; }
	    },
	  };
	  var __origGet = document.getElementById;
	  document.getElementById = function (id) {
	    if (id === "play-btn") { return __playStub; }
	    return __origGet(id);
	  };
	`, "the toggleMute stub and play-btn capture")
	// The served inline script registers its DOMContentLoaded listener at
	// load; fire every DOMContentLoaded handler, then the captured click.
	runJS(t, vm, `
	  (__domHandlers["DOMContentLoaded"] || []).forEach(function (fn) { fn(); });
	  if (!window.__clickPlay) { throw new Error("click handler was not registered on #play-btn"); }
	  window.__clickPlay();
	`, "firing DOMContentLoaded and clicking play")
	if got := runJS(t, vm, `window.__toggleMuteCalls`, "reading toggleMute calls").ToInteger(); got != 1 {
		t.Errorf("toggleMute calls after one play click = %d, want exactly 1", got)
	}
	if got := runJS(t, vm, `window.__fetchPaths()`, "reading recorded fetches").String(); got != "" {
		t.Errorf("play click fetched %q, want no requests: the Mute button must never POST /api/hush or /api/play", got)
	}
	script := servedWelcomeInlineScript(t)
	if !strings.Contains(script, "window.strudelSession.toggleMute();") {
		t.Error("served inline click handler does not call window.strudelSession.toggleMute() and nothing else")
	}
	if strings.Contains(script, "requestPlayPause") || strings.Contains(script, "getCurrentPlayingState") {
		t.Error("served inline click handler still references the intent-claiming API: the button must only toggle local mute")
	}
}
