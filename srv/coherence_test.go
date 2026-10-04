package srv

// This file proves the SERVER half of multi-client coherence (issue
// strudel-agent-3vo.8): POST /api/anchor, the guard on what an agent may
// publish as the shared timeline, and the frame that tells every listener to
// adopt it.
//
// The anchor is the entire cross-machine mechanism — strudel has no clock to
// align to — so the load-bearing properties are all about refusing to publish a
// timeline no listener could use, and never announcing one that was not stored:
//
//   - an accepted re-anchor broadcasts exactly one `anchor` frame, and it does
//     not bump the version (it moves where the shared timeline starts, not what
//     is playing);
//   - a REFUSED re-anchor broadcasts NOTHING and leaves the stored anchor
//     untouched, so no listener is ever told to adopt a bar grid the server
//     itself rejected;
//   - the response is byte-identical to GET /api/state, so the agent and the
//     listeners cannot disagree about which timeline is live.
//
// The client half (cycle-aligned commit, sync status) lives in
// strudel-agent-3vo.8.2 and .8.5 and adds to this file.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// anchorBody builds a POST /api/anchor body. epochMS is passed in rather than
// defaulted to "now" because the skew cases need to be far from it, and because
// the acceptance cases must NOT use a pinned literal: the server refuses a
// skewed epoch, so a constant would make an acceptance assertion pass only by
// being refused, which would prove nothing about acceptance.
func anchorBody(epochMS int64, cps float64) string {
	return fmt.Sprintf(`{"epochMs":%d,"cps":%g}`, epochMS, cps)
}

// TestAnchorRouteRejectsNonsenseAnchor pins the refusal half of the contract,
// and the property that makes a refusal safe: a refused re-anchor must change
// NOTHING. A handler that stored the new anchor and then complained would leave
// the stored timeline and the reported one disagreeing, and every listener would
// be scheduling against a grid the agent never got an acknowledgement for.
func TestAnchorRouteRejectsNonsenseAnchor(t *testing.T) {
	_, base, _ := fanoutServer(t)

	// Seed a known-good anchor, so the "unchanged" assertions below are about a
	// non-default value rather than the constructor's.
	good := time.Now().UnixMilli()
	if code, raw := fanoutPost(t, base, "/api/anchor", anchorBody(good, 0.75)); code != http.StatusOK {
		t.Fatalf("seeding a valid anchor: %d %q", code, bodyOf(raw))
	}

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "zero rate",
			body: anchorBody(time.Now().UnixMilli(), 0),
			want: "cps must be greater than 0",
		},
		{
			name: "negative rate",
			body: anchorBody(time.Now().UnixMilli(), -1),
			want: "cps must be greater than 0",
		},
		{
			// A unit mix-up is the realistic accident: 0.5 cycles per MINUTE.
			name: "rate above the ceiling",
			body: anchorBody(time.Now().UnixMilli(), AnchorMaxCPS*1000),
			want: "cps must be at most",
		},
		{
			name: "epoch far in the past",
			body: anchorBody(time.Now().UnixMilli()-AnchorMaxSkewMS-60_000, DefaultCPS),
			want: "epochMs must be within",
		},
		{
			name: "epoch far in the future",
			body: anchorBody(time.Now().UnixMilli()+AnchorMaxSkewMS+60_000, DefaultCPS),
			want: "epochMs must be within",
		},
		{
			// Both fields are required. An absent epochMs must not be read as
			// zero (a 1970 epoch, wildly skewed) and must not silently keep the
			// previous one: a re-anchor replaces the whole timeline.
			name: "missing epochMs",
			body: `{"cps":0.5}`,
			want: "epochMs must be within",
		},
		{
			name: "missing cps",
			body: fmt.Sprintf(`{"epochMs":%d}`, time.Now().UnixMilli()),
			want: "cps must be greater than 0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := fanoutPost(t, base, "/api/anchor", tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("POST /api/anchor %s = %d %q, want 400", tc.body, code, bodyOf(raw))
			}
			if !strings.Contains(raw, tc.want) {
				t.Errorf("error %q does not say %q: the message must name the constraint so an agent can act on it", bodyOf(raw), tc.want)
			}

			// The refusal must be total: the stored timeline is untouched.
			code, raw = fanoutGet(t, base, "/api/state")
			if code != http.StatusOK {
				t.Fatalf("GET /api/state after a refused re-anchor: %d %q", code, bodyOf(raw))
			}
			var snap Snapshot
			if err := json.Unmarshal([]byte(raw), &snap); err != nil {
				t.Fatalf("decode state: %v (%q)", err, bodyOf(raw))
			}
			if snap.Anchor.EpochMS != good || snap.Anchor.CPS != 0.75 {
				t.Errorf("a refused re-anchor changed the stored anchor to %+v, want {%d 0.75}", snap.Anchor, good)
			}
			if snap.Version != 0 {
				t.Errorf("a refused re-anchor changed the version to %d, want 0", snap.Version)
			}
		})
	}
}

// TestAnchorRouteAcceptedBroadcastsOneAnchorFrame is the positive half: an
// accepted re-anchor reaches every listener as exactly one `anchor` frame
// carrying the new timeline.
//
// Exactly one matters as much as at least one. The listener-count frame is
// excluded from the listener that caused it, for a documented reason; there is
// no such reason here, because a listener that misses the re-anchor keeps
// scheduling against the OLD grid while its peers move — which is precisely the
// incoherence this endpoint exists to fix.
func TestAnchorRouteAcceptedBroadcastsOneAnchorFrame(t *testing.T) {
	s, base, wsBase := fanoutServer(t)

	a, _ := wsDial(t, wsBase, nil)
	b, _ := wsDial(t, wsBase, nil)

	before := s.Conductor.Snapshot()
	la := fanoutAttach(t, "a", a, before)
	// b's arrival broadcasts a count frame to a. It is not what this test is
	// about, so it is drained explicitly rather than left to be misread as the
	// anchor frame.
	la.drainListenerCounts(t, 1, 2, "b joining")
	lb := fanoutAttach(t, "b", b, before)

	epoch := time.Now().UnixMilli()
	code, raw := fanoutPost(t, base, "/api/anchor", anchorBody(epoch, 1.25))
	if code != http.StatusOK {
		t.Fatalf("POST /api/anchor: %d %q", code, bodyOf(raw))
	}

	for _, l := range []*fanoutListener{la, lb} {
		ev := l.next(t, l.name+": anchor frame")
		if ev.Kind != EventAnchor {
			t.Fatalf("%s: frame kind = %q, want %q", l.name, ev.Kind, EventAnchor)
		}
		if ev.Snapshot.Anchor.EpochMS != epoch || ev.Snapshot.Anchor.CPS != 1.25 {
			t.Errorf("%s: frame carries anchor %+v, want {%d 1.25}", l.name, ev.Snapshot.Anchor, epoch)
		}
		// A re-anchor moves where the shared timeline starts; it is not a new
		// revision of the music, so it must not enter the code history.
		if ev.Snapshot.Version != before.Version {
			t.Errorf("%s: re-anchor moved the version from %d to %d: a timeline move is not a new code document",
				l.name, before.Version, ev.Snapshot.Version)
		}
		// No second copy, and no count frame provoked by the write itself.
		l.expectNone(t, l.name+" after the anchor frame")
	}
}

// TestAnchorRouteRejectedBroadcastsNothing is the negative fan-out case, and it
// is the one that needs a real socket rather than a state read.
//
// A rejected request that still broadcast would leave every listener believing
// it had adopted a new timeline. Because each listener re-derives its position
// from the anchor it was last told, that belief is invisible to it: it does not
// error, it simply starts committing to the wrong bar, and the drift surfaces
// only later as music that does not line up. Nothing on the wire distinguishes
// "adopted" from "adopted a lie", so the only place to catch it is here.
func TestAnchorRouteRejectedBroadcastsNothing(t *testing.T) {
	s, base, wsBase := fanoutServer(t)

	conn, _ := wsDial(t, wsBase, nil)
	before := s.Conductor.Snapshot()
	l := fanoutAttach(t, "listener", conn, before)

	// Every refusal mode, in one quiet window: bad rate, negative rate, skewed
	// epoch, a missing field, and an undocumented one.
	for _, body := range []string{
		anchorBody(time.Now().UnixMilli(), 0),
		anchorBody(time.Now().UnixMilli(), -2),
		anchorBody(time.Now().UnixMilli()-AnchorMaxSkewMS-60_000, DefaultCPS),
		`{"cps":0.5}`,
		`{"epochMs":1757000000000,"cps":0.5,"surprise":true}`,
	} {
		if code, raw := fanoutPost(t, base, "/api/anchor", body); code != http.StatusBadRequest {
			t.Fatalf("POST /api/anchor %s = %d %q, want 400 so the no-broadcast rule is exercised", body, code, bodyOf(raw))
		}
	}
	l.expectNone(t, "refused re-anchors")
}

// TestAnchorRouteResponseIsTheSameSnapshot proves the agent and the listeners
// read the same timeline. The re-anchor response is produced by the same encoder
// as GET /api/state and broadcast as the same frame, so byte equality is the
// check: an agent that re-anchors and then reads /api/state must not see a
// different epoch than the one its own 200 reported.
func TestAnchorRouteResponseIsTheSameSnapshot(t *testing.T) {
	_, base, _ := fanoutServer(t)

	epoch := time.Now().UnixMilli()
	code, posted := fanoutPost(t, base, "/api/anchor", anchorBody(epoch, 2))
	if code != http.StatusOK {
		t.Fatalf("POST /api/anchor: %d %q", code, bodyOf(posted))
	}
	code, state := fanoutGet(t, base, "/api/state")
	if code != http.StatusOK {
		t.Fatalf("GET /api/state: %d %q", code, bodyOf(state))
	}
	if posted != state {
		t.Errorf("the re-anchor response and GET /api/state disagree:\n POST: %s\n  GET: %s", bodyOf(posted), bodyOf(state))
	}
}

// TestAnchorRouteRejectsMalformedAndOversizedBodies holds the new endpoint to
// the SAME body handling as every other /api route. It is tempting for a new
// handler to decode its own body, and the cap in particular is installed
// centrally precisely so that no endpoint can forget it: a handler reading
// r.Body without going through decodeBody would happily buffer an unbounded
// payload.
func TestAnchorRouteRejectsMalformedAndOversizedBodies(t *testing.T) {
	_, base, _ := fanoutServer(t)

	t.Run("unknown field", func(t *testing.T) {
		code, raw := fanoutPost(t, base, "/api/anchor",
			fmt.Sprintf(`{"epochMs":%d,"cps":0.5,"tempo":120}`, time.Now().UnixMilli()))
		if code != http.StatusBadRequest {
			t.Fatalf("unknown field = %d %q, want 400", code, bodyOf(raw))
		}
		if !strings.Contains(raw, "unknown field") {
			t.Errorf("error %q does not name the unknown field", bodyOf(raw))
		}
	})

	t.Run("trailing data", func(t *testing.T) {
		code, raw := fanoutPost(t, base, "/api/anchor",
			anchorBody(time.Now().UnixMilli(), 0.5)+` {"cps":99}`)
		if code != http.StatusBadRequest {
			t.Fatalf("trailing data = %d %q, want 400", code, bodyOf(raw))
		}
		if !strings.Contains(raw, "unexpected data after JSON body") {
			t.Errorf("error %q does not report trailing data", bodyOf(raw))
		}
	})

	t.Run("oversized", func(t *testing.T) {
		// Padded past APIMaxBodyBytes with an unknown field, so the ONLY reason
		// to refuse is the cap. A handler that validated before sizing would
		// answer 400 here and quietly prove nothing about the cap.
		padding := strings.Repeat("x", APIMaxBodyBytes)
		code, raw := fanoutPost(t, base, "/api/anchor",
			fmt.Sprintf(`{"epochMs":%d,"cps":0.5,"pad":%q}`, time.Now().UnixMilli(), padding))
		if code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized body = %d %q, want 413", code, bodyOf(raw))
		}
	})

	t.Run("wrong verb", func(t *testing.T) {
		// The Allow header is part of the contract, not a courtesy: it is what
		// tells an agent which verb to use, so it is read off a real response
		// rather than assumed.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/anchor", nil)
		if err != nil {
			t.Fatalf("build GET /api/anchor: %v", err)
		}
		client := &http.Client{Transport: boundedTransport(), Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/anchor: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET /api/anchor = %d, want 405", resp.StatusCode)
		}
		if got := resp.Header.Get("Allow"); got != http.MethodPost {
			t.Errorf("405 Allow = %q, want %q", got, http.MethodPost)
		}
	})
}

// ---------------------------------------------------------------------------
// CLIENT HALF (issue strudel-agent-3vo.8.2)
// ---------------------------------------------------------------------------
//
// Everything above proves the server half. What follows proves the client half:
// the cycle-aligned commit. The load-bearing property is a BEHAVIOURAL one --
// two clients handed the same anchor must commit on the same bar -- and a
// behavioural property cannot be proved by grepping a source file for a marker.
// So these tests EXECUTE the real served bytes in goja against a fake clock,
// for the same reason TestAgentAPIDocPayloadShapesMatchARunningServer compares
// the doc to a live server rather than to the source: a json tag, a renamed
// function or a subtly wrong floor() all leave the source perfectly readable
// while every listener drifts a bar apart.
//
// The fake clock is deterministic and advanced explicitly, so a commit that
// waits on a boundary cannot hang the suite: nothing here blocks.

// syncJSPrelude installs the minimum host surface sync.js needs, plus a fake
// clock the test advances by hand. sync.js takes its clock and timers as
// arguments precisely so this is possible; without that seam the only way to
// test timing behaviour would be to sleep, which is how a test suite starts
// hanging.
//
// The timer queue fires every timer due at the new "now", oldest deadline
// first, tie-broken by scheduling order, and re-checks the queue afterwards --
// which is what lets a self-correcting timer re-arm inside the same advance.
// The loop is bounded and THROWS if it does not settle, so a timer that re-arms
// forever is a test failure rather than a hang.
const syncJSPrelude = `
// Declared, not merely assigned: a browser has a global "window", but a bare JS
// engine does not, and READING an undeclared identifier throws a
// ReferenceError. sync.js only ever assigns window.strudelSync, so an empty
// object is the whole host surface it needs.
var window = {};
// A browser window has addEventListener, and session.js registers its teardown
// hook on it. Modelling it here (rather than guarding the call in session.js)
// keeps the served bytes honest: the production file can use the real API, and
// the fake host fails loudly on anything it does not model instead of silently
// letting an untested code path through.
var __listeners = {};
window.addEventListener = function (name, fn) {
  (__listeners[name] = __listeners[name] || []).push(fn);
};
window.__fireEvent = function (name) {
  (__listeners[name] || []).forEach(function (fn) { fn(); });
};
var __now = 0;
var __timers = [];
var __nextTimerId = 1;
// __earlyBy models browser timer jitter: the NEXT timer scheduled fires this
// many ms BEFORE its deadline, and the jitter is then spent. Modelling it as a
// permanent offset would be wrong in an instructive way — a self-correcting
// timer that re-arms could then never satisfy its own re-check, so the queue
// would spin forever instead of committing. ONE early firing is the real case:
// an event loop that comes back before the deadline.
//
// __lateBy is the opposite and far more common case: the timer fires AFTER its
// deadline. It is what the LEAD exists to absorb.
var __earlyBy = 0;
var __lateBy = 0;
// The timers are declared as bare globals AND hung off window: sync.js falls
// back to a bare "setTimeout" when it is given no deps, and in a browser that
// name resolves to window.setTimeout. A bare JS engine has no such alias, so
// both spellings are installed here — otherwise the fallback path fails with a
// ReferenceError and the module could never run headless at all.
var setTimeout = function (fn, ms) {
  var id = __nextTimerId++;
  var delay = Math.max(0, ms - __earlyBy) + __lateBy;
  __earlyBy = 0;
  __lateBy = 0;
  __timers.push({ id: id, at: __now + delay, seq: id, fn: fn });
  return id;
};
var clearTimeout = function (id) {
  __timers = __timers.filter(function (t) { return t.id !== id; });
};
// A browser setInterval RE-ARMS itself; a one-shot push would mean the harness
// silently cannot observe the countdown ticking, and the pagehide teardown
// would have nothing to stop. Re-queueing on fire is what makes the interval
// testable, and the bounded __advance loop is what stops a re-arming timer from
// hanging the suite.
var setInterval = function (fn, ms) {
  var id = __nextTimerId++;
  var period = Math.max(1, ms);
  __timers.push({
    id: id, at: __now + period, seq: id, period: period, fn: function () {
      __timers.push({ id: id, at: __now + period, seq: id, period: period, fn: fn });
      fn();
    }
  });
  return id;
};
var clearInterval = function (id) {
  clearTimeout(id);
};
window.setTimeout = setTimeout;
window.clearTimeout = clearTimeout;
window.setInterval = setInterval;
window.clearInterval = clearInterval;
Date.now = function () { return __now; };
window.__setNow = function (ms) { __now = ms; };
window.__advance = function (ms) {
  __now += ms;
  for (var guard = 0; guard < 1000; guard++) {
    var due = __timers.filter(function (t) { return t.at <= __now; });
    if (due.length === 0) { return; }
    due.sort(function (a, b) { return (a.at - b.at) || (a.seq - b.seq); });
    var t = due[0];
    __timers = __timers.filter(function (x) { return x.id !== t.id; });
    t.fn();
  }
  throw new Error("timer queue did not settle: a timer is re-arming forever");
};
window.__pendingTimers = function () { return __timers.length; };
window.__fired = [];
`

// syncRuntime is one loaded client: a goja runtime carrying the REAL served
// sync.js, plus the fake clock the test advances by hand.
type syncRuntime struct {
	t  *testing.T
	vm *goja.Runtime
}

// newSyncRuntime loads src (the served bytes, not the file on disk) into a
// fresh runtime behind the prelude above.
func newSyncRuntime(t *testing.T, src string) *syncRuntime {
	t.Helper()
	vm := goja.New()
	runJS(t, vm, syncJSPrelude, "the sync.js prelude")
	runJS(t, vm, src, "the served sync.js")
	return &syncRuntime{t: t, vm: vm}
}

// runJS evaluates src under a bounded interrupt. This is the repo's boundedness
// rule applied to a script rather than to a goroutine: without it an accidental
// infinite loop in the client would hang `make verify` instead of failing.
//
// goja's Interrupt takes the value to interrupt WITH, not a channel to wait on,
// so the watchdog calls it directly. That watchdog is joined before
// ClearInterrupt, because goja requires no Interrupt to be in flight when the
// runtime is reused: clearing during a race would leave the runtime permanently
// interrupted and turn every later call into a confusing error instead of the
// real one.

// deadlineSignal is the value a runaway script is interrupted with, so the
// failure names the cause rather than reporting a bare interruption.
type deadlineSignal struct{}

func (deadlineSignal) String() string { return "sync.js exceeded its evaluation budget" }

func runJS(t *testing.T, vm *goja.Runtime, src, what string) goja.Value {
	t.Helper()

	const budget = 10 * time.Second
	done := make(chan struct{})
	var watchdog sync.WaitGroup
	watchdog.Add(1)
	go func() {
		defer watchdog.Done()
		select {
		case <-time.After(budget):
			vm.Interrupt(deadlineSignal{})
		case <-done:
		}
	}()

	v, err := vm.RunString(src)

	close(done)
	watchdog.Wait()
	vm.ClearInterrupt()

	if err != nil {
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			t.Fatalf("running %s: exceeded its %s budget (%v) — an unbounded loop in the client is a defect, not a slow test", what, budget, interrupted)
		}
		t.Fatalf("running %s: %v", what, err)
	}
	return v
}

// eval runs an expression in the runtime and returns it as a Go value.
func (r *syncRuntime) eval(expr string) goja.Value {
	r.t.Helper()
	return runJS(r.t, r.vm, expr, "the expression "+expr)
}

// number evaluates an expression that must be a finite number.
func (r *syncRuntime) number(expr string) float64 {
	r.t.Helper()
	v := r.eval(expr)
	n, ok := v.Export().(float64)
	if !ok {
		r.t.Fatalf("eval %q = %v (%T), want a number", expr, v.Export(), v.Export())
	}
	return n
}

// set installs a Go value (typically a func) as a global the JS can call.
func (r *syncRuntime) set(name string, v any) {
	r.t.Helper()
	if err := r.vm.Set(name, v); err != nil {
		r.t.Fatalf("set %s: %v", name, err)
	}
}

// advance moves this client's clock forward and settles the timer queue.
func (r *syncRuntime) advance(ms int64) {
	r.t.Helper()
	r.run(fmt.Sprintf("window.__advance(%d)", ms))
}

// arm schedules a commit through the served scheduleAtBoundary. The callback
// records into window.__fired and notifies the Go observer; the handle is kept
// in window.__handle so the test can cancel it.
//
// The fake clock is moved to nowMS FIRST, because scheduleAtBoundary derives its
// delay from Date.now(): arming at nowMS without setting the clock would arm a
// timer of the wrong length, and the test would then be measuring the harness
// rather than the client.
func (r *syncRuntime) arm(anchor string, nowMS int64, leadMS int64, onFire func(at map[string]any)) {
	r.t.Helper()
	// vm.Set defines a GLOBAL, which is not the same thing as a property on the
	// window object the callback closes over — hence the bare __onFire(...) call
	// in the expression below.
	r.set("__onFire", func(at goja.Value) {
		if onFire == nil {
			return
		}
		var m map[string]any
		if err := r.vm.ExportTo(at, &m); err != nil {
			r.t.Errorf("exporting the boundary record: %v", err)
			return
		}
		onFire(m)
	})
	r.run(fmt.Sprintf("window.__setNow(%d)", nowMS))
	expr := fmt.Sprintf(
		"window.__handle = window.strudelSync.scheduleAtBoundary("+
			"function (at) { window.__fired.push(at); __onFire(at); }, %s, %d, %d)",
		anchor, nowMS, leadMS)
	r.run(expr)
}

// run evaluates an expression for its effect, discarding the result.
func (r *syncRuntime) run(expr string) {
	r.t.Helper()
	runJS(r.t, r.vm, expr, "the expression "+expr)
}

// bar reports which bar (the integer cycle) a boundary record's commit landed
// on, computed by the CLIENT's own cyclePosition — not by the test's arithmetic.
// That matters: a client that computed its own position wrongly would agree with
// a test that duplicated the same mistake, and the drift this issue exists to
// remove would be invisible.
func (r *syncRuntime) bar(at map[string]any) int {
	r.t.Helper()
	actual := num(r.t, at, "actualMs")
	return int(r.number("window.strudelSync.cyclePosition(" + anchorJS + ", " + trimFloat(actual) + ")"))
}

// trimFloat renders a float as a JS numeric literal.
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// fires returns the boundary records the callback has seen.
//
// The records are read back through JSON rather than a reflective export: the
// reflection path hands back zero values for these plain JS objects, which
// would make every assertion below pass or fail for the wrong reason. JSON is
// also exactly what a real client does with a frame, so the round trip is not
// an accommodation — it is the same decoding the browser performs.
func (r *syncRuntime) fires() []map[string]any {
	r.t.Helper()
	raw, err := r.vm.RunString("JSON.stringify(window.__fired)")
	if err != nil {
		r.t.Fatalf("reading fired boundaries: %v", err)
	}
	var out []map[string]any
	if s := raw.String(); s != "[]" {
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			r.t.Fatalf("decoding fired boundaries %s: %v", s, err)
		}
	}
	return out
}

// num reads a numeric field out of a boundary record.
func num(t *testing.T, at map[string]any, field string) float64 {
	t.Helper()
	v, ok := at[field].(float64)
	if !ok {
		t.Fatalf("boundary record field %q = %v (%T), want a number", field, at[field], at[field])
	}
	return v
}

// bool reads a boolean field out of a boundary record.
func boolean(t *testing.T, at map[string]any, field string) bool {
	t.Helper()
	v, ok := at[field].(bool)
	if !ok {
		t.Fatalf("boundary record field %q = %v (%T), want a bool", field, at[field], at[field])
	}
	return v
}

// cancelPending cancels the handle stored by arm.
func (r *syncRuntime) cancelPending() {
	r.t.Helper()
	r.run("if (window.__handle) { window.__handle.cancel(); }")
}

// sessionJSPrelude is the host surface session.js needs to be driven headless:
// no DOM, no strudel bundle, no network. Only the live repl and the sandbox are
// stubbed, because those are the collaborators whose behaviour is under test.
//
// Why drive session.js at all, when sync.js is where the maths lives? Because
// the decision to DEFER the commit lives in session.js, and a test that only
// exercises sync.js cannot see it. Mutation 87 (commit applied on arrival)
// survives a suite that never runs the file making that decision — which is
// exactly what the first run of that row demonstrated.
const sessionJSPrelude = syncJSPrelude + `
// The fake clock's helpers live on the window object the sync prelude built, so
// this must NOT redeclare window — doing so would silently drop __advance and
// __setNow and every timing assertion would become meaningless.
var console = { info: function () {}, warn: function () {}, error: function () {}, debug: function () {}, log: function () {} };
var document = {
  readyState: "complete",
  addEventListener: function () {},
  getElementById: function () { return null; },
  activeElement: null,
};
// The stub models the PROMISE surface production actually uses: both "then" and
// "catch". Modelling only "then" was survivable while every fetch call sat inside
// an async function, where a missing method became a silently swallowed rejection;
// it is not survivable now, because the sync report is sent from a timer callback
// where the same TypeError propagates into the caller. A harness that throws on the
// served bytes is reporting its own inaccuracy, not a defect in the client -- so
// the stub is fixed rather than the production call being weakened to suit it.
var fetch = function () { return { then: function () { return apiStub(); }, catch: function () { return apiStub(); } }; };
function apiStub() { return { then: function () { return apiStub(); }, catch: function () { return apiStub(); } }; }
// A record of every commit, so the test can assert on WHEN setPattern happened
// rather than on a console line nobody reads.
window.__setPatternCalls = [];
function makeLive() {
  return {
    setPattern: function (p, autostart) {
      window.__setPatternCalls.push({ at: Date.now(), autostart: autostart });
      return apiStub();
    },
  };
}
`

// newSessionRuntime loads the served session.js on top of the fake clock, with
// stub repls installed on window.
//
// sync.js is loaded FIRST and from its own served bytes, because that is the
// order the shell's defer scripts run in and because session.js is written to
// degrade gracefully when window.strudelSync is absent. Without sync.js loaded
// here, the immediate-commit fallback would be taken and this harness would
// report the defect that the module is missing when the real defect is that the
// harness forgot to load it — a test that fails for the wrong reason teaches
// the next reader to distrust the failure.
func newSessionRuntime(t *testing.T, sessionJS string) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, sessionJSPrelude)
	rt.run(servedSyncJS(t))
	rt.run(sessionJS)

	// boot() runs at load and bails without window.strudelInitPromise, so the
	// module exposes window.strudelSession and stops there. That is the state
	// the harness drives: no socket, no audio, no repl.
	rt.run(`window.__live = makeLive();
	  window.__sandbox = {
	    evaluate: function () { return { queryArc: function () { return []; } }; },
	    state: {},
	  };`)
	return rt
}

// settle pumps the microtask queue so an async applyVersion runs to its first
// await. goja drains promise jobs as the runtime goes idle, so a couple of
// no-op evaluations is enough — and bounded, unlike sleeping.
func (r *syncRuntime) settle() {
	r.t.Helper()
	r.run("0")
	r.run("0")
}

// commits returns the timestamps at which the live repl was hot-swapped.
func (r *syncRuntime) commits() []float64 {
	r.t.Helper()
	raw, err := r.vm.RunString("JSON.stringify(window.__setPatternCalls.map(function (c) { return c.at; }))")
	if err != nil {
		r.t.Fatalf("reading commits: %v", err)
	}
	var out []float64
	if s := raw.String(); s != "[]" {
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			r.t.Fatalf("decoding commits %s: %v", s, err)
		}
	}
	return out
}

// TestSessionDefersTheCommitToTheBoundary drives the REAL served session.js —
// not sync.js — through a full version arriving, and asserts on when the live
// repl is actually hot-swapped.
//
// This is the half that a sync.js-only suite cannot see. The arithmetic can be
// perfect while session.js declines to use it, and that is precisely mutation
// 87: reverting to live.setPattern on arrival, every listener drifting onto its
// own bar, with every test of the module itself still green.
func TestSessionDefersTheCommitToTheBoundary(t *testing.T) {
	server := New()
	req := httptest.NewRequest(http.MethodGet, "/static/session.js", nil)
	w := httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/session.js status = %d, want 200", w.Code)
	}
	sessionJS := w.Body.String()

	rt := newSessionRuntime(t, sessionJS)

	// A version arrives at 1002100, inside cycle 1, so its commit belongs on
	// the 1004000 boundary rather than on arrival.
	const frameAt = int64(1002100)
	const wantTarget = 1004000.0 + 25

	rt.run(fmt.Sprintf("window.__setNow(%d)", frameAt))
	rt.run(`window.strudelSession.applyVersion(
	  { version: 1, code: 's("bd").note("c4")', anchor: { epochMs: 1000000, cps: 0.5 } },
	  window.__sandbox, window.__live);`)
	rt.settle()

	// THE assertion: validated and reported, but NOT yet committed.
	if got := rt.commits(); len(got) != 0 {
		t.Fatalf("the live repl was hot-swapped %d time(s) on arrival (%v): the commit is not deferred to the boundary, which is the drift being fixed", len(got), got)
	}

	// And it does land, on the shared boundary, once the clock gets there.
	rt.advance(wantTarget - frameAt)
	got := rt.commits()
	if len(got) != 1 {
		t.Fatalf("committed %d time(s), want exactly 1", len(got))
	}
	if got[0] != wantTarget {
		t.Errorf("committed at %v, want the shared boundary %v", got[0], wantTarget)
	}
}

// coarse enough that millisecond-scale arrival jitter is unambiguous, and the
// epoch is a round number so an expected boundary is readable by eye. Cycle
// boundaries sit at 1000000, 1002000, 1004000, ...
const anchorJS = `{epochMs: 1000000, cps: 0.5}`

// TestTwoClientsOnTheSameAnchorCommitOnTheSameBar is the load-bearing property
// of the whole issue, and the reason this file drives a JS engine.
//
// The defect was that each client committed the moment its OWN frame arrived, so
// the case that matters is two clients on ONE anchor whose frames arrive at
// DIFFERENT times inside the same cycle: early and late must still commit on the
// same bar. Asserting only "a commit eventually happens" would pass against the
// broken code, so each client here records the exact cycle it committed on.
func TestTwoClientsOnTheSameAnchorCommitOnTheSameBar(t *testing.T) {
	js := servedSyncJS(t)

	// Same anchor, frames 40ms apart, both inside cycle 1 (1002000..1004000),
	// so a correct client defers both to the same target: that cycle's boundary
	// at 1004000, plus the 25ms lead.
	const earlyFrameAt = int64(1002100)
	const lateFrameAt = int64(1002140)
	const lead = 25.0
	const wantTarget = 1004000.0 + lead

	if earlyFrameAt == lateFrameAt {
		t.Fatal("both frames arrive at the same instant: this test no longer models the drift it exists to catch")
	}

	early := newSyncRuntime(t, js)
	late := newSyncRuntime(t, js)

	early.arm(anchorJS, earlyFrameAt, 25, nil)
	late.arm(anchorJS, lateFrameAt, 25, nil)

	// Nothing may commit on arrival: that IS the defect. Both frames are inside
	// cycle 1, whose next boundary is 1004000.
	if got := len(early.fires()); got != 0 {
		t.Errorf("the early client committed %d time(s) on arrival, want 0: the commit is not deferred to the boundary", got)
	}
	if got := len(late.fires()); got != 0 {
		t.Errorf("the late client committed %d time(s) on arrival, want 0: the commit is not deferred to the boundary", got)
	}

	// Both clients' clocks run to the same wall-clock instant, which is what
	// "two listeners" means. The cross-machine case is bounded by the drift the
	// status UI reports in .8.5, not by pretending the clocks are identical.
	early.advance(wantTarget - earlyFrameAt)
	late.advance(wantTarget - lateFrameAt)

	// THE property: 40ms apart on arrival, identical target on commit. This is
	// the assertion the whole issue turns on — the early client cannot claim the
	// late one committed on a different bar, because there is only one bar.
	for name, rt := range map[string]*syncRuntime{"early": early, "late": late} {
		fires := rt.fires()
		if len(fires) != 1 {
			t.Fatalf("%s client fired %d times, want exactly 1", name, len(fires))
		}
		if target := num(t, fires[0], "targetMs"); target != wantTarget {
			t.Errorf("%s client targeted %v, want the shared target %v: it would commit on its own bar, not the shared one", name, target, wantTarget)
		}
		if bar := rt.bar(fires[0]); bar != 2 {
			t.Errorf("%s client committed on bar %d, want bar 2 (cycle 2 spans 1004000..1006000)", name, bar)
		}
	}
}

// TestTheLeadIsWhatAlignsTheLateClient is the subtle one, and the reason the
// lead is a named constant rather than a rounding detail.
//
// The commit target is the bar line itself, so a commit scheduled exactly there
// has NO margin: any lateness at all carries it across into the next bar. Two
// clients can then receive the same frame within milliseconds of each other and
// still land a full bar apart, because one commit made it and the other did
// not. The lead buys margin: the commit is targeted LEAD ms past the line, so a
// commit that runs late by up to LEAD ms still lands in the intended bar.
//
// Both subtests use the same frame arrival and the same lateness, so the lead is
// the ONLY difference between them.
func TestTheLeadIsWhatAlignsTheLateClient(t *testing.T) {
	js := servedSyncJS(t)

	// The frame arrives 10ms before the 1002000 bar line, and the commit timer
	// runs 20ms LATE — a fifth of a second of jitter is unremarkable for a
	// browser event loop.
	const frameAt = int64(1001990)
	const lateBy = 20
	const lead = 25

	// commitBar arms a commit that runs `lateBy` ms late and returns the bar
	// (the integer cycle) the commit landed on.
	commitBar := func(t *testing.T, leadMS int64) (bar int, target float64, drift float64) {
		t.Helper()
		rt := newSyncRuntime(t, js)
		rt.set("__lateBy", lateBy)
		rt.arm(anchorJS, frameAt, leadMS, nil)
		rt.advance(500)

		fires := rt.fires()
		if len(fires) != 1 {
			t.Fatalf("lead=%d: fired %d times, want exactly 1", leadMS, len(fires))
		}
		target = num(t, fires[0], "targetMs")
		drift = num(t, fires[0], "driftMs")
		return rt.bar(fires[0]), target, drift
	}

	t.Run("with the lead the late commit stays on the intended bar", func(t *testing.T) {
		bar, target, drift := commitBar(t, lead)
		if bar != 1 {
			t.Errorf("committed on bar %d, want bar 1: a commit %dms late overran the bar line the lead was supposed to protect", bar, lateBy)
		}
		if want := 1002000.0 + lead; target != want {
			t.Errorf("target = %v, want %v (the boundary plus the lead)", target, want)
		}
		if drift < lead {
			t.Errorf("drift = %vms, want at least the %dms lead: the commit ran closer to the line than the lead allows", drift, lead)
		}
	})

	// The counter-case, asserted so the subtest above cannot pass vacuously:
	// with no lead the commit is targeted AT the bar line, so the two clients'
	// commits are separated by the lateness alone and can straddle the line.
	//
	// The lateness here is deliberately LARGE relative to the bar: at cps 0.5 a
	// bar is 2000ms, so a 20ms overrun cannot cross it and the honest claim is
	// only about the target, not the resulting bar. Making the jitter comparable
	// to the bar is what makes the counter-case physically possible.
	t.Run("without the lead the commit is targeted at the bar line itself", func(t *testing.T) {
		rt := newSyncRuntime(t, js)
		rt.set("__lateBy", 20)
		rt.arm(anchorJS, frameAt, 0, nil)
		rt.advance(500)

		fires := rt.fires()
		if len(fires) != 1 {
			t.Fatalf("fired %d times, want 1", len(fires))
		}
		if target := num(t, fires[0], "targetMs"); target != 1002000.0 {
			t.Errorf("target = %v, want the raw boundary 1002000: the counter-case is not exercising what it claims", target)
		}
	})
}

// TestANewerVersionSupersedesAPendingCommit pins the cancellation half.
//
// Deferring the commit introduces a hazard the immediate commit did not have: a
// pattern can now be sitting in a timer, superseded. If it is not cancelled it
// lands AFTER the version that replaced it, and the client ends up playing stale
// code with a NEWER version number on screen — the most confusing possible
// failure, because every version-stamped surface then disagrees with the audio.
func TestANewerVersionSupersedesAPendingCommit(t *testing.T) {
	js := servedSyncJS(t)
	rt := newSyncRuntime(t, js)

	rt.arm(anchorJS, 1002100, 25, nil)
	if got := len(rt.fires()); got != 0 {
		t.Fatalf("committed %d time(s) before the boundary", got)
	}

	rt.cancelPending()
	rt.advance(5000)

	if got := len(rt.fires()); got != 0 {
		t.Errorf("a cancelled commit still fired %d time(s): a superseded pattern would land after the version that replaced it", got)
	}
}

// TestAnEarlyTimerDoesNotCommitEarly is the self-correcting re-check, and it is
// a SEPARATE property from the lead. Without the re-check a timer that fires a
// millisecond early commits a millisecond before the bar line — the exact
// off-by-one-bar defect this module exists to remove, reintroduced through the
// scheduler instead of through the arithmetic.
//
// Both halves are asserted, because either alone is satisfiable by a broken
// implementation: firing exactly once could also mean it fired early and gave
// up, and never firing could mean it waited forever.
func TestAnEarlyTimerDoesNotCommitEarly(t *testing.T) {
	js := servedSyncJS(t)
	rt := newSyncRuntime(t, js)

	// The next timer fires 30ms before its deadline, so the first tick lands at
	// 1001995 — five milliseconds BEFORE the 1002000 bar line.
	rt.set("__earlyBy", 30)
	rt.arm(anchorJS, 1001990, 0, nil)

	// Advance only to just past that early firing. The re-check must re-arm
	// rather than commit, so nothing has fired yet.
	rt.advance(5)
	if got := len(rt.fires()); got != 0 {
		t.Fatalf("committed %d time(s) on an early timer tick, want 0: the commit landed before the bar line", got)
	}

	// Now run past the real target. It must fire exactly once, and not before
	// the line.
	rt.advance(200)
	fires := rt.fires()
	if len(fires) != 1 {
		t.Fatalf("fired %d times, want exactly 1: an early tick must re-arm once, not spin or commit twice", len(fires))
	}
	actual := num(t, fires[0], "actualMs")
	if actual < 1002000 {
		t.Errorf("committed at %v, which is before the 1002000 bar line", actual)
	}
}

// TestAnUnusableAnchorCommitsImmediatelyRatherThanNever covers the degenerate
// inputs. A snapshot with no anchor (or a nonsense one) still has to make sound:
// the live pattern must not be left stranded because the maths produced NaN and
// a timer was armed at NaN, which in a browser means firing immediately at best
// and never at worst.
//
// It also proves the drift is reported HONESTLY: an unscheduled commit is marked
// unscheduled rather than recorded as an aligned one, so the status UI in .8.5
// cannot display a clean sync state for a client that never aligned.
func TestAnUnusableAnchorCommitsImmediatelyRatherThanNever(t *testing.T) {
	js := servedSyncJS(t)

	for _, tc := range []struct {
		name   string
		anchor string
	}{
		{"missing anchor", `null`},
		{"zero rate", `{epochMs: 1000000, cps: 0}`},
		{"negative rate", `{epochMs: 1000000, cps: -0.5}`},
		{"missing rate", `{epochMs: 1000000}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newSyncRuntime(t, js)
			rt.arm(tc.anchor, 1002100, 25, nil)

			// Immediately, with no clock advance at all.
			fires := rt.fires()
			if len(fires) != 1 {
				t.Fatalf("fired %d times on an unusable anchor, want 1: the commit must not be stranded behind a NaN boundary", len(fires))
			}
			if !boolean(t, fires[0], "unscheduled") {
				t.Error("the commit was not marked unscheduled: the status UI would report an aligned commit that never happened")
			}
		})
	}
}

// flexShorthandHasShrink reports whether a rule body sets a non-zero shrink
// factor, accepting either the longhand (flex-shrink: N) or the `flex` shorthand
// (flex: <grow> <shrink> <basis>). It looks for the SHORTHAND only as a
// three-number form, because the keyword shorthands (auto, none, initial) carry
// no explicit shrink and must not be mistaken for one.
func flexShorthandHasShrink(rule string) bool {
	for _, line := range strings.Split(rule, ";") {
		decl := strings.TrimSpace(line)
		if !strings.HasPrefix(decl, "flex:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(decl, "flex:"))
		if len(fields) != 3 {
			continue
		}
		if _, err := strconv.ParseFloat(fields[1], 64); err == nil {
			return fields[1] != "0"
		}
	}
	return false
}

// cssRuleBody returns the declaration block of the first rule whose selector is
// exactly selector, or "" if there is none. It lets a test assert a PROPERTY of
// one rule (does it shrink? does it collapse when empty?) instead of grepping
// the whole stylesheet for one declaration string, which cannot tell a working
// rule from a working comment.
func cssRuleBody(css, selector string) string {
	idx := strings.Index(css, selector+" {")
	if idx < 0 {
		return ""
	}
	rest := css[idx+len(selector)+2:]
	end := strings.Index(rest, "}")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// servedSyncJS fetches /static/sync.js through the REAL handler tree and fails
// the test if it is not served intact. Every client assertion below runs against
// these bytes rather than against the file on disk, so a sync.js that is not
// actually served cannot pass a test that only ever read the source.
func servedSyncJS(t *testing.T) string {
	t.Helper()
	server := New()
	req := httptest.NewRequest(http.MethodGet, "/static/sync.js", nil)
	w := httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/sync.js status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET /static/sync.js served as JSON (%q): the API layer swallowed a static asset", ct)
	}
	js := w.Body.String()
	if len(js) == 0 {
		t.Fatal("GET /static/sync.js returned an empty body")
	}
	return js
}

// TestSyncModuleIsServedAndCarriesTheContract pins the WIRING: the module is
// reachable, and session.js actually drives it. A sync.js that is never loaded
// would leave every client committing mid-cycle again while every test of the
// module's own maths still passed — the module would be perfect and inert.
func TestSyncModuleIsServedAndCarriesTheContract(t *testing.T) {
	js := servedSyncJS(t)

	for _, want := range []string{
		"window.strudelSync", // the global session.js reaches for
		"function cyclePosition(",
		"function nextCycleBoundary(",
		"function scheduleAtBoundary(",
		"LEAD_MS",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("sync.js is missing load-bearing marker %q", want)
		}
	}

	// The shell must load it, or none of the above runs in a browser.
	server := New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	server.HandleRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `<script src="/static/sync.js"`) {
		t.Error(`shell is missing <script src="/static/sync.js": the cycle-aligned commit module is never loaded`)
	}
	shellBody := w.Body.String()
	if !strings.Contains(shellBody, `id="sync-status"`) || !strings.Contains(shellBody, `class="sync-status"`) || !strings.Contains(shellBody, `aria-live="polite"`) {
		t.Error(`shell is missing #sync-status region with class="sync-status" and aria-live="polite"`)
	}

	// CSS must carry .sync-status and .sync-drift styles.
	req = httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	w = httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css status = %d, want 200", w.Code)
	}
	cssBody := w.Body.String()
	for _, wantClass := range []string{".sync-status", ".sync-drift {"} {
		if !strings.Contains(cssBody, wantClass) {
			t.Errorf("style.css is missing required class %q", wantClass)
		}
	}
	// The status line shares the agent panel's flex row with the message and the
	// buttons, so it must be the thing that gives way when space is tight.
	// Asserted as a PROPERTY (shrinkable, and allowed to reach zero width)
	// rather than as one literal declaration: pinning "flex: 0 100 auto" would
	// fail on an equivalent rewrite while passing on a rule that does nothing,
	// which is trivia, not coverage.
	//
	// Either spelling counts. The `flex` SHORTHAND carries shrink as its middle
	// component (flex: <grow> <shrink> <basis>), so demanding the longhand
	// `flex-shrink` would reject a correct rule written the short way.
	syncRule := cssRuleBody(cssBody, ".sync-status")
	if syncRule == "" {
		t.Fatal("style.css has no .sync-status rule body to inspect")
	}
	setsShrink := strings.Contains(syncRule, "flex-shrink") || flexShorthandHasShrink(syncRule)
	if !setsShrink {
		t.Errorf(".sync-status rule does not set a flex shrink factor, so it cannot yield space to the message: %q", syncRule)
	}
	if !strings.Contains(syncRule, "min-width: 0") && !strings.Contains(syncRule, "min-width:0") {
		t.Errorf(".sync-status rule does not set min-width: 0, so it cannot shrink below its content: %q", syncRule)
	}

	// session.js must route its commit through the module, drive the status UI,
	// and must NOT have quietly reverted to committing the instant a frame lands.
	req = httptest.NewRequest(http.MethodGet, "/static/session.js", nil)
	w = httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/session.js status = %d, want 200", w.Code)
	}
	session := w.Body.String()
	for _, want := range []string{
		"window.strudelSync",
		"scheduleAtBoundary",
		"cyclePosition",
		"pendingCommit.cancel()",
		"updateSyncStatusUI",
		"sync-status",
		"sync-drift",
	} {
		if !strings.Contains(session, want) {
			t.Errorf("session.js is missing cycle-alignment / status marker %q", want)
		}
	}

	// The safety invariant from .3vo.5 still holds: a deferral must not have
	// grown a second path that evaluates unvalidated code.
	if strings.Contains(session, "live.evaluate(") {
		t.Error("session.js calls live.evaluate(): unvalidated code would hush the live repl before parsing")
	}
}

// TestSyncStatusUIRendersDriftAndStateInGoja executes the served session.js in
// goja to prove that connection state, current bar/cycle, pending countdown,
// unscheduled state, and observed drift values are actually written to #sync-status.
func TestSyncStatusUIRendersDriftAndStateInGoja(t *testing.T) {
	server := New()
	req := httptest.NewRequest(http.MethodGet, "/static/session.js", nil)
	w := httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/session.js status = %d, want 200", w.Code)
	}
	sessionJS := w.Body.String()

	rt := newSessionRuntime(t, sessionJS)

	// Install a fake #sync-status DOM element.
	rt.run(`
	  var syncStatusElem = { innerHTML: "" };
	  document.getElementById = function (id) {
	    if (id === "sync-status") { return syncStatusElem; }
	    return null;
	  };
	`)

	// 1. Initial connection state write.
	rt.run("window.strudelSession.updateSyncStatusUI()")
	html := rt.eval("syncStatusElem.innerHTML").String()
	if !strings.Contains(html, "reconnecting") {
		t.Errorf("initial sync status HTML = %q, want it to include 'reconnecting'", html)
	}

	// 2. Pending commit and current cycle/bar.
	const frameAt = int64(1002100)
	rt.run(fmt.Sprintf("window.__setNow(%d)", frameAt))
	rt.run(`window.strudelSession.applyVersion(
	  { version: 1, code: 's("bd")', anchor: { epochMs: 1000000, cps: 0.5 } },
	  window.__sandbox, window.__live);`)
	rt.settle()

	html = rt.eval("syncStatusElem.innerHTML").String()
	if !strings.Contains(html, "cycle 1.1") && !strings.Contains(html, "bar 1") {
		t.Errorf("pending sync status HTML = %q, want cycle/bar info", html)
	}
	if !strings.Contains(html, "next in") && !strings.Contains(html, "1925") {
		t.Errorf("pending sync status HTML = %q, want pending countdown", html)
	}

	// 3. Boundary tick fires and writes observed drift.
	rt.advance(1925)
	html = rt.eval("syncStatusElem.innerHTML").String()
	if !strings.Contains(html, `class="sync-drift"`) {
		t.Errorf("committed sync status HTML = %q, want class=\"sync-drift\"", html)
	}

	// 4. Assert exact drift VALUE reaches the status region.
	rt.run(`window.strudelSync.observe({
	  version: 2,
	  cycle: 2.0,
	  targetMs: 1004000,
	  actualMs: 1004024,
	  driftMs: 24
	});`)
	rt.run("window.strudelSession.updateSyncStatusUI()")

	html = rt.eval("syncStatusElem.innerHTML").String()
	if !strings.Contains(html, "24ms") {
		t.Errorf("sync status HTML = %q, want drift value '24ms' to reach the region", html)
	}
	if !strings.Contains(html, "last bar 2") {
		t.Errorf("sync status HTML = %q, want last landed bar 'last bar 2'", html)
	}

	// 5. Unscheduled observation (invalid anchor).
	rt.run(`window.strudelSession.scheduleCommit(function(){}, null, 3, {}, null);`)
	html = rt.eval("syncStatusElem.innerHTML").String()
	if !strings.Contains(html, "unscheduled") {
		t.Errorf("unscheduled sync status HTML = %q, want 'unscheduled'", html)
	}
}

// TestSyncStatusUIOnlyWritesOnChangeAndStopsOnPageHide proves the two properties
// that keep the status region well-behaved rather than merely present:
//
//  1. It writes ONLY when the rendered text actually changes. The region is
//     aria-live="polite" and the countdown moves several times a second, so an
//     unconditional write would announce every tick — the feature would work
//     and still be unusable with a screen reader.
//  2. The interval is torn down on pagehide. Otherwise the timer keeps running
//     against a frozen clock in the back/forward cache, and stopStatusTimer is
//     dead code with no caller.
//
// Both are asserted by EXECUTING the served bytes and watching the fake DOM, so
// a session.js that renders nothing but still satisfies a source grep is caught.
func TestSyncStatusUIOnlyWritesOnChangeAndStopsOnPageHide(t *testing.T) {
	server := New()
	req := httptest.NewRequest(http.MethodGet, "/static/session.js", nil)
	w := httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/session.js status = %d, want 200", w.Code)
	}

	rt := newSessionRuntime(t, w.Body.String())

	// A fake element that COUNTS its writes, which is the whole point: innerHTML
	// being reassigned to an identical string is a mutation an aria-live region
	// still announces.
	rt.run(`
	  var __writes = 0;
	  var syncStatusElem = {
	    _html: "",
	    get innerHTML() { return this._html; },
	    set innerHTML(v) { __writes++; this._html = v; }
	  };
	  window.__writes = function () { return __writes; };
	  document.getElementById = function (id) {
	    return id === "sync-status" ? syncStatusElem : null;
	  };
	`)

	// An anchor, so the region has a clock-dependent countdown to render.
	rt.run("window.__setNow(1000000)")
	rt.run(`window.strudelSession.scheduleCommit(function () {}, { epochMs: 1000000, cps: 0.5 }, 1, {}, null);`)
	rt.settle()

	rt.run("window.strudelSession.updateSyncStatusUI()")
	first := rt.eval("window.__writes()").ToInteger()
	if html := rt.eval("syncStatusElem.innerHTML").String(); html == "" {
		t.Fatal("updateSyncStatusUI rendered nothing: the region is present but never written")
	}

	// Same clock, same state: an identical write must be suppressed.
	rt.run("window.strudelSession.updateSyncStatusUI()")
	rt.run("window.strudelSession.updateSyncStatusUI()")
	if got := rt.eval("window.__writes()").ToInteger(); got != first {
		t.Errorf("writes after 3 renders of unchanged state = %d, want %d: an identical write still mutates the aria-live region", got, first)
	}

	// Moving the clock changes the countdown, so the write MUST happen now.
	rt.advance(300)
	rt.run("window.strudelSession.updateSyncStatusUI()")
	if got := rt.eval("window.__writes()").ToInteger(); got <= first {
		t.Errorf("writes after the clock moved = %d, want MORE than %d: suppressing the write suppressed a real change too", got, first)
	}

	// pagehide must stop the interval, or it runs forever against a frozen clock.
	rt.run("window.strudelSession.startStatusTimer()")
	before := rt.eval("window.__pendingTimers()").ToInteger()
	if before == 0 {
		t.Fatal("no timer pending after startStatusTimer: the interval is not running, so teardown proves nothing")
	}
	rt.run("window.__fireEvent('pagehide')")
	if got := rt.eval("window.__pendingTimers()").ToInteger(); got >= before {
		t.Errorf("pending timers after pagehide = %v, want fewer than %v: the status interval outlived the page", got, before)
	}

	// And it must stay stopped: advancing a long time re-arms nothing.
	settled := rt.eval("window.__pendingTimers()").ToInteger()
	rt.advance(5000)
	if got := rt.eval("window.__pendingTimers()").ToInteger(); got > settled {
		t.Errorf("pending timers after advancing 5s post-pagehide = %v, want no more than %v: the interval re-armed after teardown", got, settled)
	}
}
