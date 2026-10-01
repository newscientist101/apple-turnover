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
	"slices"
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

	// hubCountTimeout bounds ONE SubscriberCount read. SubscriberCount is
	// answered by the hub goroutine through a reply channel, so a wedged hub
	// goroutine blocks it forever: the call itself has to be bounded, not
	// merely the loop around it. A deadline checked after the call is not a
	// bound at all — a wedged hub never lets control reach the check — which is
	// exactly the defect hubSubscriberCountWithin exists to close.
	hubCountTimeout = 2 * time.Second
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

// hubSubscriberCountWithin reads the hub's subscriber count, bounded. ok is
// false when the hub did not answer within d, which means the hub goroutine is
// wedged — information a caller must report rather than wait out.
//
// The goroutine left parked inside SubscriberCount when the bound fires is a
// deliberate, documented leak: SubscriberCount is a channel round-trip to the
// hub goroutine, and a blocked channel send cannot be cancelled. The test that
// hit this is about to fail and exit, so the parked goroutine costs nothing and
// the alternative — waiting for it forever — is the defect.
func hubSubscriberCountWithin(h *Hub, d time.Duration) (int, bool) {
	got := make(chan int, 1)
	go func() { got <- h.SubscriberCount() }()
	select {
	case n := <-got:
		return n, true
	case <-time.After(d):
		return 0, false
	}
}

// hubSubscriberCount is hubSubscriberCountWithin under this file's default
// bound, failing the test with a message that names the wedge. A hub goroutine
// that stopped answering is a different defect from a hub that answered with
// the wrong number, and the two must not be reported as one.
func hubSubscriberCount(t *testing.T, h *Hub) int {
	t.Helper()
	n, ok := hubSubscriberCountWithin(h, hubCountTimeout)
	if !ok {
		t.Fatalf("SubscriberCount did not answer within %s: the hub goroutine is wedged (it is not answering the count command), so the subscriber count cannot be read at all", hubCountTimeout)
	}
	return n
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

// ---------- the subscriber-count hook (issue .3.5) ----------

// hubCountLog records every count the hub hook publishes.
//
// It is deliberately NOT a mutex-guarded view of the live count: the tests need
// the SEQUENCE of published values, not just the latest, because "the count ended
// up right" is a much weaker claim than "the count was reported at every
// transition, and only ever with the value that transition implies".
type hubCountLog struct {
	mu     sync.Mutex
	counts []int
}

func (l *hubCountLog) record(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.counts = append(l.counts, n)
}

func (l *hubCountLog) all() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.counts)
}

func (l *hubCountLog) last() (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.counts) == 0 {
		return 0, false
	}
	return l.counts[len(l.counts)-1], true
}

// hubCountedHub is hubTestHub plus the count hook, returning the hub and the log
// the hook writes to. The teardown bound is the same one hubTestHub installs, so
// a wedged hub goroutine fails the test here exactly as it does there.
func hubCountedHub(t *testing.T, sendBuffer int) (*Hub, *hubCountLog) {
	t.Helper()
	log := &hubCountLog{}
	h := NewHubWithCountHook(sendBuffer, log.record)

	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() { h.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(hubShutdownTimeout):
			t.Errorf("Hub.Close did not return within %s: the hub goroutine is wedged", hubShutdownTimeout)
		}
	})
	return h, log
}

// hubWantPublished requires the hook's most recent published count to be want,
// bounded.
//
// It is a POSITIVE assertion about a value, not a quiet window: it polls for the
// value to BE there rather than waiting to see whether anything arrives. The
// bound is on each poll iteration, and the whole wait is separately bounded, so
// a hub that publishes nothing fails after hubCountTimeout naming what it last
// published — rather than passing because nothing showed up.
func hubWantPublished(t *testing.T, log *hubCountLog, want int) {
	t.Helper()
	deadline := time.Now().Add(hubCountTimeout)
	var last int
	var seen bool
	for {
		if n, ok := log.last(); ok {
			last, seen = n, true
			if n == want {
				return
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !seen {
		t.Fatalf("the count hook published nothing within %s, want %d", hubCountTimeout, want)
	}
	t.Fatalf("the count hook's latest published count is %d after %s, want %d", last, hubCountTimeout, want)
}

// hubWantNoPublish requires that the hook publishes NOTHING MORE within d.
//
// This is the negative counterpart to hubWantPublished, and it is the assertion
// that keeps "the count was published" distinguishable from "the count changed".
// Publishing on a transition that changed nothing (a broadcast nobody was
// dropped from, an unsubscribe of an already-gone subscriber) is not harmless
// noise: a consumer cannot tell a real change from a redundant one, so the count
// stops being evidence of anything.
func hubWantNoPublish(t *testing.T, log *hubCountLog, d time.Duration) {
	t.Helper()
	before := len(log.all())
	time.Sleep(d)
	if after := len(log.all()); after != before {
		t.Fatalf("the count hook published %d extra value(s) in %s with no subscriber-set change: %v", after-before, d, log.all()[before:])
	}
}

// TestHubCountHookReportsEveryTransition is the hub's half of issue .3.5: the
// count leaves the hub on every transition that changes the set, with the value
// that transition implies.
//
// The expected sequence is asserted in full, not just the final value, because
// the transitions are what can individually be lost: a hub that published only
// on subscribe would still end up with the right last count if this test only
// looked at the end, while a listener watching /api/state would have been told
// nothing when people left.
func TestHubCountHookReportsEveryTransition(t *testing.T) {
	h, log := hubCountedHub(t, HubDefaultSendBuffer)

	// Nothing has subscribed yet, so nothing has been published. The hub IS
	// running, though — this distinguishes "no hook installed" from "a hook that
	// reports 0 on start", which is the mutation that would make an empty hub
	// look live.
	hubWantNoPublish(t, log, 50*time.Millisecond)

	// Three connects: 1, 2, 3.
	subs := make([]*Subscriber, 3)
	for i := range subs {
		subs[i] = hubSub(t, h)
		hubWantPublished(t, log, i+1)
	}

	// One clean disconnect: 2.
	if !h.Unsubscribe(subs[1]) {
		t.Fatal("Unsubscribe of a live subscriber returned false, want true")
	}
	hubWantPublished(t, log, 2)

	// Removing something already gone changes nothing, so it must publish
	// nothing: publishing here would report a change nobody made.
	if h.Unsubscribe(subs[1]) {
		t.Error("second Unsubscribe of the same subscriber returned true, want false")
	}
	hubWantNoPublish(t, log, 50*time.Millisecond)

	// A broadcast that drops nobody is likewise not a count change.
	h.Broadcast([]byte(`{"kind":"code"}`))
	hubWantNoPublish(t, log, 50*time.Millisecond)

	// The remaining disconnects: 1, then 0.
	if !h.Unsubscribe(subs[0]) {
		t.Fatal("Unsubscribe returned false, want true")
	}
	hubWantPublished(t, log, 1)
	if !h.Unsubscribe(subs[2]) {
		t.Fatal("Unsubscribe returned false, want true")
	}
	hubWantPublished(t, log, 0)

	// And the published sequence is exactly what the transitions imply: no
	// missing step, no duplicated step, no value that was never true.
	want := []int{1, 2, 3, 2, 1, 0}
	if got := log.all(); !slices.Equal(got, want) {
		t.Fatalf("published counts = %v, want exactly %v", got, want)
	}
}

// TestHubCountHookIsPublishedBeforeTheCommandIsAnswered pins the ordering the
// whole design rests on: by the time Subscribe returns, the count has already
// been published.
//
// It is not an optimisation. srv/ws.go subscribes and then immediately takes the
// catch-up snapshot it sends to the connecting listener, so if the count were
// published after the reply, that snapshot could report a listenerCount that
// excludes the listener reading it.
//
// The assertion is made DETERMINISTIC by making the hook itself block; see the
// comments in the body. The obvious alternative — read the count log immediately
// after Subscribe returns — was tried first and does NOT work: whether the test
// goroutine wins the race against the hub goroutine's next instruction is up to
// the scheduler. Mutation 56-count-published-after-the-subscribe-reply SURVIVED
// that version, which is the proof. A test that only usually catches a race is
// not evidence.
func TestHubCountHookIsPublishedBeforeTheCommandIsAnswered(t *testing.T) {
	// entered receives each count the hook is given; release gates it. The hook
	// signalling and then blocking is what turns the ordering into something a
	// test can OBSERVE rather than infer.
	entered := make(chan int, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once

	h := NewHubWithCountHook(HubDefaultSendBuffer, func(n int) {
		entered <- n
		<-release
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		closed := make(chan struct{})
		go func() { h.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(hubShutdownTimeout):
			t.Errorf("Hub.Close did not return within %s: the hub goroutine is wedged", hubShutdownTimeout)
		}
	})

	// Subscribe runs on its own goroutine so "has it returned yet?" is
	// observable at all.
	type subResult struct {
		sub *Subscriber
		err error
	}
	returned := make(chan subResult, 1)
	go func() {
		sub, err := h.Subscribe()
		returned <- subResult{sub, err}
	}()

	// First the hook really is entered — otherwise everything below would pass
	// for the wrong reason on a hub that never calls it at all.
	select {
	case n := <-entered:
		if n != 1 {
			t.Fatalf("the count hook was entered with %d, want 1", n)
		}
	case <-time.After(hubCountTimeout):
		t.Fatal("the count hook was never entered within " + hubCountTimeout.String() + ": Subscribe never reached publishCount")
	}

	// While the hook is blocked, the hub goroutine is INSIDE publishCount, so it
	// has not reached `req.reply <- sub` and Subscribe cannot have returned.
	//
	// This wait is not doing the assertion's work by hoping nothing shows up: the
	// condition is structurally impossible under the correct ordering, because
	// the only code that can complete Subscribe is the code the hook is blocking.
	// Under the inverted order Subscribe has already returned, so this fails at
	// once rather than usually.
	select {
	case r := <-returned:
		t.Fatalf("Subscribe returned (sub=%p err=%v) while the count hook was still running: the count must be published BEFORE the command is answered, or a connect snapshot can report a count that excludes the listener reading it", r.sub, r.err)
	case <-time.After(100 * time.Millisecond):
	}

	// Let the hook finish; the subscribe then completes normally.
	releaseOnce.Do(func() { close(release) })
	var sub *Subscriber
	select {
	case r := <-returned:
		if r.err != nil {
			t.Fatalf("Hub.Subscribe: %v", r.err)
		}
		if r.sub == nil {
			t.Fatal("Hub.Subscribe returned a nil Subscriber and a nil error")
		}
		sub = r.sub
	case <-time.After(hubCountTimeout):
		t.Fatal("Subscribe did not return within " + hubCountTimeout.String() + " after the hook was released: the hub goroutine is wedged")
	}

	// And the same ordering holds on the way out, for the disconnect a killed
	// browser tab produces. The hook is released by now, so this checks the
	// published value rather than re-proving the interleaving.
	if !h.Unsubscribe(sub) {
		t.Fatal("Unsubscribe returned false, want true")
	}
	select {
	case n := <-entered:
		if n != 0 {
			t.Fatalf("the count hook was entered with %d on unsubscribe, want 0", n)
		}
	case <-time.After(hubCountTimeout):
		t.Fatal("the count hook was never entered on unsubscribe within " + hubCountTimeout.String())
	}
}

// TestHubCountHookReportsTheDropOfASlowSubscriber covers the transition the
// dropped listener's own handler cannot see.
//
// When the hub drops a subscriber for not keeping up, nothing in that
// subscriber's handler runs: the handler only ever learns its channel closed,
// and only when it next tries to write. A count maintained by the ws handler
// would therefore go stale on exactly this path — the listener stays counted
// until its handler happens to wake. Here the count must fall as soon as the
// broadcast that overflowed the buffer has been answered.
func TestHubCountHookReportsTheDropOfASlowSubscriber(t *testing.T) {
	// A ONE-message buffer, so the overflow below is a matter of construction
	// rather than of timing.
	h, log := hubCountedHub(t, 1)

	fast := hubSub(t, h)
	slow := hubSub(t, h)
	hubWantPublished(t, log, 2)

	// The slow subscriber never reads. One message fits its buffer; the next
	// does not, so it is dropped and the count falls to 1.
	first := []byte(`{"kind":"code","version":1}`)
	h.Broadcast(first)
	hubWantMessage(t, fast, first)
	hubWantPublished(t, log, 2)

	second := []byte(`{"kind":"code","version":2}`)
	h.Broadcast(second)
	hubWantMessage(t, fast, second)
	hubWantPublished(t, log, 1)

	// The dropped subscriber is genuinely gone, not merely uncounted: its
	// channel is closed and it is no longer in the hub.
	hubWantClosed(t, slow)
	if h.Unsubscribe(slow) {
		t.Error("Unsubscribe of an already-dropped subscriber returned true, want false")
	}
	if got := hubSubscriberCount(t, h); got != 1 {
		t.Fatalf("SubscriberCount after the drop = %d, want 1", got)
	}
}

// TestHubCountHookPublishesZeroOnClose covers the shutdown transition. A hub
// closed with listeners still attached must report 0, not whatever count it
// happened to be holding: the listeners are gone with it, and a count left at
// its last value would report listeners that no longer exist.
func TestHubCountHookPublishesZeroOnClose(t *testing.T) {
	h, log := hubCountedHub(t, HubDefaultSendBuffer)
	hubSub(t, h)
	hubSub(t, h)
	hubWantPublished(t, log, 2)

	closed := make(chan struct{})
	go func() { h.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(hubShutdownTimeout):
		t.Fatalf("Hub.Close did not return within %s: the hub goroutine is wedged", hubShutdownTimeout)
	}

	// Read directly, with no wait: Close returning is the synchronisation point,
	// because the count is published before Close is acknowledged.
	if got, ok := log.last(); !ok || got != 0 {
		t.Fatalf("after Hub.Close the latest published count is %v (published=%v), want 0", got, log.all())
	}
}

// TestHubWithoutACountHookIsUnaffected keeps NewHub (the no-hook constructor)
// honest: the hub must behave identically with no observer attached, so the hook
// cannot have introduced a requirement only the wired path satisfies.
func TestHubWithoutACountHookIsUnaffected(t *testing.T) {
	h := hubTestHub(t, HubDefaultSendBuffer)

	sub := hubSub(t, h)
	msg := []byte(`{"kind":"code"}`)
	h.Broadcast(msg)
	hubWantMessage(t, sub, msg)
	if got := hubSubscriberCount(t, h); got != 1 {
		t.Fatalf("SubscriberCount with no hook installed = %d, want 1", got)
	}
	if !h.Unsubscribe(sub) {
		t.Fatal("Unsubscribe returned false, want true")
	}
	if got := hubSubscriberCount(t, h); got != 0 {
		t.Fatalf("SubscriberCount after Unsubscribe with no hook = %d, want 0", got)
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
	// Bounded read: SubscriberCount is answered by the hub goroutine, so a
	// wedged hub would block this call forever. hubSubscriberCount reports
	// that wedge instead of waiting it out.
	if got := hubSubscriberCount(t, h); got != subscribers {
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
	if got := hubSubscriberCount(t, h); got != 1 {
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
	if got := hubSubscriberCount(t, h); got != 2 {
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

	if got := hubSubscriberCount(t, h); got != 0 {
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
	if got := hubSubscriberCount(t, h); got != 2 {
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
	if got := hubSubscriberCount(t, h); got != 1 {
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
			// The read is bounded: SubscriberCount is answered by the hub
			// goroutine through a reply channel, so on a wedged hub it would
			// block forever and the watcher would never re-check stop. A hub
			// that stopped answering is a real finding and a different defect
			// from a hub that answered with a wrong number, so it gets its own
			// message. Returning here (rather than only logging) is what makes
			// close(stop) able to actually reach this watcher's exit and close
			// watchDone, instead of leaving it parked forever.
			got, ok := hubSubscriberCountWithin(h, hubCountTimeout)
			if !ok {
				t.Errorf("SubscriberCount did not answer within %s: the hub goroutine is wedged (it is not answering the count command), so the watcher cannot tell how many subscribers are live", hubCountTimeout)
				return
			}
			n := int64(got)
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
	if got := hubSubscriberCount(t, h); got != 0 {
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
	// The success path still needs a bound: close(stop) stops the watcher only
	// if the watcher can reach its stop check, and a watcher parked in a hub
	// command cannot. A watcher that does not exit here is a failure with a
	// message, never an unbounded bare receive.
	select {
	case <-watchDone:
	case <-time.After(hubWatchdogTimeout):
		t.Errorf("watcher did not exit within %s after stop was closed: the watcher is blocked in a hub command and cannot be interrupted", hubWatchdogTimeout)
	}
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

// TestHubCountHookStaysConsistentUnderConcurrentChurn runs the count hook under
// the same concurrent connect/disconnect load the real server sees, and checks
// the two properties that matter under contention: the published count is never
// negative, and once the churn is over the published count equals the hub's own
// answer.
//
// The final equality is the real assertion. A hook that published a stale or
// racing value would still pass a "never negative" check, and a hook that simply
// stopped publishing would pass a test that only ever inspected intermediate
// values.
func TestHubCountHookStaysConsistentUnderConcurrentChurn(t *testing.T) {
	const (
		churners          = 8
		iterationsPerGoro = 150
	)

	h, log := hubCountedHub(t, 4)

	var wg sync.WaitGroup
	for i := 0; i < churners; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterationsPerGoro; j++ {
				sub, err := h.Subscribe()
				if err != nil {
					// Nothing here closes the hub before the churn is done, so
					// a refusal means the hub stopped answering.
					t.Errorf("Hub.Subscribe during churn: %v", err)
					return
				}
				h.Unsubscribe(sub)
			}
		}()
	}

	// A watchdog on a channel, NOT wg.Wait(): a wedged hub goroutine would park
	// every churner inside Subscribe, and waiting for them all would turn that
	// hang into an unbounded wait instead of a bounded failure.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(hubWatchdogTimeout):
		t.Fatalf("the churn did not finish within %s: the hub goroutine is wedged, so Subscribe/Unsubscribe are not being answered", hubWatchdogTimeout)
	}

	// Every published value must have been a real count. Anything below zero
	// would be a listener count claiming fewer than zero listeners, which is the
	// failure mode the Conductor's clamp exists to contain.
	for i, n := range log.all() {
		if n < 0 {
			t.Fatalf("published count %d (at index %d of %v) is negative", n, i, log.all())
		}
	}

	// And the last word is the truth: with every churner finished, the published
	// count and the hub's own answer must agree.
	want := hubSubscriberCount(t, h)
	got, ok := log.last()
	if !ok {
		t.Fatalf("the count hook published nothing across %d churned connect/disconnect pairs", churners*iterationsPerGoro)
	}
	if got != want {
		t.Fatalf("after the churn the published count is %d but SubscriberCount reports %d: the hook and the hub disagree", got, want)
	}
	if want != 0 {
		t.Fatalf("SubscriberCount after every churner unsubscribed = %d, want 0", want)
	}
}
