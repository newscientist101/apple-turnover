package srv

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestConductorTransportPlaying pins the transport flag: the conductor starts
// un-hushed, SetPlaying flips it without touching the code document or the
// version counter, and the value is observable through Snapshot.
func TestConductorTransportPlaying(t *testing.T) {
	c := NewConductor(4)

	if !c.Snapshot().Playing {
		t.Fatal("fresh conductor Playing = false, want true (transport starts un-hushed)")
	}

	c.Publish(`s("bd")`, "kick")
	c.SetPlaying(false)

	snap := c.Snapshot()
	if snap.Playing {
		t.Error("after SetPlaying(false), Playing = true, want false")
	}
	if snap.Version != 1 {
		t.Errorf("hush bumped version to %d, want 1", snap.Version)
	}
	if snap.Code != `s("bd")` {
		t.Errorf("hush changed the code document: got %q, want %q", snap.Code, `s("bd")`)
	}

	c.SetPlaying(true)
	if !c.Snapshot().Playing {
		t.Error("after SetPlaying(true), Playing = false, want true")
	}
}

// TestConductorRecordEvalResult covers the browser feedback loop state: the
// first report for an unpublished version is an error, reports are tagged with
// the version they describe, publishing new code does not erase the last
// report, and an out-of-order (stale) report never regresses a newer one.
func TestConductorRecordEvalResult(t *testing.T) {
	c := NewConductor(4)

	if got := c.Snapshot().LastEvalResult; got != nil {
		t.Fatalf("fresh LastEvalResult = %+v, want nil", got)
	}

	// Nothing has been published yet, so there is no version 1 to report on.
	if err := c.RecordEvalResult(EvalResult{Version: 1, OK: true}); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("report for unpublished version: err = %v, want ErrUnknownVersion", err)
	}

	c.Publish(`note("c4")`, "first")
	res := EvalResult{
		Version: 1,
		OK:      false,
		Error:   "SyntaxError: unexpected token",
		Stats:   json.RawMessage(`{"events":3}`),
	}
	if err := c.RecordEvalResult(res); err != nil {
		t.Fatalf("RecordEvalResult for version 1 failed: %v", err)
	}
	got := c.Snapshot().LastEvalResult
	if got == nil {
		t.Fatal("LastEvalResult = nil after a successful report")
	}
	if got.Version != 1 {
		t.Errorf("LastEvalResult.Version = %d, want 1", got.Version)
	}
	if got.OK {
		t.Error("LastEvalResult.OK = true, want false")
	}
	if got.Error != "SyntaxError: unexpected token" {
		t.Errorf("LastEvalResult.Error = %q, want the reported error", got.Error)
	}
	if string(got.Stats) != `{"events":3}` {
		t.Errorf("LastEvalResult.Stats = %s, want {\"events\":3}", got.Stats)
	}
	if got.EpochMS <= 0 {
		t.Errorf("LastEvalResult.EpochMS = %d, want a wall-clock timestamp", got.EpochMS)
	}

	// Publishing a new version must not erase the feedback already received.
	c.Publish(`note("d4")`, "")
	if c.Snapshot().LastEvalResult == nil {
		t.Error("Publish cleared LastEvalResult, want it retained until a newer report arrives")
	}

	// Version 2's verdict replaces version 1's.
	if err := c.RecordEvalResult(EvalResult{Version: 2, OK: true}); err != nil {
		t.Fatalf("RecordEvalResult for version 2 failed: %v", err)
	}
	got = c.Snapshot().LastEvalResult
	if got.Version != 2 || !got.OK {
		t.Errorf("after version 2 report: got %+v, want version 2 ok", got)
	}
	if got.Error != "" {
		t.Errorf("LastEvalResult.Error = %q, want empty on a later success", got.Error)
	}

	// A late report for version 1 must not clobber the version 2 verdict.
	if err := c.RecordEvalResult(EvalResult{Version: 1, OK: false, Error: "late"}); err != nil {
		t.Fatalf("late report for a known version returned error: %v", err)
	}
	got = c.Snapshot().LastEvalResult
	if got.Version != 2 || !got.OK {
		t.Errorf("stale report clobbered a newer verdict: got %+v, want version 2 ok", got)
	}
}

// TestConductorEvalResultSnapshotImmutability makes sure a caller cannot reach
// into Conductor internals through the returned EvalResult pointer or its stats
// bytes.
func TestConductorEvalResultSnapshotImmutability(t *testing.T) {
	c := NewConductor(2)
	c.Publish("code", "msg")
	if err := c.RecordEvalResult(EvalResult{Version: 1, OK: true, Stats: json.RawMessage(`{"events":7}`)}); err != nil {
		t.Fatalf("RecordEvalResult failed: %v", err)
	}

	first := c.Snapshot().LastEvalResult
	if first == nil {
		t.Fatal("LastEvalResult = nil")
	}
	first.Version = -99
	first.OK = false
	first.Error = "HACKED"
	first.Stats[0] = 'X' // mutate the backing array, not the slice header

	after := c.Snapshot().LastEvalResult
	if after == nil {
		t.Fatal("LastEvalResult = nil on second snapshot")
	}
	if after.Version != 1 || !after.OK || after.Error != "" {
		t.Errorf("eval result mutated through snapshot: got %+v", after)
	}
	if string(after.Stats) != `{"events":7}` {
		t.Errorf("stats bytes aliased into snapshot: got %s, want {\"events\":7}", after.Stats)
	}
}
