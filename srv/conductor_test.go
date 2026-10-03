package srv

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestConductorBumpVersion covers the monotonic version counter: a fresh
// conductor is at version 0 with no code, and every Publish advances the
// version by exactly one.
func TestConductorBumpVersion(t *testing.T) {
	c := NewConductor(3)

	snap := c.Snapshot()
	if snap.Version != 0 {
		t.Fatalf("fresh conductor version = %d, want 0", snap.Version)
	}
	if snap.Code != "" {
		t.Fatalf("fresh conductor code = %q, want empty", snap.Code)
	}

	for want := int64(1); want <= 5; want++ {
		got := c.Publish("code v"+strconv.FormatInt(want, 10), "msg "+strconv.FormatInt(want, 10))
		if got.Version != want {
			t.Fatalf("Publish #%d returned version %d, want %d", want, got.Version, want)
		}
		snap := c.Snapshot()
		if snap.Version != want {
			t.Fatalf("after %d publishes, Snapshot version = %d, want %d", want, snap.Version, want)
		}
	}
}

// TestConductorHistoryBounding covers the bounded ring buffer: history never
// exceeds the limit and the oldest entries are the ones that drop.
func TestConductorHistoryBounding(t *testing.T) {
	const limit = 4
	c := NewConductor(limit)

	if got := c.Snapshot().History; len(got) != 0 {
		t.Fatalf("fresh history len = %d, want 0", len(got))
	}

	// Fill exactly to the cap: order must be oldest -> newest.
	for i := 1; i <= limit; i++ {
		c.Publish("code-"+strconv.FormatInt(int64(i), 10), "msg-"+strconv.FormatInt(int64(i), 10))
	}
	hist := c.Snapshot().History
	if len(hist) != limit {
		t.Fatalf("history len after %d publishes = %d, want %d", limit, len(hist), limit)
	}
	for i, v := range hist {
		wantVersion := int64(i + 1)
		if v.Version != wantVersion {
			t.Fatalf("history[%d].Version = %d, want %d", i, v.Version, wantVersion)
		}
		if v.Code != "code-"+strconv.FormatInt(wantVersion, 10) {
			t.Fatalf("history[%d].Code = %q, want %q", i, v.Code, "code-"+strconv.FormatInt(wantVersion, 10))
		}
	}

	// Overflow well past the cap: still capped, and only the newest survive.
	for i := limit + 1; i <= limit+10; i++ {
		c.Publish("code-"+strconv.FormatInt(int64(i), 10), "msg-"+strconv.FormatInt(int64(i), 10))
	}
	hist = c.Snapshot().History
	if len(hist) != limit {
		t.Fatalf("history len after overflow = %d, want %d", len(hist), limit)
	}
	newest := int64(limit + 10)
	oldestKept := newest - limit + 1
	for i, v := range hist {
		wantVersion := oldestKept + int64(i)
		if v.Version != wantVersion {
			t.Fatalf("after overflow history[%d].Version = %d, want %d (full: %+v)", i, v.Version, wantVersion, hist)
		}
	}
	if hist[len(hist)-1].Version != newest {
		t.Fatalf("last history entry = %d, want newest %d", hist[len(hist)-1].Version, newest)
	}

	// A non-positive limit falls back to the default rather than unbounded.
	d := NewConductor(0)
	for i := 0; i < DefaultHistoryLimit+5; i++ {
		d.Publish("x"+strconv.FormatInt(int64(i), 10), "")
	}
	if got := len(d.Snapshot().History); got != DefaultHistoryLimit {
		t.Fatalf("default-limit history len = %d, want %d", got, DefaultHistoryLimit)
	}
}

// TestConductorSnapshotImmutability pins down that callers cannot mutate
// Conductor internals through a Snapshot: the history slice must be copied, not
// aliased, and scalar fields must be values.
//
// Expectations are derived from the known published values rather than from a
// second snapshot, so this test still catches aliasing (two aliasing snapshots
// would share their corruption and hide it).
func TestConductorSnapshotImmutability(t *testing.T) {
	const limit = 4
	c := NewConductor(limit)
	for i := 1; i <= 6; i++ {
		c.Publish("code-"+strconv.FormatInt(int64(i), 10), "msg-"+strconv.FormatInt(int64(i), 10))
	}
	// With limit=4 and 6 publishes, versions 3..6 are still retained.
	wantCodes := []string{"code-3", "code-4", "code-5", "code-6"}

	first := c.Snapshot()
	if len(first.History) != len(wantCodes) {
		t.Fatalf("history len = %d, want %d", len(first.History), len(wantCodes))
	}

	// Vandalise every field of the snapshot, including the history backing
	// array and (by appending) its spare capacity.
	first.Version = 9999
	first.Code = "HACKED"
	first.LastAgentMessage = "HACKED"
	first.Anchor.CPS = 99
	first.Anchor.EpochMS = 1
	for i := range first.History {
		first.History[i].Code = "HACKED"
		first.History[i].Version = -1
		first.History[i].Message = "HACKED"
		first.History[i].EpochMS = -1
	}
	first.History = append(first.History, Version{Version: 12345, Code: "EXTRA"})

	// A fresh snapshot must be exactly what the Conductor was told to hold.
	after := c.Snapshot()
	if after.Version != 6 {
		t.Errorf("after caller mutation, Version = %d, want 6", after.Version)
	}
	if after.Code != "code-6" {
		t.Errorf("after caller mutation, Code = %q, want %q", after.Code, "code-6")
	}
	if after.LastAgentMessage != "msg-6" {
		t.Errorf("after caller mutation, LastAgentMessage = %q, want %q", after.LastAgentMessage, "msg-6")
	}
	if after.Anchor.CPS != DefaultCPS {
		t.Errorf("after caller mutation, Anchor.CPS = %v, want %v", after.Anchor.CPS, DefaultCPS)
	}
	if after.Anchor.EpochMS == 1 {
		t.Error("after caller mutation, Anchor.EpochMS was overwritten through the snapshot")
	}
	if len(after.History) != len(wantCodes) {
		t.Fatalf("history len changed after caller append: got %d, want %d", len(after.History), len(wantCodes))
	}
	for i, want := range wantCodes {
		got := after.History[i]
		if got.Version != int64(i+3) || got.Code != want ||
			got.Message != "msg-"+strconv.FormatInt(got.Version, 10) || got.EpochMS <= 0 {
			t.Errorf("history[%d] corrupted through snapshot: %+v (want code %q, version %d)",
				i, got, want, i+3)
		}
	}
}

// TestConductorConcurrentAccess hammers the Conductor from many goroutines and
// must be clean under `go test -race`. Writers publish while readers snapshot,
// mirroring HTTP handlers and the WebSocket hub touching one Conductor.
func TestConductorConcurrentAccess(t *testing.T) {
	c := NewConductor(8)

	const (
		writers        = 8
		readers        = 8
		publishesPerGo = 200
		snapshotsPerGo = 200
	)
	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < publishesPerGo; i++ {
				c.Publish("w"+strconv.FormatInt(int64(w), 10)+"-"+strconv.FormatInt(int64(i), 10), "msg")
				if i%7 == 0 {
					c.SetMessage("narration " + strconv.FormatInt(int64(i), 10))
				}
				if i%11 == 0 {
					c.SetAnchor(1700000000000+int64(i), 0.5+float64(w)*0.1)
				}
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < snapshotsPerGo; i++ {
				snap := c.Snapshot()
				// Touch the copied history so a racy copy would be caught.
				for j := range snap.History {
					_ = snap.History[j].Code
				}
				if snap.Version < 0 {
					t.Errorf("negative version %d", snap.Version)
				}
			}
		}()
	}
	wg.Wait()

	want := int64(writers * publishesPerGo)
	if got := c.Snapshot().Version; got != want {
		t.Fatalf("version after concurrent publishes = %d, want %d (lost updates)", got, want)
	}
	if got := len(c.Snapshot().History); got != 8 {
		t.Fatalf("history len after concurrent publishes = %d, want 8", got)
	}
}

// TestConductorAnchor covers the timeline anchor: it is established at
// construction, survives publishes unchanged, and can be republished (which
// issue .8 needs to align every client onto one shared clock).
func TestConductorAnchor(t *testing.T) {
	before := time.Now().UnixMilli()
	c := NewConductor(2)
	anchor := c.Snapshot().Anchor

	if anchor.EpochMS < before || anchor.EpochMS > time.Now().UnixMilli() {
		t.Errorf("fresh anchor EpochMS = %d, want within [%d, now]", anchor.EpochMS, before)
	}
	if anchor.CPS != DefaultCPS {
		t.Errorf("fresh anchor CPS = %v, want %v", anchor.CPS, DefaultCPS)
	}

	// Publishing code must not move the anchor.
	c.Publish("a", "m")
	if got := c.Snapshot().Anchor; got != anchor {
		t.Errorf("anchor changed on publish: got %+v, want %+v", got, anchor)
	}

	// SetAnchor republishes the timeline without bumping the version. The epoch
	// is near "now" because SetAnchor now REFUSES a skewed one: an anchor is the
	// one number every client maps its scheduler position onto, so an epoch from
	// another machine's badly-skewed clock would put the shared bar boundary
	// somewhere no listener can reach.
	now := time.Now().UnixMilli()
	if err := c.SetAnchor(now, 1.5); err != nil {
		t.Fatalf("SetAnchor(now, 1.5) = %v, want nil", err)
	}
	got := c.Snapshot()
	if got.Anchor.EpochMS != now || got.Anchor.CPS != 1.5 {
		t.Errorf("SetAnchor not applied: got %+v", got.Anchor)
	}
	if got.Version != 1 {
		t.Errorf("SetAnchor bumped version to %d, want 1", got.Version)
	}
}

// TestConductorSetAnchorRejectsNonsense pins the anchor guard, and the two
// properties that make it more than a version bump in disguise.
//
// The epoch used to be arbitrary (a fixed constant far in the past). It is now
// bounded, which is the point: an anchor is a shared absolute timeline, so an
// epoch from a badly-skewed clock is not a harmless value, it is a bar grid no
// listener can find itself on.
func TestConductorSetAnchorRejectsNonsense(t *testing.T) {
	now := time.Now().UnixMilli()

	for _, tc := range []struct {
		name    string
		epochMS int64
		cps     float64
	}{
		{"zero rate", now, 0},
		{"negative rate", now, -0.5},
		{"absurd rate", now, AnchorMaxCPS * 1000},
		{"epoch far in the past", now - AnchorMaxSkewMS - 60_000, DefaultCPS},
		{"epoch far in the future", now + AnchorMaxSkewMS + 60_000, DefaultCPS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewConductor(0)
			c.Publish("a", "")
			before := c.Snapshot().Anchor

			err := c.SetAnchor(tc.epochMS, tc.cps)
			if err == nil {
				t.Fatalf("SetAnchor(%d, %v) = nil, want a refusal", tc.epochMS, tc.cps)
			}
			if !errors.Is(err, ErrInvalidAnchor) {
				t.Errorf("SetAnchor error = %v, want it to wrap ErrInvalidAnchor", err)
			}
			// A refused re-anchor must not half-apply. If it stored the
			// timeline anyway, listeners would be told to adopt a bar grid the
			// server itself does not believe.
			if got := c.Snapshot().Anchor; got != before {
				t.Errorf("refused SetAnchor still changed the stored anchor: got %+v, want %+v", got, before)
			}
			if got := c.Snapshot().Version; got != 1 {
				t.Errorf("refused SetAnchor changed the version to %d, want 1", got)
			}
		})
	}
}

// TestConductorSetAnchorAcceptsTheEdges documents where the guard stops
// rejecting: exactly at the rate ceiling and exactly at the skew limit is
// accepted, one step beyond is not. Without this, a future edit could tighten
// the bound by accident and quietly refuse a legitimate tempo.
func TestConductorSetAnchorAcceptsTheEdges(t *testing.T) {
	now := time.Now().UnixMilli()

	for _, tc := range []struct {
		name    string
		epochMS int64
		cps     float64
	}{
		{"rate exactly at the ceiling", now, AnchorMaxCPS},
		{"skew exactly at the limit, past", now - AnchorMaxSkewMS, DefaultCPS},
		{"skew exactly at the limit, future", now + AnchorMaxSkewMS, DefaultCPS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewConductor(0)
			if err := c.SetAnchor(tc.epochMS, tc.cps); err != nil {
				t.Errorf("SetAnchor(%d, %v) = %v, want nil: the bound must include its own edge", tc.epochMS, tc.cps, err)
			}
		})
	}
}

// TestConductorLastAgentMessage covers narration semantics: the conductor
// remembers the most recent agent message, a publish without a message does not
// wipe it, and SetMessage can narrate without publishing code.
func TestConductorLastAgentMessage(t *testing.T) {
	c := NewConductor(2)

	if got := c.Snapshot().LastAgentMessage; got != "" {
		t.Fatalf("fresh LastAgentMessage = %q, want empty", got)
	}

	c.Publish("a", "first thought")
	if got := c.Snapshot().LastAgentMessage; got != "first thought" {
		t.Fatalf("LastAgentMessage = %q, want %q", got, "first thought")
	}

	// A publish with no narration keeps the previous message (history still
	// records the empty message for that version).
	c.Publish("b", "")
	snap := c.Snapshot()
	if snap.LastAgentMessage != "first thought" {
		t.Fatalf("empty-message publish wiped narration: got %q", snap.LastAgentMessage)
	}
	if last := snap.History[len(snap.History)-1]; last.Message != "" {
		t.Fatalf("history entry message = %q, want empty", last.Message)
	}

	// Narration without a code change: version untouched, message updated.
	before := c.Snapshot()
	c.SetMessage("just talking")
	snap = c.Snapshot()
	if snap.LastAgentMessage != "just talking" {
		t.Fatalf("SetMessage not applied: got %q", snap.LastAgentMessage)
	}
	if snap.Version != before.Version {
		t.Fatalf("SetMessage bumped version to %d, want %d", snap.Version, before.Version)
	}
	if len(snap.History) != len(before.History) {
		t.Fatalf("SetMessage changed history len: got %d, want %d", len(snap.History), len(before.History))
	}
	if snap.Code != before.Code {
		t.Fatalf("SetMessage changed code: got %q, want %q", snap.Code, before.Code)
	}
}

// TestConductorHistoryRingWrapLongRun checks ring bookkeeping over many wraps:
// after far more publishes than the limit, the retained window is exactly the
// newest entries, in oldest-to-newest order, with no stale slotted entries
// leaking in.
func TestConductorHistoryRingWrapLongRun(t *testing.T) {
	const limit = 3
	const total = 50
	c := NewConductor(limit)
	for i := 1; i <= total; i++ {
		c.Publish("code-"+strconv.FormatInt(int64(i), 10), "msg-"+strconv.FormatInt(int64(i), 10))
	}

	hist := c.Snapshot().History
	if len(hist) != limit {
		t.Fatalf("history len = %d, want %d", len(hist), limit)
	}
	for i, v := range hist {
		wantVersion := int64(total - limit + 1 + i)
		if v.Version != wantVersion {
			t.Errorf("history[%d].Version = %d, want %d", i, v.Version, wantVersion)
		}
		if v.Code != "code-"+strconv.FormatInt(wantVersion, 10) {
			t.Errorf("history[%d].Code = %q, want %q", i, v.Code, "code-"+strconv.FormatInt(wantVersion, 10))
		}
		if v.Message != "msg-"+strconv.FormatInt(wantVersion, 10) {
			t.Errorf("history[%d].Message = %q, want %q", i, v.Message, "msg-"+strconv.FormatInt(wantVersion, 10))
		}
		if v.EpochMS <= 0 {
			t.Errorf("history[%d].EpochMS = %d, want > 0", i, v.EpochMS)
		}
	}
	// The live document must agree with the newest history entry.
	snap := c.Snapshot()
	newest := hist[len(hist)-1]
	if snap.Code != newest.Code || snap.Version != newest.Version {
		t.Errorf("live state %d/%q disagrees with newest history %d/%q",
			snap.Version, snap.Code, newest.Version, newest.Code)
	}
}

// TestConductorSetListenerCountClampsAndLeavesTheVersionAlone covers the
// Conductor half of issue .3.5, which the hub's count hook depends on.
//
// The clamp is the point that matters most: the hub can only ever report
// len(subs), so a negative count cannot come from a correct hub — it would mean
// some other caller passed nonsense, and publishing it would put an impossible
// number ("-1 listeners") into the snapshot every listener and the agent read.
// Clamping to 0 means the worst case is a stale 0 rather than a lie.
//
// The version must not move. Listeners arriving and leaving is not a change to
// the performance: a version bump would add a connect to the code history and
// make every browser reconnect look like a new document to anything keyed on the
// version.
func TestConductorSetListenerCountClampsAndLeavesTheVersionAlone(t *testing.T) {
	c := NewConductor(4)

	if got := c.Snapshot().ListenerCount; got != 0 {
		t.Fatalf("a fresh Conductor reports listenerCount=%d, want 0", got)
	}

	for _, n := range []int{0, 1, 3, 7, 0} {
		c.SetListenerCount(n)
		if got := c.Snapshot().ListenerCount; got != n {
			t.Fatalf("after SetListenerCount(%d) the snapshot reports %d", n, got)
		}
	}

	// Negative inputs clamp rather than serialise: a listener count claiming
	// fewer than zero listeners is never true, and the wire format has no way
	// to express "unknown".
	for _, n := range []int{-1, -7} {
		c.SetListenerCount(n)
		if got := c.Snapshot().ListenerCount; got != 0 {
			t.Errorf("SetListenerCount(%d) published %d, want 0: a negative listener count must be clamped", n, got)
		}
	}

	// The clamp must survive being the LAST thing that happened, so it is
	// re-asserted after the loop rather than trusted from inside it.
	if got := c.Snapshot().ListenerCount; got != 0 {
		t.Errorf("final listenerCount = %d, want 0", got)
	}
}

// TestConductorListenerCountDoesNotBumpVersion pins that publishing a count is
// not a change to the performance.
//
// This is a separate assertion from the one above because the failure it catches
// is invisible in a snapshot comparison: a bumped version would still serialise
// the listener count correctly, so only the version and the history reveal it.
func TestConductorListenerCountDoesNotBumpVersion(t *testing.T) {
	c := NewConductor(4)
	c.Publish(`s("bd*4")`, "kick")

	before := c.Snapshot()
	c.SetListenerCount(5)
	after := c.Snapshot()

	if after.Version != before.Version {
		t.Errorf("version moved from %d to %d across SetListenerCount: a listener count is not a change to the performance", before.Version, after.Version)
	}
	if len(after.History) != len(before.History) {
		t.Errorf("history length moved from %d to %d across SetListenerCount: connecting listeners must not enter the code history", len(before.History), len(after.History))
	}
	if after.ListenerCount != 5 {
		t.Errorf("listenerCount = %d, want 5", after.ListenerCount)
	}
}

// TestConductorListenerCountIsSafeUnderConcurrentPublishesAndCounts exercises
// the real call pattern under -race: the hub's count hook writes the listener
// count from the hub goroutine while agent writes read and rewrite everything
// else, and Snapshot reads the result.
//
// It is here because that is the one way the new wiring could be unsafe. The
// hook runs on the hub goroutine and takes the Conductor's write lock; if any
// Conductor method held that lock while calling back into the Hub, this would
// deadlock rather than merely race. A watchdog bounds the whole thing so a
// deadlock fails the test with a message instead of hanging the run.
func TestConductorListenerCountIsSafeUnderConcurrentPublishesAndCounts(t *testing.T) {
	c := NewConductor(8)

	const (
		counters    = 4
		publishers  = 4
		iterations  = 200
		watchdogFor = 20 * time.Second
	)

	var wg sync.WaitGroup

	for i := 0; i < counters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				c.SetListenerCount(id*iterations + j)
			}
		}(i)
	}

	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				c.Publish("code-"+strconv.Itoa(id)+"-"+strconv.Itoa(j), "msg")
				c.SetMessage("msg-" + strconv.Itoa(j))
				c.SetPlaying(j%2 == 0)
			}
		}(i)
	}

	// A reader, so the count is being read under contention too rather than only
	// written.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iterations*2; j++ {
			snap := c.Snapshot()
			if snap.ListenerCount < 0 {
				t.Errorf("Snapshot().ListenerCount = %d, want >= 0 under concurrent writes", snap.ListenerCount)
				return
			}
		}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(watchdogFor):
		t.Fatalf("concurrent publishes and count publishes did not finish within %s: the count hook and the Conductor lock are deadlocking against each other", watchdogFor)
	}

	// Every version published must be accounted for and contiguous: the count
	// writes must not have disturbed the version counter at all.
	if got, want := c.Snapshot().Version, int64(publishers*iterations); got != want {
		t.Errorf("version = %d after %d concurrent publishes, want %d: listener-count writes disturbed the version counter", got, want, want)
	}
}
