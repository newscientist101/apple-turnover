package srv

import (
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

	// SetAnchor republishes the timeline without bumping the version.
	c.SetAnchor(1700000000000, 1.5)
	got := c.Snapshot()
	if got.Anchor.EpochMS != 1700000000000 || got.Anchor.CPS != 1.5 {
		t.Errorf("SetAnchor not applied: got %+v", got.Anchor)
	}
	if got.Version != 1 {
		t.Errorf("SetAnchor bumped version to %d, want 1", got.Version)
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
