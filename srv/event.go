package srv

// This file owns the listener event vocabulary: the ONE JSON shape every frame
// on /ws carries, and the Server.broadcast helper that puts it on the wire.
//
// The Hub stays deliberately ignorant of all of this. It moves opaque []byte and
// knows nothing about the Conductor or the wire format, so the encoding lives
// here, in the layer that owns both.
//
// The envelope is uniform on purpose. There is exactly ONE frame shape:
//
//	{"kind":"<what happened>","snapshot":{...}}
//
// A listener that connects mid-performance receives the same kind of frame as
// one that watches a change arrive, so the browser client has a single decode
// path and cannot get the two cases subtly different. The alternative — a bare
// snapshot on connect, an envelope on change — would force every client to
// branch on frame shape before it could read a field.
//
// The snapshot is a Snapshot VALUE, not a json.RawMessage or a re-shaped
// copy, and that is what keeps the two readings of the state identical. Go
// marshals struct fields in declaration order, so the snapshot object inside
// the envelope is produced by the same encoder, from the same value, with the
// same field order as the body GET /api/state returns. Encoding it any other way
// (a hand-built map, a bespoke struct, a re-serialised blob) is what would let
// the two drift apart, so there is deliberately no second shape to keep in
// sync. The harness asserts the two bodies are byte-identical, which is what
// stops that from quietly becoming true some other day.

import (
	"encoding/json"
	"log/slog"
)

// Event kinds. These are the complete vocabulary a listener can observe; a
// client switches on this string and never has to guess.
//
// The kinds are named for what the AGENT did, not for which endpoint it used,
// so two routes that produce the same listener-visible effect agree on one
// name.
const (
	// EventSnapshot is the opening frame: the full state, sent once when a
	// listener subscribes. Nothing changed; this is the catch-up.
	EventSnapshot = "snapshot"

	// EventCode is a new code document (POST /api/code): the version bumped
	// and the music may have changed.
	EventCode = "code"

	// EventMessage is new agent narration (POST /api/message): the same
	// version, the same music, different words on screen.
	EventMessage = "message"

	// EventTransport is a transport intent change (POST /api/hush or
	// /api/play). One kind for both: what a listener needs is the resulting
	// Playing flag, which the snapshot carries, and hush-then-play is a
	// transition rather than two distinct facts about the performance.
	EventTransport = "transport"

	// EventEvalResult is an ACCEPTED browser verdict on a published version
	// (POST /api/eval-result). A stale or rejected report is deliberately NOT
	// broadcast: see Server.broadcast's caller contract below.
	EventEvalResult = "eval-result"

	// EventListenerCount is a change to how many listeners are attached to this
	// performance (issue .3.7): somebody connected or left, or the hub dropped
	// somebody for not keeping up. It is a SIXTH kind rather than a fold into
	// one of the five above because a count change is not a change to the
	// performance: no new code, no new narration, no transport move, no new
	// verdict. Folding it in would force every client to infer "nothing about
	// the music changed" from a kind that otherwise means the opposite.
	//
	// It never bumps the version (the version belongs to the code document) and
	// it never tells the listener that caused it — the listener that just
	// connected has already been handed this exact count in its catch-up
	// snapshot, so a second copy of the same state would be noise on the one
	// frame every client reads first. See Hub's fanOut for the exclusion.
	EventListenerCount = "listener-count"
	// EventAnchor is a re-anchoring of the shared timeline
	// (POST /api/anchor): every listener was told to map its scheduler position
	// onto a new epoch and/or rate. It is a SEVENTH kind rather than a fold into
	// one of the six above because a timeline move is not a change to the
	// performance in the ordinary sense — the code, the narration, the transport
	// intent and the stored verdict are all untouched — but it IS a change every
	// client must act on immediately, or its next commit lands on a different bar
	// from everyone else's. Folding it into EventTransport would force clients to
	// infer "nothing about the music changed" from a kind that means the
	// opposite.
	//
	// Like the listener-count frame it never bumps the version (the version
	// belongs to the code document) and a REJECTED re-anchor is never broadcast,
	// so a listener can never be told to adopt a timeline the server refused to
	// store.
	EventAnchor = "anchor"

	// EventAgent is a change to whether an external agent is present
	// (POST /api/heartbeat, or the lease lapsing). It is an EIGHTH kind for the
	// same reason listener-count is a sixth: agent liveness is not a property of
	// the performance — the code, the narration, the transport intent and the
	// stored verdict are all untouched — but it IS something every listener must
	// act on immediately, or the status indicator reports a state the server has
	// already stopped believing.
	//
	// It is sent on exactly ONE occasion: the lease going from held to lapsed.
	// A heartbeat that renews an already-held lease broadcasts NOTHING, because
	// every listener already holds the active flag it would be told. That is the
	// same contract as an accepted-but-stale eval-result: an accepted write that
	// changed nothing observable is understood and deliberately not published.
	// Re-announcing on every tick would also let a heartbeat loop alone evict a
	// listener through the hub's bounded queue.
	//
	// It never bumps the version, for the same reason nothing else here does:
	// presence is not code.
	EventAgent = "agent"

	// EventDryRun asks every listener to evaluate a candidate that was NEVER
	// PUBLISHED, and is answered out of band by POST /api/dry-run-result rather
	// than by anything on this socket.
	//
	// It is a REQUEST, not a transition, which is why it is the one kind whose
	// meaning does not come from the snapshot it carries: the snapshot says the
	// performance is unchanged (which is the whole point), and the candidate
	// rides in the frame's optional `dryRun` member instead. Putting it in the
	// snapshot would make unpublished code visible as performance state.
	//
	// It never bumps the version and never enters history.
	EventDryRun = "dry-run"
)

// Event is the single frame shape sent to listeners. Snapshot holds the full
// Conductor state as of this event, so a listener never has to merge a partial
// delta into its own copy: it replaces what it holds. That is what makes a
// reconnecting or lagging listener safe rather than merely usually-right.
//
// DryRun is the single OPTIONAL member, and it exists because one kind needs to
// say something the snapshot cannot. Every other frame is fully described by its
// kind plus the snapshot, which is the property that lets a client decode any
// frame with one code path. A dry-run frame has to carry a candidate document
// that was never published, and the snapshot is the wrong place for it twice
// over: a snapshot is Conductor state, and the candidate is deliberately NOT
// state. So the one request-shaped frame carries one extra field, omitted (not
// null) on every other kind.
//
// omitempty rather than a pointer-only tag is what keeps the other frames
// BYTE-IDENTICAL to what they were before dry-run existed: a nil pointer plus
// omitempty emits nothing at all, so a client decoding the documented two-field
// envelope sees exactly two fields on every kind it already knew.
type Event struct {
	Kind     string         `json:"kind"`
	Snapshot Snapshot       `json:"snapshot"`
	DryRun   *DryRunRequest `json:"dryRun,omitempty"`
}

// broadcast encodes one event for listeners and hands it to the Hub.
//
// The caller contract is load-bearing and is the reason this is a helper rather
// than a raw Hub.Broadcast at each call site: broadcast MUST be called only
// AFTER a mutation has been ACCEPTED. A rejected request (empty code, oversized
// or malformed body, a verdict for an unpublished version) and an
// accepted-but-ignored one (a stale eval-result) must broadcast NOTHING.
// Telling listeners about a change that did not happen is worse than telling
// them nothing, because they cannot tell the two apart on the wire.
//
// Broadcasting does not block on any subscriber: the Hub drops a client that
// cannot keep up rather than waiting for it, so a wedged listener can never
// delay an API response.
//
// There is exactly ONE caller of this helper per accepted mutation, and one
// caller that is not this at all: the listener-count frame (issue .3.7) is
// built by the Conductor's count hook and handed to the hub, which fans it out
// itself. See hub.go for why that indirection exists — it is what lets the
// count reach listeners without the hook ever re-entering the hub it is running
// on.
func (s *Server) broadcast(kind string, snap Snapshot) {
	s.Hub.Broadcast(s.encodeEvent(kind, snap))
}

// broadcastDryRun asks every listener to evaluate a candidate, without changing
// any state.
//
// It goes through the same Hub and the same encoder as every other frame, so a
// listener has exactly one decode path; what differs is only that the snapshot
// attached to it is the current, UNCHANGED one. That is worth stating plainly,
// because a dry-run frame is the only one whose snapshot does not describe what
// the frame is about: the code in it was never published and must never reach a
// live repl.
//
// It deliberately does not go through broadcast(), which has no way to attach
// the optional member. The caller has already validated and confirmed a
// listener exists, so this is not an accepted-mutation path and needs none of
// broadcast's "only after commit" discipline — there is no commit.
func (s *Server) broadcastDryRun(req DryRunRequest) {
	msg, err := json.Marshal(Event{
		Kind:     EventDryRun,
		Snapshot: s.Conductor.Snapshot(),
		DryRun:   &req,
	})
	if err != nil {
		slog.Warn("encode dry-run event", "id", req.ID, "error", err)
		return
	}
	s.Hub.Broadcast(msg)
}

// encodeEvent is the single place a frame is built. broadcast and the
// snapshot-on-connect path both go through it, so a listener cannot be able to
// receive the two kinds of frame in two different shapes — the failure mode
// where the catch-up snapshot is encoded slightly differently from a change
// event, and every client grows a special case for it.
//
// It returns nil if marshalling fails, which for a Snapshot of stored scalars
// and already-parsed opaque JSON is unreachable. A nil broadcast is a dropped
// message rather than a corrupt one: subscribers receive nothing for that event
// instead of an unparseable frame.
func (s *Server) encodeEvent(kind string, snap Snapshot) []byte {
	msg, err := json.Marshal(Event{Kind: kind, Snapshot: snap})
	if err != nil {
		// A dropped broadcast is a silent wrongness, so it is logged rather
		// than swallowed — but it is not fatal, because the Conductor state is
		// already correct and the next event will carry it again.
		slog.Warn("encode listener event", "kind", kind, "error", err)
		return nil
	}
	return msg
}
