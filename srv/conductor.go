package srv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// ErrStatsNotObject is returned by RecordEvalResult when a report carries a
// stats value that is valid JSON but not a JSON object.
//
// Stats is the one field of the report that arrives as raw bytes rather than a
// typed Go value, so it is also the one field whose type nothing else checks:
// version and ok are decoded into int64 and bool, and a wrong type fails the
// decoder itself. Left permissive, stats becomes the single documented type the
// server does not enforce — and it is worse than a no-op, because the value is
// stored and echoed back verbatim, so a consumer that reasonably does
// stats.haps gets undefined/false/null instead of an error. Rejecting it once,
// at the boundary, is cheaper than defending against it in every future reader.
var ErrStatsNotObject = errors.New("stats must be a JSON object")

// EvalResult is a browser's report on whether one published version actually
// evaluated in its sandbox repl. The Go server never evaluates JavaScript; it
// only stores and forwards these reports so the external agent can close its
// feedback loop. Stats is opaque client-supplied JSON — the browser decides
// which keys it reports and the server never interprets them — but it must be a
// JSON OBJECT; see ErrStatsNotObject. A nil Stats means "none reported" and is
// omitted from the wire rather than serialised as null.
//
// SamplesResolved is the tri-state the bead strudel-agent-uvj.18 is about, and it
// is a *bool rather than a bool for a reason that is easy to undo by accident.
// Three facts have to stay distinguishable:
//
//	true   every sound the pattern named resolved in the REPORTING browser
//	false  at least one named sound did not resolve there
//	nil    UNKNOWN — the reporting browser had no registry to check, or the
//	       evaluation failed before it named a sound
//
// Collapsing nil into false would report "I checked and it failed" for a browser
// that never checked, and collapsing it into true would report success for a check
// that never ran. The second is the original defect of this bead wearing a fix's
// clothes: the system saying SUCCESS for work that provably cannot work. So the
// field is a pointer with omitempty, and "no registry" travels as an ABSENT field
// that a reader can tell apart from a false.
//
// OK is deliberately NOT folded into this. ok:true means the pattern parsed,
// evaluated and was committed — it says nothing about whether it will be audible,
// and an agent optimising against ok alone is optimising against a signal blind to
// silence. See RecordEvalResult for how the two are stored independently.
type EvalResult struct {
	Version int64           `json:"version"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Stats   json.RawMessage `json:"stats,omitempty"`
	EpochMS int64           `json:"epochMs"`

	// SamplesResolved is tri-state; see the type comment. It never increments the
	// version and never enters history: it is a property of a verdict about a
	// version, not a new document.
	SamplesResolved *bool `json:"samplesResolved,omitempty"`
}

// ErrInvalidSyncObservation is returned by RecordSyncObservation when a report
// does not make exactly one claim: neither a measured drift nor "unscheduled",
// or both at once.
//
// The rule lives in the Conductor rather than only in the HTTP handler, for the
// same reason normalizeStats does: an invariant enforced at one call site is
// enforced once. And it is a real rule rather than pedantry. A report carrying
// neither would store as a successful observation while claiming nothing at all
// -- which is precisely the shape of the original defect, where the browser
// measured drift and dropped it. A report carrying both asserts that the commit
// landed on a bar line AND that there was no bar line, and the honest response to
// that is to refuse it rather than pick a winner.
var ErrInvalidSyncObservation = errors.New("a sync report must carry exactly one of driftMs or unscheduled")

// SyncObservation is one browser's measurement of when its commit of a version
// actually landed, relative to the shared bar grid (strudel-agent-uvj.15).
//
// It exists because drift was measured in the browser, painted into #sync-status
// and then dropped: the only outbound call in any browser asset was the
// eval-result POST. README and AGENT_API.md both presented drift as the signal
// that drives recovery, so an agent driving over HTTP could not observe any of
// it and could not know when to re-anchor.
//
// It is deliberately NOT part of EvalResult. Drift is a property of a COMMIT at
// a bar and is measured at a different instant from an evaluation; the two also
// have different lifetimes, since an observation can arrive after the version it
// describes has been superseded. Folding it into the verdict would make one
// stored value describe two moments.
//
// DriftMS is a POINTER, and that is the load-bearing part of the type. There are
// three distinct facts and all three must survive:
//
//	nil  Unscheduled is false and no drift was measured -- NOT YET, which is not
//	     the same claim as a measured zero
//	&0   the commit landed exactly on the bar line it targeted
//	&n   the commit landed n ms after that line
//
// A plain int64 cannot express the first two apart, and collapsing them makes a
// browser that has not committed yet indistinguishable from one that is
// perfectly aligned -- a silent system reading as a healthy one.
//
// EpochMS is the SERVER's receipt stamp, not the browser's clock. The browser
// has already demonstrated that its clock disagrees with the server's (that is
// what drift is), so stamping with the reporter's time would make freshness
// unreadable. An agent compares it against its own reads of the server clock.
type SyncObservation struct {
	// Version is the published version whose commit this describes.
	Version int64 `json:"version"`

	// DriftMS is the measured lateness against the targeted bar line, or nil when
	// Unscheduled is true.
	DriftMS *int64 `json:"driftMs,omitempty"`

	// Unscheduled records that there was NO usable anchor, so the browser
	// committed immediately and claimed no bar position at all. See the type
	// comment for why this is not driftMs:0.
	Unscheduled bool `json:"unscheduled,omitempty"`

	// TargetMS and ActualMS are the browser's own boundary arithmetic, carried so
	// an agent can see WHICH bar line the measurement was taken against. They are
	// diagnostic context; the bar grid itself is the shared anchor.
	TargetMS int64 `json:"targetMs,omitempty"`
	ActualMS int64 `json:"actualMs,omitempty"`

	// EpochMS is the server's receipt time in epoch milliseconds, stamped on
	// store. A browser-supplied value is deliberately not accepted for this: see
	// the type comment.
	EpochMS int64 `json:"epochMs"`
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

	// LastSync is the most recent drift observation any listener reported, or nil
	// if none ever has. It is a POINTER for the same reason Agent is a value and
	// LastEvalResult is a pointer: Snapshot must not expose mutable internal
	// state, and the nil/absent case is a fact of its own ("nothing observed
	// yet") rather than a zero value. See SyncObservation.
	LastSync *SyncObservation `json:"lastSync"`

	// Samples is what the AUDIENCE can hear, or nil before any listener has
	// reported (strudel-agent-f79). It is the per-listener answer to a question
	// LastEvalResult cannot answer, because a verdict records whichever listener
	// reported last and cannot say anything about the others.
	//
	// A pointer, for the same reason LastSync is one: "nobody has reported yet" is
	// a fact an agent must be able to see, and it is not the same fact as "no
	// listener has samples". See SamplesSummary.
	Samples *SamplesSummary `json:"samples"`
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

	// lastSync is the newest drift observation a listener reported. Like
	// lastEval it is a verdict-adjacent fact about the performance rather than
	// part of the document: it never increments the version and never enters
	// history.
	lastSync *SyncObservation

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

	// knownListeners is every connection the hub has issued an id to that has not
	// yet been forgotten (strudel-agent-f79).
	//
	// It is the authority RecordListenerSamples checks before storing anything.
	// Without it the store would hold claims about listeners that never existed,
	// which is the same defect as storing a verdict for a version that was never
	// published: a report about nothing, kept as though it were about something.
	knownListeners map[string]struct{}

	// listenerSamples is one stored report per connected listener that has
	// reported at least once. It is keyed by the same ids as knownListeners, so
	// every entry here names a live connection and a departed listener's record
	// leaves with it.
	//
	// It is a map because the audience is a SET, not a sequence — the snapshot
	// sorts it into order on the way out — and because the summary's whole point
	// is to enumerate what is there rather than count it.
	listenerSamples map[string]listenerSampleRecord

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
		// Both maps are made here rather than lazily, so the connect path — which
		// is concurrent with the first report a listener can make — never writes
		// to a nil map.
		knownListeners:  make(map[string]struct{}),
		listenerSamples: make(map[string]listenerSampleRecord),
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
// It also returns ErrStatsNotObject when the report's stats is not a JSON
// object, and it is where that check LIVES rather than in the HTTP handler:
// this is the invariant's owner, and an invariant enforced only at one entry
// point is enforced once. See normalizeStats.
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

	// Body validation before version validation. Both are 400s to the caller,
	// so the order is only visible in the message — but a body wrong in two
	// ways should be diagnosed by the check the sender can actually act on.
	stats, err := normalizeStats(res.Stats)
	if err != nil {
		return false, err
	}

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
		Stats:   stats,
		EpochMS: time.Now().UnixMilli(),
		// Copied, not aliased: the caller keeps its *bool, and a later write
		// through it must not reach back into the stored verdict. The stored
		// finding is the only sample evidence the agent will ever get for this
		// version, so it has to be as isolated from the reporter as Stats is.
		SamplesResolved: cloneBool(res.SamplesResolved),
	}
	c.lastEval = &stored
	return true, nil
}

// RecordSyncObservation stores one browser's measurement of where its commit of
// a version landed (strudel-agent-uvj.15).
//
// Three properties are load-bearing and each is easy to undo by accident:
//
//  1. It NEVER touches the version, the code document or the history. An
//     observation is evidence about a commit that has already happened; storing
//     it as though it were a document would put a second entry for one push in
//     the history and make a commit look like an edit.
//  2. The newest ARRIVAL wins, unconditionally -- including an observation
//     naming an OLDER version than the newest one stored. This is a deliberate
//     asymmetry with RecordEvalResult, which discards a stale verdict so a
//     straggling client cannot regress the agent's view of the code. The two
//     facts are not comparable: a verdict is about the document, and the newest
//     verdict is the one the agent wants; an observation is about WHEN a
//     listener's commit landed, and a listener that committed version 1 late can
//     perfectly well report that after version 2 was published. Discarding it
//     would drop the exact evidence an agent needs to know the audience is
//     drifting.
//  3. The report must make EXACTLY ONE claim. See ErrInvalidSyncObservation.
//
// The stored copy is deep: the caller's *int64 does not alias it, and neither
// does anything a later Snapshot hands out.
func (c *Conductor) RecordSyncObservation(obs SyncObservation) error {
	// Body validation BEFORE version validation, both are 400s to the caller, so
	// the order is only visible in the message -- and a report wrong in two ways
	// should be diagnosed by the check the sender can act on.
	if err := validateSyncObservation(obs); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if obs.Version <= 0 || obs.Version > c.version {
		return fmt.Errorf("%w: %d (latest is %d)", ErrUnknownVersion, obs.Version, c.version)
	}

	// The receipt stamp is the SERVER's clock, overwriting anything the reporter
	// sent: freshness is only comparable against the clock the agent reads
	// /api/state with.
	stored := cloneSyncObservation(obs)
	stored.EpochMS = c.nowMS()
	c.lastSync = stored
	return nil
}

// validateSyncObservation enforces the exactly-one-claim rule. It is separate
// from the store so the rule reads as one statement of what a valid observation
// IS, and so the guard cannot drift away from the field it protects.
//
// It is written as an equality of the two booleans rather than as two separate
// refusals, so the "both" case and the "neither" case cannot be handled
// inconsistently later.
func validateSyncObservation(obs SyncObservation) error {
	if (obs.DriftMS != nil) == obs.Unscheduled {
		return fmt.Errorf("%w (driftMs present=%t, unscheduled=%t)",
			ErrInvalidSyncObservation, obs.DriftMS != nil, obs.Unscheduled)
	}
	return nil
}

// cloneSyncObservation deep-copies an observation so neither the reporter nor a
// snapshot holder can write through the *int64 drift into Conductor internals.
//
// This is the same reason cloneEvalResult copies Stats and SamplesResolved: the
// write would succeed, so nothing would report it, and the next reader would see
// a value nobody measured.
func cloneSyncObservation(obs SyncObservation) *SyncObservation {
	cp := obs
	if obs.DriftMS != nil {
		d := *obs.DriftMS
		cp.DriftMS = &d
	}
	return &cp
}

// cloneBool copies a tri-state pointer so a stored verdict never aliases the
// report it came from. nil is UNKNOWN and stays nil — that is the whole point of
// the tri-state, so this must not be written as "return &*in".
func cloneBool(in *bool) *bool {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

// normalizeStats validates that opaque report stats really is a JSON object and
// returns the bytes to store.
//
// "Absent" and "present but empty" both mean the same thing — no stats — and
// both collapse to nil so `omitempty` drops the key, which is what the API
// documents. The JSON null deserves the same treatment and is the case easy to
// get wrong: json.RawMessage implements json.Unmarshaler, so a null does NOT
// leave the field nil, it becomes the four bytes "null". Storing that verbatim
// emits "stats":null from a verdict that reported no stats at all.
//
// Everything else — scalar, string, array — is refused with the offending value
// in the message, because the documented contract is that an error names what
// the caller must change. Malformed JSON never reaches here: the HTTP decoder
// rejects it first, and this deliberately does not try to recover from bytes it
// cannot parse.
func normalizeStats(raw json.RawMessage) (json.RawMessage, error) {
	switch trimmed := bytes.TrimSpace(raw); {
	case len(trimmed) == 0:
		return nil, nil
	case bytes.Equal(trimmed, []byte("null")):
		return nil, nil
	case trimmed[0] == '{':
		return cloneRawMessage(trimmed), nil
	default:
		return nil, fmt.Errorf("%w, got %s", ErrStatsNotObject, trimmed)
	}
}

// ErrUnknownListener is returned when a per-listener report names an id the
// server never issued, or one it has already forgotten.
//
// It is the same refusal dry-run makes for a second answer, and for the same
// reason: the report is about a CONNECTION, and there is no such connection. A
// server that stored it would be publishing a claim about an audience it cannot
// enumerate — a tab that closed moments ago, or an id a client made up — and
// that is the precise confusion strudel-agent-f79 exists to remove.
//
// The check lives here rather than only in the HTTP handler for the reason every
// other invariant in this file lives here: a guard enforced at one call site is
// enforced once.
var ErrUnknownListener = errors.New("unknown listener")

// ListenerSamples is what ONE listener says about its own sample registry
// (strudel-agent-f79).
//
// The bead this type exists for is the remainder of strudel-agent-uvj.18.
// `samplesResolved` is verdict-scoped: every listener evaluates and reports, the
// server stored whichever answer arrived last, and with listeners anonymous it
// could neither dedupe them, forget a departed one, nor say which answered. So a
// pattern naming a missing sample could be reported resolved — by the one tab
// that had the pack — while every other listener heard nothing at all.
//
// Loaded is a POINTER, for exactly the reason EvalResult.SamplesResolved is.
// Three facts must stay apart:
//
//	true   this listener's registry holds every sound its last verdict checked
//	false  this listener's registry is missing at least one
//	nil    UNKNOWN — this listener has no registry to check, so nothing was
//	       learned. Reporting false here would invent a defect nobody found, and
//	       reporting true would be the original uvj.18 defect wearing a fix's
//	       clothes: success for a check that never ran.
//
// Count is how many sounds the registry held at report time. It is a browser
// observation, not something the server can verify, and it exists because a
// boolean cannot distinguish "no samples at all" from "some samples, but not the
// one this code names" — a distinction an agent needs when deciding whether to
// ask a human to press the Samples button.
//
// EpochMS is the SERVER's receipt stamp, never the reporter's clock, for the
// same reason SyncObservation.EpochMS is the server's: the browser's clock is
// precisely what disagrees with the server's, and freshness is only comparable
// against the clock an agent reads /api/state with.
type ListenerSamples struct {
	Loaded *bool `json:"loaded,omitempty"`
	Count  int   `json:"count,omitempty"`
}

// listenerSampleRecord is the STORED form of one listener's report: the report
// plus who sent it and when the server received it.
//
// It is a distinct type from ListenerSamples rather than an embedded pointer so
// that no code path can confuse "what this listener claims" with "the audience
// summary", and so the identity travels inside the record rather than beside it.
type listenerSampleRecord struct {
	ListenerID string
	Samples    ListenerSamples
	EpochMS    int64
}

// SamplesSummary is what the snapshot exposes about the AUDIENCE
// (strudel-agent-f79).
//
// It answers a question `lastEvalResult.samplesResolved` structurally cannot:
// not "could the reporting browser resolve the sounds?" but "what do we know
// about what the listeners can hear?".
//
// The enumerated Listeners is the load-bearing field. Loaded and Reporting are
// counts DERIVED from it, and they exist only because an agent should not have to
// walk a slice to answer "can anyone hear this". They are earned rather than
// invented: this is exactly the opaque-number case AGENTS.md declined to publish
// when listeners had no identity, and the reason it can be published now is that
// identity makes the audience enumerable at all.
//
// Listeners is sorted by id so two successive reads — and two agents reading at
// once — see the same order; ranging a map directly would make an unstable
// ordering look like listeners coming and going.
type SamplesSummary struct {
	// Reporting is how many listeners have reported at least once and are still
	// connected.
	Reporting int `json:"reporting"`

	// Loaded is how many of those reported Loaded == true. It is a COUNT of
	// findings, not a verdict: Loaded == Reporting means every listener that
	// answered has the pack, and Loaded == 0 with Reporting > 0 means none does.
	Loaded int `json:"loaded"`

	// Listeners is the per-listener evidence, sorted by id. Each entry's Loaded
	// is tri-state, so a listener that could not check is visible as such rather
	// than counted as a failure.
	Listeners []ListenerSampleView `json:"listeners"`
}

// ListenerSampleView is one listener's row in the audience summary.
type ListenerSampleView struct {
	// ID is the opaque connection id the hub minted. It is echoed verbatim and
	// must be treated as a token, never parsed.
	ID string `json:"id"`

	// Loaded is the tri-state; see ListenerSamples.
	Loaded *bool `json:"loaded,omitempty"`

	// Count is how many sounds the listener's registry held at report time.
	Count int `json:"count,omitempty"`

	// EpochMS is the server's receipt time for this report.
	EpochMS int64 `json:"epochMs"`
}

// ReportListenerSamples records what one listener says about its sample registry,
// and reports whether that CHANGED anything an observer could notice.
//
// The bool is the whole point of the second return value, and it means the same
// thing RecordEvalResult's does: a nil error alone cannot distinguish "this
// changed the audience" from "this was understood and changed nothing". The HTTP
// layer needs the distinction because a `samples` frame is sent for a
// TRANSITION, not for an accepted write that changed nothing observable — a
// browser polling its registry would otherwise put a frame on the wire every
// poll, filling listeners' bounded queues and, on a busy tab, evicting peers.
//
// It never touches the version, the code document or the history. Sample state is
// evidence about the AUDIENCE, in the same class as a drift observation and a
// listener count: reporting on your packs is not an edit, and bumping the version
// would put a browser's registry in an agent's code history.
func (c *Conductor) RecordListenerSamples(id string, s ListenerSamples) (bool, error) {
	if id == "" {
		return false, fmt.Errorf("%w: a report must name a listener", ErrUnknownListener)
	}
	if s.Count < 0 {
		return false, fmt.Errorf("listener %q reported a negative registry size: %d", id, s.Count)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, known := c.knownListeners[id]; !known {
		return false, fmt.Errorf("%w: %q has not connected to this server", ErrUnknownListener, id)
	}

	prev, existed := c.listenerSamples[id]
	if existed && triStateKey(prev.Samples.Loaded) == triStateKey(s.Loaded) && prev.Samples.Count == s.Count {
		return false, nil
	}

	c.listenerSamples[id] = listenerSampleRecord{
		ListenerID: id,
		// Copied, not aliased: the caller keeps its *bool, and a later write
		// through it must not reach back into the stored finding.
		Samples: ListenerSamples{Loaded: cloneBool(s.Loaded), Count: s.Count},
		// The receipt stamp is the SERVER's clock, overwriting anything the
		// reporter sent.
		EpochMS: c.nowMS(),
	}
	return true, nil
}

// ForgetListenerSamples drops a listener's report when its connection ends.
//
// It exists because a departed tab cannot be allowed to keep answering for the
// audience. Without this, a tab that closed while holding a pack would leave
// behind a `loaded: true` nothing is standing behind, and an agent reading it
// would believe a pack is available to listeners that have all gone.
//
// Removing a listener is a CHANGE, and the caller is told whether a record was
// actually removed. Forgetting the last listener is the case that matters most:
// the summary then becomes ABSENT rather than an audience of zero, because
// "nobody has said anything" and "the audience checked and has no samples" are
// different facts and only one of them is true.
//
// Forgetting an id that was never recorded is not an error. This is called from a
// deferred cleanup on the /ws connect path, where it runs on EVERY exit,
// including the ones where the listener never reported; making the normal case an
// error would force every disconnect path to handle it.
func (c *Conductor) ForgetListenerSamples(id string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if id == "" {
		return false, nil
	}
	if _, known := c.knownListeners[id]; !known {
		return false, nil
	}
	delete(c.knownListeners, id)
	_, existed := c.listenerSamples[id]
	delete(c.listenerSamples, id)
	return existed, nil
}

// NoteListener records that the server issued an id to a live connection, which
// is what makes a later report from it acceptable.
//
// It is deliberately separate from RecordListenerSamples: connecting is not a
// report. A listener that connects and never evaluates anything has told the
// server NOTHING about its samples, and admitting its record here — rather than
// at report time — is what lets the store refuse a report naming an id it never
// issued.
//
// It never bumps the version and never broadcasts. A listener arriving is a count
// change, and the count hook already announces that; a second frame for the same
// arrival would be indistinguishable from a stream of no-ops.
func (c *Conductor) NoteListener(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.knownListeners[id] = struct{}{}
}

// triStateKey renders a tri-state pointer as a comparable string.
//
// It exists because "did this change" is a question about VALUES, and a *bool
// cannot be compared by content: two distinct pointers to false are the same
// fact, so a browser re-sending false would otherwise look like a transition
// every poll. nil is its own key so UNKNOWN never compares equal to either
// boolean, which is what stops a listener moving from "could not check" to
// "checked and missing" from being filed as a no-op.
func triStateKey(b *bool) string {
	if b == nil {
		return "unknown"
	}
	if *b {
		return "loaded"
	}
	return "unloaded"
}

// samplesSummaryLocked builds the audience summary, or nil when no listener has
// ever reported. The caller must hold the mutex.
//
// The nil is the load-bearing part: it is the same "nothing observed yet is not
// zero" rule that LastSync follows. A summary object with reporting:0 would read
// as "the audience checked and has no samples", which is a finding about
// listeners rather than the absence of one.
func (c *Conductor) samplesSummaryLocked() *SamplesSummary {
	if len(c.listenerSamples) == 0 {
		return nil
	}
	sum := &SamplesSummary{Reporting: len(c.listenerSamples)}
	for _, rec := range c.listenerSamples {
		sum.Listeners = append(sum.Listeners, ListenerSampleView{
			ID:      rec.ListenerID,
			Loaded:  cloneBool(rec.Samples.Loaded),
			Count:   rec.Samples.Count,
			EpochMS: rec.EpochMS,
		})
		if rec.Samples.Loaded != nil && *rec.Samples.Loaded {
			sum.Loaded++
		}
	}
	// Sorted so successive reads agree; ranging a map would not.
	sort.Slice(sum.Listeners, func(i, j int) bool { return sum.Listeners[i].ID < sum.Listeners[j].ID })
	return sum
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
		LastSync:         cloneSyncObservationPtr(c.lastSync),
		// Built fresh on every read, sorted, and deep-copied per listener: a
		// summary assembled from the map once and cached would hand every
		// successive snapshot the same slice, so a caller mutating one would
		// rewrite what the next reader sees.
		Samples: c.samplesSummaryLocked(),
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
	// Deep-copied for the same reason as Stats: a caller holding the snapshot must
	// not be able to write through this pointer and change what the next reader
	// sees. The shallow struct copy above aliased it.
	cp.SamplesResolved = cloneBool(res.SamplesResolved)
	return &cp
}

// cloneSyncObservationPtr is cloneSyncObservation at the pointer level, for the
// Snapshot path where the stored value may legitimately be nil ("nothing
// observed yet"). It is kept beside the value clone so the nil case and the
// copy case are one function's business rather than two callers'.
//
// It matters that a snapshot gets a FRESH allocation every time. Handing back
// the stored pointer would make two successive snapshots alias one another, so a
// caller mutating the first would silently rewrite the second's evidence.
func cloneSyncObservationPtr(obs *SyncObservation) *SyncObservation {
	if obs == nil {
		return nil
	}
	return cloneSyncObservation(*obs)
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

// knownListenerIDs returns the ids of every connected listener, in no particular
// order. Only tests read it: it is the read side of the authority
// RecordListenerSamples checks, exposed so a test can build a report that the
// store will actually accept rather than one it invented.
func (c *Conductor) knownListenerIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.knownListeners))
	for id := range c.knownListeners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
