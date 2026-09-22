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
//   - no snapshot on connect: a listener is not sent the current code document,
//     the anchor or the transport state when it connects (issue .3.4.3);
//   - no Conductor wiring: the /api endpoints that change the performance do not
//     broadcast yet (issue .3.4.3), so the only writer is a test or a future
//     caller, via Hub.Broadcast;
//   - no listener counting through the Conductor (issue .3.5): a listener is
//     visible here only as Hub.SubscriberCount;
//   - no ping/pong keepalive and no dead-socket reaping (issue .3.6). What this
//     slice provides is the piece a reaper needs: every path through the loop is
//     bounded and releases the subscriber.
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
	defer s.Hub.Unsubscribe(sub)

	// CloseRead is what makes the read side bounded and useful: it reads
	// actively in its own goroutine (so control frames, including the client's
	// close frame, are answered by the library), and it hands back a context that
	// is cancelled when the connection goes away — for a close frame, a TCP
	// reset or a dead socket. Nothing in the loop below waits on the read side
	// directly, so a client that sends nothing can never block this handler; that
	// context is the "client disconnected" signal. When the connection closes,
	// CloseRead's goroutine ends with it, so nothing is left behind here either.
	clientGone := conn.CloseRead(context.Background())

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
		}
	}
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
