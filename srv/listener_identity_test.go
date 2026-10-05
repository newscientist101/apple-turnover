package srv

// Listener identity (strudel-agent-f79), part 1: the HUB.
//
// The bead exists because listeners are anonymous. Until a /ws connection has a
// name, the server cannot dedupe two reports from one tab, cannot forget a tab
// that has closed, and cannot say WHICH listener answered — so every statement
// it makes about the audience is a statement about whoever spoke last.
//
// Identity therefore has to be assigned where connections are created, which is
// the hub: it is the one owner of the subscriber set, and it is what both
// handleWS and the count hook already go through. Assigning an id anywhere else
// would mean two places that can mint one.
//
// Three properties are load-bearing and each is a separate test:
//
//  1. DISTINCT per connection. Two subscribers sharing an id could not be told
//     apart, which would reproduce the original defect with extra steps.
//  2. NEVER REUSED. A recycled id would make a report from a closed tab
//     indistinguishable from a report by the tab that inherited its number —
//     and this feature's entire purpose is to stop confusing one listener with
//     another.
//  3. STABLE for the life of the connection, because the id is captured once on
//     connect and used for every later report.
//
// Direct is tested here too because it exists for the same reason: a listener
// must be told its OWN id, and no listener else's.

import (
	"strings"
	"testing"
	"time"
)

// TestSubscriberIDsAreDistinct proves two live listeners never share an id.
//
// Under -race this also covers the minting itself: the counter is bumped on the
// hub goroutine, so an id read from a Subscriber is a value the hub has already
// finished writing.
func TestSubscriberIDsAreDistinct(t *testing.T) {
	h := hubTestHub(t, 4)
	a, b := hubSub(t, h), hubSub(t, h)

	if a.ID() == "" || b.ID() == "" {
		t.Fatalf("a subscriber was handed an empty id: a=%q b=%q", a.ID(), b.ID())
	}
	if a.ID() == b.ID() {
		t.Errorf("two live subscribers share id %q; the server could not tell these listeners apart", a.ID())
	}
}

// TestSubscriberIDsAreNeverReused proves a departed listener's id is not handed
// to its replacement.
//
// This is the property that makes a stored report attributable. If an id is
// reused, then a report that arrived from the first holder would be
// indistinguishable from one its replacement sent — and a stale finding about a
// closed tab would be read as evidence about the tab that replaced it, which is
// precisely the confusion this bead exists to remove.
func TestSubscriberIDsAreNeverReused(t *testing.T) {
	h := hubTestHub(t, 4)

	first := hubSub(t, h)
	firstID := first.ID()
	h.Unsubscribe(first)

	second := hubSub(t, h)
	if second.ID() == firstID {
		t.Errorf("id %q was reissued to a new subscriber after the first left; "+
			"a stored report from the departed listener would be indistinguishable from the new one's",
			firstID)
	}
}

// TestSubscriberIDIsStableForTheConnection proves the id does not change under
// its owner. The browser captures it once, on the `listener` frame, and reuses
// it on every later report; an id that moved would silently mis-key them.
func TestSubscriberIDIsStableForTheConnection(t *testing.T) {
	h := hubTestHub(t, 8)
	sub := hubSub(t, h)
	first := sub.ID()

	// Churn the hub around this subscriber: broadcasts, and other listeners
	// arriving and leaving, must not disturb it.
	h.Broadcast([]byte(`{"kind":"code"}`))
	other := hubSub(t, h)
	h.Broadcast([]byte(`{"kind":"message"}`))
	h.Unsubscribe(other)

	if got := sub.ID(); got != first {
		t.Errorf("subscriber id changed from %q to %q while it was connected", first, got)
	}
}

// TestSubscriberIDIsOpaqueAndUnambiguous proves the id is a string a browser can
// echo back verbatim.
//
// The id travels from the server, through a JSON frame, into a browser, and back
// on a POST body, so anything awkward to transcribe would make the round trip
// lossy. The test pins the SHAPE rather than the exact spelling, because the
// spelling is a server implementation detail an agent must never be asked to
// parse.
func TestSubscriberIDIsOpaqueAndUnambiguous(t *testing.T) {
	h := hubTestHub(t, 4)
	id := hubSub(t, h).ID()

	if id == "" {
		t.Fatal("a subscriber was handed an empty id; it could not be echoed back on a report")
	}
	if strings.ContainsAny(id, `",:{ }[]`) {
		t.Errorf("id %q contains a character that would be ambiguous in JSON or a log line", id)
	}
}

// TestDirectDeliversToOneSubscriberOnly proves the one-subscriber send path
// exists and is exact.
//
// The `listener` frame is what tells a browser its own id, and it must reach
// that browser and no other: delivered to everyone it would hand every listener
// an id belonging to somebody else, and delivered to nobody the feature cannot
// work at all.
func TestDirectDeliversToOneSubscriberOnly(t *testing.T) {
	h := hubTestHub(t, 4)
	target, bystander := hubSub(t, h), hubSub(t, h)

	if err := h.Direct(target, []byte(`{"kind":"listener"}`)); err != nil {
		t.Fatalf("Hub.Direct: %v", err)
	}

	msg, closed, ok := hubRecvWithin(target, hubRecvTimeout)
	if !ok {
		t.Fatalf("the addressed subscriber received nothing within %s (closed=%t)", hubRecvTimeout, closed)
	}
	if string(msg) != `{"kind":"listener"}` {
		t.Errorf("addressed subscriber got %q, want the exact bytes sent", msg)
	}

	if _, closed, ok := hubRecvWithin(bystander, 100*time.Millisecond); ok {
		t.Errorf("a frame addressed to one subscriber also reached another (closed=%t); "+
			"every listener would be told an id belonging to somebody else", closed)
	}
}

// TestDirectDoesNotBlockOnAFullBuffer proves the slow-client guarantee survives
// the new command.
//
// This is the property that made the hub a hub: no send in the delivery path
// blocks, so one client that stopped reading cannot wedge the goroutine every
// other listener depends on. A Direct that blocked here would reintroduce
// exactly that, and it would do so on the /ws connect path, where the caller is
// a handler holding a socket open.
func TestDirectDoesNotBlockOnAFullBuffer(t *testing.T) {
	// A buffer of 1, and the subscriber never reads: the first message fills it.
	h := hubTestHub(t, 1)
	stuck := hubSub(t, h)

	if err := h.Direct(stuck, []byte(`{"kind":"listener"}`)); err != nil {
		t.Fatalf("first Hub.Direct: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- h.Direct(stuck, []byte(`{"kind":"code"}`)) }()

	select {
	case err := <-done:
		// Either answer is acceptable — delivering into a drained slot, or
		// refusing because the subscriber is not keeping up — and BOTH are
		// required to be non-blocking. What is not acceptable is hanging.
		t.Logf("Direct into a full buffer returned %v (non-blocking, as required)", err)
	case <-time.After(hubCountTimeout):
		t.Fatalf("Hub.Direct blocked for %s on a subscriber that stopped reading; "+
			"the delivery path must never block on a client", hubCountTimeout)
	}

	// The hub must still be answering other clients after that.
	if _, ok := hubSubscriberCountWithin(h, hubCountTimeout); !ok {
		t.Fatalf("the hub stopped answering SubscriberCount after a full-buffer Direct; it is wedged")
	}
}

// TestDirectToAnUnknownSubscriberIsRefused proves Direct cannot be used to
// resurrect a connection that has gone.
//
// The membership check has to live on the hub goroutine, which is the only
// place the subscriber set is authoritative: a subscriber dropped for falling
// behind, one already unsubscribed, and one closed with the hub all have closed
// channels, and a Direct that queued onto any of them would write to a channel
// nobody will ever read — or, worse, race the close.
func TestDirectToAnUnknownSubscriberIsRefused(t *testing.T) {
	h := hubTestHub(t, 4)
	sub := hubSub(t, h)

	h.Unsubscribe(sub)

	done := make(chan error, 1)
	go func() { done <- h.Direct(sub, []byte(`{"kind":"listener"}`)) }()
	select {
	case err := <-done:
		if err == nil {
			t.Errorf("Direct to an unsubscribed subscriber returned nil; " +
				"a report for a connection that has gone must be refused, not delivered to a closed channel")
		}
	case <-time.After(hubCountTimeout):
		t.Fatalf("Hub.Direct to an unsubscribed subscriber did not return within %s", hubCountTimeout)
	}
}

// TestDirectOnAClosedHubDoesNotHang proves the command is absorbed by a hub that
// has shut down, like every other command.
//
// Close drains the command channels before exiting, so a racing Direct is
// answered rather than left parked on a channel nobody will read again.
func TestDirectOnAClosedHubDoesNotHang(t *testing.T) {
	h := NewHub(4)
	sub, err := h.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	closed := make(chan struct{})
	go func() { h.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(hubShutdownTimeout):
		t.Fatalf("Hub.Close did not return within %s", hubShutdownTimeout)
	}

	done := make(chan error, 1)
	go func() { done <- h.Direct(sub, []byte(`{"kind":"listener"}`)) }()
	select {
	case <-done:
		// An error is the honest answer; the only requirement is that it
		// arrives rather than hanging.
	case <-time.After(hubCountTimeout):
		t.Fatalf("Hub.Direct on a closed hub did not return within %s", hubCountTimeout)
	}
}

// TestARefusedIdentityIsNotAStoredFinding proves the Conductor will not invent a
// listener record for a report it cannot attribute.
//
// The whole feature rests on every stored record naming a connection that really
// exists. A server that minted a record for an unknown id would let any client —
// or a reconnecting browser replaying a stale id — assert things about an
// audience it is not part of.
func TestARefusedIdentityIsNotAStoredFinding(t *testing.T) {
	c := NewConductor(0)

	if _, err := c.RecordListenerSamples("L1", ListenerSamples{}); err == nil {
		t.Fatal("RecordListenerSamples accepted a report for a listener that never connected; " +
			"every stored record must name a connection the server issued an id to")
	}
	if snap := c.Snapshot(); snap.Samples != nil {
		t.Errorf("a refused report left state behind: %+v", snap.Samples)
	}
}

// TestARefusedIdentityBroadcastsNothing is the Conductor half of the same rule,
// expressed as the signal the HTTP layer keys on: the second return value says
// whether anything was actually stored, so a refused report cannot be announced.
func TestARefusedIdentityBroadcastsNothing(t *testing.T) {
	c := NewConductor(0)

	// The id here is one the server NEVER issued, which is the case this covers:
	// a stale id replayed by a reconnecting browser, or one a client invented.
	changed, err := c.RecordListenerSamples("nope", ListenerSamples{})
	if err == nil {
		t.Fatal("expected a refusal for an unknown listener id")
	}
	if changed {
		t.Error("RecordListenerSamples reported changed=true for a refused report; " +
			"a listener told about a measurement the server refused would be describing a finding that does not exist")
	}
}

// TestSamplesStayUnknownUntilReported proves absence is not a finding.
//
// A listener that has never reported has said NOTHING about its samples, and a
// server that answered "unloaded" for it would be inventing a defect nobody
// reported — or worse, answering "loaded" over a check that never ran, which is
// the exact defect strudel-agent-uvj.18 was filed for. The summary must be
// absent, not zero-valued, so this is a fact a reader can see.
func TestSamplesStayUnknownUntilReported(t *testing.T) {
	c := NewConductor(0)

	snap := c.Snapshot()
	if snap.Samples != nil {
		t.Fatalf("a server nobody has reported to claims sample state: %+v", snap.Samples)
	}
}

// TestAnUnknownListenerIsNotZeroLoaded proves the tri-state survives the summary.
//
// This is the SamplesResolved trap arriving one layer out: a listener that
// reported "I have no registry to check" is UNKNOWN, and folding that into false
// would tell an agent its samples are missing when nobody ever looked.
func TestAnUnknownListenerIsNotZeroLoaded(t *testing.T) {
	c := NewConductor(0)
	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{}); err != nil {
		t.Fatalf("first report: %v", err)
	}

	snap := c.Snapshot()
	if snap.Samples == nil {
		t.Fatal("a reported listener produced no summary at all")
	}
	if snap.Samples.Loaded != 0 {
		t.Errorf("loaded = %d with no listener claiming to have samples; "+
			"an unknown answer must not be counted as a loaded one", snap.Samples.Loaded)
	}
	if snap.Samples.Reporting != 1 {
		t.Errorf("reporting = %d, want 1: a listener that reported IS reporting", snap.Samples.Reporting)
	}
	if snap.Samples.Listeners == nil || len(snap.Samples.Listeners) != 1 {
		t.Fatalf("the enumerated listeners do not include the one that reported: %+v", snap.Samples.Listeners)
	}
	if snap.Samples.Listeners[0].ID != "L1" {
		t.Errorf("listener id = %q, want L1", snap.Samples.Listeners[0].ID)
	}
	if snap.Samples.Listeners[0].Loaded != nil {
		t.Errorf("loaded = %v for a listener that reported nothing known; "+
			"UNKNOWN must stay nil, not collapse to false", *snap.Samples.Listeners[0].Loaded)
	}
}

// TestAnUnloadedListenerIsCountedAsNotLoaded proves false is a FINDING and is
// counted, unlike unknown. An agent asking "did anyone have the pack" needs this
// one to be counted; if false and unknown both counted as zero, the summary
// could not answer the question the bead exists to answer.
func TestAnUnloadedListenerIsCountedAsNotLoaded(t *testing.T) {
	c := NewConductor(0)
	missing, loaded := false, true

	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &missing}); err != nil {
		t.Fatalf("report L1: %v", err)
	}
	c.NoteListener("L2")
	if _, err := c.RecordListenerSamples("L2", ListenerSamples{Loaded: &loaded}); err != nil {
		t.Fatalf("report L2: %v", err)
	}

	snap := c.Snapshot()
	if snap.Samples == nil {
		t.Fatal("two reports produced no summary")
	}
	if snap.Samples.Reporting != 2 {
		t.Errorf("reporting = %d, want 2", snap.Samples.Reporting)
	}
	if snap.Samples.Loaded != 1 {
		t.Errorf("loaded = %d, want 1: one of two listeners has the pack, so the audience answer is a partial one", snap.Samples.Loaded)
	}
}

// TestAnIdenticalReReportIsNotAChange proves the transition rule at the store.
//
// "A frame is sent for a TRANSITION, not for an accepted write that changed
// nothing observable." A browser polling its sample state every second would
// otherwise produce a `samples` frame every second forever, filling listeners'
// bounded queues and — on a busy tab — evicting peers. The store is where this
// has to be decided, because it is the only place that knows the previous value.
func TestAnIdenticalReReportIsNotAChange(t *testing.T) {
	c := NewConductor(0)
	loaded := true
	report := ListenerSamples{Loaded: &loaded, Count: 412}

	c.NoteListener("L1")
	if changed, err := c.RecordListenerSamples("L1", report); err != nil || !changed {
		t.Fatalf("first report: changed=%t err=%v, want a change", changed, err)
	}
	if changed, err := c.RecordListenerSamples("L1", report); err != nil {
		t.Fatalf("second report: %v", err)
	} else if changed {
		t.Error("an identical re-report reported changed=true; " +
			"that would broadcast a frame describing a transition that did not happen")
	}
}

// TestARealChangeIsAnnounced proves the transition rule cuts both ways.
//
// The counterpart to the row above: a listener that loads a pack mid-session IS
// a transition, and an agent watching the audience must learn about it. A rule
// that simply never reported a change would satisfy "no frame for a no-op" and
// fail the entire feature.
func TestARealChangeIsAnnounced(t *testing.T) {
	c := NewConductor(0)
	before, after := false, true

	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &before}); err != nil {
		t.Fatalf("first report: %v", err)
	}
	if changed, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &after}); err != nil {
		t.Fatalf("second report: %v", err)
	} else if !changed {
		t.Error("a listener loading a pack reported changed=false; " +
			"an agent must be able to see the audience become able to hear the code it pushed")
	}
}

// TestARegistrySizeChangeIsATransition proves count participates.
//
// The size is the observable detail behind the boolean — one listener may go
// from "no pack" to "pack loaded" between two reports without the tri-state
// flipping if it already had SOME samples. A boolean-only comparison would call
// that no change and hide the transition.
func TestARegistrySizeChangeIsATransition(t *testing.T) {
	c := NewConductor(0)
	loaded := true

	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &loaded, Count: 3}); err != nil {
		t.Fatalf("first report: %v", err)
	}
	if changed, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &loaded, Count: 900}); err != nil || !changed {
		t.Errorf("a registry growing from 3 to 900 reported changed=%t err=%v, want a change", changed, err)
	}
}

// TestForgettingAListenerRemovesItsFinding proves departure is not silent.
//
// A tab that closes keeps reporting for ever unless the server forgets it, and a
// stale "loaded: true" from a departed tab would keep an agent believing the
// audience can hear a pack nobody has open. This is the third thing the bead says
// the server cannot do today, and the reason identity had to exist first.
func TestForgettingAListenerRemovesItsFinding(t *testing.T) {
	c := NewConductor(0)
	loaded := true
	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &loaded}); err != nil {
		t.Fatalf("report: %v", err)
	}

	// Two listeners, so that forgetting one leaves a visible audience rather than
	// emptying it — the "absence once the last one goes" case is its own test,
	// and conflating the two here would let a summary that wrongly went nil pass.
	c.NoteListener("L2")
	other := true
	if _, err := c.RecordListenerSamples("L2", ListenerSamples{Loaded: &other}); err != nil {
		t.Fatalf("report L2: %v", err)
	}

	removed, err := c.ForgetListenerSamples("L1")
	if err != nil {
		t.Fatalf("ForgetListenerSamples: %v", err)
	}
	if !removed {
		t.Error("forgetting a listener that had reported reported removed=false")
	}

	snap := c.Snapshot()
	if snap.Samples == nil {
		t.Fatal("the whole summary vanished when one of two listeners departed")
	}
	if snap.Samples.Reporting != 1 {
		t.Errorf("reporting = %d after one of two departed, want 1", snap.Samples.Reporting)
	}
	if snap.Samples.Loaded != 1 {
		t.Errorf("loaded = %d, want 1: the surviving listener is the one that has the pack", snap.Samples.Loaded)
	}
	for _, l := range snap.Samples.Listeners {
		if l.ID == "L1" {
			t.Errorf("a departed listener is still enumerated: %+v", l)
		}
	}
}

// TestForgettingTheLastListenerRestoresAbsence proves the summary does not
// outlive the audience.
//
// When the last listener leaves, the honest answer is "nobody has said
// anything", which is ABSENT — not a summary reporting zero listeners, which a
// reader could easily mistake for "the audience checked and has no samples".
func TestForgettingTheLastListenerRestoresAbsence(t *testing.T) {
	c := NewConductor(0)
	loaded := true
	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &loaded}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := c.ForgetListenerSamples("L1"); err != nil {
		t.Fatalf("forget: %v", err)
	}

	if snap := c.Snapshot(); snap.Samples != nil {
		t.Errorf("with no listeners left the snapshot still carries %+v; "+
			"'nobody has reported' must read as absent, not as an audience of zero", snap.Samples)
	}
}

// TestForgettingAnUnknownListenerIsHarmless proves teardown cannot fail.
//
// The forget is called from a deferred cleanup on the /ws connect path, where it
// runs on every exit including ones where the listener never reported. It must
// never be an error the handler has to handle, or a disconnect path would need
// error handling for the normal case.
func TestForgettingAnUnknownListenerIsHarmless(t *testing.T) {
	c := NewConductor(0)

	removed, err := c.ForgetListenerSamples("never-existed")
	if err != nil {
		t.Errorf("forgetting a listener that never reported returned an error: %v", err)
	}
	if removed {
		t.Error("forgetting an unknown listener reported removed=true")
	}
}

// TestSamplesNeverTouchThePerformance proves the audience claim is evidence, not
// a document.
//
// The rule is already established twice in this file's siblings — a sync
// observation and a listener count both describe something about a performance
// rather than changing it — and both refuse to bump the version or enter the
// history. Sample state is the same kind of fact, so it carries the same rule: a
// browser reporting on its packs must not make an agent's next push look like
// revision 5.
func TestSamplesNeverTouchThePerformance(t *testing.T) {
	c := NewConductor(0)
	c.Publish(`s("bd*2")`, "first")

	before := c.Snapshot()
	loaded := true
	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &loaded, Count: 1}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := c.ForgetListenerSamples("L1"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	after := c.Snapshot()

	if after.Version != before.Version {
		t.Errorf("version moved from %d to %d across a sample report and a departure; "+
			"a listener's pack state is not a code document", before.Version, after.Version)
	}
	if len(after.History) != len(before.History) {
		t.Errorf("history grew from %d to %d entries across sample reporting; "+
			"one push must produce one history entry", len(before.History), len(after.History))
	}
	if after.Code != before.Code {
		t.Errorf("the code document changed across sample reporting: %q -> %q", before.Code, after.Code)
	}
}

// TestSamplesDoNotDisturbTheStoredVerdict proves the two evidence streams are
// independent.
//
// lastEvalResult is the agent's feedback on the DOCUMENT and samples is
// evidence about the AUDIENCE. Folding one into the other, or letting a sample
// report overwrite a verdict, would destroy the only signal the agent loop reads
// to decide whether its push worked.
func TestSamplesDoNotDisturbTheStoredVerdict(t *testing.T) {
	c := NewConductor(0)
	c.Publish(`s("bd*2")`, "first")
	if _, err := c.RecordEvalResult(EvalResult{Version: 1, OK: true}); err != nil {
		t.Fatalf("verdict: %v", err)
	}

	loaded := true
	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &loaded}); err != nil {
		t.Fatalf("report: %v", err)
	}

	snap := c.Snapshot()
	if snap.LastEvalResult == nil || !snap.LastEvalResult.OK {
		t.Errorf("the stored verdict was disturbed by a sample report: %+v", snap.LastEvalResult)
	}
}

// TestTheSnapshotDoesNotAliasTheAudience proves the summary is a fresh copy.
//
// This is the same aliasing trap Stats, DriftMS and SamplesResolved each fell
// to, and each time the write SUCCEEDED — so nothing reported it and the next
// reader saw a value nobody measured. A snapshot is handed to handlers, the
// encoder and tests; if the slice or the tri-state pointer inside it aliased the
// store, one of them could rewrite what the next reader sees.
func TestTheSnapshotDoesNotAliasTheAudience(t *testing.T) {
	c := NewConductor(0)
	loaded := false
	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &loaded, Count: 5}); err != nil {
		t.Fatalf("report: %v", err)
	}

	first := c.Snapshot()
	if first.Samples == nil || len(first.Samples.Listeners) != 1 {
		t.Fatalf("no listener recorded: %+v", first.Samples)
	}

	// Write through everything the snapshot hands out.
	first.Samples.Loaded = 999
	first.Samples.Reporting = 999
	first.Samples.Listeners[0].ID = "tampered"
	first.Samples.Listeners[0].Count = 999
	if first.Samples.Listeners[0].Loaded != nil {
		*first.Samples.Listeners[0].Loaded = true
	}

	second := c.Snapshot()
	if second.Samples.Loaded != 0 || second.Samples.Reporting != 1 {
		t.Errorf("a write through the snapshot's summary reached the store: %+v", second.Samples)
	}
	if second.Samples.Listeners[0].ID != "L1" || second.Samples.Listeners[0].Count != 5 {
		t.Errorf("a write through the snapshot's listener reached the store: %+v", second.Samples.Listeners[0])
	}
	if second.Samples.Listeners[0].Loaded == nil || *second.Samples.Listeners[0].Loaded {
		t.Errorf("a write through the snapshot's tri-state reached the store: %+v", second.Samples.Listeners[0].Loaded)
	}
}

// TestTwoSnapshotsDoNotAliasEachOther proves successive reads are independent.
//
// Handing back the stored pointer would make two snapshots alias one another, so
// a caller mutating the first would silently rewrite the second's evidence. Each
// snapshot must get a fresh allocation every time.
func TestTwoSnapshotsDoNotAliasEachOther(t *testing.T) {
	c := NewConductor(0)
	loaded := true
	c.NoteListener("L1")
	if _, err := c.RecordListenerSamples("L1", ListenerSamples{Loaded: &loaded}); err != nil {
		t.Fatalf("report: %v", err)
	}

	first := c.Snapshot()
	second := c.Snapshot()
	if first.Samples == second.Samples {
		t.Fatal("two snapshots share one summary pointer; a caller mutating one would rewrite the other")
	}
	first.Samples.Listeners[0].ID = "tampered"
	if got := second.Samples.Listeners[0].ID; got != "L1" {
		t.Errorf("mutating the first snapshot changed the second's listener id to %q", got)
	}
}

// TestAnEmptyListenerIdIsRefused proves the id is actually checked.
//
// An empty id would key every nameless reporter together, and the store cannot
// tell whether it names a connection the server issued: the counter starts at 1,
// so "" was never a value the hub mints. Refusing it here is what keeps the
// "every record names a real connection" promise total rather than best-effort.
func TestAnEmptyListenerIdIsRefused(t *testing.T) {
	c := NewConductor(0)

	if _, err := c.RecordListenerSamples("", ListenerSamples{}); err == nil {
		t.Error("RecordListenerSamples accepted an empty listener id; " +
			"that would collapse every nameless reporter into one record")
	}
}
