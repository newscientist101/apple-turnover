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
// expiry is free; announcing it needs a clock. The same argument covers an agent
// that ARRIVES or RETURNS: a badge showing NEVER or LOST while the server holds
// a live lease is the identical defect in the other direction, and until
// strudel-agent-uvj.7.6 no tick ever announced it, so the badge corrected only
// when an unrelated frame happened along.
//
// So this file owns exactly one job: notice that the PRESENCE STATE listeners
// were last told has changed, and say so — arrival, lapse, or return, each
// exactly once. It deliberately does not maintain the presence boolean, does not
// mutate the Conductor's presence, and does not touch lastSeenMS. If it could be
// deleted and presence would still be correct in every snapshot and every HTTP
// response, that is by design — only the push is lost.
//
// The SWEEPER is the sole announcer of presence frames. POST /api/heartbeat
// itself broadcasts nothing (TestHeartbeatDoesNotBroadcast pins that, and the
// bounded subscriber queue depends on it): a renewal that changed nothing
// observable is silent, and the one heartbeat that DOES change something — a
// transition into active — is announced from here, by the same goroutine that
// announces the transition out of it. Two announcers could interleave into
// [active, lapsed] ordering against the state they each read; one announcer
// compares against what it last said and self-corrects on the next tick.

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
// It announces presence TRANSITIONS — never-seen→active (arrival), active→lapsed
// (departure), lapsed→active (return) — each exactly once, and nothing else. A
// renewal of a held lease, a repeat of an already-announced lapse, and the
// never-seen initial state are all silent.
//
// The bookkeeping is announcedActive (what listeners were last TOLD about the
// active flag) plus announcedMS (the lastSeenMs of the last announcement), and
// the decision is one disjunction with two clauses that each defuse a distinct
// historical mistake:
//
//  1. presence.Active != announcedActive — the flag listeners hold is wrong.
//     This covers arrival (never-seen→active), lapse (active→false), and return
//     (lapsed→active) in one comparison, and it is keyed on the remembered
//     STATE rather than on observing a lease grow stale — so a lease taken and
//     dropped between two ticks cannot slip through just because no tick saw
//     it held. Real decay also lands here: it does not move lastSeenMs, but it
//     always flips this flag.
//  2. !presence.Active && presence.LastSeenMS != announcedMS — an inactive
//     lease whose timestamp is one not yet announced: the taken-and-dropped
//     case above, where the flag never changed from the listeners' perspective
//     (false → false) so clause 1 cannot see it.
//
// The never-seen state falls out of the conjunction: lastSeenMs is 0 and
// announcedMS seeds to 0 (the server's "no agent has ever been seen" marker),
// and the flag matches, so a server that never sees an agent says nothing —
// never-seen is the initial state, not a transition. The seed carries the rule
// and the comment carries the reason; a mutation changing the seed to -1 makes
// clause 2 fire every tick and is caught by TestAgentFrameDoesNotFireWithoutAnAgent.
//
// Announcements are made from THIS goroutine only. POST /api/heartbeat never
// broadcasts (TestHeartbeatDoesNotBroadcast pins that, and the bounded
// subscriber queue depends on it): the endpoint records the lease, and the next
// tick notices the flag listeners hold no longer matches reality. One announcer
// means the frames cannot interleave into a wrong order, and the memory is
// updated BEFORE the broadcast so a concurrent tick cannot double-announce. The
// frame itself re-reads a fresh Snapshot at broadcast time, so even a heartbeat
// landing mid-decision sends the truth; the memory then converges on the next
// tick — at worst one redundant frame, never a wrong one.
//
// The announcement delay is one sweep interval (AgentTTL/10 = 1.5s in
// production), the same bound the lapse announcement has always had.
func (s *Server) sweepAgentLease(done, stopped chan struct{}) {
	defer close(stopped)

	ticker := time.NewTicker(s.agentSweepInterval)
	defer ticker.Stop()

	// announcedMS is the lastSeenMs of the most recent presence announcement
	// already published. It starts at 0, the server's "no agent has ever been
	// seen" marker.
	announcedMS := int64(0)
	// announcedActive is the active flag of the most recent announcement: what
	// listeners currently believe. It starts false, matching the never-seen
	// connect snapshot every listener receives.
	announcedActive := false

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.agentSweepTicks.Add(1)

			presence := s.Conductor.AgentPresenceAt(s.Conductor.NowMS())

			announce := presence.Active != announcedActive ||
				(!presence.Active && presence.LastSeenMS != announcedMS)
			if !announce {
				continue
			}

			announcedActive = presence.Active
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
