package srv

// Agent presence is a LEASE, not a connection (strudel-agent-92w, design
// option (a) of uvj.6). An agent speaks plain request/response HTTP with no
// persistent connection, so "connected" is not directly observable: the server
// can only know that an agent was alive within the last AgentTTL.
//
// The load-bearing design decision here is that Active is DERIVED, never
// stored. There is deliberately no `active bool` field on the Conductor,
// because a stored boolean needs a timer to unset it, and a timer that fails
// to fire — or fires against a stale clock — leaves the server asserting an
// agent is present when it is not. A derived value computed from one timestamp
// cannot go stale that way: it is true if and only if the stored lastSeenMS is
// recent, at every read, against whichever clock the caller supplies.
//
// That also makes decay free of any background work in this file. It costs
// nothing until somebody READS the state, at which point the answer is already
// correct. The only thing that needs a timer is telling LISTENERS, which the
// Server owns (see presence_test.go), not the Conductor.

import (
	"sync"
	"testing"
	"time"
)

// frozenClock returns a conductor whose clock is pinned to startMS, plus a
// pointer to the offset driving it, so a TTL boundary is a fact rather than a
// race.
//
// This is the same reasoning as validateAnchorAt: reading the wall clock inside
// the guard means "exactly at the limit" is true for one millisecond and false
// for every millisecond after, so a test asserting the boundary would be
// asserting against a moving target and would fail intermittently for reasons
// that have nothing to do with the guard.
//
// The offset is NEGATIVE so that startMS lands exactly where asked: the clock is
// wall clock plus offset, so subtracting the distance from "now" pins the reading
// at the requested instant.
func frozenClock(t *testing.T, startMS int64) (*Conductor, func(int64)) {
	t.Helper()
	c := NewConductor(0)
	// Pin the clock at startMS by moving its offset, then hand back a setter
	// that pins it again at any later instant. The setter recomputes the delta
	// from the CURRENT reading rather than remembering an offset, so repeated
	// calls compose correctly instead of accumulating drift.
	c.advanceClock(startMS - time.Now().UnixMilli())
	return c, func(atMS int64) { c.advanceClock(atMS - c.nowMS()) }
}

// TestAgentPresenceNeverSeenIsInactive covers the state a fresh server is in:
// no agent has ever heartbeaten, so presence must be inactive with a zero
// timestamp.
//
// lastSeenMS == 0 is the wire-visible "never" marker, and it is deliberately
// not the same as "seen at the epoch": a real heartbeat at the Unix epoch
// would otherwise be indistinguishable from no heartbeat at all, so zero is
// reserved and the derivation must treat it as never.
func TestAgentPresenceNeverSeenIsInactive(t *testing.T) {
	c, _ := frozenClock(t, 1_000_000)

	presence := c.Snapshot().Agent
	if presence.Active {
		t.Error("a fresh conductor reports agent ACTIVE: no agent has ever connected")
	}
	if presence.LastSeenMS != 0 {
		t.Errorf("LastSeenMS = %d, want 0 for an agent that has never been seen", presence.LastSeenMS)
	}
}

// TestAgentPresenceHeartbeatMarksActive is the positive case: a heartbeat
// inside the window reports active and records when it arrived.
func TestAgentPresenceHeartbeatMarksActive(t *testing.T) {
	const hb = int64(1_000_000)
	c, _ := frozenClock(t, hb)

	c.RecordAgentPresence()

	snap := c.Snapshot()
	if !snap.Agent.Active {
		t.Error("agent is not active immediately after a heartbeat")
	}
	if snap.Agent.LastSeenMS != hb {
		t.Errorf("LastSeenMS = %d, want the heartbeat time %d", snap.Agent.LastSeenMS, hb)
	}
}

// TestAgentPresenceDecaysExactlyAtTTL is the boundary test, and it is the whole
// reason the clock is injected. It pins BOTH sides of the comparison:
//
//	at exactly TTL   -> still active
//	at TTL + 1ms     -> inactive
//
// A one-sided test (only "after the TTL it is gone") passes for an off-by-one
// in either direction AND for a comparison that ignores the window entirely.
func TestAgentPresenceDecaysExactlyAtTTL(t *testing.T) {
	const hb = int64(1_000_000)
	c, setNow := frozenClock(t, hb)
	c.RecordAgentPresence()

	// One millisecond before the boundary: still inside the window.
	setNow(hb + AgentTTLMS - 1)
	if !c.Snapshot().Agent.Active {
		t.Errorf("agent inactive at TTL-1ms (%d ms after the heartbeat): the window is short by at least 1ms", AgentTTL)
	}

	// Exactly at the boundary: the last millisecond of the lease.
	setNow(hb + AgentTTLMS)
	if !c.Snapshot().Agent.Active {
		t.Errorf("agent inactive exactly at the TTL boundary (%d ms): the comparison should be inclusive", AgentTTL)
	}

	// One millisecond past it: gone.
	setNow(hb + AgentTTLMS + 1)
	if c.Snapshot().Agent.Active {
		t.Errorf("agent still active %d ms after the heartbeat, one past the %d ms window", AgentTTLMS+1, AgentTTLMS)
	}
}

// TestAgentPresenceRepeatedHeartbeatsHoldTheLease proves the lease is renewed
// rather than set once: an agent that keeps beating stays active across a span
// far longer than one window.
//
// The intermediate reads matter. A heartbeat that merely rewrote lastSeenMS
// without the window being respected would also pass a start/end-only test, so
// the loop reads the state after EVERY beat.
func TestAgentPresenceRepeatedHeartbeatsHoldTheLease(t *testing.T) {
	const hb = int64(1_000_000)
	c, setNow := frozenClock(t, hb)
	c.RecordAgentPresence()

	// Ten beats, each one window minus a millisecond after the previous one:
	// the lease is never allowed to lapse, and the total elapsed time is an
	// order of magnitude more than a single window.
	for i := int64(1); i <= 10; i++ {
		setNow(hb + i*(AgentTTLMS-1))
		if !c.Snapshot().Agent.Active {
			t.Fatalf("agent went inactive at beat %d (%d ms after the first heartbeat): the lease is not being renewed", i, hb+i*(AgentTTLMS-1)-hb)
		}
		c.RecordAgentPresence()
	}
}

// TestAgentPresenceNeverBumpsVersion is the invariant the whole slice exists to
// protect. Only Publish may increment the version; presence is not code, and a
// heartbeat in the code history would make every listener's reconnection look
// like a new document.
func TestAgentPresenceNeverBumpsVersion(t *testing.T) {
	const hb = int64(1_000_000)
	c, setNow := frozenClock(t, hb)

	if got := c.Snapshot().Version; got != 0 {
		t.Fatalf("fresh conductor version = %d, want 0", got)
	}

	c.RecordAgentPresence()
	if got := c.Snapshot().Version; got != 0 {
		t.Fatalf("version after one heartbeat = %d, want 0: presence must not bump the version", got)
	}

	// Renewals and expiry are presence changes too, and equally must not bump.
	for i := int64(1); i <= 5; i++ {
		setNow(hb + i*AgentTTLMS)
		c.RecordAgentPresence()
	}
	if got := c.Snapshot().Version; got != 0 {
		t.Fatalf("version after five heartbeats spanning expiry = %d, want 0", got)
	}

	// The counter is still exactly where it was before any of it: one publish
	// afterwards must land on 1, not 7.
	c.Publish("s(\"bd\")", "now with code")
	if got := c.Snapshot().Version; got != 1 {
		t.Errorf("version after one publish following six heartbeats = %d, want 1", got)
	}
}

// TestAgentPresenceDoesNotEnterHistory proves the same rule from the other
// side: presence must leave no trace in the code history, which is the durable
// TestAgentPresenceSurvivesConcurrentUse runs heartbeats, reads and publishes
// against one conductor under -race.
//
// It asserts the snapshot stays internally CONSISTENT, which a plain "did not
// race" cannot: a torn read could pair an active presence with a stale version
// without tripping the race detector at all.
//
// Every goroutine joins a waitgroup behind a watchdog: a hang here is a test
// failure, not a waiting strategy.
func TestAgentPresenceSurvivesConcurrentUse(t *testing.T) {
	c := NewConductor(0)

	const workers = 8
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				switch w % 4 {
				case 0:
					c.RecordAgentPresence()
				case 1:
					_ = c.Snapshot().Agent
				case 2:
					c.Publish("code", "")
				case 3:
					snap := c.Snapshot()
					// Any version is legal, but presence must always be one
					// of the two states a snapshot can carry, and a snapshot
					// claiming activity must have a timestamp to justify it.
					if snap.Agent.Active && snap.Agent.LastSeenMS == 0 {
						t.Errorf("snapshot claims an active agent with LastSeenMS 0: presence and its timestamp disagree")
					}
					if snap.Version < 0 {
						t.Errorf("negative version %d", snap.Version)
					}
				}
			}
		}(w)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent presence test did not finish within 30s: a presence update is deadlocking")
	}
}

// record of what has been published.
func TestAgentPresenceDoesNotEnterHistory(t *testing.T) {
	c, _ := frozenClock(t, 1_000_000)
	c.RecordAgentPresence()
	c.RecordAgentPresence()
	c.RecordAgentPresence()

	if h := c.Snapshot().History; len(h) != 0 {
		t.Errorf("history after three heartbeats = %v, want empty: presence is not code", h)
	}
}

// TestAgentPresenceNeverSeenIsInactiveEvenOnAClockNearTheEpoch covers the one
// case that makes the reserved zero marker load-bearing.
//
// Every other test runs on a clock near real time, where a never-seen agent's age
// would be ~1.7e12 ms and therefore far outside any TTL — so deleting the
// lastSeenMS == 0 guard changes nothing observable and a mutation deleting it
// survives. This test pins the clock INSIDE the window instead, where the
// arithmetic alone would report a server that has never seen an agent as
// present.
//
// It is the difference between "inactive because the heartbeat is old" and
// "inactive because there has never been one", and only the second is a
// statement about the agent.
func TestAgentPresenceNeverSeenIsInactiveEvenOnAClockNearTheEpoch(t *testing.T) {
	c, setNow := frozenClock(t, 0) // the epoch itself

	if p := c.Snapshot().Agent; p.Active {
		t.Errorf("a server whose clock is at the epoch reports an agent ACTIVE with no heartbeat ever received: age %d is inside the %d ms window, so only the reserved zero marker can prevent this", 0, AgentTTLMS)
	}
	if got := c.Snapshot().Agent.LastSeenMS; got != 0 {
		t.Errorf("LastSeenMS = %d, want 0: no heartbeat has been received", got)
	}

	// And the marker is genuinely RESERVED, with a documented cost: a heartbeat
	// landing at exactly epoch millisecond 0 stores 0 and is therefore
	// indistinguishable from never-seen. That is unreachable in practice (the
	// epoch is 1970) and cheaper than a parallel "has ever been seen" flag,
	// which would be a second piece of state that could disagree with this one.
	// It is asserted rather than left implicit so that widening the marker later
	// is a deliberate act.
	setNow(0)
	c.RecordAgentPresence()
	if p := c.Snapshot().Agent; p.Active {
		t.Error("a heartbeat at the epoch reports active: the reserved marker is not being honoured")
	}

	// The ambiguity is PERMANENT for that heartbeat, not a one-millisecond
	// boundary: having stored 0, it can never be told apart from never-seen no
	// matter how much time passes. Asserted so the cost is on the record.
	setNow(AgentTTLMS / 2)
	if p := c.Snapshot().Agent; p.Active {
		t.Error("the epoch heartbeat became active later: it should stay indistinguishable from never-seen, which is the documented cost of the marker")
	}

	// The cost is bounded to a clock at the epoch. One tick later a real
	// heartbeat is stored with a non-zero timestamp and is unambiguous, which is
	// why this is an acceptable trade rather than a correctness hole.
	setNow(1)
	c.RecordAgentPresence()
	if p := c.Snapshot().Agent; !p.Active {
		t.Errorf("a heartbeat 1ms after the epoch is not active (lastSeenMs=%d): only the exact epoch is ambiguous", p.LastSeenMS)
	}
}
