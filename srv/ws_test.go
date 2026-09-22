package srv

// This file is the WebSocket slice of the repo-owned integration harness. It
// drives the real handler tree (`Server.routes()`, the same tree `Server.Serve`
// mounts) over a real loopback listener and over an in-process recorder, which
// is the same two-mode discipline the rest of `integration_test.go` uses.
//
// Scope today (issues .3.2 and .3.4.2): mount `GET /ws`, accept the upgrade,
// register the listener with the hub, relay every hub message to it as a text
// frame, and unsubscribe on every exit path. There is still deliberately no
// snapshot on connect, no /api fan-out, no ping/pong and no listener counting,
// so a listener that connects hears nothing until the next broadcast. The
// accept-and-close expectations issue .3.2 pinned here were REVISED on purpose
// when the hub was wired in — a behaviour change made in the open, which is
// what this file's history is for — and the connect/disconnect/close paths are
// now asserted directly instead.
//
// Boundedness is a requirement, not a nicety: every dial and every read carries
// an explicit deadline, and the test server is torn down with a timeout on a
// goroutine rather than a bare `defer ts.Close()`, because `httptest.Server.Close`
// waits for outstanding requests and would itself hang on a wedged handler.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Every bound this file uses, in one place. Nothing here may block forever.
const (
	// wsDialTimeout bounds one handshake, and wsReadTimeout bounds one read of
	// whatever the server sends after it. Both are generous for a loopback
	// connection: anything slower is a hang, and a hang is a defect.
	wsDialTimeout = 5 * time.Second
	wsReadTimeout = 5 * time.Second

	// wsServerCloseTimeout bounds httptest.Server.Close, which waits for
	// outstanding requests. A wedged handler must fail the test, not hang it.
	wsServerCloseTimeout = 10 * time.Second

	// wsHubCloseTimeout bounds Hub.Close (and the hub's own exit) during test
	// teardown, for the same reason: a missing hub goroutine must fail the test
	// rather than stall it.
	wsHubCloseTimeout = 5 * time.Second

	// wsSubscriberTimeout bounds the poll that waits for a listener to appear in
	// (or disappear from) the hub. Polling SubscriberCount is deterministic
	// rather than a guess: Subscribe and Unsubscribe both return only after the
	// hub goroutine has acted, so the count is already correct when the frame
	// that triggered the change has been read, and this poll normally succeeds on
	// its very first iteration.
	wsSubscriberTimeout = 5 * time.Second

	// wsWriteBudget is the per-write deadline the harness installs on the server
	// under test. The production value is generous; the tests set it small so one
	// wedged listener test costs a fraction of a second instead of the full
	// timeout. 250ms is still many times longer than any loopback write needs.
	wsWriteBudget = 250 * time.Millisecond

	// wsWedgeReceiveBuffer is the kernel receive buffer (SO_RCVBUF) the wedged
	// client asks for, and wsWedgedPayloadBytes is the frame the server tries to
	// write to it. The socket window is then a couple of KiB, so a 1 MiB frame
	// cannot fit in any buffer on either side of the connection: the server's
	// write must block until the client reads (which it never does) or the
	// deadline fires. That is what makes "wedged" a matter of construction
	// rather than of timing. (Measured on this VM: a 64 KiB frame blocks for the
	// full deadline with this receive buffer, so 1 MiB has ~16x the margin.)
	wsWedgeReceiveBuffer  = 1024
	wsWedgedPayloadBytes  = 1 << 20
	wsWedgedBroadcastTime = 4 * wsWriteBudget
)

// wsTestServerOptions configures the server a websocket test runs against. A
// zero value means "the defaults New installs", which is what the tests that
// are not specifically about the write bound want.
type wsTestServerOptions struct {
	// writeTimeout overrides Server.wsWriteTimeout when non-zero, so the wedged
	// listener test spends 250ms proving the bound instead of 5s.
	writeTimeout time.Duration
}

// wsTestServer boots the real handler tree on a loopback listener (a real TCP
// socket, not the in-process recorder) and returns the server plus the
// ws:// URL of the /ws endpoint. Teardown is registered on the test and
// bounded, so a handler that never returns fails the test instead of stalling
// the run.
func wsTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, url, _ := wsTestServerWith(t, wsTestServerOptions{})
	return s, url
}

// wsTestServerWith is wsTestServer with the knobs a specific test needs, and it
// additionally hands back the httptest.Server so a test can assert on teardown
// directly instead of leaving it entirely to the cleanup.
//
// Teardown order matters and is deliberate: the hub is closed BEFORE the test
// server, so every /ws handler is unblocked and can return, and only then is the
// listener closed. httptest.Server.Close does not itself wait for a hijacked
// websocket handler (the connection left the server's tracking at the upgrade),
// so the hub close is what keeps handlers from being left behind — and both
// steps are bounded, so a handler that never returns fails the test instead of
// stalling the run.
func wsTestServerWith(t *testing.T, opts wsTestServerOptions) (*Server, string, *httptest.Server) {
	t.Helper()

	s := New("ws-host")
	if opts.writeTimeout > 0 {
		s.wsWriteTimeout = opts.writeTimeout
	}
	ts := httptest.NewServer(s.routes())

	t.Cleanup(func() {
		closedHub := make(chan struct{})
		go func() {
			s.Hub.Close()
			close(closedHub)
		}()
		select {
		case <-closedHub:
		case <-time.After(wsHubCloseTimeout):
			t.Errorf("Hub.Close did not return within %s during teardown: the hub goroutine is wedged", wsHubCloseTimeout)
		}
		wsCloseServer(t, ts)
	})

	// ws:// (or wss://) on the same host:port the listener picked.
	return s, "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws", ts
}

// wsCloseServer closes an httptest.Server with a bound. It exists because a
// bare `defer ts.Close()` (or one hidden in a cleanup, which is the same thing)
// is exactly the construct that turns a wedged handler into an unhittable hang:
// Close waits for outstanding requests. Closing from a goroutine and requiring
// it to return keeps "the handler is wedged" a test FAILURE with a message.
// Calling it more than once is safe: httptest.Server.Close is idempotent.
func wsCloseServer(t *testing.T, ts *httptest.Server) {
	t.Helper()
	closed := make(chan struct{})
	go func() {
		ts.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(wsServerCloseTimeout):
		t.Errorf("httptest.Server.Close did not return within %s: a /ws handler is wedged", wsServerCloseTimeout)
	}
}

// wsWaitForSubscribers waits, bounded, for the hub to hold exactly want
// subscribers. It is the observable half of "a listener connected" and "a
// listener went away / was dropped": SubscriberCount is answered by the hub
// goroutine itself, so the poll is a real synchronisation point and not a
// sleep-and-hope.
func wsWaitForSubscribers(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(wsSubscriberTimeout)
	for {
		if got := h.SubscriberCount(); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hub holds %d subscribers after %s, want %d", h.SubscriberCount(), wsSubscriberTimeout, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// wsReadMessage reads one message from a client, bounded, and requires it to be
// a text frame carrying EXACTLY want. Nothing about the payload is paraphrased:
// the bytes a listener receives are compared to the bytes that were broadcast,
// so an extra newline, a re-encoding or a truncation is a failure.
func wsReadMessage(t *testing.T, conn *websocket.Conn, what string, want []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()

	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("%s: no message within %s: %v", what, wsReadTimeout, err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("%s: frame type = %v, want %v: the hub relays encoded text, and a binary frame is a different contract on the wire", what, typ, websocket.MessageText)
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("%s: message = %q, want exactly %q", what, data, want)
	}
}

// wsDial performs one handshake against url with every step bounded: the
// handshake gets wsDialTimeout and the client transport has its own dial and
// response-header timeouts. The returned connection is closed with the test.
func wsDial(t *testing.T, url string, header http.Header) (*websocket.Conn, *http.Response) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), wsDialTimeout)
	defer cancel()

	client := &http.Client{Transport: boundedTransport(), Timeout: wsDialTimeout}
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: header,
	})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("websocket.Dial(%s): %v (handshake status=%d)", url, err, status)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, resp
}

// TestWSUpgradeSucceedsAndStaysOpen is this slice's first behaviour, over a
// real listener: GET /ws with a genuine WebSocket handshake must be answered
// 101 Switching Protocols, and the connection must then STAY OPEN — a connected
// listener is a subscriber now, not a connection that is closed again as soon
// as it is upgraded.
//
// (This replaces issue .3.2's expectation that the first thing a client sees is
// a close frame. That expectation was correct while the handler discarded the
// connection; it is now wrong by design, and the replacement asserts the new
// contract positively — the listener stays subscribed — instead of merely
// dropping the old assertion.)
func TestWSUpgradeSucceedsAndStaysOpen(t *testing.T) {
	s, url := wsTestServer(t)

	conn, resp := wsDial(t, url, nil)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("GET /ws handshake status = %d, want %d Switching Protocols", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	// The upgrade registered this listener with the hub: the count moving to 1
	// is the positive evidence, and it is answered by the hub goroutine, so it
	// needs no sleep.
	wsWaitForSubscribers(t, s.Hub, 1)

	// The connection is still open: a read that is given a short, explicit
	// window must expire with a timeout rather than with a close frame. A close
	// here means the handler tore the listener down after subscribing.
	shortCtx, cancel := context.WithTimeout(context.Background(), wsWriteBudget)
	defer cancel()
	if typ, data, err := conn.Read(shortCtx); err == nil {
		t.Fatalf("a freshly connected listener received %v %q: this slice sends nothing on connect (no snapshot yet)", typ, data)
	} else if code := websocket.CloseStatus(err); code != -1 {
		t.Fatalf("the listener's connection closed with %v (code %d), want it to stay open and subscribed", err, code)
	} else if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("read on an idle, subscribed connection failed with %v, want a timeout: the connection must stay open", err)
	}
}

// TestWSBroadcastReachesTheListenerAsExactBytes is the behaviour this slice
// exists for: a message handed to Hub.Broadcast arrives at a connected /ws
// listener as one text frame carrying EXACTLY the bytes that were broadcast.
//
// Multiple messages in sequence are used, because the interesting failures are
// not "nothing arrived": they are a dropped or duplicated message, a message
// delivered to the wrong listener, or a payload that was subtly altered on the
// way (re-encoded as JSON, newline-trimmed, truncated). Comparing bytes and
// order catches all of them, and it also pins that Broadcast order is preserved
// end to end.
func TestWSBroadcastReachesTheListenerAsExactBytes(t *testing.T) {
	s, url := wsTestServer(t)

	conn, _ := wsDial(t, url, nil)
	wsWaitForSubscribers(t, s.Hub, 1)

	// Deliberately awkward payloads: a trailing newline, inner quotes and
	// braces, a multi-byte UTF-8 rune and a NUL. Anything that prettifies, trims
	// or re-encodes the hub's bytes changes at least one of these.
	payloads := [][]byte{
		[]byte(`{"kind":"code","version":1}` + "\n"),
		[]byte(`{"kind":"message","text":"now with 100% more "quotes""}`),
		[]byte(`{"kind":"anchor","text":"héllo — ünicode ✓"}`),
		[]byte("{\"kind\":\"raw\",\"bytes\":\"a\x00b\"}"),
	}

	for i, payload := range payloads {
		s.Hub.Broadcast(payload)
		wsReadMessage(t, conn, fmt.Sprintf("broadcast %d", i+1), payload)
	}
}

// TestWSDisconnectUnsubscribesTheListener pins the other half of the lifecycle
// with an observation, not a hope: when the client goes away, the handler must
// leave the hub, so SubscriberCount drops back. A leaked subscriber would keep
// a dead listener in the fan-out forever and, once .3.5 publishes the count,
// would report listeners that are not there.
//
// The disconnecting client closes without a close frame (CloseNow), which is
// also the realistic case for a browser tab being killed: the TCP connection
// goes away and the server must notice.
func TestWSDisconnectUnsubscribesTheListener(t *testing.T) {
	s, url := wsTestServer(t)

	conn, _ := wsDial(t, url, nil)
	wsWaitForSubscribers(t, s.Hub, 1)

	// A live subscriber really is wired up: prove it before tearing it down.
	first := []byte(`{"kind":"code","version":1}`)
	s.Hub.Broadcast(first)
	wsReadMessage(t, conn, "before disconnect", first)

	// The client vanishes.
	if err := conn.CloseNow(); err != nil {
		t.Fatalf("client CloseNow: %v", err)
	}

	wsWaitForSubscribers(t, s.Hub, 0)

	// And the hub stays healthy and empty: a broadcast now reaches nobody and
	// must not resurrect the departed subscriber.
	s.Hub.Broadcast([]byte(`{"kind":"code","version":2}`))
	wsWaitForSubscribers(t, s.Hub, 0)
}

// TestWSHubCloseEndsTheListenerWithACleanClose pins the server-side shutdown
// path: when the hub closes, every connected listener's read loop must end and
// the client must see a CLEAN normal-closure (1000) frame, not a dropped TCP
// connection. This is the difference between a browser that knows the
// performance is over and one that reconnects in a loop.
func TestWSHubCloseEndsTheListenerWithACleanClose(t *testing.T) {
	s, url := wsTestServer(t)

	conn, _ := wsDial(t, url, nil)
	wsWaitForSubscribers(t, s.Hub, 1)

	// Broadcast once first, so this test also proves the close arrives on a
	// connection that was demonstrably working, not merely one that was never
	// read from.
	msg := []byte(`{"kind":"code","version":1}`)
	s.Hub.Broadcast(msg)
	wsReadMessage(t, conn, "before hub close", msg)

	// Close the hub from another goroutine, bounded: Hub.Close blocks until the
	// hub goroutine has exited, and a hub that never exits must fail the test
	// rather than hang it.
	closed := make(chan struct{})
	go func() {
		s.Hub.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(wsHubCloseTimeout):
		t.Fatalf("Hub.Close did not return within %s", wsHubCloseTimeout)
	}

	// The client's next read must report a clean 1000 normal closure. The read
	// is bounded, and a handler that simply returned without closing would leave
	// the client hanging here until the bound fired.
	ctx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err == nil {
		t.Fatalf("after Hub.Close the listener received %v %q, want a close frame", typ, data)
	}
	if !errors.Is(err, net.ErrClosed) && websocket.CloseStatus(err) < 0 {
		t.Fatalf("after Hub.Close the listener's read failed with %v, want a close frame or a closed connection", err)
	}
	if code := websocket.CloseStatus(err); code != websocket.StatusNormalClosure {
		t.Fatalf("after Hub.Close the listener saw close code %d (%v), want %d normal closure", code, err, websocket.StatusNormalClosure)
	}

	// The hub removed the listener's subscriber on the way out, so nothing was
	// leaked by the shutdown.
	wsWaitForSubscribers(t, s.Hub, 0)
}

// TestWSWedgedListenerCannotHangTeardown is the boundedness test, and the
// client in it never reads a byte of the frame the server is trying to write.
//
// The wedge is a matter of construction, not of timing. The client asks the
// kernel for a 1 KiB receive buffer (SO_RCVBUF), so its socket window is a
// couple of KiB, and a 1 MiB frame cannot fit in any buffer anywhere on the
// path (measured on this VM: even a 64 KiB frame blocks for the full deadline
// with this receive buffer, so this has ~16x of margin). No read, no ack, no
// window update: the server's write must block on the socket. The write budget
// is set to 250ms for this test, so the doomed write costs a fraction of a
// second instead of the production 5s.
//
// What is asserted is the consequence a missing per-write deadline would have,
// and it is asserted at the connection, because that is where it is observable:
//
//  1. the listener really is subscribed before it stops reading (so the
//     broadcast had somewhere to go);
//  2. Hub.Close returns within the bound and Done is then closed;
//  3. the wedged listener is no longer a subscriber;
//  4. the server GAVE UP on it: the wedged client's socket is dead, so a read
//     fails. This is the assertion a missing deadline breaks. Without one, the
//     handler is still sitting in conn.Write when the client finally drains the
//     socket, the full 1 MiB message lands, and the connection stays open — a
//     leaked handler goroutine and a leaked subscriber slot per dead listener.
//     (httptest.Server.Close does NOT wait for a hijacked connection, so that
//     leak does not hang anything on its own: it has to be asserted, which is
//     exactly what this assertion is for.)
//
// Teardown of this test's own socket is bounded too (CloseNow in the cleanup),
// so nothing here can hang whatever the handler did.
func TestWSWedgedListenerCannotHangTeardown(t *testing.T) {
	s, url, _ := wsTestServerWith(t, wsTestServerOptions{writeTimeout: wsWriteBudget})

	conn := wsDialWedged(t, url)

	// The socket is connected and the handler is running: this client is a real
	// subscriber before it stops reading.
	wsWaitForSubscribers(t, s.Hub, 1)

	// One frame the client's socket cannot possibly accept.
	huge := bytes.Repeat([]byte("x"), wsWedgedPayloadBytes)
	broadcastDone := make(chan struct{})
	go func() {
		// The hub only buffers the message; the handler's write is what wedges.
		s.Hub.Broadcast(huge)
		close(broadcastDone)
	}()
	select {
	case <-broadcastDone:
	case <-time.After(wsWedgedBroadcastTime):
		t.Fatalf("Broadcast blocked for %s: the hub must never wait on a listener", wsWedgedBroadcastTime)
	}

	// Give the handler long enough to be well inside the doomed write (it has
	// work to do before it can even call Write), then close the hub. The
	// interesting assertion is the next one: Close must return, which requires
	// a handler that is mid-write on a dead client not to hold the hub open.
	time.Sleep(wsWedgedBroadcastTime)

	closedHub := make(chan struct{})
	go func() {
		s.Hub.Close()
		close(closedHub)
	}()
	select {
	case <-closedHub:
	case <-time.After(wsHubCloseTimeout):
		t.Fatalf("Hub.Close did not return within %s while a wedged listener was mid-write: a non-reading client must not be able to hang shutdown", wsHubCloseTimeout)
	}

	select {
	case <-s.Hub.Done():
	case <-time.After(wsHubCloseTimeout):
		t.Fatalf("Hub.Done was not closed within %s of Close returning: the hub goroutine is wedged", wsHubCloseTimeout)
	}

	// The wedged listener is dropped for not keeping up (and/or unsubscribed on
	// shutdown), so no subscriber slot survived the episode.
	wsWaitForSubscribers(t, s.Hub, 0)

	// The server must have given up on this client rather than still be trying
	// to write to it. Reading now cannot make the connection healthy: the write
	// that was in flight has already been abandoned and the socket closed, so
	// the read fails on the partially written frame. If the deadline were
	// missing, this read would instead complete the whole frame — the client is
	// finally draining, which is exactly what unblocks a write with no deadline
	// — and succeed. (This client's read limit is disabled in wsDialWedged for
	// precisely this reason: with the default limit the read would fail on size
	// instead, and would pass whether or not the server had given up.)
	readCtx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()
	typ, data, err := conn.Read(readCtx)
	if err == nil {
		t.Fatalf("the wedged listener completed a %v message of %d bytes after the server's write budget had long passed: the write had no deadline, so the handler was still waiting on a client that never read",
			typ, len(data))
	}
}

// wsDialWedged performs a handshake with a client that will never read, using a
// tiny kernel receive buffer so that a large frame cannot be buffered anywhere.
// The receive buffer is set on the raw socket in the dialer's Control hook:
// that is the one point where the fd exists and nothing has been written yet,
// so the window the server sees is small from the very first byte.
func wsDialWedged(t *testing.T, url string) *websocket.Conn {
	t.Helper()

	dialer := &net.Dialer{
		Timeout: wsDialTimeout,
		Control: func(_, _ string, raw syscall.RawConn) error {
			var sockErr error
			if err := raw.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, wsWedgeReceiveBuffer)
			}); err != nil {
				return err
			}
			return sockErr
		},
	}

	client := &http.Client{
		Transport: &http.Transport{DialContext: dialer.DialContext},
		Timeout:   wsDialTimeout,
	}

	ctx, cancel := context.WithTimeout(context.Background(), wsDialTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("wedged client handshake against %s: %v", url, err)
	}
	// The library's default 32 KiB read limit would reject the 1 MiB frame with
	// ErrMessageTooBig, which would make the test's closing read fail for a
	// reason that has nothing to do with the socket being wedged — and would
	// therefore pass even when the server is still writing. Disabling the limit
	// puts the whole question on the socket, where the wedge lives.
	conn.SetReadLimit(-1)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// TestWSConnectingAfterHubCloseIsClosedGoingAway pins the one path where a
// handshake can succeed and still be refused service: the hub was already shut
// down when the listener arrived, so Subscribe fails and there is no fan-out to
// join. The listener must be told so with a proper close handshake and a
// specific status — StatusGoingAway (1001), "this endpoint is going away" —
// rather than being left connected to a server that will never send it
// anything, or dropped with a bare TCP close.
//
// No subscriber is created on this path, so the hub count must stay 0: a handler
// that subscribed anyway would leak a slot into a hub that is already gone.
func TestWSConnectingAfterHubCloseIsClosedGoingAway(t *testing.T) {
	s, url := wsTestServer(t)

	// Shut the hub down before anyone connects. Close is idempotent, so the
	// test's own teardown can close it again safely.
	s.Hub.Close()

	conn, resp := wsDial(t, url, nil)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake against a closed hub status = %d, want %d: the upgrade is accepted before the hub is consulted",
			resp.StatusCode, http.StatusSwitchingProtocols)
	}

	ctx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err == nil {
		t.Fatalf("a listener connecting to a closed hub received %v %q, want a close frame", typ, data)
	}
	if code := websocket.CloseStatus(err); code != websocket.StatusGoingAway {
		t.Fatalf("a listener connecting to a closed hub saw close code %d (%v), want %d going away",
			code, err, websocket.StatusGoingAway)
	}

	if got := s.Hub.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount after a refused connection = %d, want 0: no subscriber may be created on the failure path", got)
	}
}

// TestWSRejectsNonGetVerbs pins the same routing idiom the /api routes use: a
// verb other than GET on /ws is a 405 carrying an Allow header that names the
// one permitted method, so a client can discover the contract instead of
// guessing. It runs in-process through the real handler tree (no listener
// needed) and over a real listener, because net/http strips the body of a HEAD
// response over the wire while the recorder keeps it.
func TestWSRejectsNonGetVerbs(t *testing.T) {
	verbs := []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodOptions,
		http.MethodTrace,
	}

	// In-process: every verb, body included.
	t.Run("in-process", func(t *testing.T) {
		s := newIntegrationSession(t)
		for _, method := range verbs {
			t.Run(method, func(t *testing.T) {
				w := s.do(httptest.NewRequest(method, "/ws", nil))
				if w.Code != http.StatusMethodNotAllowed {
					t.Fatalf("%s /ws status = %d, want %d (body=%q)", method, w.Code, http.StatusMethodNotAllowed, body(w))
				}
				if allow := w.Header().Get("Allow"); allow != http.MethodGet {
					t.Errorf("%s /ws Allow = %q, want %q", method, allow, http.MethodGet)
				}
				if msg := strings.TrimSpace(body(w)); msg == "" {
					t.Errorf("%s /ws has an empty body; a 405 must say which method is permitted", method)
				}
			})
		}
	})

	// Over a real listener: the rejection must survive the whole stack, not
	// just the in-process recorder.
	t.Run("over a listener", func(t *testing.T) {
		_, url := wsTestServer(t)
		ctx, cancel := context.WithTimeout(context.Background(), wsDialTimeout)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpURL(url), nil)
		if err != nil {
			t.Fatalf("build POST %s: %v", url, err)
		}
		resp, err := (&http.Client{Transport: boundedTransport(), Timeout: wsDialTimeout}).Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", url, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s status = %d, want %d", url, resp.StatusCode, http.StatusMethodNotAllowed)
		}
		if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
			t.Errorf("POST %s Allow = %q, want %q", url, allow, http.MethodGet)
		}
	})
}

// httpURL turns the ws:// URL wsTestServer returns back into an http:// one,
// for the plain-HTTP requests that are not handshakes.
func httpURL(wsURL string) string {
	return "http" + strings.TrimPrefix(wsURL, "ws")
}

// TestWSWithoutUpgradeHeadersIsAClearError pins that a plain GET of /ws — a
// browser opening the URL, a crawler, a health check — gets a specific HTTP
// error instead of a panic or a 500.
//
// Both sides of the request are covered on purpose because they fail on
// different paths inside the upgrade library:
//
//   - no upgrade headers at all (a bare GET) => 426 Upgrade Required;
//   - a valid handshake except for the wrong Sec-WebSocket-Version => 400.
//
// The 426 case deliberately ALSO runs through an in-process recorder, whose
// ResponseWriter cannot be hijacked: for a fully-formed handshake that path
// yields 501 Not Implemented, and the point of asserting it is that the handler
// must not panic and must not be the thing that invents a status code. Anything
// outside the documented set means the clear error contract broke.
func TestWSWithoutUpgradeHeadersIsAClearError(t *testing.T) {
	// 426 Upgrade Required: net/http's own text body, and the two headers RFC
	// 6455 says to send so a client knows what is expected.
	t.Run("plain GET is 426 Upgrade Required", func(t *testing.T) {
		inProcess := func(t *testing.T) *httptest.ResponseRecorder {
			t.Helper()
			return newIntegrationSession(t).get("/ws")
		}

		t.Run("in-process", func(t *testing.T) {
			w := inProcess(t)
			if w.Code != http.StatusUpgradeRequired {
				t.Fatalf("GET /ws without upgrade headers status = %d, want %d (body=%q)",
					w.Code, http.StatusUpgradeRequired, body(w))
			}
			if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("GET /ws without upgrade headers Content-Type = %q, want a plain-text error", ct)
			}
			if hint := w.Header().Get("Upgrade"); hint != "websocket" {
				t.Errorf("GET /ws without upgrade headers Upgrade = %q, want the %q hint", hint, "websocket")
			}
			if msg := strings.TrimSpace(body(w)); msg == "" {
				t.Error("GET /ws without upgrade headers has an empty body; the error must say what is wrong")
			}
		})

		t.Run("over a listener", func(t *testing.T) {
			_, url := wsTestServer(t)
			ctx, cancel := context.WithTimeout(context.Background(), wsDialTimeout)
			defer cancel()

			// A plain HTTP client makes exactly this request: no Connection,
			// Upgrade, Sec-WebSocket-Key or Sec-WebSocket-Version headers.
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpURL(url), nil)
			if err != nil {
				t.Fatalf("build GET %s: %v", url, err)
			}
			resp, err := (&http.Client{Transport: boundedTransport(), Timeout: wsDialTimeout}).Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", url, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusUpgradeRequired {
				t.Fatalf("plain GET %s status = %d, want %d", url, resp.StatusCode, http.StatusUpgradeRequired)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("plain GET %s Content-Type = %q, want a plain-text error", url, ct)
			}
		})
	})

	// A real WebSocket client with a corrupted version header is a different
	// code path inside the upgrade library, and must still be a clean 400.
	t.Run("bad Sec-WebSocket-Version is 400", func(t *testing.T) {
		_, url := wsTestServer(t)

		ctx, cancel := context.WithTimeout(context.Background(), wsDialTimeout)
		defer cancel()
		client := &http.Client{Transport: boundedTransport(), Timeout: wsDialTimeout}
		conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPClient: client,
			HTTPHeader: http.Header{"Sec-WebSocket-Version": []string{"12"}},
		})
		if conn != nil {
			_ = conn.CloseNow()
		}
		if err == nil {
			t.Fatal("handshake with Sec-WebSocket-Version: 12 succeeded, want a 400 rejection")
		}
		if resp == nil {
			t.Fatalf("handshake with Sec-WebSocket-Version: 12 failed without a response: %v", err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Sec-WebSocket-Version: 12 status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		if want := "13"; resp.Header.Get("Sec-WebSocket-Version") != want {
			t.Errorf("Sec-WebSocket-Version: 12 response advertises %q, want %q",
				resp.Header.Get("Sec-WebSocket-Version"), want)
		}
	})

	// A fully-formed handshake through a non-hijackable ResponseWriter is the
	// in-process edge the httptest recorder exposes. It must be a clean 501,
	// never a panic, and never a status the handler invented itself.
	t.Run("non-hijackable writer is 501", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/ws", nil)
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Sec-WebSocket-Version", "13")
		r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

		w := newIntegrationSession(t).do(r)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("upgrade through a non-hijackable writer status = %d, want %d (body=%q)",
				w.Code, http.StatusNotImplemented, body(w))
		}
	})
}

// TestWSRouteDoesNotLeakIntoTheShell pins the containment of the new route.
// Mounting /ws must not widen either catch-all in routes():
//
//   - /ws is exact: /ws/ and /ws/anything stay net/http's plain-text 404
//     (only the `{$}` and prefix patterns ever match more than one path);
//   - /api/ws keeps its JSON 404 body, because the bare "/api" catch-all is
//     registered for the whole /api subtree and must stay that way.
//
// Both were already specified elsewhere in the harness; they are re-asserted
// here from the /ws side because a too-broad "/ws" pattern (or a "/ws/"
// catch-all added for convenience) is exactly the kind of change that would
// silently break them.
func TestWSRouteDoesNotLeakIntoTheShell(t *testing.T) {
	s := newIntegrationSession(t)

	for _, path := range []string{"/ws/", "/ws/extra", "/wss", "/wsx"} {
		t.Run("plain "+path, func(t *testing.T) {
			w := s.get(path)
			if w.Code != http.StatusNotFound {
				t.Fatalf("GET %s status = %d, want 404", path, w.Code)
			}
			if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
				t.Errorf("GET %s Content-Type = %q, want net/http's plain text: /ws must match exactly one path", path, ct)
			}
			if !strings.Contains(body(w), "404 page not found") {
				t.Errorf("GET %s body = %q, want net/http's plain-text 404", path, body(w))
			}
		})
	}

	// The pre-existing non-/api 404 shape, unchanged by this issue.
	w := s.get("/definitely-not-here")
	if w.Code != http.StatusNotFound || !strings.Contains(body(w), "404 page not found") {
		t.Errorf("GET /definitely-not-here = %d %q, want the plain-text 404", w.Code, body(w))
	}

	// /api/ws is NOT the WebSocket: it stays the API's JSON 404.
	w = s.get("/api/ws")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/ws status = %d, want 404", w.Code)
	}
	if msg := errorMessage(t, w); !strings.Contains(msg, "/api/ws") {
		t.Errorf("GET /api/ws error = %q, want it to name the requested path", msg)
	}
}

// TestWSUpgradeIsSameOriginOnly makes the security property of the handshake
// explicit, so nobody "fixes" a browser bug later by disabling it. The upgrade
// library refuses a cross-origin handshake (403) unless an explicit origin
// allow-list is passed to Accept; neither this slice nor any peer on the same
// host needs that, because the page is served from the same origin as /ws.
//
// This is the only assertion here that would notice an `InsecureSkipVerify:
// true` added to the AcceptOptions, which is the tempting shortcut for a client
// that gets an inexplicable 403 in a browser console.
func TestWSUpgradeIsSameOriginOnly(t *testing.T) {
	_, url := wsTestServer(t)

	t.Run("cross-origin is refused", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), wsDialTimeout)
		defer cancel()
		client := &http.Client{Transport: boundedTransport(), Timeout: wsDialTimeout}
		conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPClient: client,
			HTTPHeader: http.Header{"Origin": []string{"http://evil.example"}},
		})
		if conn != nil {
			_ = conn.CloseNow()
		}
		if err == nil {
			t.Fatal("a cross-origin handshake succeeded, want a 403: Accept must keep its same-origin default")
		}
		if resp == nil {
			t.Fatalf("cross-origin handshake failed without a response: %v", err)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("cross-origin handshake status = %d, want %d", resp.StatusCode, http.StatusForbidden)
		}
	})

	t.Run("same-origin is accepted", func(t *testing.T) {
		self := httpURL(url) // the exact origin this listener is served from
		ctx, cancel := context.WithTimeout(context.Background(), wsDialTimeout)
		defer cancel()
		client := &http.Client{Transport: boundedTransport(), Timeout: wsDialTimeout}
		conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPClient: client,
			HTTPHeader: http.Header{"Origin": []string{self}},
		})
		if err != nil {
			t.Fatalf("same-origin handshake (%s) failed: %v", self, err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		if resp.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("same-origin handshake status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
		}
	})
}

// TestWSHeadIsNotRejected pins the one verb that must NOT be in the 405 list:
// net/http's ServeMux treats a "GET <path>" pattern as matching HEAD too, so
// HEAD /ws reaches the upgrade handler instead of the fallback. Its actual
// outcome is therefore the same as a bare GET — a 426, because a HEAD request
// carries no upgrade headers — and that is asserted here rather than left to
// chance, so nobody "fixes" HEAD into a 405 by reflex.
//
// (Were HEAD ever to be given an explicit 405, this test and
// TestWSRejectsNonGetVerbs together make the choice visible in the diff.)
func TestWSHeadIsNotRejected(t *testing.T) {
	s := newIntegrationSession(t)
	w := s.do(httptest.NewRequest(http.MethodHead, "/ws", nil))

	if w.Code == http.StatusMethodNotAllowed {
		t.Fatalf("HEAD /ws = 405 with Allow %q: ServeMux folds HEAD into the GET pattern, so HEAD must reach the upgrade handler",
			w.Header().Get("Allow"))
	}
	if w.Code != http.StatusUpgradeRequired {
		t.Fatalf("HEAD /ws without upgrade headers status = %d, want %d (body=%q)",
			w.Code, http.StatusUpgradeRequired, body(w))
	}
}
