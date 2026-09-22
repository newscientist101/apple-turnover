package srv

import (
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

// Version is a single published revision of the live code document.
type Version struct {
	Version int64  `json:"version"`
	Code    string `json:"code"`
	Message string `json:"message"`
	EpochMS int64  `json:"epochMs"`
}

// Snapshot is a copy of the Conductor's state at one instant.
type Snapshot struct {
	Version          int64     `json:"version"`
	Code             string    `json:"code"`
	LastAgentMessage string    `json:"lastAgentMessage"`
	Anchor           Anchor    `json:"anchor"`
	History          []Version `json:"history"`
}

// Conductor owns the single live performance. It is safe for concurrent use:
// Publish takes the write lock, Snapshot the read lock.
type Conductor struct {
	mu sync.RWMutex

	version int64
	code    string
	message string
	anchor  Anchor

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
	}
}
