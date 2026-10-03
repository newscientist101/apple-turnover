package srv

// The agent lease sweeper (strudel-agent-sfu, part of uvj.6).
//
// The Conductor DERIVES agent presence, so it needs no timer to stay correct:
// every read re-derives Active from lastSeenMS and the clock. That is what makes
// the derivation trustworthy, and it is also why nothing here maintains the flag.
//
// What a timer is genuinely needed for is PUSHING. A listener only learns
// something when the server broadcasts, so a lease that lapses in silence leaves
// every open page reporting an agent the server already considers gone —
// precisely the stale green badge this feature exists to replace. Deciding the
// expiry is free; announcing it needs a clock.
//
// So this file owns exactly one job: notice that a HELD lease has lapsed, once,
// and say so. It deliberately does not maintain a boolean, does not mutate the
// Conductor's presence, and does not touch lastSeenMS. If it could be deleted
// and presence would still be correct in every snapshot and every HTTP response,
// that is by design — only the push is lost.

import (
	"log/slog"
	"time"
)

// defaultAgentSweepInterval is how often the sweeper looks for a lapsed lease.
//
// It is a fraction of AgentTTL on purpose. The sweeper's job is to notice an
// expiry promptly, and its cost is one derived read per tick against a store
// already in memory, so a coarse interval buys nothing. A tenth of the TTL
// bounds how stale the "agent gone" report can be to about a second and a half
// without making the sweep itself hot.
//
// It must be comfortably SMALLER than AgentTTL. A sweeper ticking less often than
// the lease lasts would report an expiry late by up to a full interval.
const defaultAgentSweepInterval = AgentTTL / 10

// agentSweepStopTimeout bounds one sweeper shutdown.
const agentSweepStopTimeout = 2 * time.Second

// startAgentSweeper launches the lease sweeper.
//
// It is idempotent, so New may start one and a test may start another without
// the second leaking a goroutine.
func (s *Server) startAgentSweeper() {
	if s.agentSweepInterval <= 0 {
		s.agentSweepInterval = defaultAgentSweepInterval
	}
	s.agentSweepOnce.Do(func() {
		s.agentSweepDone = make(chan struct{})
		s.agentSweepStopped = make(chan struct{})
		go s.sweepAgentLease(s.agentSweepDone, s.agentSweepStopped)
	})
}

// stopAgentSweeper reaps the sweeper and returns only once it has exited.
//
// The wait is the whole point. A stop that signalled without joining would let a
// sweeper broadcast into a hub the shutdown is closing, and would leak one
// goroutine per Server in a suite that builds a Server per test. The join is
// bounded by agentSweepStopTimeout: a wedged sweeper must fail the test that
// stopped it rather than hang it.
//
// Closing is guarded because both the shutdown path and a test cleanup can reach
// this, and closing a closed channel panics.
func (s *Server) stopAgentSweeper() {
	s.agentSweepStopOnce.Do(func() {
		if s.agentSweepDone == nil {
			return // never started
		}
		close(s.agentSweepDone)
		select {
		case <-s.agentSweepStopped:
		case <-time.After(agentSweepStopTimeout):
			// Deliberately not fatal: this may run from a deferred cleanup with
			// no *testing.T, and a logged leak is better than a panic there. The
			// sweeper's own bounded select still guarantees it exits.
			slog.Warn("agent lease sweeper did not stop within its bound", "timeout", agentSweepStopTimeout)
		}
	})
}

// sweepAgentLease is the sweeper goroutine.
//
// It announces expiries, and only expiries: a lease that has already lapsed and
// been announced must not be announced again on every later tick.
//
// The bookkeeping is a single remembered timestamp, announcedMS, and the order of
// the guards below is the whole design. Two distinct mistakes are being avoided,
// and each corresponds to a way this sweeper was wrong first:
//
//  1. Reacting only to "a lastSeenMs value I have not seen before". That misses
//     real decay outright, because decay does not move lastSeenMs: time passes
//     and the stored heartbeat gets OLDER while keeping the same value. The
//     sweeper would see a value it already knows and stay silent, leaving every
//     listener showing an agent the server already considers gone.
//  2. Reacting only to "I watched this lease go from held to lapsed". That misses
//     a lease taken and dropped between two ticks, because no tick ever saw it
//     held.
//
// So the announcement is keyed on the STATE, with the remembered timestamp only
// suppressing a repeat of the state already reported: a lease is announced when
// it is inactive AND its timestamp is one not yet announced. Both orderings fall
// out of that, and re-announcement cannot.
func (s *Server) sweepAgentLease(done, stopped chan struct{}) {
	defer close(stopped)

	ticker := time.NewTicker(s.agentSweepInterval)
	defer ticker.Stop()

	// announcedMS is the lastSeenMs of the most recent expiry already published.
	// It starts at 0, the server's "no agent has ever been seen" marker, so a
	// server that never sees an agent never announces one going away.
	announcedMS := int64(0)

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.agentSweepTicks.Add(1)

			presence := s.Conductor.AgentPresenceAt(s.Conductor.NowMS())

			// Still inside the window. Nothing to announce.
			if presence.Active {
				continue
			}
			// A lapsed lease whose departure has not been published yet.
			//
			// This single comparison is also what suppresses announcing an agent
			// that was NEVER seen: announcedMS seeds to 0, the server's "no
			// agent has ever been here" marker, so a never-seen server finds
			// lastSeenMs == announcedMS and stays silent. An earlier version
			// spelled that out as its own `if lastSeenMs == 0` guard, which a
			// mutation showed could be deleted with every test still green —
			// it was unreachable as a separate case. The seed carries the rule
			// and the comment carries the reason.
			if presence.LastSeenMS == announcedMS {
				continue
			}

			announcedMS = presence.LastSeenMS
			s.broadcast(EventAgent, s.Conductor.Snapshot())
			s.agentSweeps.Add(1)
		}
	}
}

// NowMS returns the Conductor's clock reading in epoch milliseconds.
//
// The lease sweeper reads the clock through here rather than calling
// time.Now() itself, so a test can drive DECAY by advancing the conductor's
// clock while leaving lastSeenMS untouched.
//
// That distinction is the whole point. Expiring a lease by rewinding its
// timestamp also CHANGES lastSeenMS, which is what makes a sweeper keyed on
// lastSeenMS look correct — it sees a value it has not seen and reports it. Real
// decay does not move lastSeenMS at all: time passes and the stored heartbeat
// gets older. A test that only ever rewinds the timestamp therefore proves
// nothing about the case that actually happens when an agent dies, and would let
// a sweeper keyed on the wrong value ship.
func (c *Conductor) NowMS() int64 {
	return c.nowMS()
}

// expireAgentLeaseForTest rewinds the stored heartbeat timestamp so a test can
// reach the lapsed state without sleeping out the production AgentTTL.
//
// It is a test seam and is deliberately NOT reachable by any HTTP client: there
// is no endpoint that expires a lease on demand, because an agent that could
// declare itself gone would make the presence flag something it asserts rather
// than something the server observes.
//
// Prefer advancing the Conductor clock over calling this: rewinding the
// timestamp changes lastSeenMS, whereas real decay leaves it alone. See
// Conductor.NowMS.
func (s *Server) expireAgentLeaseForTest() {
	s.Conductor.mu.Lock()
	defer s.Conductor.mu.Unlock()
	if s.Conductor.agentLastSeenMS != 0 {
		s.Conductor.agentLastSeenMS -= AgentTTLMS + 1
	}
}
