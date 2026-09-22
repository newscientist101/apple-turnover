package srv

import (
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
)

// This file owns the listener-facing WebSocket endpoint. Issue .3.2 is the
// smallest possible slice of the hub (issue .3): the dependency, the
// `GET /ws` route and a handler that accepts the handshake and closes cleanly.
//
// Deliberately absent, and owned by later subtasks of .3:
//
//   - no hub and no fan-out: nothing is broadcast to a connected client, so a
//     listener that connects now receives exactly one close frame;
//   - no snapshot on connect: a listener is not yet sent the current code
//     document, the anchor or the transport state;
//   - no ping/pong keepalive and no read loop: nothing ever reads from the
//     client (later issues use websocket.Conn.CloseRead, which is what makes
//     `Close` a bounded clean shutdown);
//   - no listener counting: the Conductor's ListenerCount stays 0.
//
// Keeping the slice this small is the point: it proves the upgrade path works
// through both a real listener and httptest before any state is layered on top.

// handleWS accepts a listener's WebSocket handshake and closes the connection
// again with a normal-closure frame. Accept writes the 101 Switching Protocols
// response itself, including the RFC 6455 Sec-WebSocket-Accept header.
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

	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		slog.Debug("websocket close", "path", r.URL.Path, "error", err)
	}
}
