package srv

// Dry-run: validate a candidate WITHOUT publishing it (strudel-agent-uvj.16).
//
// The server never evaluates JavaScript, so a dry-run cannot be a client-side
// flag: something has to ask a connected BROWSER to evaluate a candidate that
// was never published. This file owns that rendezvous -- the registry of
// in-flight dry-runs and the wait that an agent's request blocks on.
//
// It lives on the Server and deliberately NOT on the Conductor. A dry-run is not
// performance state: the whole promise is that the version, the history and the
// stored verdict do not move, and the surest way to keep a promise like that is
// to give the thing that could break it nowhere to write. A Conductor field
// would also have to appear in Snapshot to be reachable, which is precisely the
// surface dry-run must not touch.
//
// Three properties are load-bearing:
//
//  1. Nothing is stored. Resolve hands the verdict to the waiting request and
//     the entry is gone; there is no dry-run history to read back, because a
//     verdict about unpublished code is not a fact about the performance.
//  2. The first answer wins and the losers are told so. Every listener
//     evaluates and every listener reports, so Resolve must be idempotent-safe
//     under exactly one winner rather than last-write-wins.
//  3. Every exit path removes the entry. Answered, timed out, or client gone:
//     a registry that leaks one key and one buffered channel per abandoned
//     dry-run is a slow leak an agent loop can trigger on every iteration.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// DefaultDryRunTimeout bounds how long POST /api/dry-run waits for a browser to
// answer.
//
// It is a SERVER-side bound rather than only the caller's, because the caller is
// an agent that may have chosen no timeout at all: without one, a dry-run sent
// to a wedged or backgrounded tab would hold a handler open indefinitely. The
// default sits above the CLI's own 10s per-request timeout so that the CLI's
// deadline is what normally reports the failure -- the server bound is the
// backstop for callers that did not set one.
const DefaultDryRunTimeout = 30 * time.Second

// DryRunRequest is the candidate a dry-run asks a browser to evaluate.
//
// It travels on the wire as the optional `dryRun` member of a dry-run frame,
// NOT inside the snapshot: the candidate was never published, and putting it in
// a Snapshot would make unpublished code visible as performance state.
type DryRunRequest struct {
	// ID correlates this dry-run with the report that answers it. It is the
	// agent's request id, not a version: versions identify published documents
	// and a dry-run publishes nothing, so reusing the version counter here
	// would either collide with the very code being protected or imply a
	// revision that does not exist.
	ID int64 `json:"id"`

	// Code is the candidate to validate. It is never stored and never becomes
	// the live document.
	Code string `json:"code"`
}

// DryRunVerdict is the answer to one dry-run: exactly the shape a real push
// produces, so an agent can read a dry-run and a stored verdict the same way.
//
// Stats is opaque client-supplied JSON with the same rule as EvalResult.Stats —
// it must be a JSON OBJECT, not any JSON value — so a consumer can rely on
// stats.haps existing rather than silently reading undefined. It is normalized
// through the same helper, so "absent", "empty" and "null" all mean no stats.
type DryRunVerdict struct {
	ID    int64           `json:"id"`
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Stats json.RawMessage `json:"stats,omitempty"`
}

// ErrNoDryRun is returned by Resolve for an id that was never registered, has
// already been answered, or has expired. All three mean the same thing to a
// browser: nobody is waiting for this report, so sending it achieves nothing.
var ErrNoDryRun = errors.New("unknown dry-run")

// errDryRunTimeout is what a dry-run returns when a connected listener never
// answers. It is distinct from ErrNoDryRun (which is about a REPORT arriving for
// nobody) so the two failures cannot be reported the same way to an agent: one
// means "the evaluator did not reply", the other "you reported too late".
var errDryRunTimeout = errors.New("no listener answered the dry-run in time")

// dryRunEntry is one in-flight request. The channel is buffered so a browser
// reporting an instant before the request reaches its wait never blocks: the
// report is the one thing here that arrives from outside, and it must not be
// able to wedge an unrelated handler.
type dryRunEntry struct {
	req    DryRunRequest
	answer chan DryRunVerdict
}

// dryRunRegistry holds the in-flight dry-runs.
//
// One mutex rather than a per-entry lock: the map is small (one entry per
// concurrent agent) and every operation is O(1), so finer locking would buy
// contention nobody can observe.
type dryRunRegistry struct {
	mu      sync.Mutex
	next    int64
	entries map[int64]*dryRunEntry
}

func newDryRunRegistry() *dryRunRegistry {
	return &dryRunRegistry{entries: make(map[int64]*dryRunEntry)}
}

// Register allocates a correlation id and returns the channel the verdict for it
// should arrive on. The id is monotonic and never reused within a process, so a
// late report for an expired dry-run can never be mistaken for a live one —
// which is what would happen if ids were reused after an expiry.
func (r *dryRunRegistry) Register(code string) (DryRunRequest, <-chan DryRunVerdict) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.next++
	entry := &dryRunEntry{
		req:    DryRunRequest{ID: r.next, Code: code},
		answer: make(chan DryRunVerdict, 1),
	}
	r.entries[entry.req.ID] = entry
	return entry.req, entry.answer
}

// Resolve delivers a verdict to the waiting request and removes the entry.
//
// The removal is what makes it first-answer-wins: the losers find no entry and
// get ErrNoDryRun, so a slow second listener cannot overwrite the answer the
// agent has already been given. Delivery is non-blocking because the channel is
// buffered and the entry is already gone, so a report arriving for an id whose
// waiter has timed out is dropped rather than parked forever.
func (r *dryRunRegistry) Resolve(v DryRunVerdict) error {
	r.mu.Lock()
	entry, ok := r.entries[v.ID]
	if ok {
		delete(r.entries, v.ID)
	}
	r.mu.Unlock()

	if !ok {
		return ErrNoDryRun
	}
	select {
	case entry.answer <- v:
	default:
		// The waiter has already given up and its slot is buffered but unread.
		// Dropping here is correct: there is nobody left to tell, and blocking
		// would wedge whichever handler the browser reached us through.
	}
	return nil
}

// Cancel removes an entry whose request will never be answered — the caller
// timed out, or the client disconnected. It is separate from Resolve because
// there is no verdict to deliver, and because a handler that gave up must still
// not leave its key behind.
func (r *dryRunRegistry) Cancel(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, id)
}

// Len reports how many dry-runs are in flight. Only tests read it, and it
// exists so "the registry is emptied on every exit path" is observable rather
// than inferred.
func (r *dryRunRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// awaitRegisteredDryRun blocks until a verdict arrives, the server-side timeout
// expires, or the caller's own context is done — and removes the entry on every
// one of those paths.
//
// The three ways out are all bounded, which is the whole no-hang requirement:
// ctx.Done covers an agent that gave up, the timer covers a browser that never
// answers, and Resolve covers the happy path. The timer is a plain time.Timer
// rather than a ticker because there is nothing to repeat.
//
// It takes an already-registered entry rather than registering itself, because
// the candidate must be BROADCAST between allocating the id and waiting on it —
// otherwise a browser that answers instantly could find an id nobody is
// listening for yet.
func (s *Server) awaitRegisteredDryRun(ctx context.Context, req DryRunRequest, answer <-chan DryRunVerdict) (DryRunVerdict, error) {
	// Resolve removes the entry on the happy path, so this Cancel only has work
	// to do on the two failure paths. It is safe to call unconditionally:
	// deleting a missing key is a no-op.
	defer s.dryRuns.Cancel(req.ID)

	timeout := s.dryRunTimeout
	if timeout <= 0 {
		timeout = DefaultDryRunTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case v := <-answer:
		return v, nil
	case <-timer.C:
		return DryRunVerdict{}, errDryRunTimeout
	case <-ctx.Done():
		return DryRunVerdict{}, ctx.Err()
	}
}
