package srv

// This file owns the listener hub: the piece that takes one encoded message
// and hands a copy to every subscriber, without knowing anything about the
// Conductor, net/http or the WebSocket wire format.
//
// The hub is DECOUPLED from the live performance on purpose (see the parent
// issue, .3): it is a plain `chan []byte` fan-out, so the Conductor's writes
// (issue .3.4) can drive it, a test can drive it, and it can be tested for
// concurrency without a socket, a browser or an audio device.
//
// Design (the classic Go hub, with the sharp edges filed off):
//
//   - one hub goroutine owns all mutable state (the subscriber set). Every
//     Subscribe/Unsubscribe/Broadcast is a message to that goroutine, so there
//     is no lock to hold across a send, no map to lock and no ordering to
//     reason about. `-race` is clean because nothing is shared, not because a
//     mutex happens to be used correctly.
//   - each subscriber gets its own buffered send channel. A broadcast copies
//     the message into every subscriber's buffer; a subscriber that cannot
//     accept it right now is DROPPED rather than waited for, so one slow or
//     non-reading client can never delay the hub or its peers. The fan-out loop
//     contains no operation that can block on a subscriber: every send is
//     guarded by a non-blocking select with a `default:` branch. A dropped
//     subscriber is unsubscribed on the spot, so it is indistinguishable from
//     an explicit unsubscribe except that nobody called Unsubscribe: what a
//     dropped client needs to know is "the hub no longer has me", and the
//     closed channel says exactly that.
//   - "closed exactly once" is structural, not a promise. The subscriber set is
//     a map keyed by the subscriber; a subscriber leaves it by exactly one
//     transition, performed by the one goroutine that owns it, and that same
//     transition closes its channel. There is no second close site, so there is
//     no interleaving that could close twice (which would panic).
//   - Close shuts the hub down: the hub goroutine closes every remaining
//     subscriber channel exactly once (the same single transition), then closes
//     Done and exits, so a caller can wait for a clean, goroutine-free shutdown
//     instead of guessing with runtime.NumGoroutine.
//
// Deliberately absent, and owned by later subtasks of .3:
//
//   - no Conductor interaction at all: the hub never publishes a listener count
//     (issue .3.5) and never attaches a snapshot to a new subscriber (issue
//     .3.4). Subscribe hands back a channel and nothing else.
//   - no ping/pong, no write deadline and no dead-socket reaping (issue .3.6):
//     wire liveness is that subtask's business. What lives here is the bounded
//     send guarantee it depends on: a client that cannot keep up with the
//     hub's buffer is dropped and its channel closed, which is all a reaper
//     needs to close the socket and log the client out.
//   - no encoding and no message framing: the hub moves opaque bytes, so the
//     event vocabulary stays in the Conductor's layer.

import (
	"errors"
	"sync"
)

// HubDefaultSendBuffer is the per-subscriber send buffer used by NewHub when a
// non-positive buffer size is requested. It is large enough that a listener
// that is merely a little behind (a slow browser, a GC pause) does not lose
// messages, and small enough that a client which has stopped reading entirely
// costs a bounded, trivial amount of memory before it is dropped.
//
// The buffer counts messages, not bytes: at the API's 64 KiB body cap the worst
// case held on behalf of one client is a few MiB, and such a client is dropped
// at the first message that does not fit.
const HubDefaultSendBuffer = 64

// ErrHubClosed is returned by Subscribe on a hub that has been closed (or is
// being closed). There is no subscriber channel to hand out: the hub is gone.
var ErrHubClosed = errors.New("hub closed")

// Subscriber is one listener's view of the hub: a buffered channel of encoded
// messages. The channel is closed by the hub when the subscriber is removed —
// by Unsubscribe, by being dropped for not keeping up, or by Hub.Close. A
// receiver must therefore use the two-value receive form and treat `ok ==
// false` as "you are no longer subscribed".
//
// Only the hub goroutine sends on the channel or closes it, so a receiver never
// races with a second close and never sees a value after the close. Messages
// already buffered when the channel closes stay readable, exactly as Go
// defines; the close is the signal, not a discard.
//
// A Subscriber is a handle, not a lock: C is safe to call from any goroutine.
type Subscriber struct {
	ch chan []byte
}

// C returns the subscriber's message channel. It is closed when the subscriber
// leaves the hub.
func (s *Subscriber) C() <-chan []byte { return s.ch }

// Hub fans one encoded message out to every current subscriber. It is created
// with NewHub, which starts its goroutine, and stopped with Close.
//
// The Hub is not coupled to the Conductor, to HTTP or to the WebSocket wire
// format: it moves opaque bytes. Its methods are safe for concurrent use by any
// number of goroutines.
type Hub struct {
	// Commands to the hub goroutine. Subscribe and Unsubscribe carry a reply
	// channel because registration (and removal) is ORDERED against Broadcast
	// from the caller's point of view: "subscribe, then broadcast, then expect
	// the message" and "unsubscribe, then broadcast, then expect silence" must
	// both hold without a sleep, so neither returns before the hub has acted.
	subscribe   chan *subscribeReq
	unsubscribe chan *unsubscribeReq
	broadcast   chan []byte
	count       chan chan int

	// closeOnce makes Close idempotent and safe to race with itself. closeReq
	// asks the hub goroutine to shut down and reports back when it has.
	closeOnce sync.Once
	closeReq  chan chan struct{}

	// done is closed by the hub goroutine just before it exits. Close waits on
	// it, so callers can assert a clean shutdown instead of guessing.
	done chan struct{}

	// sendBuffer is the per-subscriber channel capacity.
	sendBuffer int
}

type subscribeReq struct {
	reply chan *Subscriber
}

type unsubscribeReq struct {
	sub   *Subscriber
	reply chan bool
}

// NewHub starts a hub whose subscribers each get a send buffer of sendBuffer
// messages. A non-positive sendBuffer means HubDefaultSendBuffer.
func NewHub(sendBuffer int) *Hub {
	if sendBuffer <= 0 {
		sendBuffer = HubDefaultSendBuffer
	}
	h := &Hub{
		subscribe:   make(chan *subscribeReq),
		unsubscribe: make(chan *unsubscribeReq),
		broadcast:   make(chan []byte),
		count:       make(chan chan int),
		closeReq:    make(chan chan struct{}),
		done:        make(chan struct{}),
		sendBuffer:  sendBuffer,
	}
	go h.run()
	return h
}

// Done is closed when the hub goroutine has exited and every subscriber channel
// has been closed. A caller that wants a deterministic "the hub is gone" check
// waits on this (with a timeout) instead of counting goroutines.
func (h *Hub) Done() <-chan struct{} { return h.done }

// Broadcast delivers one encoded message to every current subscriber.
//
// It never blocks on a subscriber: a subscriber whose buffer is full is dropped
// and unsubscribed instead, so a slow or non-reading client cannot delay this
// call, any other subscriber, or the hub. Broadcast returns once the hub has
// taken the message; delivery into each subscriber's buffer is asynchronous.
//
// The message is not copied: callers must not mutate the slice afterwards. It
// is handed to every subscriber as-is, which is what makes "identical bytes to
// every subscriber" true by construction rather than by copy discipline.
func (h *Hub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	case <-h.done:
	}
}

// Subscribe registers a new subscriber and returns its handle. It returns
// ErrHubClosed if the hub has been closed. On success the subscriber is already
// registered when Subscribe returns, so a Broadcast issued afterwards sees it.
//
// Every reply channel here has capacity 1 on purpose: the hub goroutine always
// completes its send without a receiver, so a caller that gives up (because
// Close won the race and Done fired) can never wedge the hub.
func (h *Hub) Subscribe() (*Subscriber, error) {
	req := &subscribeReq{reply: make(chan *Subscriber, 1)}
	select {
	case h.subscribe <- req:
	case <-h.done:
		return nil, ErrHubClosed
	}
	select {
	case sub := <-req.reply:
		if sub == nil {
			return nil, ErrHubClosed
		}
		return sub, nil
	case <-h.done:
		// The hub exited between accepting the request and answering it; the
		// subscriber it may have created was closed on the way out.
		return nil, ErrHubClosed
	}
}

// Unsubscribe removes sub from the hub and closes its channel, exactly once.
// It returns true if sub was still a subscriber and false if it had already
// left (unsubscribed before, dropped for not keeping up, or closed with the
// hub). Calling it twice is therefore safe: the second call cannot double-close
// the channel, which would panic.
//
// When Unsubscribe returns, sub will receive no further broadcasts: any message
// already in its buffer is still readable, and the channel is then closed.
func (h *Hub) Unsubscribe(sub *Subscriber) bool {
	if sub == nil {
		return false
	}
	req := &unsubscribeReq{sub: sub, reply: make(chan bool, 1)}
	select {
	case h.unsubscribe <- req:
	case <-h.done:
		return false
	}
	select {
	case removed := <-req.reply:
		return removed
	case <-h.done:
		// Close raced this Unsubscribe: the hub's exit closed every remaining
		// subscriber channel, sub included, so it is definitely not subscribed.
		return false
	}
}

// SubscriberCount reports how many subscribers are live at the moment the hub
// answers. It is the observability that makes "a subscriber was removed"
// assertable without a sleep, and it is the value issue .3.5 publishes through
// the Conductor. A closed hub has no subscribers, so it reports 0.
func (h *Hub) SubscriberCount() int {
	reply := make(chan int, 1)
	select {
	case h.count <- reply:
	case <-h.done:
		return 0
	}
	select {
	case n := <-reply:
		return n
	case <-h.done:
		return 0
	}
}

// Close stops the hub: the hub goroutine closes every remaining subscriber
// channel exactly once, then closes Done and exits. Close is idempotent and
// safe to call concurrently with itself and with Broadcast/Subscribe; it blocks
// until the hub goroutine has finished. All command channels stay usable, so a
// racing Broadcast/Subscribe/Unsubscribe is answered (or absorbed) rather than
// left hanging.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		reply := make(chan struct{})
		h.closeReq <- reply
		<-reply
	})
	<-h.done
}

// run is the hub goroutine: the single owner of the subscriber set. It exits
// when Close asks it to, closing every remaining subscriber channel on the way
// out. It handles one command at a time, so the map is never shared and no
// command can interleave with another.
func (h *Hub) run() {
	defer close(h.done)

	subs := make(map[*Subscriber]struct{})

	for {
		select {
		case req := <-h.subscribe:
			sub := &Subscriber{ch: make(chan []byte, h.sendBuffer)}
			subs[sub] = struct{}{}
			req.reply <- sub

		case req := <-h.unsubscribe:
			req.reply <- removeSubscriber(subs, req.sub)

		case reply := <-h.count:
			reply <- len(subs)

		case msg := <-h.broadcast:
			for sub := range subs {
				if !h.deliver(sub, msg) {
					// The subscriber's buffer is full: it is not keeping up, so
					// it is dropped here and now rather than allowed to slow the
					// hub down. removeSubscriber closes its channel exactly once.
					removeSubscriber(subs, sub)
				}
			}

		case reply := <-h.closeReq:
			for sub := range subs {
				removeSubscriber(subs, sub)
			}
			close(reply)
			return
		}
	}
}

// removeSubscriber takes sub out of the live set and closes its channel. It is
// the ONLY place a subscriber channel is closed, and it first checks membership
// and reports whether sub was still present, so a second removal (a double
// unsubscribe, or an unsubscribe racing Close) is a no-op instead of a
// double close — which would panic and take the whole server down.
//
// Deleting while ranging over subs is safe in Go: an entry removed during
// iteration is not produced later in the same range.
func removeSubscriber(subs map[*Subscriber]struct{}, sub *Subscriber) bool {
	if _, present := subs[sub]; !present {
		return false
	}
	close(sub.ch)
	delete(subs, sub)
	return true
}

// deliver hands msg to one subscriber without ever blocking. It reports
// whether the subscriber accepted the message; false means its buffer is full
// and the caller must drop it.
//
// The non-blocking select with a `default:` branch is the entire slow-client
// guarantee. A plain `sub.ch <- msg` here — one blocking send to one client
// that has stopped reading — is all it would take to wedge the hub goroutine
// and, with it, every other listener. There is deliberately no blocking send
// anywhere in the fan-out path.
func (h *Hub) deliver(sub *Subscriber, msg []byte) bool {
	select {
	case sub.ch <- msg:
		return true
	default:
		return false
	}
}
