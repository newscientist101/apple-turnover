package srv

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
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

// Snapshot is a copy of the Conductor's state at one instant.
type Snapshot struct {
	Version          int64       `json:"version"`
	Code             string      `json:"code"`
	LastAgentMessage string      `json:"lastAgentMessage"`
	Anchor           Anchor      `json:"anchor"`
	History          []Version   `json:"history"`
	Playing          bool        `json:"playing"`
	ListenerCount    int         `json:"listenerCount"`
	LastEvalResult   *EvalResult `json:"lastEvalResult"`
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

	// listeners is pushed in by the WebSocket hub (issue .3). It stays 0
	// until a hub exists and is always serialised.
	listeners int

	lastEval *EvalResult

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

// SetAnchor republishes the shared timeline anchor without touching the code
// document or the version counter. Issue .8 (multi-client coherence) uses this
// to align every listener onto one clock.
func (c *Conductor) SetAnchor(epochMS int64, cps float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.anchor = Anchor{EpochMS: epochMS, CPS: cps}
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

// SetListenerCount records how many clients are currently subscribed. It exists
// so the future WebSocket hub (issue .3) has one place to publish its
// subscriber count; it deliberately does not bump the version. The count always
// serialises (0 until a hub reports otherwise) and negative values are clamped
// to 0.
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
func (c *Conductor) RecordEvalResult(res EvalResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if res.Version <= 0 || res.Version > c.version {
		return fmt.Errorf("%w: %d (latest is %d)", ErrUnknownVersion, res.Version, c.version)
	}
	if c.lastEval != nil && res.Version < c.lastEval.Version {
		return nil
	}
	stored := EvalResult{
		Version: res.Version,
		OK:      res.OK,
		Error:   res.Error,
		Stats:   cloneRawMessage(res.Stats),
		EpochMS: time.Now().UnixMilli(),
	}
	c.lastEval = &stored
	return nil
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
