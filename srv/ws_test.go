package srv

// This file is the WebSocket slice of the repo-owned integration harness. It
// drives the real handler tree (`Server.routes()`, the same tree `Server.Serve`
// mounts) over a real loopback listener and over an in-process recorder, which
// is the same two-mode discipline the rest of `integration_test.go` uses.
//
// Scope today (issues .3.2, .3.4.2 and .3.4.3): mount `GET /ws`, accept the
// upgrade, register the listener with the hub, send it the catch-up snapshot,
// relay every hub message to it as a text frame, and unsubscribe on every exit
// path. There is still deliberately no ping/pong and no listener counting, so
// nothing detects a dead socket that has not closed. The accept-and-close
// expectations issue .3.2 pinned here were REVISED on purpose when the hub was
// wired in — a behaviour change made in the open, which is what this file's
// history is for — and the connect/disconnect/close paths are now asserted
// directly instead. The "nothing on connect" expectation was revised the same
// way when snapshot-on-connect landed; both revisions inverted an assertion
// rather than deleting it.
//
// Boundedness is a requirement, not a nicety: every dial and every read carries
// an explicit deadline, and the test server is torn down with a timeout on a
// goroutine rather than a bare `defer ts.Close()`, because `httptest.Server.Close`
// waits for outstanding requests and would itself hang on a wedged handler.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
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

	// pingInterval and pongTimeout override the keepalive budgets when
	// non-zero, so the reaping tests cost tens of milliseconds rather than the
	// production 30s. They are separate knobs because the reaper's timing is a
	// RATIO of the two: a client is declared dead only when it fails to answer
	// within pongTimeout of an interval, so a test that scaled only one of them
	// would be asserting a different policy than production runs.
	pingInterval time.Duration
	pongTimeout  time.Duration
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

	s := New()
	if opts.writeTimeout > 0 {
		s.wsWriteTimeout = opts.writeTimeout
	}
	if opts.pingInterval > 0 {
		s.wsPingInterval = opts.pingInterval
	}
	if opts.pongTimeout > 0 {
		s.wsPongTimeout = opts.pongTimeout
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
//
// Each read of the count is bounded on its own (hubSubscriberCountWithin), not
// just the loop: a deadline checked after a call that never returns is not a
// bound, and a hub goroutine that stops answering the count command would
// otherwise park every caller here until the go test timeout. The two failure
// modes are reported separately, because they are different defects: the hub
// stopped answering at all (a wedge) versus the hub answered with a count that
// never became want (a wrong count).
func wsWaitForSubscribers(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(wsSubscriberTimeout)
	last := 0
	for {
		got, ok := hubSubscriberCountWithin(h, hubCountTimeout)
		if !ok {
			t.Fatalf("hub did not answer SubscriberCount within %s (last count read: %d, want %d after %s): the hub goroutine is wedged, so the subscriber count cannot be read at all", hubCountTimeout, last, want, wsSubscriberTimeout)
		}
		last = got
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			// Report the count already read rather than calling
			// SubscriberCount again: a second unbounded call on the
			// failure path would reintroduce the very hang this bounds.
			t.Fatalf("hub holds %d subscribers after %s, want %d", last, wsSubscriberTimeout, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// wsReadConnectSnapshot reads the one frame every listener is sent on connect
// and asserts it is a well-formed EventSnapshot whose payload equals wantSnap.
//
// It exists because "the first thing a listener sees" is now a load-bearing
// part of the contract rather than an absence of one. Every test that goes on
// to assert about LATER frames has to consume this one first, or it would read
// the catch-up snapshot and mistake it for the broadcast it meant to check.
//
// wantSnap is compared field by field rather than as encoded bytes because the
// snapshot is captured at a slightly different instant on each run (the anchor
// carries a wall-clock epoch); the byte-identity with GET /api/state is proved
// once, properly, in the integration harness.
func wsReadConnectSnapshot(t *testing.T, conn *websocket.Conn, wantSnap Snapshot) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()

	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("connect snapshot: no frame within %s: %v", wsReadTimeout, err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("connect snapshot: frame type = %v, want %v", typ, websocket.MessageText)
	}

	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("connect snapshot: frame is not a JSON Event (%v): %q", err, data)
	}
	if ev.Kind != EventSnapshot {
		t.Fatalf("connect snapshot: kind = %q, want %q: the opening frame tells a client it has caught up, not that the performance changed", ev.Kind, EventSnapshot)
	}
	if ev.Snapshot.Version != wantSnap.Version || ev.Snapshot.Code != wantSnap.Code ||
		ev.Snapshot.LastAgentMessage != wantSnap.LastAgentMessage || ev.Snapshot.Playing != wantSnap.Playing {
		t.Fatalf("connect snapshot: got %+v, want %+v", ev.Snapshot, wantSnap)
	}
	return ev
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

// ---------- the listener count, end to end (issue .3.5) ----------

// wsGetJSON performs one bounded GET and decodes the body as a JSON object.
// json.Number is used so numeric fields compare as numbers and never as float
// text.
func wsGetJSON(t *testing.T, base, path string) (map[string]any, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: boundedTransport(), Timeout: wsReadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	var got map[string]any
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		return nil, fmt.Errorf("GET %s: decode body: %w", path, err)
	}
	return got, nil
}

// wsListenerCountField extracts listenerCount from a decoded state object, as a
// number. A missing or non-numeric field is the failure it is, not a zero:
// reading a missing field as 0 is exactly how a permanently-zero listener count
// would pass unnoticed.
func wsListenerCountField(t *testing.T, got map[string]any) int {
	t.Helper()
	raw, ok := got["listenerCount"]
	if !ok {
		t.Fatalf("the state object has no listenerCount field: %v", got)
	}
	n, ok := raw.(json.Number)
	if !ok {
		t.Fatalf("listenerCount = %v (%T), want a JSON number", raw, raw)
	}
	v, err := n.Int64()
	if err != nil {
		t.Fatalf("listenerCount = %s, want an integer: %v", n.String(), err)
	}
	return int(v)
}

// wsWantStateListenerCount waits, bounded, for GET /api/state to report
// listenerCount == want.
//
// The poll is over the HTTP body rather than over Hub.SubscriberCount, because
// the body is what the agent contract promises: a count that was correct in the
// hub but absent from /api/state would pass a hub-level assertion and fail every
// real client. Each individual read is bounded (so a wedged handler fails the
// test rather than parking the poll), and so is the whole wait.
func wsWantStateListenerCount(t *testing.T, base string, want int) {
	t.Helper()
	deadline := time.Now().Add(wsSubscriberTimeout)
	last := -1
	for {
		got, err := wsGetJSON(t, base, "/api/state")
		if err != nil {
			t.Fatalf("GET /api/state: %v: the count must be readable over HTTP, not merely present in the hub", err)
		}
		last = wsListenerCountField(t, got)
		if last == want {
			return
		}
		if time.Now().After(deadline) {
			// Report what was read rather than reading again: a second
			// unbounded read on the failure path is the hang this bounds.
			t.Fatalf("GET /api/state reports listenerCount=%d after %s, want %d", last, wsSubscriberTimeout, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// wsDeadListener dials /ws with a raw net.Conn, completes the RFC 6455
// handshake by hand, and then goes silent FOREVER: it never reads, never
// answers a ping, and never sends a close frame.
//
// A coder/websocket client cannot be used for this. Its read goroutine answers
// pings automatically, so such a client is always responsive and can never
// reproduce a dead socket. Speaking the handshake directly is what makes the
// silence a property of construction rather than of timing — the same
// discipline the wedged-listener test uses for its write wedge.
//
// The returned conn is closed with the test.
func wsDeadListener(t *testing.T, url string) net.Conn {
	t.Helper()

	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatalf("parse %s: %v", url, err)
	}
	host := u.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "80")
	}

	ctx, cancel := context.WithTimeout(context.Background(), wsDialTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		t.Fatalf("dial %s: %v", host, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Any 16 bytes, base64'd, is a valid Sec-WebSocket-Key. The server echoes
	// the accept token derived from it, which is what proves the handshake
	// completed and this connection is a real listener.
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	req := "GET " + u.Path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"\r\n"
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d, want 101 (this connection is not a listener, so the test would prove nothing)", resp.StatusCode)
	}
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got == "" {
		t.Fatal("handshake response has no Sec-WebSocket-Accept")
	}

	// From here on the socket is silent forever. Clearing the handshake
	// deadline is what makes this a DEAD client rather than a slow one: every
	// later failure is the server failing to detect silence, never the test
	// failing to wait.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		t.Fatalf("clear handshake deadline: %v", err)
	}
	return conn
}

// wsWantSubscriberCount waits, bounded, for the hub to hold exactly want
// subscribers.
func wsWantSubscriberCount(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(wsSubscriberTimeout)
	last := -1
	for {
		got := hubSubscriberCount(t, h)
		last = got
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hub still holds %d subscribers after %s, want %d: a listener that stopped answering was never reaped", last, wsSubscriberTimeout, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ---------- ping/pong keepalive and dead-client reaping (issue .3.6) ----------

// TestWSDeadListenerIsReaped is the core of issue .3.6: a listener that stops
// answering must be detected and reclaimed, rather than holding its handler
// goroutine and its subscriber slot for the lifetime of the process.
//
// The client is wsDeadListener — a completed handshake followed by permanent
// silence — so this is a property of construction, not of timing. Its pings go
// into the kernel buffer and are never answered, so the pong deadline is the
// only thing that can end it.
//
// The listener count is read back over HTTP rather than from Hub.SubscriberCount,
// because the body is what the agent contract promises: a count that was correct
// in the hub but never published would pass a hub-level assertion and fail every
// real client. That reaping decrements the count at all is a load-bearing part of
// this feature — a reaper that logged the client out of the hub but left the
// published count stale would leak a phantom listener into /api/state forever.
func TestWSDeadListenerIsReaped(t *testing.T) {
	s, url, ts := wsTestServerWith(t, wsTestServerOptions{
		pingInterval: 20 * time.Millisecond,
		pongTimeout:  20 * time.Millisecond,
	})

	dead := wsDeadListener(t, url)

	// It really is a listener first. A socket that never subscribed would
	// satisfy "the count is 0 again" for entirely the wrong reason.
	wsWaitForSubscribers(t, s.Hub, 1)
	wsWantStateListenerCount(t, ts.URL, 1)

	// Now it is silent forever, so the server must give up on it.
	wsWantStateListenerCount(t, ts.URL, 0)
	wsWaitForSubscribers(t, s.Hub, 0)

	// The socket is closed, not merely forgotten: the subscriber is gone but the
	// handler could still be holding the connection.
	//
	// The server legitimately sent frames before giving up (the connect
	// snapshot, and any ping that fitted in the socket buffer), so this DRAINS
	// until the socket errors rather than expecting the first read to fail.
	// Draining is bounded by the read deadline, and the deadline firing — the
	// server still holding the connection open — is the failure.
	if err := dead.SetReadDeadline(time.Now().Add(wsReadTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	for {
		if _, err := dead.Read(buf); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				t.Errorf("the dead listener's socket is still readable after %s: the server reaped the subscriber but never closed the connection", wsReadTimeout)
			}
			// Any other error (EOF, ECONNRESET) is the server having closed it.
			break
		}
	}
}

// TestWSListenerSurvivesPingCycles is the other half of the keepalive, and the
// half a reaper that fires too eagerly would break: a listener that is alive and
// answering must NOT be reaped.
//
// A reaper that reaped the living would be indistinguishable from one that never
// runs at the byte level — both end with an absent subscriber — so without this
// test a mutation that inverted the ping result, or that ignored a pong, could
// satisfy the reaping test above.
func TestWSListenerSurvivesPingCycles(t *testing.T) {
	const cycles = 5
	s, url, _ := wsTestServerWith(t, wsTestServerOptions{
		pingInterval: 10 * time.Millisecond,
		pongTimeout:  10 * time.Millisecond,
	})

	conn, _ := wsDial(t, url, nil)
	wsWaitForSubscribers(t, s.Hub, 1)
	wsReadConnectSnapshot(t, conn, s.Conductor.Snapshot())

	// A browser answers control frames in its network stack, with no
	// JavaScript reading them. A coder/websocket client does NOT: it replies to
	// a ping only while something is reading the connection. So a reader has to
	// run for the whole test, or the server quite correctly reaps a client that
	// is — from the library's point of view — as silent as a dead one.
	//
	// CloseRead would give that reader for free, but it also DISCARDS data
	// frames and closes the connection on the first one, which makes the
	// post-ping broadcast assertion below impossible. So this reads frames into
	// a channel instead: it answers pings AND keeps the payloads.
	//
	// (This test failed first because it read nothing, which is worth recording:
	// the failure looked like a reaper that killed the living, and the cause was
	// entirely in the test.)
	frames := make(chan []byte, 8)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			rctx, rcancel := context.WithTimeout(context.Background(), wsReadTimeout)
			typ, data, err := conn.Read(rctx)
			rcancel()
			if err != nil || typ != websocket.MessageText {
				return
			}
			select {
			case frames <- data:
			default:
			}
		}
	}()
	t.Cleanup(func() {
		_ = conn.CloseNow()
		select {
		case <-readerDone:
		case <-time.After(wsReadTimeout):
			t.Errorf("the client reader did not exit within %s after the socket was closed", wsReadTimeout)
		}
	})

	// Sit through several ping intervals. The wait is generous on purpose (many
	// times cycles x interval): the assertion is that the listener SURVIVES a
	// long quiet period, not that it survives a fast one.
	time.Sleep(cycles * 10 * time.Millisecond)

	// Still subscribed, and still one.
	if got := hubSubscriberCount(t, s.Hub); got != 1 {
		t.Fatalf("hub holds %d subscribers after %d ping cycles, want 1: a listener that answered every ping was reaped anyway", got, cycles)
	}

	// And still WORKING: a survivor that had stopped being written to would pass
	// the count check above. Broadcasting proves the relay is intact, and that
	// this connection was never quietly reaped and resubscribed behind our back.
	msg := []byte(`{"kind":"code","version":1}`)
	s.Hub.Broadcast(msg)

	select {
	case got := <-frames:
		if !bytes.Equal(got, msg) {
			t.Fatalf("after %d ping cycles the listener received %q, want exactly %q", cycles, got, msg)
		}
	case <-time.After(wsReadTimeout):
		t.Fatalf("after %d ping cycles no broadcast arrived within %s: the listener survived the reaper but stopped being served", cycles, wsReadTimeout)
	}
}

// TestWSListenerCountIsPublishedOnConnectAndDisconnect is the end-to-end
// behaviour issue .3.5 exists for: GET /api/state.listenerCount tracks the real
// number of connected listeners across connect, clean disconnect and abrupt
// disconnect.
//
// The sequence 0 -> 1 -> 3 -> 2 -> 0 is the bead's own scenario, and every step
// is asserted on the response body. The two disconnects are deliberately
// different kinds — a clean close frame (a listener navigating away) and
// CloseNow with no close frame at all (a killed tab, a yanked cable) — because
// the server detects them by different routes, and only the abrupt one proves
// the detection is the read side noticing rather than the close handshake.
func TestWSListenerCountIsPublishedOnConnectAndDisconnect(t *testing.T) {
	s, url, ts := wsTestServerWith(t, wsTestServerOptions{})
	base := ts.URL

	// Nobody has connected yet.
	wsWantStateListenerCount(t, base, 0)

	// One, then three.
	c1, _ := wsDial(t, url, nil)
	wsWantStateListenerCount(t, base, 1)
	c2, _ := wsDial(t, url, nil)
	wsWantStateListenerCount(t, base, 2)
	c3, _ := wsDial(t, url, nil)
	wsWantStateListenerCount(t, base, 3)

	// The hub and the API must agree: the published count is not a second,
	// independent tally maintained somewhere else.
	if got := hubSubscriberCount(t, s.Hub); got != 3 {
		t.Fatalf("SubscriberCount = %d while /api/state reports 3: the published count and the hub disagree", got)
	}

	// A CLEAN disconnect: the client sends a close frame.
	if err := c2.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("client clean close: %v", err)
	}
	wsWantStateListenerCount(t, base, 2)

	// An ABRUPT disconnect: the socket goes away with no close frame, which is
	// what a killed browser tab looks like to the server.
	if err := c1.CloseNow(); err != nil {
		t.Fatalf("client CloseNow: %v", err)
	}
	wsWantStateListenerCount(t, base, 1)

	// The last one, also abruptly.
	if err := c3.CloseNow(); err != nil {
		t.Fatalf("client CloseNow: %v", err)
	}
	wsWantStateListenerCount(t, base, 0)

	// And the count really reached zero rather than being pinned there: the hub
	// holds nobody, and a broadcast reaches nobody rather than resurrecting a
	// departed subscriber.
	if got := hubSubscriberCount(t, s.Hub); got != 0 {
		t.Fatalf("SubscriberCount = %d after every listener left, want 0", got)
	}
	s.Hub.Broadcast([]byte(`{"kind":"code"}`))
	wsWantStateListenerCount(t, base, 0)
}

// TestWSConnectSnapshotCountsTheConnectingListener pins the user-visible
// consequence of the publish-before-reply ordering, which is the entire reason
// that ordering exists.
//
// A listener's FIRST frame is its catch-up snapshot, so the count a new listener
// sees on arrival is decided by when the hub published relative to when
// Subscribe returned. Published after the reply, this snapshot could report a
// listenerCount that excludes the very listener reading it — and because that is
// a race it would be intermittent, passing in almost every run.
//
// The count is read straight out of the frame with no waiting, because the claim
// is that it was ALREADY correct when the frame was written.
func TestWSConnectSnapshotCountsTheConnectingListener(t *testing.T) {
	s, url, _ := wsTestServerWith(t, wsTestServerOptions{})

	conn, _ := wsDial(t, url, nil)
	ev := wsReadConnectSnapshot(t, conn, s.Conductor.Snapshot())
	if ev.Snapshot.ListenerCount != 1 {
		t.Fatalf("the first listener's catch-up snapshot reports listenerCount=%d, want 1: the count must be published before Subscribe is acknowledged, or the snapshot counts the listener reading it as absent", ev.Snapshot.ListenerCount)
	}

	// A second listener sees two, which also rules out a count that is merely
	// "not zero".
	conn2, _ := wsDial(t, url, nil)
	ev2 := wsReadConnectSnapshot(t, conn2, s.Conductor.Snapshot())
	if ev2.Snapshot.ListenerCount != 2 {
		t.Fatalf("the second listener's catch-up snapshot reports listenerCount=%d, want 2", ev2.Snapshot.ListenerCount)
	}

	// The Conductor agrees with the frame it produced: the hook targets the same
	// field the frame carries, so a count published to one and not the other
	// would surface here.
	if got := s.Conductor.Snapshot().ListenerCount; got != 2 {
		t.Fatalf("Conductor.Snapshot().ListenerCount = %d, want 2", got)
	}
}

// TestWSListenerCountIsNotBroadcastAsAnEvent pins the deliberate scope limit of
// this issue: the count reaches /api/state and every snapshot, but a listener
// ARRIVING or LEAVING sends no frame of its own.
//
// This is asserted rather than assumed, because "the count changed" is exactly
// the kind of thing a later change would helpfully add a frame for — and adding
// an event kind is a wire-contract change that deserves a decision made on
// purpose rather than arriving by accident. If this test ever starts failing
// because a listener-count event WAS added, that is the moment to make the
// decision deliberately; see the listener-count follow-up bead.
//
// The negative assertion is a bounded WAIT, not a non-blocking peek: a peek
// cannot distinguish "nothing yet" from "nothing ever", so it would pass for a
// merely slow delivery. The reader runs on its own goroutine so the connection
// stays healthy and a later failure elsewhere cannot be masked by a dead socket.
func TestWSListenerCountIsNotBroadcastAsAnEvent(t *testing.T) {
	_, url, _ := wsTestServerWith(t, wsTestServerOptions{})

	conn, _ := wsDial(t, url, nil)
	// Consume the catch-up frame first: it is expected, and counting it as an
	// unexpected event would make this test fail for the right reason by the
	// wrong route.
	ctx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	if _, _, err := conn.Read(ctx); err != nil {
		cancel()
		t.Fatalf("connect snapshot: no frame within %s: %v", wsReadTimeout, err)
	}
	cancel()

	// Drain anything that arrives from here on.
	frames := make(chan string, 8)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			rctx, rcancel := context.WithTimeout(context.Background(), wsReadTimeout)
			_, data, err := conn.Read(rctx)
			rcancel()
			if err != nil {
				return
			}
			var ev Event
			if err := json.Unmarshal(data, &ev); err != nil {
				return
			}
			select {
			case frames <- ev.Kind:
			default:
			}
		}
	}()
	defer func() {
		_ = conn.CloseNow()
		select {
		case <-readerDone:
		case <-time.After(wsReadTimeout):
			t.Errorf("the background reader did not exit within %s after the socket was closed", wsReadTimeout)
		}
	}()

	// Somebody arrives and somebody leaves. Neither is a change to the
	// performance, so neither may produce a frame here.
	other, _ := wsDial(t, url, nil)
	_ = other.CloseNow()

	quiet := time.After(250 * time.Millisecond)
	for {
		select {
		case kind := <-frames:
			t.Fatalf("a listener connecting or leaving produced a %q frame for an already-connected listener; this issue publishes the count through /api/state only, and a count event is a separate wire-contract decision", kind)
		case <-quiet:
			return
		}
	}
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

	// The first thing a listener receives is the catch-up snapshot, not silence.
	// (This assertion INVERTS the one this test used to make. While the handler
	// discarded the connection it was correct to require a timeout; wiring the
	// hub in made "stays open" the real claim, and snapshot-on-connect made
	// "sends nothing first" false. The old expectation is replaced rather than
	// deleted, because a listener that connects mid-performance and gets no
	// state would sit silent until the next change — possibly never.)
	wsReadConnectSnapshot(t, conn, s.Conductor.Snapshot())

	// And the connection is STILL open afterwards: the snapshot is not the
	// prelude to an immediate close. A read given a short, explicit window must
	// expire with a timeout rather than with a close frame. A close here means
	// the handler tore the listener down after sending the catch-up.
	shortCtx, cancel := context.WithTimeout(context.Background(), wsWriteBudget)
	defer cancel()
	if typ, data, err := conn.Read(shortCtx); err == nil {
		t.Fatalf("a subscribed listener received a second frame %v %q with nothing changing: the connection must stay open and quiet until something happens", typ, data)
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
	// Consume the catch-up snapshot so it cannot be mistaken for broadcast 1.
	wsReadConnectSnapshot(t, conn, s.Conductor.Snapshot())

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
	wsReadConnectSnapshot(t, conn, s.Conductor.Snapshot())

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
	wsReadConnectSnapshot(t, conn, s.Conductor.Snapshot())

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
	// NOTE (issue .3.6): this test deliberately does NOT shorten the keepalive
	// budgets. It proves the per-write deadline, and the reaper added in .3.6 is
	// a second mechanism that could end this same wedged connection — which
	// would let the test pass for the wrong reason and quietly retire mutation
	// 38-ws-write-deadline-removed. Leaving pingInterval at its 30s default
	// keeps the write deadline the only thing that can end the connection inside
	// a test measured in seconds. Do not add pingInterval here without re-running
	// that mutation.
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
	// to write to it. The loop drains and inspects rather than reading once,
	// because the connect snapshot is a small frame that this client had simply
	// never read: it sits in the socket and a single read would return it,
	// proving nothing about the doomed write.
	//
	// So the question is not "did any read succeed" but "did the 1 MiB frame
	// ever arrive". Reading now cannot make the connection healthy: the write
	// that was in flight has already been abandoned and the socket closed. If
	// the deadline were missing, this handler would still be sitting in
	// conn.Write, and draining the socket here — which is exactly what this
	// loop does — would let the entire frame through, and the loop would see it
	// and fail. That is the assertion a missing deadline breaks. (This client's
	// read limit is disabled in wsDialWedged for precisely this reason: with the
	// default limit the read would fail on size instead, and would pass whether
	// or not the server had given up.)
	readCtx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()
	for {
		typ, data, err := conn.Read(readCtx)
		if err != nil {
			// The server abandoned the write and closed: this is the outcome
			// the per-frame deadline exists to produce.
			break
		}
		if len(data) == wsWedgedPayloadBytes {
			t.Fatalf("the wedged listener completed a %v message of %d bytes after the server's write budget had long passed: the write had no deadline, so the handler was still waiting on a client that never read",
				typ, len(data))
		}
		// Anything else (in practice the small connect snapshot) is drained and
		// the loop keeps going, bounded by readCtx: the frame we are looking
		// for must never arrive.
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

	// Bounded read, for the same reason as every other count assertion: it is
	// a channel round-trip to the hub goroutine, which a wedged hub never
	// answers. Here the hub was closed before this call, so it returns 0 via
	// Hub.Done rather than blocking today — the bound is latent-only here, not
	// a proven hang, but it keeps the whole suite on one guarded path.
	if got := hubSubscriberCount(t, s.Hub); got != 0 {
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
