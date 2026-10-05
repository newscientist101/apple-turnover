package srv

// Audience sample state over HTTP and on the wire (strudel-agent-f79).
//
// These tests drive the real route tree and a real listener socket, because two
// of the claims under test are about the ROUTE existing and about the FRAME
// listeners receive. A unit test calling Conductor.RecordListenerSamples would
// pass against a server that mounted nothing and broadcast nothing.
//
// The broadcast rule is the one borrowed from sync-result and eval-result, with
// one addition that is the whole point of this feature: a report that changes
// NOTHING is accepted and silent. The store, not the handler, decides that,
// because the store is the only place that knows the previous value.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// samplesListener connects one real listener and returns the id the server
// assigned it plus the socket to read further frames from.
//
// The id comes from the connection's own `listener` frame rather than from
// anything the test made up. That is what keeps these tests honest about the
// route being reachable the way a browser reaches it, and it is the only kind of
// id the server will accept a report under — so a report built from an invented
// id would be testing nothing.
//
// wsListener consumes both connect frames before returning, so the id is captured
// with a dedicated dial here rather than read afterwards from a stream that has
// already moved past it.
func samplesListener(t *testing.T, s *Server) (string, *websocket.Conn) {
	t.Helper()
	ts := httptest.NewServer(s.routes())
	t.Cleanup(func() { wsCloseServer(t, ts) })

	conn := dialListener(t, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws")
	ev := wsReadEvent(t, conn, "listener identity")
	if ev.Kind != EventListener {
		t.Fatalf("first frame on a fresh connection = %q, want %q", ev.Kind, EventListener)
	}
	if ev.Listener == nil || ev.Listener.ID == "" {
		t.Fatalf("the listener frame carried no usable id: %+v", ev.Listener)
	}
	// Consume the catch-up snapshot so it cannot be mistaken for a later frame,
	// and wait for the count to land the way every listener harness does.
	wsReadEvent(t, conn, "connect snapshot")
	waitFor(t, 2*time.Second, func() bool {
		return s.Conductor.Snapshot().ListenerCount >= 1
	}, "listener never registered on the conductor")
	return ev.Listener.ID, conn
}

// samplesState reads the audience summary back the way an agent does: from
// GET /api/state, which is the only path a report is documented on.
func samplesState(t *testing.T, s *Server) *SamplesSummary {
	t.Helper()
	w := apiRequest(t, s, http.MethodGet, "/api/state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/state: %d %q", w.Code, w.Body.String())
	}
	var snap Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode state %q: %v", w.Body.String(), err)
	}
	return snap.Samples
}

// samplesLoadedBody is a report body for one listener with an explicit tri-state.
func samplesLoadedBody(id string, loaded bool, count int) string {
	return `{"listenerId":"` + id + `","loaded":` + boolJSON(loaded) + `,"count":` + itoa(count) + `}`
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// samplesSnapshot reads the whole snapshot, for the assertions that are about the
// performance rather than the audience.
func samplesSnapshot(t *testing.T, s *Server) Snapshot {
	t.Helper()
	w := apiRequest(t, s, http.MethodGet, "/api/state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/state: %d %q", w.Code, w.Body.String())
	}
	var snap Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode state %q: %v", w.Body.String(), err)
	}
	return snap
}

// listenerIDFrom returns the one connected listener's id, read back out of the
// SERVER's own view of who it admitted.
//
// It is deliberately not the variable the connect helper returned. Reading it from
// the server means these tests would FAIL if the id on the wire and the id the
// store admits ever diverged — which is the failure that would make every report
// silently un-attributable, and which reusing a local would hide.
func listenerIDFrom(t *testing.T, s *Server, _ *websocket.Conn) string {
	t.Helper()
	if n := s.Conductor.Snapshot().ListenerCount; n != 1 {
		t.Fatalf("expected exactly one connected listener, got %d", n)
	}
	ids := s.Conductor.knownListenerIDs()
	if len(ids) != 1 {
		t.Fatalf("the server admitted %d listener ids, want 1: %v", len(ids), ids)
	}
	return ids[0]
}

// TestSamplesReportIsStoredAndReadBack proves the endpoint accepts a report and
// that the stored result is readable from GET /api/state.
//
// This is the whole point of the endpoint: an agent polls the snapshot, it does
// not read a push response. Asserting the POST succeeded without reading the
// state back would prove the server accepted a body and nothing more.
func TestSamplesReportIsStoredAndReadBack(t *testing.T) {
	s := New()
	id, _ := samplesListener(t, s)

	w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody(id, false, 0))
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/samples: %d %q, want 200", w.Code, w.Body.String())
	}
	var ack apiEvalAck
	if err := json.Unmarshal(w.Body.Bytes(), &ack); err != nil {
		t.Fatalf("decode ack %q: %v", w.Body.String(), err)
	}
	if !ack.Accepted {
		t.Errorf("ack = %+v, want accepted", ack)
	}

	got := samplesState(t, s)
	if got == nil {
		t.Fatal("GET /api/state.samples is absent after a listener reported; " +
			"the audience answer must be readable, not just accepted")
	}
	if got.Reporting != 1 || got.Loaded != 0 {
		t.Errorf("summary = %+v, want reporting:1 loaded:0", got)
	}
	if len(got.Listeners) != 1 || got.Listeners[0].ID != id {
		t.Fatalf("the summary does not name the listener that reported: %+v", got.Listeners)
	}
	if got.Listeners[0].Loaded == nil || *got.Listeners[0].Loaded {
		t.Errorf("listener loaded = %v, want an explicit false", got.Listeners[0].Loaded)
	}
}

// TestTwoListenersDisagreeingAreBothVisible proves the feature's reason for
// existing, through the real route.
//
// One verdict cannot express this: lastEvalResult stores whichever listener
// reported LAST, so an agent reading `samplesResolved:true` from the tab that has
// the pack learns nothing about the tab that does not. This is the case the whole
// bead was filed for, and it is asserted as a claim about the AUDIENCE rather
// than about any one connection's document.
func TestTwoListenersDisagreeingAreBothVisible(t *testing.T) {
	s := New()
	withPack, _ := samplesListener(t, s)

	// A second listener needs its own server socket, so this test drives the hub
	// directly for the second id rather than dialling twice into one listener slot.
	second, err := s.Hub.Subscribe()
	if err != nil {
		t.Fatalf("subscribe second listener: %v", err)
	}
	s.Conductor.NoteListener(second.ID())

	if w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody(withPack, true, 412)); w.Code != http.StatusOK {
		t.Fatalf("POST /api/samples (loaded): %d %q", w.Code, w.Body.String())
	}
	if w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody(second.ID(), false, 0)); w.Code != http.StatusOK {
		t.Fatalf("POST /api/samples (unloaded): %d %q", w.Code, w.Body.String())
	}

	got := samplesState(t, s)
	if got == nil {
		t.Fatal("no summary with two disagreeing listeners")
	}
	if got.Reporting != 2 {
		t.Errorf("reporting = %d, want 2", got.Reporting)
	}
	if got.Loaded != 1 {
		t.Errorf("loaded = %d, want 1: exactly one of the two has the pack, and that partial answer is the point", got.Loaded)
	}
	byID := map[string]ListenerSampleView{}
	for _, l := range got.Listeners {
		byID[l.ID] = l
	}
	if l, ok := byID[withPack]; !ok || l.Loaded == nil || !*l.Loaded || l.Count != 412 {
		t.Errorf("the listener holding the pack is described as %+v", l)
	}
	if l, ok := byID[second.ID()]; !ok || l.Loaded == nil || *l.Loaded {
		t.Errorf("the listener without the pack is described as %+v, want an explicit false", l)
	}
}

// TestAnUnknownSampleReportIsRefused proves a report the server cannot attribute
// stores nothing.
//
// A stale id replayed by a reconnecting browser, or one a client invented, names
// no connection. Accepting it would let any client assert things about an audience
// it is not part of — which is the confusion identity exists to remove, arriving
// through the new door.
func TestAnUnknownSampleReportIsRefused(t *testing.T) {
	s := New()

	w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody("L999", true, 10))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/samples with an id the server never issued: %d %q, want 400", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "listener") {
		t.Errorf("the error does not name the problem: %q", w.Body.String())
	}
	if got := samplesState(t, s); got != nil {
		t.Errorf("a refused report left state behind: %+v", got)
	}
}

// TestAMissingListenerIdIsRefused proves the field is actually required.
//
// An empty id would key every nameless reporter together, which is the anonymous
// listener this feature replaced.
func TestAMissingListenerIdIsRefused(t *testing.T) {
	s := New()
	id, _ := samplesListener(t, s)

	w := apiRequest(t, s, http.MethodPost, "/api/samples", `{"loaded":true}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("POST /api/samples with no listenerId: %d %q, want 400", w.Code, w.Body.String())
	}
	if got := samplesState(t, s); got != nil {
		t.Errorf("a refused report left state behind: %+v", got)
	}
	_ = id
}

// TestAnUnknownTriStateIsNotFalse proves absence stays distinct from false, on
// the wire as well as in the store.
//
// This is the SamplesResolved trap arriving at the audience layer: a browser with
// no registry has learned nothing, and a server that stored that as false would
// tell an agent its listeners are missing samples on the strength of a check that
// never ran.
func TestAnUnknownTriStateIsNotFalse(t *testing.T) {
	s := New()
	id, _ := samplesListener(t, s)

	w := apiRequest(t, s, http.MethodPost, "/api/samples",
		`{"listenerId":"`+id+`","count":0}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/samples with no loaded field: %d %q", w.Code, w.Body.String())
	}

	got := samplesState(t, s)
	if got == nil || len(got.Listeners) != 1 {
		t.Fatalf("the report was not stored: %+v", got)
	}
	if got.Listeners[0].Loaded != nil {
		t.Errorf("loaded = %v, want absent: a listener that could not check must not read as false", *got.Listeners[0].Loaded)
	}
	if got.Loaded != 0 {
		t.Errorf("loaded count = %d, want 0: an unknown answer is not a loaded one", got.Loaded)
	}
	// And the raw bytes must OMIT the key rather than send null, because a
	// zero-length omitempty is what drops it.
	state := apiRequest(t, s, http.MethodGet, "/api/state", "")
	if strings.Contains(state.Body.String(), `"loaded":null`) {
		t.Errorf("the summary serialises an unknown as null; it must be absent: %q", state.Body.String())
	}
}

// TestASamplesChangeIsBroadcast proves the frame reaches a listener when the
// audience really changed.
//
// A report accepted and stored but never announced would leave every listener
// holding a stale picture of what the audience can hear, which is the same class
// of defect as the drift this repository already fixed: measured, stored, and
// dropped on the floor before anyone could read it.
func TestASamplesChangeIsBroadcast(t *testing.T) {
	s := New()
	_, conn := samplesListener(t, s)
	id := listenerIDFrom(t, s, conn)

	w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody(id, true, 412))
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/samples: %d %q", w.Code, w.Body.String())
	}

	ev := wsReadEvent(t, conn, "samples frame")
	if ev.Kind != EventSamples {
		t.Fatalf("frame kind = %q, want %q", ev.Kind, EventSamples)
	}
	if ev.Snapshot.Samples == nil || ev.Snapshot.Samples.Loaded != 1 {
		t.Errorf("the samples frame does not carry the new audience state: %+v", ev.Snapshot.Samples)
	}
}

// TestAnUnchangedReReportBroadcastsNothing proves the transition rule on the wire.
//
// This is the property that keeps a browser free to poll. Without it, a client
// re-reporting its registry every few seconds would put a frame on the wire every
// few seconds forever; listeners' queues are bounded at 64, so a busy tab would
// evict its peers. The store decides, so this asserts the decision reached the
// wire rather than merely returning 200.
func TestAnUnchangedReReportBroadcastsNothing(t *testing.T) {
	s := New()
	_, conn := samplesListener(t, s)
	id := listenerIDFrom(t, s, conn)

	body := samplesLoadedBody(id, true, 412)
	if w := apiRequest(t, s, http.MethodPost, "/api/samples", body); w.Code != http.StatusOK {
		t.Fatalf("first report: %d %q", w.Code, w.Body.String())
	}
	// The first report IS a transition, so its frame is expected and drained here.
	wsReadEvent(t, conn, "first samples frame")

	// The second report says exactly the same thing.
	w := apiRequest(t, s, http.MethodPost, "/api/samples", body)
	if w.Code != http.StatusOK {
		t.Fatalf("identical re-report: %d %q, want 200: an accepted no-op is still an accepted report", w.Code, w.Body.String())
	}

	wsExpectQuiet(t, conn,
		"an identical re-report was broadcast; a frame must describe a transition, not an accepted write",
		250*time.Millisecond)
}

// TestARealSampleChangeBroadcastsAfterASilentOne proves the rule cuts both ways.
//
// The counterpart: once a listener is in a state, a genuine change from it must
// still be announced. A rule that simply never broadcast a change would satisfy
// the no-op test above and fail the entire feature — the agent would never learn
// that a listener loaded the pack it needs.
func TestARealSampleChangeBroadcastsAfterASilentOne(t *testing.T) {
	s := New()
	_, conn := samplesListener(t, s)
	id := listenerIDFrom(t, s, conn)

	if w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody(id, false, 0)); w.Code != http.StatusOK {
		t.Fatalf("first report: %d %q", w.Code, w.Body.String())
	}
	wsReadEvent(t, conn, "first samples frame")

	if w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody(id, true, 412)); w.Code != http.StatusOK {
		t.Fatalf("change report: %d %q", w.Code, w.Body.String())
	}

	ev := wsReadEvent(t, conn, "changed samples frame")
	if ev.Kind != EventSamples {
		t.Fatalf("frame kind = %q, want %q: loading a pack is a transition every listener must see", ev.Kind, EventSamples)
	}
}

// TestARefusedSampleReportBroadcastsNothing proves the refused path is silent.
//
// The rule every accepted-mutation path in this server already follows: a
// listener told about sample state the server refused to store would be
// describing a claim that does not exist.
func TestARefusedSampleReportBroadcastsNothing(t *testing.T) {
	s := New()
	_, conn := samplesListener(t, s)

	if w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody("L999", true, 1)); w.Code != http.StatusBadRequest {
		t.Fatalf("refused report: %d %q, want 400", w.Code, w.Body.String())
	}

	wsExpectQuiet(t, conn,
		"a refused report was broadcast; listeners must never be told about a claim the server refused",
		250*time.Millisecond)
}

// TestSamplesDoNotBumpTheVersionOrEnterHistory proves the audience claim is
// evidence, through the real route rather than by calling the store.
//
// The rule sync-results already follow: a listener reporting on its packs is not
// an edit, and a version bump would put a browser's registry into an agent's code
// history and make every reconnection look like a new document.
func TestSamplesDoNotBumpTheVersionOrEnterHistory(t *testing.T) {
	s := New()
	id, _ := samplesListener(t, s)

	if w := apiRequest(t, s, http.MethodPost, "/api/code", `{"code":"s(\"bd*2\")"}`); w.Code != http.StatusOK {
		t.Fatalf("POST /api/code: %d %q", w.Code, w.Body.String())
	}
	before := samplesSnapshot(t, s)

	if w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody(id, true, 412)); w.Code != http.StatusOK {
		t.Fatalf("POST /api/samples: %d %q", w.Code, w.Body.String())
	}
	after := samplesSnapshot(t, s)

	if after.Version != before.Version {
		t.Errorf("version moved from %d to %d across a sample report", before.Version, after.Version)
	}
	if len(after.History) != len(before.History) {
		t.Errorf("history grew from %d to %d entries across a sample report", len(before.History), len(after.History))
	}
}

// TestADepartedListenerStopsAnsweringForTheAudience proves the disconnect rule
// through the real socket.
//
// A tab that closes while holding a pack must stop counting, or an agent reads
// `loaded:1` for an audience that has gone. This is the third thing the bead says
// the server could not do, and it is why identity had to come first.
func TestADepartedListenerStopsAnsweringForTheAudience(t *testing.T) {
	s := New()
	_, conn := samplesListener(t, s)
	id := listenerIDFrom(t, s, conn)

	if w := apiRequest(t, s, http.MethodPost, "/api/samples", samplesLoadedBody(id, true, 412)); w.Code != http.StatusOK {
		t.Fatalf("POST /api/samples: %d %q", w.Code, w.Body.String())
	}
	wsReadEvent(t, conn, "samples frame")
	if got := samplesState(t, s); got == nil || got.Loaded != 1 {
		t.Fatalf("precondition: the listener is not recorded as loaded: %+v", got)
	}

	// Close the socket and wait for the server to have processed the departure.
	conn.Close(websocket.StatusNormalClosure, "")
	wsWaitForSubscribers(t, s.Hub, 0)

	// The forget is what must happen, and it happens on the handler's deferred
	// path; the count reaching zero proves that handler ran to completion.
	deadline := time.Now().Add(2 * time.Second)
	var last *SamplesSummary
	for {
		last = samplesState(t, s)
		if last == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a departed listener is still answering for the audience: %+v", last)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestListenerIdentityIsNotBroadcast proves one listener's id reaches one
// listener.
//
// This is the exclusivity of `Hub.Direct`, and it is not a detail. The identity
// frame carries the recipient's OWN id, so broadcasting it would tell every
// browser which listener it is not — and then every report each of them sent
// would be keyed to somebody else's connection, which is precisely the confusion
// identity was introduced to remove. A server that got this wrong would look
// perfectly healthy: every report would be accepted, because every browser would
// hold a real id, just not its own.
//
// So the bystander is the assertion that matters here, not the recipient.
func TestListenerIdentityIsNotBroadcast(t *testing.T) {
	s := New()

	// samplesListener already consumes both connect frames, so anything this
	// connection receives from here on is a frame it should not have been sent.
	_, bystander := samplesListener(t, s)

	// A second listener now connects, which is the event that would be fanned out.
	// The helper returns the id it was given, having already consumed both connect
	// frames -- so a second read here would block on a frame that has been and gone.
	secondID, _ := samplesListener(t, s)
	if secondID == "" {
		t.Fatal("the second connection received no id of its own")
	}

	// The bystander IS told the count changed -- that is the documented
	// listener-count behaviour and it is owed. What it must never be told is the
	// other connection's IDENTITY, so the check is on the kind rather than on
	// silence: an exact-silence assertion here would be wrong, and would also pass
	// against a server that broadcast identity AND dropped the count frame it is
	// supposed to send.
	//
	// The second id is the thing to look for: a frame carrying somebody else's id
	// is the defect, whether or not it calls itself a listener frame.
	ev := wsReadEvent(t, bystander, "bystander reaction to a second connect")
	if ev.Kind == EventListener {
		t.Fatalf("a %q frame carrying id %+v reached a connection that was not its recipient; "+
			"every browser would be handed an id belonging to somebody else",
			ev.Kind, ev.Listener)
	}
	if ev.Listener != nil {
		t.Fatalf("a %q frame carried a listener member: %+v; identity must appear on its own kind only",
			ev.Kind, ev.Listener)
	}
	if ev.Kind != EventListenerCount {
		t.Errorf("bystander saw %q, want the documented %q: the count change is owed to every listener",
			ev.Kind, EventListenerCount)
	}
}
