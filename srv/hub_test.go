package srv

// This file is the fan-out (hub) slice of the repo-owned integration harness.
// It drives the Hub directly: the hub is deliberately decoupled from the
// Conductor and from net/http, so its concurrency properties can be pinned
// without a socket, a browser or an audio device.
//
// The rules are the same ones integration_test.go and ws_test.go follow:
//
//   - every operation is BOUNDED. Every receive has an explicit deadline and
//     every multi-step interaction runs under a watchdog, so a hub that wedges
//     (the defect the slow-client test exists to catch) FAILS the test instead
//     of hanging the run. A hanging test is itself a defect.
//   - teardown is bounded for the same reason: Hub.Close is called from a
//     goroutine with a timeout rather than a bare `defer h.Close()`.
//   - assertions are deterministic wherever possible: none of the tests below
//     sleeps and hopes. Where a message is expected the test reads it and
//     compares bytes; where a closed channel is expected the test requires
//     `ok == false` rather than a quiet window.
//
// The mutation-check counterpart lives in scripts/mutation-check.sh, which
// breaks the hub on purpose (double close, dropless fan-out, mutex removed) and
// requires these tests to notice.

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Every bound this file uses, in one place. Nothing here may block forever.
const (
	// hubRecvTimeout bounds a single receive from one subscriber channel. Every
	// message the hub delivers is delivered from memory, so anything slower than
	// this is a wedge, not slowness.
	hubRecvTimeout = 2 * time.Second

	// hubWatchdogTimeout bounds one whole multi-step hub interaction (a
	// broadcast loop, a churn run). It is the backstop that turns "broadcast
	// blocked on a client that stopped reading" into a failure with a message
	// instead of an infinite hang.
	hubWatchdogTimeout = 10 * time.Second

	// hubShutdownTimeout bounds Hub.Close and the hub's own exit, so a wedged
	// hub goroutine fails teardown instead of stalling it.
	hubShutdownTimeout = 5 * time.Second
)

// hubTestHub returns a started hub whose shutdown is registered on the test
// with a bound. Close is deliberately not deferred directly: a hub goroutine
// that never exits must fail the test, and a bare defer would hang instead.
func hubTestHub(t *testing.T, sendBuffer int) *Hub {
	t.Helper()
	h := NewHub(sendBuffer)
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() { h.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(hubShutdownTimeout):
			t.Errorf("Hub.Close did not return within %s: the hub goroutine is wedged", hubShutdownTimeout)
		}
	})
	return h
}

// hubSub subscribes to a live hub and fails the test if the hub refuses. A
// non-nil Subscriber is always registered with the hub at this point: Subscribe
// only returns after the hub has acknowledged the registration. That is what
// makes a Broadcast issued after Subscribe deterministic.
func hubSub(t *testing.T, h *Hub) *Subscriber {
	t.Helper()
	sub, err := h.Subscribe()
	if err != nil {
		t.Fatalf("Hub.Subscribe on a live hub: %v", err)
	}
	if sub == nil {
		t.Fatal("Hub.Subscribe returned a nil Subscriber and a nil error")
	}
	return sub
}

// hubRecvWithin reads one message from a subscriber channel, bounded. It
// reports whether a message arrived (ok), whether the channel was closed
// instead (closed), and the message bytes.
func hubRecvWithin(sub *Subscriber, d time.Duration) (msg []byte, closed, ok bool) {
	select {
	case m, open := <-sub.C():
		return m, !open, true
	case <-time.After(d):
		return nil, false, false
	}
}

// hubWantMessage requires the next thing on sub's channel to be a message with
// exactly these bytes.
func hubWantMessage(t *testing.T, sub *Subscriber, want []byte) {
	t.Helper()
	got, closed, ok := hubRecvWithin(sub, hubRecvTimeout)
	switch {
	case !ok:
		t.Fatalf("no message within %s, want %q", hubRecvTimeout, want)
	case closed:
		t.Fatalf("subscriber channel is closed, want the message %q", want)
	case !bytes.Equal(got, want):
		t.Fatalf("message = %q, want %q", got, want)
	}
}

// hubWantClosed requires sub's channel to be closed within the bound. It is a
// positive assertion, not a quiet window: once the channel is closed every
// further receive completes immediately with ok == false, so a merely idle
// channel (which has nothing to deliver) fails here after hubRecvTimeout.
//
// It drains any messages that were already in the buffer before the close:
// those are legitimately readable — closing a channel does not discard what is
// already buffered — so the assertion is "eventually closed", which is exactly
// the property that matters and the one a double close would violate.
func hubWantClosed(t *testing.T, sub *Subscriber) {
	t.Helper()
	deadline := time.Now().Add(hubRecvTimeout)
	for {
		got, closed, ok := hubRecvWithin(sub, time.Until(deadline))
		switch {
		case !ok:
			t.Fatalf("subscriber channel is still open after %s, want it closed (last message %q)", hubRecvTimeout, got)
		case closed:
			return
		}
	}
}

// TestHubBroadcastReachesEverySubscriberWithIdenticalBytes is the first
// required behaviour of the hub: one Broadcast delivers one message to EVERY
// current subscriber, byte for byte, in broadcast order.
//
// The message count is deliberately kept at or below the send buffer, so this
// test needs no concurrent readers and no timing assumptions at all: nothing
// can legitimately be dropped, so any drop (a lost fan-out entry, an
// off-by-one, a subscriber skipped) shows up as a missing or wrong message.
// The drop path itself is the subject of the slow-subscriber test below.
func TestHubBroadcastReachesEverySubscriberWithIdenticalBytes(t *testing.T) {
	const (
		subscribers = 5
		messages    = 12
	)
	if messages > HubDefaultSendBuffer {
		t.Fatalf("test bug: %d messages exceed the default send buffer %d, so a slow reader could drop one", messages, HubDefaultSendBuffer)
	}

	h := hubTestHub(t, HubDefaultSendBuffer)
	subs := make([]*Subscriber, subscribers)
	for i := range subs {
		subs[i] = hubSub(t, h)
	}
	if got := h.SubscriberCount(); got != subscribers {
		t.Fatalf("hub holds %d subscribers after %d subscribes, want %d", got, subscribers, subscribers)
	}

	payloads := make([][]byte, messages)
	for i := range payloads {
		// Realistic payloads: the hub relays encoded messages it never parses.
		payloads[i] = []byte(fmt.Sprintf(`{"kind":"code","version":%d}`, i+1))
		h.Broadcast(payloads[i])
	}

	for i, sub := range subs {
		for j, want := range payloads {
			got, closed, ok := hubRecvWithin(sub, hubRecvTimeout)
			switch {
			case !ok:
				t.Fatalf("subscriber %d: no message %d within %s (want %q)", i, j, hubRecvTimeout, want)
			case closed:
				t.Fatalf("subscriber %d: channel closed before message %d (want %q)", i, j, want)
			case !bytes.Equal(got, want):
				t.Fatalf("subscriber %d message %d = %q, want %q", i, j, got, want)
			}
		}
	}
}

// TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce is the second required
// behaviour: an unsubscribed subscriber stops receiving, its channel is closed,
// and the close happens EXACTLY ONCE.
//
// The "exactly once" half is pinned by unsubscribing twice and by reading the
// channel again afterwards. A second close of a Go channel panics, and that
// panic would either crash the hub goroutine or take the test binary down, so a
// hub that closed unconditionally fails here rather than passing quietly.
func TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce(t *testing.T) {
	h := hubTestHub(t, 4)
	leaving, staying := hubSub(t, h), hubSub(t, h)

	first := []byte(`{"kind":"code","version":1}`)
	h.Broadcast(first)
	hubWantMessage(t, leaving, first)
	hubWantMessage(t, staying, first)

	if !h.Unsubscribe(leaving) {
		t.Fatal("Unsubscribe of a live subscriber returned false, want true")
	}
	// Closed, not merely quiet: reading a closed channel completes with ok ==
	// false immediately, so this both proves the close and bounds the test.
	hubWantClosed(t, leaving)
	if got := h.SubscriberCount(); got != 1 {
		t.Fatalf("SubscriberCount after unsubscribe = %d, want 1", got)
	}

	// A subscriber that has left receives nothing further — the channel is
	// closed, so a broadcast cannot even be delivered into it.
	second := []byte(`{"kind":"code","version":2}`)
	h.Broadcast(second)
	hubWantMessage(t, staying, second)
	hubWantClosed(t, leaving)

	// The channel must stay closed and nothing must panic. A second Unsubscribe
	// is a no-op: it reports false and, crucially, does NOT close again.
	if h.Unsubscribe(leaving) {
		t.Fatal("second Unsubscribe returned true, want false: the subscriber was already gone")
	}
	if h.Unsubscribe(leaving) {
		t.Fatal("third Unsubscribe returned true, want false")
	}
	hubWantClosed(t, leaving)

	// The survivor is untouched by any of that.
	third := []byte(`{"kind":"code","version":3}`)
	h.Broadcast(third)
	hubWantMessage(t, staying, third)
}

// TestHubCloseClosesEverySubscriberExactlyOnceAndStopsTheGoroutine pins the
// shutdown path of the same invariant. Close must close the channels of
// subscribers that are still live, exactly once, answer Subscribe with
// ErrHubClosed afterwards, absorb a racing Broadcast without blocking, be
// idempotent, and actually end the hub goroutine (observed through Done, which
// is deterministic where a goroutine count is not).
func TestHubCloseClosesEverySubscriberExactlyOnceAndStopsTheGoroutine(t *testing.T) {
	h := NewHub(4)

	a, b := hubSub(t, h), hubSub(t, h)
	if got := h.SubscriberCount(); got != 2 {
		t.Fatalf("SubscriberCount before Close = %d, want 2", got)
	}

	closed := make(chan struct{})
	go func() { h.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(hubShutdownTimeout):
		t.Fatalf("Hub.Close did not return within %s", hubShutdownTimeout)
	}

	select {
	case <-h.Done():
	case <-time.After(hubShutdownTimeout):
		t.Fatalf("Hub.Done was not closed within %s of Close returning: the hub goroutine leaked", hubShutdownTimeout)
	}

	hubWantClosed(t, a)
	hubWantClosed(t, b)

	// Already-closed subscribers stay closed: Unsubscribe must not close again.
	if h.Unsubscribe(a) {
		t.Error("Unsubscribe after Close returned true, want false")
	}
	hubWantClosed(t, a)

	// A closed hub has nothing to hand out, and does not block doing so.
	if sub, err := h.Subscribe(); err == nil {
		t.Errorf("Subscribe on a closed hub = (%v, nil), want (nil, ErrHubClosed)", sub)
	} else if !errors.Is(err, ErrHubClosed) {
		t.Errorf("Subscribe on a closed hub error = %v, want ErrHubClosed", err)
	}

	if got := h.SubscriberCount(); got != 0 {
		t.Errorf("SubscriberCount on a closed hub = %d, want 0", got)
	}

	// Idempotent and non-blocking: a second Close returns immediately, and
	// Broadcast after shutdown is absorbed rather than blocking on a receive
	// that no goroutine will ever service.
	done := make(chan struct{})
	go func() {
		h.Close()
		h.Broadcast([]byte(`{"kind":"code","version":99}`))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(hubRecvTimeout):
		t.Fatal("second Close or a post-shutdown Broadcast blocked: both must be absorbed once the hub has exited")
	}

	select {
	case <-h.Done():
	default:
		t.Error("Hub.Done is not closed after a complete shutdown")
	}
}

// TestHubSlowSubscriberCannotBlockOthers is the key design property of the hub:
// a subscriber that stops reading must not delay any other subscriber, and must
// not make Broadcast block. It is written so that a hub which fans out with a
// plain blocking send HANGS here instead of failing quietly — the hang is turned
// into a failure by the watchdog around the broadcast loop (and, in CI, by
// `go test -timeout`), because a hanging test is itself a defect.
//
// The construction is deliberately not timing based:
//
//   - the slow subscriber's buffer is 1 and it never reads, so its buffer fills
//     on the second broadcast by construction, whatever the scheduler does;
//   - the fast subscriber's buffer is the default and it is drained after each
//     round, so it can never fill and can never be dropped;
//   - every broadcast is issued from a goroutine and awaited with a deadline,
//     so "broadcast blocked on the slow client" is reported as a failure with a
//     message rather than as a stuck test.
func TestHubSlowSubscriberCannotBlockOthers(t *testing.T) {
	const rounds = 5

	// The hub's send buffer is ONE message: that is what makes the slow
	// subscriber's overflow a matter of construction rather than of timing. The
	// fast subscriber is read after every single round, so it never holds a
	// message across a broadcast and can never overflow.
	h := hubTestHub(t, 1)

	// A live, well-behaved subscriber: it is read after every round.
	fast := hubSub(t, h)

	// The slow client: a one-message buffer, and nothing ever reads from it.
	slow, err := h.Subscribe()
	if err != nil {
		t.Fatalf("Hub.Subscribe (slow): %v", err)
	}
	if got := h.SubscriberCount(); got != 2 {
		t.Fatalf("SubscriberCount with a fast and a slow subscriber = %d, want 2", got)
	}

	for i := 0; i < rounds; i++ {
		msg := []byte(fmt.Sprintf(`{"kind":"code","version":%d}`, i+1))

		// Broadcast from another goroutine so a blocking fan-out (the defect)
		// cannot simply stall this test's own goroutine unnoticed: it shows up
		// as the deadline below, with a message naming the cause.
		done := make(chan struct{})
		go func() {
			h.Broadcast(msg)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(hubWatchdogTimeout):
			t.Fatalf("Broadcast %d blocked for %s: a non-reading subscriber must be dropped, never waited for", i+1, hubWatchdogTimeout)
		}

		// The fast subscriber got it, every round: the slow one is invisible.
		hubWantMessage(t, fast, msg)
	}

	// The slow subscriber was dropped for not keeping up: its channel is closed
	// exactly once (a second close would panic the hub goroutine), Unsubscribe
	// reports it is already gone, and it no longer counts. Its buffer still
	// holds the one message that fitted before it was dropped — closing a
	// channel does not discard what is already buffered — but nothing after.
	hubWantClosed(t, slow)
	if h.Unsubscribe(slow) {
		t.Error("Unsubscribe of an already-dropped subscriber returned true, want false (no double close)")
	}
	if got := h.SubscriberCount(); got != 1 {
		t.Fatalf("SubscriberCount after the slow subscriber was dropped = %d, want 1", got)
	}

	// And the hub is still fully alive: the survivor keeps receiving.
	final := []byte(`{"kind":"code","version":"final"}`)
	h.Broadcast(final)
	hubWantMessage(t, fast, final)
}

// TestHubConcurrentSubscribeUnsubscribeBroadcast hammers subscribe,
// unsubscribe and broadcast from many goroutines at once. It must be clean
// under `go test -race`, must never panic on a double close, and every
// subscriber must end up closed exactly once no matter how the operations
// interleave.
//
// The assertions are chosen to be race-free by construction: each churner owns
// its own subscriber and reads it single-threadedly, and the only shared values
// are atomics. Nothing here asserts on a message a churner might legitimately
// have missed because its buffer filled — that is the legal drop path, covered
// deliberately by the slow-subscriber test.
//
// "Closed exactly once" is asserted the only way it can be under contention: a
// two-value receive reports the close as ok == false, and repeated receives
// after that keep reporting it. (Draining the buffer first is NOT reading the
// close twice: messages already delivered before the close stay legitimately
// readable, which is why the helper drains and then requires the close.)
func TestHubConcurrentSubscribeUnsubscribeBroadcast(t *testing.T) {
	const (
		churners          = 8
		broadcasters      = 4
		iterationsPerGoro = 150
	)

	// A small buffer so the drop path is exercised hard; readers that fall
	// behind are dropped, which is legal and must not be a failure.
	h := hubTestHub(t, 4)

	var broadcasts atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// A watcher polls SubscriberCount throughout the whole churn. This is not
	// decoration: issue .3.5 publishes the listener count on every connect and
	// disconnect, so the real server CALLS SubscriberCount while Subscribe and
	// Unsubscribe are running. Exercising that here is what makes the count
	// part of the hub observable concurrently, and it is what a racy count
	// implementation (a map read from the caller's goroutine) would be caught
	// by under -race.
	var maxCount atomic.Int64
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n := int64(h.SubscriberCount())
			if n < 0 {
				t.Errorf("SubscriberCount = %d, want >= 0", n)
				return
			}
			for {
				cur := maxCount.Load()
				if n <= cur || maxCount.CompareAndSwap(cur, n) {
					break
				}
			}
		}
	}()

	// Broadcasters churn the message path continuously; they stop at the top of
	// each loop so they cannot spin forever after a failure.
	for i := 0; i < broadcasters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				h.Broadcast([]byte(fmt.Sprintf(`{"from":%d,"n":%d}`, id, n)))
				broadcasts.Add(1)
			}
		}(i)
	}

	// Churners subscribe, optionally read a message, then unsubscribe. Half of
	// them never read, so the drop path runs concurrently with the orderly
	// unsubscribe path and with the Close at the end.
	var churnDone sync.WaitGroup
	for i := 0; i < churners; i++ {
		churnDone.Add(1)
		go func(id int) {
			defer churnDone.Done()
			reads := id%2 == 0 // half of them actually read
			for n := 0; n < iterationsPerGoro; n++ {
				sub, err := h.Subscribe()
				if err != nil {
					t.Errorf("churner %d: Subscribe: %v", id, err)
					return
				}
				if reads {
					// Bounded, non-blocking: read one message if one is there.
					select {
					case <-sub.C():
					default:
					}
				}
				// Either the churner unsubscribes it or the hub dropped it for
				// not keeping up (only possible for a non-reading churner).
				// Both are legal, and both must leave it removed.
				h.Unsubscribe(sub)
				// Idempotence under churn: the second call must never double
				// close (that panics) and must never report a removal.
				if h.Unsubscribe(sub) {
					t.Errorf("churner %d: second Unsubscribe reported a removal", id)
				}
				// Drain to the close: buffered messages delivered before the
				// close are legitimately readable, so the assertion is that the
				// channel does close (exactly once) and stays closed.
				hubWantClosed(t, sub)
			}
		}(i)
	}

	finished := make(chan struct{})
	go func() { churnDone.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(hubWatchdogTimeout):
		// The churn did not finish: the hub is likely wedged (e.g., blocked in
		// deliver). close(stop) signals the watcher, but the watcher may be parked
		// inside a hub command (SubscriberCount) and cannot be interrupted.
		// Do NOT call h.Close() here — it would also block on the wedged hub.
		// Let the test's Cleanup handle hub teardown with its own timeout.
		close(stop)
		select {
		case <-watchDone:
			t.Fatalf("concurrent churn did not finish within %s: subscribe/unsubscribe/broadcast is wedged", hubWatchdogTimeout)
		case <-time.After(hubWatchdogTimeout):
			// The watcher itself is wedged (blocked in SubscriberCount on a wedged hub).
			// This is a structural limitation: a goroutine parked in a hub command
			// cannot be interrupted by close(stop) alone. Report it explicitly.
			t.Errorf("watcher did not exit within %s after stop was closed: the watcher is blocked in a hub command and cannot be interrupted", hubWatchdogTimeout)
			t.Fatalf("concurrent churn did not finish within %s: subscribe/unsubscribe/broadcast is wedged", hubWatchdogTimeout)
		}
	}

	// Every churner removed its subscriber, so the hub owns nothing again. This
	// is deterministic: no churner leaves its subscriber behind, and there is
	// no other subscriber.
	if got := h.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount after churn = %d, want 0 (every churner unsubscribed)", got)
	}
	if broadcasts.Load() == 0 {
		t.Fatal("no broadcasts happened: the test did not exercise fan-out")
	}

	// The broadcasters have done their job: stop them (and the count watcher)
	// before the liveness check below, otherwise a running broadcaster
	// legitimately delivers its own messages into the fresh subscriber first and
	// the check is not deterministic.
	close(stop)
	wg.Wait()
	<-watchDone
	if maxCount.Load() == 0 {
		t.Error("SubscriberCount never reported a live subscriber during churn: the watcher proved nothing")
	}

	// The hub still works: a subscriber created after the churn receives a
	// message. This is the liveness half that a hub which had deadlocked (or
	// whose map had been corrupted) would fail.
	after := hubSub(t, h)
	post := []byte(`{"kind":"code","version":"after-churn"}`)
	h.Broadcast(post)
	hubWantMessage(t, after, post)

	// Finally, race Close against a fresh set of broadcasters. Close must not
	// deadlock against a concurrent Broadcast, and a Broadcast issued after the
	// hub has exited must be absorbed (the select on Done) rather than blocking
	// forever on a channel no goroutine will service. All of this must finish
	// within the watchdog; a wedge fails the test with a message.
	var racers sync.WaitGroup
	racing := make(chan struct{})
	for i := 0; i < 4; i++ {
		racers.Add(1)
		go func(id int) {
			defer racers.Done()
			for n := 0; n < 200; n++ {
				h.Broadcast([]byte(fmt.Sprintf(`{"race":%d,"n":%d}`, id, n)))
			}
		}(i)
	}
	racers.Add(1)
	go func() {
		defer racers.Done()
		h.Close()
	}()
	go func() { racers.Wait(); close(racing) }()
	select {
	case <-racing:
	case <-time.After(hubWatchdogTimeout):
		t.Fatalf("Close racing concurrent Broadcasts did not finish within %s: one of them is blocked on a dead hub", hubWatchdogTimeout)
	}

	select {
	case <-h.Done():
	default:
		t.Error("Hub.Done is not closed after the racing Close")
	}
}
