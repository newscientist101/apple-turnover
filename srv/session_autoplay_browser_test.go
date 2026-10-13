package srv

import (
	"strings"
	"testing"
)

// Autoplay unlock on the first user gesture (strudel-agent-3q0.6),
// executed as served.
//
// The two prior fixes each made one bad assumption. The boot-retry fix
// (f00b91b) treated scheduler .started as audible; the resume fix (d9f2514)
// modelled the blocked browser as one whose resume() NEVER resolves and whose
// only gesture is the Mute button. Neither is true: in a browser that blocks
// autoplay, resume() stays PENDING until the first user gesture, and the
// gesture can be ANY interaction with the page -- it does not have to be a
// Mute/Unmute cycle. The web-audio standard for this is the unlock handler:
// the page listens for its first pointerdown/keydown/touchend and resumes the
// still-suspended AudioContext then, touching nothing else.
//
// These tests model the blocked browser FAITHFULLY: resume() before a gesture
// increments the call count and returns a promise that never settles, so a
// fire-and-forget resume at boot proves nothing -- exactly the defect the
// instantly-resolving stubs of the previous fix hid.
//
// The autoplay probe tests assert on the POSTED BODY, per the uvj.15 rule: a
// browser-side audio state an agent cannot read from the server is a claim
// only a human watching the tab could verify.

const autoplayPrelude = mutePrelude + `
window.__docHandlers = {};
document.addEventListener = function (name, fn) {
  window.__docHandlers[name] = window.__docHandlers[name] || [];
  window.__docHandlers[name].push(fn);
};
var navigator = { userActivation: { hasBeenActive: false } };
`

// blockedAudioContextJS installs the faithful blocked-browser stub: resume
// before any user gesture pends forever and leaves the state suspended; once
// __activation is set (the browser's grant on a real gesture, which the
// harness cannot emit), resume resolves and the state flips to running.
const blockedAudioContextJS = `
window.__activation = false;
window.__resumeCalls = 0;
window.__audioContext = {
  state: "suspended",
  resume: function () {
    window.__resumeCalls++;
    if (window.__activation) {
      window.__audioContext.state = "running";
      return Promise.resolve();
    }
    return new Promise(function () {});
  },
};
`

// patternGatedLiveJS installs the live stub whose start() is ineffective
// before the first setPattern, mirroring the pinned bundle's scheduler
// rejection, and whose commit continuation must be fired by hand because
// harness thenables never assimilate under await.
const patternGatedLiveJS = `
window.__liveStarts = 0;
window.__liveStops = 0;
window.__liveHasPattern = false;
window.__liveStub = {
  start: function () {
    if (window.__liveHasPattern) { window.__liveStarts++; }
    else { throw new Error("Scheduler: no pattern set! call .setPattern first."); }
  },
  stop: function () { window.__liveStops++; },
  setPattern: function () { window.__liveHasPattern = true; return { then: function (cb) { window.__commitCb = cb; return apiStub(); }, catch: function () { return apiStub(); } }; },
};
`

// newAutoplayRuntime loads the served session.js over the autoplay prelude.
func newAutoplayRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, autoplayPrelude)
	rt.run(servedSyncJS(t))
	rt.run(servedSessionJS(t))
	rt.run(`window.__live = makeLive();
	  window.__sandbox = {
	    evaluate: function () { return { queryArc: function () { return []; } }; },
	    state: {},
	  };`)
	return rt
}

// driveCodeFrame lands a snapshot frame carrying a document on the stub live
// repl, then fires the hand-modelled commit continuation so the pattern is
// committed exactly as the boot-retry tests drive it.
func driveCodeFrame(t *testing.T, rt *syncRuntime, version int) {
	t.Helper()
	driveFrameTo(t, rt, `JSON.stringify({ kind: "snapshot", snapshot: { version: `+itoa(version)+`, code: 's("bd")', anchor: null, playing: true } })`)
	rt.run(`window.__commitCb && window.__commitCb(undefined)`)
	rt.settle()
}

// fireGesture models the browser side of a real gesture: activation is
// granted by the BROWSER (never by page code), then the captured listener
// runs. The harness cannot emit trusted input events, so this is the seam.
func fireGesture(t *testing.T, rt *syncRuntime, name string) {
	t.Helper()
	handlers := rt.eval(`window.__docHandlers["` + name + `"] || null`).Export()
	list, ok := handlers.([]interface{})
	if !ok || len(list) == 0 {
		t.Fatalf("no %s listener registered on document: the page must listen for its first gesture of ANY kind", name)
	}
	rt.run(`window.__activation = true;
	  (window.__docHandlers["` + name + `"] || []).forEach(function (fn) { fn({}); });`)
	rt.settle()
}

// TestFirstGestureUnlocksBlockedAudioContext is the behaviour the bead was
// reopened for. The stub models the blocked browser faithfully: the boot-time
// fire-and-forget resume() pends and leaves the context suspended, so a page
// that only resumes when it starts audio stays silent until a Mute/Unmute
// cycle. The FIRST gesture of any kind must resume the still-suspended
// context -- and must touch nothing else: the scheduler is already started
// and sounding, so the only missing thing is the context.
func TestFirstGestureUnlocksBlockedAudioContext(t *testing.T) {
	rt := newAutoplayRuntime(t)
	rt.run(blockedAudioContextJS)
	rt.run(patternGatedLiveJS)
	bootSettle(rt)

	// The boot start was swallowed (no pattern yet), but refreshAudio still
	// attempted its Tier-1 resume, which pends: the context stays suspended.
	if got := rt.eval(`window.__audioContext.state`).String(); got != "suspended" {
		t.Fatalf("context state after boot = %q, want suspended: the harness models a blocked browser", got)
	}
	if got := rt.eval(`window.__resumeCalls`).ToInteger(); got != 1 {
		t.Fatalf("resume calls after boot = %d, want 1: refreshAudio must still attempt the resume (Tier 1)", got)
	}

	// The first code commit lands and retries the boot start: audio intent is
	// applied, yet the context is STILL suspended -- the exact user-visible
	// defect (scheduler started, tab silent).
	driveCodeFrame(t, rt, 1)
	if got := liveStarts(t, rt); got != 1 {
		t.Fatalf("live repl starts after the first commit = %d, want 1", got)
	}
	if got := rt.eval(`window.__audioContext.state`).String(); got != "suspended" {
		t.Fatalf("context state after commit = %q, want suspended: a fire-and-forget resume cannot unlock a blocked browser", got)
	}

	// The page must be listening for its first gesture of ANY kind.
	for _, name := range []string{"pointerdown", "keydown", "touchend"} {
		if !rt.eval(`!!(window.__docHandlers["` + name + `"] || []).length`).ToBoolean() {
			t.Errorf("no %s listener registered on document: a blocked browser resolves resume only on a gesture, and the gesture must not have to be the Mute button", name)
		}
	}

	// The first gesture arrives (activation granted by the browser, not by
	// page code) and the unlock handler resumes the context -- and does
	// nothing else.
	startsBefore, stopsBefore := liveStarts(t, rt), liveStops(t, rt)
	fireGesture(t, rt, "pointerdown")
	if got := rt.eval(`window.__audioContext.state`).String(); got != "running" {
		t.Fatalf("context state after the first gesture = %q, want running: the first gesture must unlock the context", got)
	}
	if got := rt.eval(`window.__resumeCalls`).ToInteger(); got < 2 {
		t.Errorf("resume calls after the first gesture = %d, want >= 2: the unlock handler must resume the still-suspended context", got)
	}
	if got := liveStarts(t, rt); got != startsBefore {
		t.Errorf("live repl starts changed across the gesture (%d -> %d): the scheduler is already started; the unlock must not re-drive it", startsBefore, got)
	}
	if got := liveStops(t, rt); got != stopsBefore {
		t.Errorf("live repl stops changed across the gesture (%d -> %d): the unlock must never hush a sounding listener", stopsBefore, got)
	}
	if got := rt.eval(`window.strudelSession.getMutedState()`).String(); got != "false" {
		t.Errorf("getMutedState() after gesture = %s, want false: the unlock must not touch the mute gate", got)
	}
	for _, p := range strings.Split(fetchPaths(t, rt), ",") {
		if p == "/api/hush" || p == "/api/play" {
			t.Errorf("gesture unlock fetched %q: resuming the context must never POST to a transport endpoint", p)
		}
	}
}

// TestGestureUnlockIgnoresRunningContext pins the no-op half: in a browser
// whose policy allows autoplay (or a listener the user already unlocked), the
// gesture handlers must not call resume again -- and must not do anything
// else either.
func TestGestureUnlockIgnoresRunningContext(t *testing.T) {
	rt := newAutoplayRuntime(t)
	rt.run(`window.__resumeCalls = 0;
	window.__audioContext = {
	  state: "running",
	  resume: function () { window.__resumeCalls++; return Promise.resolve(); },
	};`)
	rt.run(patternGatedLiveJS)
	bootSettle(rt)
	driveCodeFrame(t, rt, 1)
	startsBefore, stopsBefore := liveStarts(t, rt), liveStops(t, rt)

	for _, name := range []string{"pointerdown", "keydown", "touchend"} {
		fireGesture(t, rt, name)
	}
	if got := rt.eval(`window.__resumeCalls`).ToInteger(); got != 0 {
		t.Errorf("resume calls across gestures on a running context = %d, want 0: never resume a running context", got)
	}
	if got := liveStarts(t, rt); got != startsBefore {
		t.Errorf("live repl starts changed across the gestures (%d -> %d)", startsBefore, got)
	}
	if got := liveStops(t, rt); got != stopsBefore {
		t.Errorf("live repl stops changed across the gestures (%d -> %d)", stopsBefore, got)
	}
}

// TestGestureUnlockNeverUnmutes is the invariant the unlock shares with the
// mute gate: a LOCALLY MUTED listener stays muted across any gesture. The
// unlock may resume the context (the context is not the transport), but it
// must never start the scheduler, clear the gate, or touch the wire -- so a
// muted tab stays silent no matter how much the user clicks elsewhere.
func TestGestureUnlockNeverUnmutes(t *testing.T) {
	rt := newAutoplayRuntime(t)
	rt.run(blockedAudioContextJS)
	rt.run(patternGatedLiveJS)
	bootSettle(rt)
	rt.run(`window.strudelSession.setMutedState(true)`)
	driveCodeFrame(t, rt, 1)

	fireGesture(t, rt, "keydown")
	if got := rt.eval(`window.strudelSession.getMutedState()`).String(); got != "true" {
		t.Errorf("getMutedState() after a gesture while muted = %s, want true: no gesture may clear the local mute gate", got)
	}
	if got := liveStarts(t, rt); got != 0 {
		t.Errorf("live repl starts after a gesture while muted = %d, want 0: a muted tab must stay silent across any gesture", got)
	}
	if got := muteLabel(t, rt); got != "Unmute" {
		t.Errorf("button label after a gesture while muted = %q, want Unmute", got)
	}
	for _, p := range strings.Split(fetchPaths(t, rt), ",") {
		if p == "/api/hush" || p == "/api/play" {
			t.Errorf("gesture unlock fetched %q while muted: must never POST to a transport endpoint", p)
		}
	}
}

// TestGestureBeforeFirstCommitNeverStartsScheduler pins the other ordering:
// a gesture that arrives BEFORE any code has committed must resume the
// context at most -- never drive the scheduler, which has no pattern yet. The
// pending boot start is still consumed by the later commit, so the tab
// becomes audible without a second gesture.
func TestGestureBeforeFirstCommitNeverStartsScheduler(t *testing.T) {
	rt := newAutoplayRuntime(t)
	rt.run(blockedAudioContextJS)
	rt.run(patternGatedLiveJS)
	bootSettle(rt)

	fireGesture(t, rt, "pointerdown")
	if got := liveStarts(t, rt); got != 0 {
		t.Errorf("live repl starts after a gesture with no pattern = %d, want 0: the unlock must never drive the scheduler", got)
	}
	if got := rt.eval(`window.__audioContext.state`).String(); got != "running" {
		t.Fatalf("context state after the gesture = %q, want running", got)
	}

	// The first commit lands later and consumes the pending boot start: the
	// context is already unlocked, so the tab is audible -- one gesture, no
	// Mute/Unmute cycle, in either arrival order.
	driveCodeFrame(t, rt, 1)
	if got := liveStarts(t, rt); got != 1 {
		t.Errorf("live repl starts after the commit following the gesture = %d, want 1", got)
	}
}

// evalResultBody returns the recorded /api/eval-result POST body for the
// given version, so the probe tests assert on the body that would have been
// POSTed -- never on what the module computed.
func evalResultBody(t *testing.T, rt *syncRuntime, version int) map[string]interface{} {
	t.Helper()
	bodies := rt.eval(`window.__fetches.filter(function (f) { return f.path === "/api/eval-result"; }).map(function (f) { return f.body; })`).Export()
	list, ok := bodies.([]interface{})
	if !ok || len(list) == 0 {
		t.Fatalf("no /api/eval-result POST recorded: the verdict never reached the wire")
	}
	for _, raw := range list {
		if body, ok := raw.(map[string]interface{}); ok {
			if v, ok := body["version"].(int64); ok && int(v) == version {
				return body
			}
		}
	}
	t.Fatalf("no /api/eval-result POST recorded for version %d", version)
	return nil
}

// TestEvalResultCarriesAutoplayProbeOnTheWire is the uvj.15 rule applied to
// audio: a browser-side autoplay state the agent cannot read is a claim only a
// human watching the tab could verify. Every eval verdict must carry, in its
// opaque stats, the autoplay state the pattern would play through, so an
// agent reading lastEvalResult can tell "ok:true but the browser has the
// context suspended" from "ok:true and audible".
func TestEvalResultCarriesAutoplayProbeOnTheWire(t *testing.T) {
	rt := newAutoplayRuntime(t)
	rt.run(blockedAudioContextJS)
	rt.run(patternGatedLiveJS)
	bootSettle(rt)
	driveCodeFrame(t, rt, 1)

	body := evalResultBody(t, rt, 1)
	stats, ok := body["stats"].(map[string]interface{})
	if !ok {
		t.Fatalf("eval-result body has no stats object: %v", body)
	}
	probe, ok := stats["autoplay"].(map[string]interface{})
	if !ok {
		t.Fatalf("eval-result stats carries no autoplay probe: an agent cannot see why ok:true is silent: %v", stats)
	}
	if got := probe["ctx"]; got != "suspended" {
		t.Errorf("autoplay probe ctx = %v, want suspended: the blocked browser must be reported as suspended", got)
	}
	if got, ok := probe["act"].(bool); !ok || got {
		t.Errorf("autoplay probe act = %v, want false: no gesture has happened at eval time", probe["act"])
	}

	// The unlocked browser reports the unlocked state: same resolver, same
	// key, different fact -- the agent sees which of the two worlds it is in.
	rt2 := newAutoplayRuntime(t)
	rt2.run(`window.__activation = true;
	navigator.userActivation.hasBeenActive = true;
	window.__resumeCalls = 0;
	window.__audioContext = {
	  state: "running",
	  resume: function () { window.__resumeCalls++; return Promise.resolve(); },
	};`)
	rt2.run(patternGatedLiveJS)
	bootSettle(rt2)
	driveCodeFrame(t, rt2, 1)

	body2 := evalResultBody(t, rt2, 1)
	stats2, ok := body2["stats"].(map[string]interface{})
	if !ok {
		t.Fatalf("eval-result body has no stats object: %v", body2)
	}
	probe2, ok := stats2["autoplay"].(map[string]interface{})
	if !ok {
		t.Fatalf("unlocked browser reports no autoplay probe: %v", stats2)
	}
	if got := probe2["ctx"]; got != "running" {
		t.Errorf("autoplay probe ctx = %v, want running", got)
	}
	if got, ok := probe2["act"].(bool); !ok || !got {
		t.Errorf("autoplay probe act = %v, want true: the unlocked browser has seen a gesture", probe2["act"])
	}
}

// TestDryRunCarriesAutoplayProbeOnTheWire pins the "one resolver feeds both
// paths" rule for the probe: the eval verdict and the dry-run verdict are two
// ways an agent asks the same question, and the autoplay state is half of
// what the answer means.
func TestDryRunCarriesAutoplayProbeOnTheWire(t *testing.T) {
	rt := newAutoplayRuntime(t)
	rt.run(blockedAudioContextJS)
	bootSettle(rt)
	rt.run(`window.strudelSession.dryRunCandidate({ id: 1, code: 's("bd")' }, window.__sandbox)`)
	rt.settle()
	rt.settle()

	bodies := rt.eval(`window.__fetches.filter(function (f) { return f.path === "/api/dry-run-result"; }).map(function (f) { return f.body; })`).Export()
	list, ok := bodies.([]interface{})
	if !ok || len(list) == 0 {
		t.Fatalf("no /api/dry-run-result POST recorded: the dry-run verdict never reached the wire")
	}
	body, _ := list[0].(map[string]interface{})
	stats, ok := body["stats"].(map[string]interface{})
	if !ok {
		t.Fatalf("dry-run body has no stats object: %v", body)
	}
	probe, ok := stats["autoplay"].(map[string]interface{})
	if !ok {
		t.Fatalf("dry-run stats carries no autoplay probe: %v", stats)
	}
	if got := probe["ctx"]; got != "suspended" {
		t.Errorf("autoplay probe ctx = %v, want suspended", got)
	}
}
