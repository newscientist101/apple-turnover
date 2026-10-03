package srv

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultHistoryLimit is the number of recent code versions retained when a
// Conductor is constructed with a non-positive limit.
const DefaultHistoryLimit = 32

// DefaultCPS is the anchor's initial cycles-per-second. Strudel's own default
// is 0.5 cps, i.e. a two-second cycle.
const DefaultCPS = 0.5

// Anchor ties the one shared performance timeline to wall-clock time. Clients
// derive their scheduler position from EpochMS + CPS so listeners sharing a
// page do not drift apart.
type Anchor struct {
	EpochMS int64   `json:"epochMs"`
	CPS     float64 `json:"cps"`
}

// ErrUnknownVersion is returned by RecordEvalResult when the report names a
// version that has never been published (including version 0, the empty
// document, and any version from the future).
var ErrUnknownVersion = errors.New("unknown version")

// EvalResult is a browser's report on whether one published version actually
// evaluated in its sandbox repl. The Go server never evaluates JavaScript; it
// only stores and forwards these reports so the external agent can close its
// feedback loop. Stats are opaque client-supplied JSON (e.g. hap counts) and
// are echoed back to /api/state verbatim.
type EvalResult struct {
	Version int64           `json:"version"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Stats   json.RawMessage `json:"stats,omitempty"`
	EpochMS int64           `json:"epochMs"`
}

// Version is a single published revision of the live code document.
type Version struct {
	Version int64  `json:"version"`
	Code    string `json:"code"`
	Message string `json:"message"`
	EpochMS int64  `json:"epochMs"`
}

// AgentPresence is what the server knows about the external agent's liveness.
//
// It is a VALUE struct on Snapshot, never a pointer, for the same reason
// History is a slice built per read: Snapshot must not expose mutable internal
// state, and a pointer field would alias whatever the Conductor handed out.
// Returning it by value makes aliasing impossible to express.
//
// Active is DERIVED at read time from LastSeenMS and the Conductor's clock, not
// stored. See conductor_presence_test.go for why a stored boolean is the wrong
// shape.
type AgentPresence struct {
	// Active is true iff an agent heartbeat arrived within AgentTTL of now.
	Active bool `json:"active"`

	// LastSeenMS is when the most recent heartbeat arrived, in epoch
	// milliseconds, or 0 if no agent has EVER been seen. Zero is reserved as
	// the "never" marker and is deliberately distinct from a heartbeat that
	// genuinely landed at the Unix epoch.
	LastSeenMS int64 `json:"lastSeenMs"`
}

// Snapshot is a copy of the Conductor's state at one instant.
type Snapshot struct {
	Version          int64         `json:"version"`
	Code             string        `json:"code"`
	LastAgentMessage string        `json:"lastAgentMessage"`
	Anchor           Anchor        `json:"anchor"`
	History          []Version     `json:"history"`
	Playing          bool          `json:"playing"`
	ListenerCount    int           `json:"listenerCount"`
	LastEvalResult   *EvalResult   `json:"lastEvalResult"`
	Agent            AgentPresence `json:"agent"`
}

// Conductor owns the single live performance. It is safe for concurrent use:
// Publish takes the write lock, Snapshot the read lock.
type Conductor struct {
	mu sync.RWMutex

	version int64
	code    string
	message string
	anchor  Anchor

	// playing is transport intent, not audio: the server only records and
	// broadcasts whether the performance is running or hushed.
	playing bool

	// listeners is pushed in by the WebSocket hub's count hook (issue .3.5),
	// which reports the live subscriber count before acknowledging each change.
	// It is always serialised.
	listeners int

	lastEval *EvalResult

	// agentLastSeenMS is the lease: when an agent last proved it was alive.
	// 0 means no agent has ever been seen. There is deliberately no companion
	// `agentActive` boolean — see AgentPresence.
	agentLastSeenMS int64

	// clockOffsetMS offsets the Conductor's clock. It is how a test drives DECAY
	// — ageing a stored heartbeat without moving it — which real elapsed time
	// does and rewinding a timestamp does not.
	//
	// It is an atomic rather than a bare int64 because the lease sweeper reads
	// the clock on its own goroutine while a test advances it from the test's.
	// The alternative, a func() int64 field swapped before the sweeper starts,
	// still races as soon as a test wants to move time while the sweeper is
	// running — which is exactly what reproducing real decay requires.
	clockOffsetMS atomic.Int64

	history  []Version
	histLen  int
	histNext int
}

// NewConductor returns an empty Conductor at version 0.
func NewConductor(historyLimit int) *Conductor {
	if historyLimit <= 0 {
		historyLimit = DefaultHistoryLimit
	}
	return &Conductor{
		history: make([]Version, historyLimit),
		anchor:  Anchor{EpochMS: time.Now().UnixMilli(), CPS: DefaultCPS},
		// A fresh conductor is un-hushed: nothing has been sounded yet, but the
		// transport is not muted either.
		playing: true,
	}
}

// nowMS is the Conductor's clock: the wall clock plus any test offset. It is the
// single place time enters the Conductor.
func (c *Conductor) nowMS() int64 {
	return time.Now().UnixMilli() + c.clockOffsetMS.Load()
}

// advanceClock moves this Conductor's notion of "now" forward by ms, returning
// the new reading. Only tests call it; it exists so a lease can DECAY under test
// the way it decays in production.
//
// Ageing the clock is deliberately not the same operation as ageing the stored
// timestamp. Advancing time leaves lastSeenMs exactly where it was, which is
// what real decay looks like to a reader of the state; rewriting lastSeenMs
// moves the very field a sweeper may be keyed on, and would make a sweeper that
// reacts to "a value I have not seen" pass without ever handling an agent that
// simply stopped beating.
func (c *Conductor) advanceClock(ms int64) int64 {
	return c.clockOffsetMS.Add(ms)
}

// AgentTTL is how long an agent's heartbeat keeps it counted as present.
//
// It is a LIVENESS WINDOW, not a session: the agent holds no connection, so
// this is the only thing the server can honestly say. The tradeoff it encodes
// is liveness against false alarms, and it is deliberately asymmetric — a
// window too short reports "agent gone" while the agent is merely mid-thought
// between two calls, which is worse than useless because it trains an operator
// to ignore the indicator; a window too long delays the report of a real crash.
//
// 15s suits an agent whose loop is: push code, wait for a browser verdict, push
// again. That cycle is normally a second or two, so 15s is several missed
// heartbeats before anyone is told the agent is gone, and short enough that a
// crashed agent is reported within a coffee break. The bound is inclusive: a
// heartbeat exactly AgentTTL old is still active.
//
// This is deliberately much longer than the 5s WebSocket write deadline or the
// 30s listener ping. Those are about not blocking on a client; this is about an
// agent that is entitled to take its time between HTTP calls.
const AgentTTL = 15 * time.Second

// AgentTTLMS is AgentTTL in milliseconds, which is the unit both the presence
// derivation and LastSeenMS are denominated in.
//
// It exists so the comparison cannot silently change units. The earlier shape
// converted inline — int64(AgentTTL/time.Millisecond) — which is correct but
// leaves the unit boundary implicit at every call site, and a caller writing
// `age <= AgentTTL` against a millisecond age is a compile error only by luck.
const AgentTTLMS = int64(AgentTTL / time.Millisecond)

// RecordAgentPresence renews the agent's lease and returns the resulting
// snapshot. It never touches the code document, the version counter or the
// history: an agent proving it is alive is not a change to what is playing, and
// bumping the version would put a heartbeat in the code history and make every
// listener's reconnection look like a new document.
//
// Like SetAnchor, the rule lives HERE rather than only in the HTTP handler. A
// guard that exists at one call site is a guard a second caller can forget, and
// presence is exactly the field a future "mark the agent as departed" endpoint
// would want to write without re-deriving the TTL.
//
// It returns the snapshot so the caller can broadcast the SAME value it returns
// to the agent, which is the validate -> commit -> broadcast contract every
// other write here follows.
func (c *Conductor) RecordAgentPresence() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.agentLastSeenMS = c.nowMS()
	return c.snapshotLocked()
}

// AgentPresenceAt returns the presence derived at an EXPLICIT instant.
//
// The clock is a parameter for the same reason validateAnchorAt takes one: the
// derivation is a pure predicate over (lastSeen, now, ttl), and making it a
// pure function is what lets the TTL boundary be asserted exactly rather than
// approximately. The Server's lease sweeper uses it with the wall clock to
// notice an expiry; tests use it with a fixed instant to pin the boundary.
//
// lastSeenMS == 0 is handled BEFORE the arithmetic, so a server that has never
// seen an agent reports inactive rather than computing a nonsensical age
// measured from the epoch.
func (c *Conductor) AgentPresenceAt(nowMS int64) AgentPresence {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return presenceAt(c.agentLastSeenMS, nowMS)
}

// presenceAt is the pure derivation behind AgentPresenceAt, split out so the
// rule reads as one statement of what "present" means.
func presenceAt(lastSeenMS, nowMS int64) AgentPresence {
	p := AgentPresence{LastSeenMS: lastSeenMS}
	if lastSeenMS == 0 {
		// Never seen. This branch is only reachable for a clock within AgentTTL
		// of the epoch — real time is ~1.7e12 ms, so without it the arithmetic
		// below would still yield inactive, and a mutation deleting this line
		// survives every other test. It is kept because the marker is RESERVED
		// (see AgentPresence) and a server whose clock is wrong must not report
		// an agent that has never existed as present.
		return p
	}
	// Compared as an absolute distance, so a heartbeat stamped in the future by
	// a skewed client cannot sit "active" indefinitely and cannot be made
	// negative-and-therefore-false by the same skew.
	age := nowMS - lastSeenMS
	if age < 0 {
		age = -age
	}
	p.Active = age <= AgentTTLMS
	return p
}

// Publish installs a new code document, bumping the version by one.
func (c *Conductor) Publish(code, message string) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.version++
	c.code = code
	if message != "" {
		c.message = message
	}
	c.history[c.histNext] = Version{
		Version: c.version,
		Code:    code,
		Message: message,
		EpochMS: time.Now().UnixMilli(),
	}
	c.histNext = (c.histNext + 1) % len(c.history)
	if c.histLen < len(c.history) {
		c.histLen++
	}
	return c.snapshotLocked()
}

// SetMessage records agent narration without changing the code document or
// bumping the version. Issue .2's POST /api/message maps onto this.
func (c *Conductor) SetMessage(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.message = message
}

// Anchor validation bounds. An anchor is the one number every client maps its
// whole scheduler position onto, so a bad one desynchronises every listener at
// once — there is no per-client fallback that could soften it. These bounds are
// therefore deliberately far wider than any real use (a two-second cycle is
// DefaultCPS; even a 1000 cps machine-music rate is 2000x the default) and exist
// to catch the two ways an agent can get this wrong by accident: a zero or
// negative rate, which divides by nothing sensible, and an epoch from a
// different machine's badly-skewed clock, which puts the shared bar boundary
// somewhere no client can reach.
const (
	// AnchorMaxCPS is the largest accepted cycles-per-second. It is not a
	// policy about tempo; it is a guard against a unit mix-up (0.5 cycles per
	// MINUTE, or milliseconds posted as seconds) silently becoming a timeline
	// nobody can schedule against.
	AnchorMaxCPS = 1000.0

	// AnchorMaxSkewMS is how far an anchor's epoch may sit from the server's
	// own clock. Listeners are assumed to be roughly synchronised, and this is
	// generous enough for a browser on a phone with a drifting clock while
	// still rejecting an epoch that would place the next bar boundary hours
	// away.
	AnchorMaxSkewMS = 5 * 60 * 1000
)

// ErrInvalidAnchor is returned by SetAnchor for an anchor the server refuses to
// publish. It is a distinct error so the HTTP layer can map it to a 400 and so
// the reason is nameable in a test rather than matched on prose.
var ErrInvalidAnchor = errors.New("invalid anchor")

// validateAnchor rejects an anchor the server refuses to publish. It is
// separate from SetAnchor so the rule reads as one statement of what a usable
// anchor IS, and so the guard cannot drift away from the store it protects.
//
// A NaN cps is rejected by the same clause as a zero: `!(cps > 0)` is true for
// NaN, so there is no separate NaN case to forget.
func validateAnchor(epochMS int64, cps float64) error {
	return validateAnchorAt(epochMS, cps, time.Now().UnixMilli())
}

// validateAnchorAt is validateAnchor against an EXPLICIT reference clock, in
// epoch milliseconds.
//
// It exists so the boundary cases of the skew bound can be asserted exactly.
// validateAnchor reads the wall clock at the instant it is called, so an epoch
// built as (some captured now) - AnchorMaxSkewMS is inside the bound for one
// millisecond and outside it for every millisecond after -- a test asserting
// that value is accepted is asserting against a moving target, and fails
// intermittently for reasons that have nothing to do with the guard. Passing the
// clock in makes "exactly at the limit" a fact rather than a race.
//
// nowMS is deliberately a parameter of the guard rather than a field on the
// Conductor: the guard is a pure predicate over (epoch, rate, clock), and the
// store it protects is not what makes it time-dependent.
func validateAnchorAt(epochMS int64, cps float64, nowMS int64) error {
	if !(cps > 0) {
		return fmt.Errorf("%w: cps must be greater than 0, got %g", ErrInvalidAnchor, cps)
	}
	if cps > AnchorMaxCPS {
		return fmt.Errorf("%w: cps must be at most %g, got %g", ErrInvalidAnchor, AnchorMaxCPS, cps)
	}
	// The epoch is compared as an ABSOLUTE skew, so a clock running fast is
	// refused on the same terms as one running slow rather than only the past
	// being checked.
	skew := nowMS - epochMS
	if skew < 0 {
		skew = -skew
	}
	if skew > AnchorMaxSkewMS {
		return fmt.Errorf("%w: epochMs must be within %d ms of the server clock, got %d",
			ErrInvalidAnchor, int64(AnchorMaxSkewMS), epochMS)
	}
	return nil
}

// SetAnchor republishes the shared timeline anchor without touching the code
// document or the version counter. Issue .8 (multi-client coherence) uses this
// to align every listener onto one clock.
//
// The anchor is validated HERE, in the one place that owns the field, rather
// than in the HTTP handler: a guard that lives only at one call site is a guard
// a second caller can forget, and the cost of a bad anchor is that every
// listener computes its position from nonsense. SetAnchor therefore returns an
// error and leaves the stored anchor UNCHANGED when the request is refused, so a
// rejected re-anchor cannot half-apply.
//
// A re-anchor never bumps the version: it changes where the shared timeline
// starts, not what is playing, so it does not belong in the code history.
func (c *Conductor) SetAnchor(epochMS int64, cps float64) error {
	if err := validateAnchor(epochMS, cps); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.anchor = Anchor{EpochMS: epochMS, CPS: cps}
	return nil
}

// SetPlaying records transport intent (POST /api/hush and POST /api/play)
// without touching the code document or the version counter. The Go server has
// no audio and never evaluates JavaScript: this flag is broadcast so browsers
// know whether to hush or resume their own repl.
func (c *Conductor) SetPlaying(playing bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.playing = playing
}

// SetListenerCount records how many clients are currently subscribed. It is the
// hub's count hook (issue .3.5) target: New wires Hub → this, so the number in
// GET /api/state is the live count rather than a permanently-0 field.
//
// It deliberately does not bump the version: listeners arriving and leaving is
// not a change to the performance, and a version bump would put a connect in the
// code history and make every listener reconnect look like a new document. The
// count always serialises as a number and negative values are clamped to 0, so a
// bug elsewhere can never publish a listener count that claims more listeners
// than could exist.
//
// It is safe to call from the hub goroutine, which is where it is actually
// called from: it takes only c.mu and never calls back into the Hub, so it
// cannot deadlock against the command the hub goroutine is serving.
func (c *Conductor) SetListenerCount(n int) {
	if n < 0 {
		n = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = n
}

// RecordEvalResult stores the most recent browser verdict on a published
// version and returns ErrUnknownVersion if no such version exists. Reports that
// name an older version than the stored one are accepted but ignored, so a
// straggling client cannot regress the agent's view of the newest version.
//
// The bool reports whether the verdict was actually STORED, and it is the whole
// point of the second return value. A nil error alone cannot express the
// difference between "this changed the performance" and "this was understood
// and deliberately dropped as stale": both return nil. The HTTP handler needs
// the distinction because a stale report must NOT be broadcast to listeners —
// announcing a verdict that was not stored would tell every listener the agent
// knows something it does not, and no listener can tell that from the wire.
//
// Callers that only care whether the report was understood can ignore it.
func (c *Conductor) RecordEvalResult(res EvalResult) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if res.Version <= 0 || res.Version > c.version {
		return false, fmt.Errorf("%w: %d (latest is %d)", ErrUnknownVersion, res.Version, c.version)
	}
	if c.lastEval != nil && res.Version < c.lastEval.Version {
		// Understood, valid, and deliberately not stored: nothing changed.
		return false, nil
	}
	stored := EvalResult{
		Version: res.Version,
		OK:      res.OK,
		Error:   res.Error,
		Stats:   cloneRawMessage(res.Stats),
		EpochMS: time.Now().UnixMilli(),
	}
	c.lastEval = &stored
	return true, nil
}

// Snapshot returns a copy of the current state.
func (c *Conductor) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked()
}

// snapshotLocked builds a snapshot; the caller must hold the mutex (read or
// write). It must never return anything that aliases Conductor internals.
func (c *Conductor) snapshotLocked() Snapshot {
	hist := make([]Version, 0, c.histLen)
	if c.histLen > 0 {
		start := c.histNext - c.histLen
		if start < 0 {
			start += len(c.history)
		}
		for i := 0; i < c.histLen; i++ {
			hist = append(hist, c.history[(start+i)%len(c.history)])
		}
	}
	return Snapshot{
		Version:          c.version,
		Code:             c.code,
		LastAgentMessage: c.message,
		Anchor:           c.anchor,
		History:          hist,
		Playing:          c.playing,
		ListenerCount:    c.listeners,
		LastEvalResult:   cloneEvalResult(c.lastEval),
		// Derived here rather than stored, so every snapshot is consistent with
		// the instant it was built. c.nowMS is called under the lock the caller
		// already holds, so the timestamp cannot drift mid-snapshot.
		Agent: presenceAt(c.agentLastSeenMS, c.nowMS()),
	}
}

// cloneEvalResult deep-copies a stored eval result so callers holding a
// snapshot can never mutate (or corrupt the bytes of) Conductor internals.
func cloneEvalResult(res *EvalResult) *EvalResult {
	if res == nil {
		return nil
	}
	cp := *res
	cp.Stats = cloneRawMessage(res.Stats)
	return &cp
}

// cloneRawMessage copies opaque JSON so returned bytes never alias the stored
// backing array.
func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	cp := make(json.RawMessage, len(raw))
	copy(cp, raw)
	return cp
}
