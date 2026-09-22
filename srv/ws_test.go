package srv

// This file is the WebSocket slice of the repo-owned integration harness. It
// drives the real handler tree (`Server.routes()`, the same tree `Server.Serve`
// mounts) over a real loopback listener and over an in-process recorder, which
// is the same two-mode discipline the rest of `integration_test.go` uses.
//
// Scope of the first slice (issue .3.2): mount `GET /ws`, accept the upgrade,
// close cleanly. There is deliberately no hub, no fan-out, no snapshot on
// connect, no ping/pong and no listener counting yet, so the only thing a
// connected client sees is the close frame. The later hub issues are expected
// to REVISE the "the connection closes immediately" expectations here on
// purpose, the same way any behaviour change has to be made in the open.
//
// Boundedness is a requirement, not a nicety: every dial and every read carries
// an explicit deadline, and the test server is torn down with a timeout on a
// goroutine rather than a bare `defer ts.Close()`, because `httptest.Server.Close`
// waits for outstanding requests and would itself hang on a wedged handler.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
)

// wsTestServer boots the real handler tree on a loopback listener (a real TCP
// socket, not the in-process recorder) and returns the server plus the
// ws:// URL of the /ws endpoint. Teardown is registered on the test and
// bounded, so a handler that never returns fails the test instead of stalling
// the run.
func wsTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	s := New("ws-host")
	ts := httptest.NewServer(s.routes())

	t.Cleanup(func() {
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
	})

	// ws:// (or wss://) on the same host:port the listener picked.
	return s, "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
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

// TestWSUpgradeSucceedsAndClosesCleanly is the first required behaviour of this
// slice, over a real listener: GET /ws with a genuine WebSocket handshake must
// be answered 101 Switching Protocols, and then the connection must be closed
// with a normal-closure (1000) close frame so the client does not sit waiting
// for data that this slice never sends.
func TestWSUpgradeSucceedsAndClosesCleanly(t *testing.T) {
	_, url := wsTestServer(t)

	conn, resp := wsDial(t, url, nil)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("GET /ws handshake status = %d, want %d Switching Protocols", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	readCtx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()
	typ, data, err := conn.Read(readCtx)
	if err == nil {
		t.Fatalf("the first message from /ws was %v %q, want a close frame: this slice only accepts and closes", typ, data)
	}
	if code := websocket.CloseStatus(err); code != websocket.StatusNormalClosure {
		t.Fatalf("GET /ws close = %v (code %d), want a %d normal-closure frame", err, code, websocket.StatusNormalClosure)
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
