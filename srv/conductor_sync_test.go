// Drift observations in the Conductor (strudel-agent-uvj.15).
//
// Before this, drift was measured by the browser, painted into #sync-status and
// dropped. These tests own the stored half: that an observation is kept, that
// it is reachable from a snapshot, that it is COPIED rather than aliased, and
// above all that recording one changes nothing about the performance.
//
// That last part is the invariant most likely to be broken by a well-meaning
// future change. An observation is keyed to a version, it arrives after that
// version is already live, and it is the evidence an agent uses to decide to
// re-anchor -- so storing it as though it were a new document would put a
// second entry for one push in the history and make every listener's
// reconnection look like an edit.

package srv

import (
	"encoding/json"
	"testing"
)

// ptrInt is a readability helper for the *int64 drift field.
func ptrInt(v int64) *int64 { return &v }

// TestRecordSyncObservationStoresWithoutTouchingThePerformance is the central
// server-side property: an observation is stored, visible, and inert.
func TestRecordSyncObservationStoresWithoutTouchingThePerformance(t *testing.T) {
	c := NewConductor(0)
	c.Publish(`s("bd*2")`, "first")
	before := c.Snapshot()

	obs := SyncObservation{
		Version:  before.Version,
		DriftMS:  ptrInt(14),
		TargetMS: 1757000002000,
		ActualMS: 1757000002014,
	}
	if err := c.RecordSyncObservation(obs); err != nil {
		t.Fatalf("RecordSyncObservation: %v", err)
	}

	after := c.Snapshot()
	if after.Version != before.Version {
		t.Errorf("version moved from %d to %d on a sync report: only POST /api/code increments it", before.Version, after.Version)
	}
	if len(after.History) != len(before.History) {
		t.Errorf("history grew from %d to %d entries on a sync report: an observation is not a revision of the document", len(before.History), len(after.History))
	}
	if after.Code != before.Code {
		t.Errorf("the code document changed on a sync report: %q -> %q", before.Code, after.Code)
	}

	got := after.LastSync
	if got == nil {
		t.Fatal("lastSync is nil after a stored observation: an agent polling /api/state would see no drift at all, which is the defect this bead exists to close")
	}
	if got.Version != before.Version {
		t.Errorf("stored observation names version %d, want %d", got.Version, before.Version)
	}
	if got.DriftMS == nil || *got.DriftMS != 14 {
		t.Errorf("stored driftMs = %v, want 14", got.DriftMS)
	}
	if got.Unscheduled {
		t.Error("a scheduled observation stored unscheduled:true")
	}
	if got.EpochMS <= 0 {
		t.Errorf("stored epochMs = %d, want the server's receipt stamp: an agent cannot judge a stale observation without knowing when it arrived", got.EpochMS)
	}
}

// TestSnapshotLastSyncIsNullUntilAnObservationArrives pins the distinction the
// acceptance criteria call for: "no drift observed yet" is not "drift observed
// and it is zero".
//
// It matters because the healthy case and the silent case look identical on the
// wire otherwise. A browser that has not committed yet, and a browser that has
// committed and landed exactly on the bar line, would both read as "0".
func TestSnapshotLastSyncIsNullUntilAnObservationArrives(t *testing.T) {
	c := NewConductor(0)
	if snap := c.Snapshot(); snap.LastSync != nil {
		t.Errorf("a fresh Conductor reports lastSync = %+v, want null: nothing has been observed yet", snap.LastSync)
	}

	c.Publish("s(1)", "")
	// Publishing is not observing: a push the browser has not committed yet has
	// produced no measurement.
	if snap := c.Snapshot(); snap.LastSync != nil {
		t.Errorf("lastSync = %+v straight after a publish, want null: an uncommitted version has no drift to report", snap.LastSync)
	}

	// Zero drift IS an observation, and must be stored as such.
	if err := c.RecordSyncObservation(SyncObservation{Version: 1, DriftMS: ptrInt(0)}); err != nil {
		t.Fatalf("RecordSyncObservation: %v", err)
	}
	snap := c.Snapshot()
	if snap.LastSync == nil {
		t.Fatal("lastSync is nil after observing zero drift: a silent system must not be indistinguishable from a healthy one")
	}
	if snap.LastSync.DriftMS == nil || *snap.LastSync.DriftMS != 0 {
		t.Errorf("stored driftMs = %v, want a present 0", snap.LastSync.DriftMS)
	}
}

// TestSnapshotLastSyncIsCopiedNotAliased is the aliasing guard. Snapshot must
// not expose mutable internal state, and a *SyncObservation pointer is exactly
// the shape that breaks that rule if the store hands out its own pointer.
func TestSnapshotLastSyncIsCopiedNotAliased(t *testing.T) {
	c := NewConductor(0)
	c.Publish("s(1)", "")
	// The reporter keeps its own pointer, as the HTTP handler's decoded body
	// does after the call returns.
	drift := int64(7)
	if err := c.RecordSyncObservation(SyncObservation{Version: 1, DriftMS: &drift}); err != nil {
		t.Fatalf("RecordSyncObservation: %v", err)
	}

	// A later write through the reporter's pointer must not reach the store.
	drift = 9999
	if got := c.Snapshot().LastSync.DriftMS; got == nil || *got != 7 {
		t.Errorf("stored driftMs = %v after the reporter mutated its own pointer to 9999, want the stored 7", got)
	}

	// And a write through a snapshot must not corrupt the next reader.
	snap := c.Snapshot()
	*snap.LastSync.DriftMS = 12345
	if got := c.Snapshot().LastSync.DriftMS; got == nil || *got != 7 {
		t.Errorf("stored driftMs = %v after a caller wrote 12345 through a snapshot, want the stored 7: Snapshot handed out internal state", got)
	}
}

// TestRecordSyncObservationRequiresExactlyOneClaim is the body-validation rule:
// a report must say EITHER a measured drift OR that the commit was unscheduled.
//
// Both together is a contradiction, and neither is silence. A report with
// neither is the more dangerous of the two, because it would store as a
// successful observation while claiming nothing at all -- the exact shape of the
// original defect, where drift was measured and then dropped.
func TestRecordSyncObservationRequiresExactlyOneClaim(t *testing.T) {
	c := NewConductor(0)
	c.Publish("s(1)", "")

	for _, tc := range []struct {
		name string
		obs  SyncObservation
	}{
		{"neither drift nor unscheduled", SyncObservation{Version: 1}},
		{"both drift and unscheduled", SyncObservation{Version: 1, DriftMS: ptrInt(0), Unscheduled: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := c.RecordSyncObservation(tc.obs)
			if err == nil {
				t.Fatalf("%s was ACCEPTED: a report that claims nothing, or claims two contradictory things, would store as a successful observation", tc.name)
			}
			if snap := c.Snapshot(); snap.LastSync != nil {
				t.Errorf("a refused report still stored lastSync = %+v: a rejected observation must leave the stored one untouched", snap.LastSync)
			}
		})
	}
}

// TestUnscheduledObservationIsStoredAsUnscheduled proves the unscheduled case
// survives storage rather than being normalised into a numeric drift.
func TestUnscheduledObservationIsStoredAsUnscheduled(t *testing.T) {
	c := NewConductor(0)
	c.Publish("s(1)", "")
	if err := c.RecordSyncObservation(SyncObservation{Version: 1, Unscheduled: true}); err != nil {
		t.Fatalf("RecordSyncObservation(unscheduled): %v", err)
	}
	got := c.Snapshot().LastSync
	if got == nil || !got.Unscheduled {
		t.Fatalf("lastSync = %+v, want an unscheduled observation", got)
	}
	if got.DriftMS != nil {
		t.Errorf("an unscheduled observation stored driftMs = %d, want absent: there was no bar line to measure against", *got.DriftMS)
	}

	// The wire form must not invent a drift AT ALL for an unscheduled commit.
	//
	// The assertion is that the key is ABSENT, not merely that it is not zero.
	// That distinction is the whole point and it is easy to state wrongly: a
	// check for `"driftMs":0` passes against a snapshot that emits
	// `"driftMs":null`, which is exactly the encoding a dropped omitempty
	// produces -- a reader doing `lastSync.driftMs ?? 0` would then see a
	// perfectly aligned listener that never aligned at all. "No bar line" has to
	// travel as a missing field, because every JSON reader treats null and absent
	// differently and only absent is unambiguous.
	raw, err := json.Marshal(c.Snapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	// Assert on KEY PRESENCE, decoded through map[string]json.RawMessage.
	//
	// Decoding into a typed struct is the wrong tool here and quietly so: a
	// `*json.RawMessage` field left nil by an ABSENT key is indistinguishable
	// from one left nil by an explicit JSON null, so the assertion below passes
	// against a snapshot emitting "driftMs":null. That encoding is precisely what
	// a dropped omitempty produces, and a reader doing `lastSync.driftMs ?? 0`
	// would then see a perfectly aligned listener that never aligned at all. The
	// map lookup is the only form that distinguishes "no key" from "null key".
	var wire struct {
		LastSync map[string]json.RawMessage `json:"lastSync"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode snapshot %s: %v", raw, err)
	}
	if wire.LastSync == nil {
		t.Fatalf("the snapshot has no lastSync object at all: %s", raw)
	}
	if v, present := wire.LastSync["driftMs"]; present {
		t.Errorf("an unscheduled observation carries the driftMs key as %s, want it ABSENT: there was no bar line to be late against, and null reads as a zero to any consumer doing a null-coalesce (%s)", v, raw)
	}
	if _, present := wire.LastSync["unscheduled"]; !present {
		t.Errorf("an unscheduled observation did not serialise unscheduled:true: %s", raw)
	}
}

// TestRecordSyncObservationRefusesAnUnpublishedVersion keeps the report tied to
// the document it describes. A drift observation naming a version that was never
// published is not evidence about this performance.
func TestRecordSyncObservationRefusesAnUnpublishedVersion(t *testing.T) {
	c := NewConductor(0)
	c.Publish("s(1)", "")
	for _, version := range []int64{0, 2, 9999} {
		err := c.RecordSyncObservation(SyncObservation{Version: version, DriftMS: ptrInt(0)})
		if err == nil {
			t.Errorf("version %d was accepted as an observation, want a refusal: it names no published document", version)
		}
	}
}

// TestTheNewestObservationWinsRegardlessOfVersion is the deliberate asymmetry
// with stored verdicts.
//
// A verdict for an OLDER version is stale and is discarded, because it would
// regress the agent's view of the code. An observation is a different fact: it
// is a measurement of when a listener's commit actually landed, and a listener
// that committed version 1 late can perfectly well report that AFTER version 2
// was published. Discarding it would drop the very evidence an agent needs to
// decide the audience is drifting.
func TestTheNewestObservationWinsRegardlessOfVersion(t *testing.T) {
	c := NewConductor(0)
	c.Publish("s(1)", "")
	c.Publish("s(2)", "")

	if err := c.RecordSyncObservation(SyncObservation{Version: 2, DriftMS: ptrInt(3)}); err != nil {
		t.Fatalf("RecordSyncObservation(v2): %v", err)
	}
	if err := c.RecordSyncObservation(SyncObservation{Version: 1, DriftMS: ptrInt(41)}); err != nil {
		t.Fatalf("RecordSyncObservation(v1): %v", err)
	}
	got := c.Snapshot().LastSync
	if got == nil || got.Version != 1 || got.DriftMS == nil || *got.DriftMS != 41 {
		t.Errorf("lastSync = %+v, want the newest ARRIVAL (v1, drift 41): a late report about an older commit is still the most recent measurement", got)
	}
}
