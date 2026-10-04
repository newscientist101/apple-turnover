package srv

import (
	"encoding/json"
	"errors"
	"strings"
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
	if _, err := c.RecordEvalResult(EvalResult{Version: 1, OK: true}); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("report for unpublished version: err = %v, want ErrUnknownVersion", err)
	}

	c.Publish(`note("c4")`, "first")
	res := EvalResult{
		Version: 1,
		OK:      false,
		Error:   "SyntaxError: unexpected token",
		Stats:   json.RawMessage(`{"events":3}`),
	}
	if _, err := c.RecordEvalResult(res); err != nil {
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
	if _, err := c.RecordEvalResult(EvalResult{Version: 2, OK: true}); err != nil {
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
	if _, err := c.RecordEvalResult(EvalResult{Version: 1, OK: false, Error: "late"}); err != nil {
		t.Fatalf("late report for a known version returned error: %v", err)
	}
	got = c.Snapshot().LastEvalResult
	if got.Version != 2 || !got.OK {
		t.Errorf("stale report clobbered a newer verdict: got %+v, want version 2 ok", got)
	}
}

// TestConductorRecordEvalResultSameVersionReplaces pins the < versus <= boundary
// of the staleness guard in RecordEvalResult, which no other test distinguishes.
//
// The guard ignores a report that names a STRICTLY OLDER version than the stored
// one. A report for the SAME version is not stale: the browser re-reports a
// verdict for the current version as it re-renders, and the latest verdict for
// that version is the true one, so it must REPLACE what is stored. Reading the
// guard as <= instead drops the re-report and leaves the previous verdict in
// place, which is a silent behavioural regression that no conductor test
// noticed: the existing negative case reports version 1 against a stored
// version 2, which both < and <= reject.
//
// This is a separate test from TestConductorRecordEvalResult rather than an
// extra case inside it, so the boundary has a name of its own and the existing
// assertions stay exactly as they were.
func TestConductorRecordEvalResultSameVersionReplaces(t *testing.T) {
	c := NewConductor(4)
	c.Publish(`s("bd")`, "first")

	// The first verdict for version 1 succeeds.
	if _, err := c.RecordEvalResult(EvalResult{Version: 1, OK: true}); err != nil {
		t.Fatalf("first report for version 1 failed: %v", err)
	}
	if got := c.Snapshot().LastEvalResult; got == nil || !got.OK {
		t.Fatalf("after a successful report: got %+v, want version 1 ok", got)
	}

	// Re-reporting the SAME version with the opposite verdict must replace it.
	if _, err := c.RecordEvalResult(EvalResult{Version: 1, OK: false, Error: "SyntaxError: unexpected token"}); err != nil {
		t.Fatalf("re-report for the same version returned error: %v", err)
	}
	got := c.Snapshot().LastEvalResult
	if got == nil {
		t.Fatal("LastEvalResult = nil after a same-version re-report")
	}
	if got.OK {
		t.Error("same-version re-report was ignored: stored OK = true, want false (the latest report for a version wins)")
	}
	if got.Version != 1 {
		t.Errorf("LastEvalResult.Version = %d, want 1", got.Version)
	}
	if got.Error != "SyntaxError: unexpected token" {
		t.Errorf("LastEvalResult.Error = %q, want the re-reported text: a same-version report replaces the stored verdict wholesale", got.Error)
	}
}

// TestConductorEvalResultStatsMustBeAnObject pins the ONE field on the eval
// report that is not a plain scalar, and the only place its type can be
// checked: stats arrives as opaque JSON bytes, so nothing but an explicit check
// stops a scalar from being stored and echoed back to every future consumer.
//
// Every neighbouring field (version, ok, error) is decoded into a typed Go
// field, so a wrong type is rejected by the decoder itself. stats alone would
// otherwise be the one hole in "validate first, commit second" — and it is the
// more dangerous of the two, because the value is echoed verbatim: a consumer
// that reads stats.haps gets undefined/false/null instead of an error.
func TestConductorEvalResultStatsMustBeAnObject(t *testing.T) {
	tests := []struct {
		name    string
		stats   json.RawMessage
		wantErr error // nil means the report must be accepted
		want    string
	}{
		{name: "absent", stats: nil, want: ""},
		{name: "empty", stats: json.RawMessage(``), want: ""},
		{name: "null", stats: json.RawMessage(`null`), want: ""},
		{name: "empty object", stats: json.RawMessage(`{}`), want: `{}`},
		{name: "object", stats: json.RawMessage(`{"haps":12}`), want: `{"haps":12}`},
		// Opaque means the KEYS are opaque, not the type: an object the server
		// has never heard of must survive byte for byte, or the browser cannot
		// report anything new without a server change.
		{name: "unknown keys pass through", stats: json.RawMessage(`{"haps":1,"future":{"deep":[1,2]}}`), want: `{"haps":1,"future":{"deep":[1,2]}}`},
		{name: "bool", stats: json.RawMessage(`true`), wantErr: ErrStatsNotObject},
		{name: "number", stats: json.RawMessage(`123`), wantErr: ErrStatsNotObject},
		{name: "string", stats: json.RawMessage(`"a string"`), wantErr: ErrStatsNotObject},
		{name: "empty string", stats: json.RawMessage(`""`), wantErr: ErrStatsNotObject},
		{name: "array", stats: json.RawMessage(`[1,2]`), wantErr: ErrStatsNotObject},
		{name: "empty array", stats: json.RawMessage(`[]`), wantErr: ErrStatsNotObject},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewConductor(2)
			c.Publish("code", "msg")

			stored, err := c.RecordEvalResult(EvalResult{Version: 1, OK: true, Stats: tt.stats})

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("RecordEvalResult(stats=%s) error = %v, want %v", tt.stats, err, tt.wantErr)
				}
				if stored {
					t.Error("RecordEvalResult reported a rejected stats value as STORED")
				}
				// Validate first, commit second: a refused report must leave the
				// conductor exactly as it found it, or a bad body becomes state.
				if snap := c.Snapshot(); snap.LastEvalResult != nil {
					t.Errorf("a rejected report stored a verdict anyway: %+v", snap.LastEvalResult)
				}
				return
			}

			if err != nil {
				t.Fatalf("RecordEvalResult(stats=%s) failed: %v", tt.stats, err)
			}
			if !stored {
				t.Fatal("RecordEvalResult did not store a valid report")
			}
			got := c.Snapshot().LastEvalResult
			if got == nil {
				t.Fatal("LastEvalResult = nil after a stored report")
			}
			if string(got.Stats) != tt.want {
				t.Errorf("stored stats = %q, want %q", got.Stats, tt.want)
			}
		})
	}
}

// TestConductorEvalResultRejectedStatsNamesTheValue pins that the refusal is
// actionable. An agent that gets a bare 400 cannot tell WHICH field was wrong,
// and the documented promise is that error messages "name the offending field".
func TestConductorEvalResultRejectedStatsNamesTheValue(t *testing.T) {
	c := NewConductor(2)
	c.Publish("code", "msg")

	_, err := c.RecordEvalResult(EvalResult{Version: 1, OK: true, Stats: json.RawMessage(`[1,2]`)})
	if err == nil {
		t.Fatal("RecordEvalResult accepted an array stats")
	}
	msg := err.Error()
	for _, want := range []string{"stats", "[1,2]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q, so the caller cannot tell which value was refused", msg, want)
		}
	}
}

// TestConductorEvalResultStatsCheckedBeforeVersion pins the ORDER of the two
// rejections. Both are 400s to the caller, so this is only observable in the
// message — but a report that is wrong in two ways should not be diagnosed by
// whichever check happens to run first, and the stats check is the one that
// describes the field the sender can actually fix.
func TestConductorEvalResultStatsCheckedBeforeVersion(t *testing.T) {
	c := NewConductor(2)

	_, err := c.RecordEvalResult(EvalResult{Version: 9999, OK: true, Stats: json.RawMessage(`true`)})
	if !errors.Is(err, ErrStatsNotObject) {
		t.Errorf("error = %v, want ErrStatsNotObject: a malformed body should be diagnosed before an unknown version", err)
	}
}

// TestConductorEvalResultSnapshotImmutability makes sure a caller cannot reach
// into Conductor internals through the returned EvalResult pointer or its stats
// bytes.
func TestConductorEvalResultSnapshotImmutability(t *testing.T) {
	c := NewConductor(2)
	c.Publish("code", "msg")
	if _, err := c.RecordEvalResult(EvalResult{Version: 1, OK: true, Stats: json.RawMessage(`{"events":7}`)}); err != nil {
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
