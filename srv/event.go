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
)

// Event is the single frame shape sent to listeners. Snapshot holds the full
// Conductor state as of this event, so a listener never has to merge a partial
// delta into its own copy: it replaces what it holds. That is what makes a
// reconnecting or lagging listener safe rather than merely usually-right.
type Event struct {
	Kind     string   `json:"kind"`
	Snapshot Snapshot `json:"snapshot"`
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
func (s *Server) broadcast(kind string, snap Snapshot) {
	s.Hub.Broadcast(s.encodeEvent(kind, snap))
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
