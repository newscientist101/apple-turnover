package srv

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// defaultWSWriteTimeout is the per-frame write budget handleWS gives one
// listener. It is generous for a frame that is a few KiB in practice and short
// enough that a listener which has stopped reading — a backgrounded tab, a
// suspended laptop, a stalled network — cannot pin the handler goroutine (and
// with it, its subscriber slot) for long. Server.wsWriteTimeout carries it into
// the handler, so a test can prove the bound exists without spending the
// production timeout.
const defaultWSWriteTimeout = 5 * time.Second

// defaultWSPingInterval is how often a listener is asked to prove it is still
// there, and defaultWSPongTimeout how long it then has to answer.
//
// The two are a policy, not two independent numbers: a listener is declared
// dead only if it fails to answer within wsPongTimeout of a ping sent every
// wsPingInterval, so the ratio is what decides how patient the server is. 30s
// between probes with 5s to answer is a ~6:1 ratio — generous enough that a
// listener on a slow link or a loaded machine is never reaped for being slow,
// while a genuinely dead socket is reclaimed well inside a minute rather than
// holding its handler goroutine and subscriber slot until the process exits.
//
// Neither value may be a test literal: both live on Server so the reaping
// tests can run this same policy in milliseconds.
const (
	defaultWSPingInterval = 30 * time.Second
	defaultWSPongTimeout  = 5 * time.Second
)

// This file owns the listener-facing WebSocket endpoint: the `GET /ws` route,
// the handshake, and the loop that turns this listener into a hub subscriber.
//
// The shape is: Accept (issue .3.2) -> Subscribe (issue .3.3's hub) -> pump each
// message to the socket as a text frame -> Unsubscribe. Every step out of the
// loop is bounded and every exit path unsubscribes, so a listener cannot leak a
// subscriber slot, and a client that has stopped reading cannot pin a handler
// goroutine:
//
//   - the read side is websocket.Conn.CloseRead, which reads in the library's
//     own goroutine (answering ping/pong/close frames) and cancels a context
//     when the connection goes away. That context, not a read of this handler's
//     own, is the "client disconnected" signal, so a client that sends nothing
//     can never block the loop.
//   - the write side carries a per-frame deadline (Server.wsWriteTimeout).
//     Without one, a frame larger than the dead client's socket buffers waits
//     forever for buffer space the client will never provide.
//   - the hub side selects on the subscriber channel being CLOSED as well as on
//     Hub.Done, because a listener the hub dropped for falling behind is closed
//     exactly like a listener the hub shut down.
//
// Deliberately absent, and owned by later subtasks of .3:
//
//   - no ping/pong keepalive and no dead-socket reaping (issue .3.6). What this
//     slice provides is the piece a reaper needs: every path through the loop is
//     bounded and releases the subscriber.
//
// The listener count is not tracked HERE either (issue .3.5). Counting in this
// file would be wrong twice over: the hub drops a slow subscriber from its own
// goroutine without this handler's involvement, so this file could only ever
// learn about that indirectly, via a closed channel, and be late; and the count
// would then have to be recomputed by every handler that touched it, with two
// concurrent handlers free to publish values that arrive out of order. The hub
// publishes the count instead (see hub.go), and this file needs no counting code
// at all — which is also why a listener dropped mid-broadcast still decrements.
//
// The one thing added on top of the subscribe/relay/teardown shape is the
// catch-up snapshot (issue .3.4.3): a newly connected listener is sent the
// current state immediately, so it lands mid-performance instead of waiting for
// a change that may never come. The /api endpoints that change the performance
// broadcast through the same hub (also .3.4.3, see srv/event.go), so a write
// reaches listeners as well as the agent.
//
// handleWS accepts a listener's WebSocket handshake and then stays with the
// connection for as long as it is useful. Accept writes the 101 Switching
// Protocols response itself, including the RFC 6455 Sec-WebSocket-Accept
// header.
//
// A request without valid upgrade headers never reaches this handler for its
// 101: Accept writes its own clear error response (426 Upgrade Required for a
// missing `Connection: Upgrade`/`Upgrade: websocket`, 400 for a bad
// Sec-WebSocket-Version or Sec-WebSocket-Key, 501 when the ResponseWriter
// cannot be hijacked) and returns an error. That error is logged, never
// panicked, so a bare GET of /ws is a legible failure rather than a 500.
//
// Close performs the full WebSocket close handshake (close frame out, peer's
// close frame in, both bounded by a 5s timeout), so the client observes a
// clean 1000 StatusNormalClosure rather than a socket that just goes away.
// Server.Serve sets no WriteTimeout, so this handshake cannot be cut short by
// the http.Server either; the deadline is the library's own.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept has already written a response for the client. Nothing here
		// can help further; record it so a misbehaving listener is visible.
		slog.Debug("websocket accept", "path", r.URL.Path, "error", err)
		return
	}

	// Every exit path below closes the socket. CloseNow is the backstop: it
	// cannot block on a peer that never answers a close handshake, so leaving
	// the handler is always bounded, and a deliberate close frame sent first
	// makes it a no-op.
	defer func() {
		if err := conn.CloseNow(); err != nil {
			slog.Debug("websocket close now", "path", r.URL.Path, "error", err)
		}
	}()

	sub, err := s.Hub.Subscribe()
	if err != nil {
		// The hub is already shut down, so this listener could never be sent
		// anything. A clean "going away" close is the honest answer; there is
		// deliberately no retry, because a listener that insists would only spin
		// against a hub that is gone.
		wsCloseGoingAway(conn)
		return
	}
	// Unsubscribe on EVERY path below this point, so a subscriber slot can never
	// be leaked by an early return, a panic or a wedged client. The call is
	// idempotent: if the hub already removed this subscriber (it dropped it for
	// not keeping up, or Close closed every channel), it is a no-op and reports
	// false, and the channel is never closed twice.
	//
	// The listener count needs no code here at all (issue .3.5). Both of these
	// calls are what publish it: the hub has already reported the new count by
	// the time Subscribe and Unsubscribe return, so the connect snapshot below
	// counts this listener, and /api/state is correct the instant a disconnect
	// handler has finished. See hub.go.
	defer s.Hub.Unsubscribe(sub)

	// The server issues this connection its identity here, and only here: the hub
	// is the one owner of the subscriber set and the one place an id is minted, so
	// admitting the listener to the Conductor at the same instant is what makes
	// every report it can possibly send acceptable later. A report arriving before
	// this point would name an id the server had not yet admitted.
	//
	// NoteListener neither bumps the version nor broadcasts. Arriving is a count
	// change and the count hook has already announced it; a second frame for the
	// same arrival would be indistinguishable from a stream of no-ops.
	listenerID := sub.ID()
	s.Conductor.NoteListener(listenerID)

	// Departure is remembered so a closed tab stops answering for the audience.
	//
	// The ORDER of these two defers is load-bearing and is the reason this one is
	// registered second. Go runs deferred calls last-in-first-out, so this forget
	// runs BEFORE the Unsubscribe above, and therefore before the count frame the
	// hub publishes as part of removing the subscriber. The reverse order would
	// encode a listener-count frame whose snapshot still listed the listener that
	// had just left — a frame describing a state that never existed, sent to every
	// listener still connected.
	//
	// It is deferred rather than called at each return because the exits are
	// numerous and a forget missed on one would leak a record for ever: a departed
	// tab holding a pack would leave a `loaded: true` behind that no connection
	// stands behind, and an agent would believe samples were available to an
	// audience that has gone.
	defer func() {
		if _, err := s.Conductor.ForgetListenerSamples(listenerID); err != nil {
			slog.Debug("forget listener samples", "id", listenerID, "error", err)
		}
	}()

	// Send the catch-up snapshot BEFORE entering the loop, so a listener that
	// connects mid-performance lands in the music rather than waiting for the
	// next change — which may be minutes away, or may never come.
	//
	// The ordering (subscribe, THEN snapshot) is deliberate and is the whole
	// reason this cannot drop a change: because the subscription is already
	// registered, any write from this instant on is queued for this listener.
	// Taking the snapshot afterwards means it is at least as new as anything
	// already queued, so the listener never observes a state older than one it
	// has been sent. Snapshotting first and subscribing second would leave a
	// window in which a write is broadcast to everyone else and silently lost
	// here.
	//
	// A failure to write it is not fatal: the listener is still subscribed and
	// will receive every subsequent change. Dropping the connection instead
	// would turn a transient write timeout into a listener that can never hear
	// anything again.
	//
	// IDENTITY GOES FIRST, and that order is load-bearing rather than incidental.
	// The snapshot may carry a code version the browser has never seen; it
	// validates it and reports on its samples straight away. A listener that
	// learned its id AFTER that snapshot would have to make its first report
	// against an identity it did not have yet, and the server — which refuses a
	// report naming an id it never issued — would correctly reject it.
	//
	// Both writes carry the same per-frame deadline as every other, so a listener
	// that has stopped reading cannot pin the handler here either.
	//
	// A failure to deliver the identity is not fatal either, for the same reason:
	// the connection stays up and keeps receiving every other frame. What it loses
	// is the ability to have its reports accepted, which is a degraded listener
	// rather than a dead one. Sending the snapshot regardless is therefore
	// deliberate: a listener is better off with an unusable identity than with no
	// performance at all.
	if err := s.writeToListener(conn, s.encodeListenerEvent(listenerID)); err != nil {
		slog.Debug("websocket listener identity", "path", r.URL.Path, "error", err)
	}

	if err := s.sendSnapshot(conn); err != nil {
		slog.Debug("websocket snapshot", "path", r.URL.Path, "error", err)
	}

	// CloseRead is what makes the read side bounded and useful: it reads
	// actively in its own goroutine (so control frames, including the client's
	// close frame, are answered by the library), and it hands back a context that
	// is cancelled when the connection goes away — for a close frame, a TCP
	// reset or a dead socket. Nothing in the loop below waits on the read side
	// directly, so a client that sends nothing can never block this handler; that
	// context is the "client disconnected" signal. When the connection closes,
	// CloseRead's goroutine ends with it, so nothing is left behind here either.
	clientGone := conn.CloseRead(context.Background())

	// The keepalive ticker. It is stopped on every exit path below, so a
	// handler that returns cannot leave the runtime holding a timer for a
	// listener that is gone.
	ping := time.NewTicker(s.wsPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-clientGone.Done():
			// The listener disconnected. There is nothing left to send to, and no
			// close frame is due: the library has already answered the client's
			// own close frame, if it sent one.
			return

		case <-s.Hub.Done():
			// The hub has shut down, so this listener can never be sent anything
			// again. End the conversation with a clean normal-closure frame rather
			// than letting the socket simply vanish.
			wsCloseListener(conn)
			return

		case msg, ok := <-sub.C():
			if !ok {
				// The hub removed this subscriber: it shut down, or it dropped this
				// listener because it was not keeping up with the send buffer. The
				// two are indistinguishable from here — and indistinguishable in
				// consequence, because either way the hub no longer has this
				// listener — so both are answered the same clean way. (Close closes
				// every subscriber channel before it closes Done, so this branch is
				// normally the one a shutdown reaches first; selecting on Done as
				// well makes the outcome the same whichever the runtime picks.)
				wsCloseListener(conn)
				return
			}

			// One message, one text frame, one bounded write. The hub's message is
			// relayed verbatim — no re-encoding, no trimming, no interpretation —
			// so what a client receives is byte-for-byte what was broadcast.
			if err := s.writeToListener(conn, msg); err != nil {
				// The frame could not be written (a broken socket or a client that
				// did not accept it within the write budget). Either way this
				// listener is not usable: drop the connection and let the deferred
				// Unsubscribe release its subscriber slot.
				slog.Debug("websocket write", "path", r.URL.Path, "bytes", len(msg), "error", err)
				return
			}

		case <-ping.C:
			// Liveness check (issue .3.6). The hub and the write path both
			// report a listener that has stopped reading — the hub by
			// dropping it once its send buffer fills — but a listener on a
			// quiet performance is sent nothing at all, so neither ever
			// notices it. A half-open socket (a yanked cable, a killed
			// phone, a tab suspended by the OS) is exactly that case: no
			// error, no traffic, no end.
			//
			// The pong deadline is what distinguishes a slow listener from a
			// dead one, which is why this may block the loop for up to
			// wsPongTimeout: a listener that has not answered by then is
			// treated as gone. That is the same shape as the per-write
			// budget above, and it is why the "/ws never blocks on a
			// client" invariant survives this addition.
			if err := s.wsPingListener(conn); err != nil {
				// It did not answer in time, or the socket is already
				// broken. Both mean the same thing here: this listener
				// cannot be reached, so release it and let the deferred
				// Unsubscribe — which is what publishes the decremented
				// listener count — and the deferred CloseNow clean up.
				slog.Debug("websocket keepalive", "path", r.URL.Path, "error", err)
				return
			}
		}
	}
}

// wsPingListener asks one listener to prove it is still there, and waits a
// bounded time for the answer.
//
// A nil return means the listener answered; an error means it is gone. It is
// deliberately a QUESTION rather than a policy: the loop above owns what happens
// next, because only it knows the surrounding exit paths.
//
// The pong is read by the goroutine CloseRead already started, not by a read
// here — that is what makes this safe to call from the relay loop. The library
// documents that CloseRead answers ping, pong and close frames, so a ping sent
// here is answered on that goroutine and this call only waits for the outcome.
// Starting a second reader would be a concurrent-read violation and could
// corrupt the frame stream.
func (s *Server) wsPingListener(conn *websocket.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.wsPongTimeout)
	defer cancel()
	return conn.Ping(ctx)
}

// sendSnapshot sends one listener the current state as an EventSnapshot frame,
// under the same per-write deadline as every other frame. It is the first thing
// a newly connected listener sees, so its kind says "this is the catch-up, not
// a change" — a client can tell "I have just caught up" from "the performance
// moved" without inspecting the payload.
//
// It returns the write error rather than handling it, because the caller has
// the context (this is a connect, not a relay) and the honest reaction to a
// failed catch-up is to log it and carry on serving changes, not to hang up on
// a listener that is otherwise perfectly subscribed.
func (s *Server) sendSnapshot(conn *websocket.Conn) error {
	return s.writeToListener(conn, s.encodeEvent(EventSnapshot, s.Conductor.Snapshot()))
}

// writeToListener writes one hub message to one listener as a single text frame
// under a per-write deadline. That deadline is the whole reason a non-reading
// client cannot hang teardown: without it this write waits for socket buffer
// space that a wedged peer will never provide, so the handler never returns, its
// subscriber is never released, and a shutdown blocks behind it.
func (s *Server) writeToListener(conn *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.wsWriteTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, msg)
}

// wsCloseListener ends one listener's conversation because the hub let go of it.
// Close performs the full WebSocket close handshake (close frame out, peer's
// close frame in, both bounded by the library's own 5s timeouts), so the client
// observes a clean 1000 StatusNormalClosure rather than a socket that just goes
// away. Server.Serve sets no WriteTimeout, so the http.Server cannot cut the
// handshake short; the deadline is the library's.
func wsCloseListener(conn *websocket.Conn) {
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		slog.Debug("websocket close", "error", err)
	}
}

// wsCloseGoingAway ends one listener's conversation because the server is not
// accepting listeners: the hub was already shut down when the handshake
// succeeded, so StatusGoingAway (1001) — "this endpoint is going away" — is the
// accurate status. It is still a proper close handshake, not a drop.
func wsCloseGoingAway(conn *websocket.Conn) {
	if err := conn.Close(websocket.StatusGoingAway, "server shutting down"); err != nil {
		slog.Debug("websocket close", "error", err)
	}
}
