// Drift reporting over HTTP and on the wire (strudel-agent-uvj.15).
//
// These tests drive the real route tree and a real listener socket, because two
// of the claims under test are about the ROUTE existing and about the FRAME
// listeners receive. A unit test calling Conductor.RecordSyncObservation would
// pass against a server that mounted nothing and broadcast nothing.
//
// The broadcast rule is the one borrowed from eval-result and worth restating:
// a frame is sent for an ACCEPTED mutation. A refused report broadcasts
// nothing, because a listener told about an observation the server refused
// would be describing a measurement that does not exist.

package srv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// syncTestPublish pushes one version and returns its number.
func syncTestPublish(t *testing.T, s *Server) int64 {
	t.Helper()
	w := apiRequest(t, s, http.MethodPost, "/api/code", `{"code":"s(\"bd*2\")","message":"first"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/code: %d %q", w.Code, w.Body.String())
	}
	var snap Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode publish response %q: %v", w.Body.String(), err)
	}
	return snap.Version
}

// TestSyncResultIsStoredAndReturned proves the endpoint accepts an observation
// and that the stored one is readable from GET /api/state -- which is the whole
// point: an agent polls the snapshot, it does not read a push response.
func TestSyncResultIsStoredAndReturned(t *testing.T) {
	s := New()
	version := syncTestPublish(t, s)

	w := apiRequest(t, s, http.MethodPost, "/api/sync-result",
		fmt.Sprintf(`{"version":%d,"driftMs":14,"targetMs":1757000002000,"actualMs":1757000002014}`, version))
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/sync-result: %d %q, want 200", w.Code, w.Body.String())
	}
	var ack apiEvalAck
	if err := json.Unmarshal(w.Body.Bytes(), &ack); err != nil {
		t.Fatalf("decode ack %q: %v", w.Body.String(), err)
	}
	if !ack.Accepted || ack.Version != version {
		t.Errorf("ack = %+v, want accepted with version %d", ack, version)
	}

	state := apiRequest(t, s, http.MethodGet, "/api/state", "")
	if state.Code != http.StatusOK {
		t.Fatalf("GET /api/state: %d %q", state.Code, state.Body.String())
	}
	if !strings.Contains(state.Body.String(), `"driftMs":14`) {
		t.Errorf("GET /api/state does not carry the observation: %q", state.Body.String())
	}
}

// TestSyncResultDoesNotBumpTheVersion proves the invariant the whole Conductor
// half exists for, through the real route tree rather than by calling the store
// directly: an observation is evidence about a commit, never a new document.
func TestSyncResultDoesNotBumpTheVersion(t *testing.T) {
	s := New()
	version := syncTestPublish(t, s)

	var pre Snapshot
	if err := json.Unmarshal(apiRequest(t, s, http.MethodGet, "/api/state", "").Body.Bytes(), &pre); err != nil {
		t.Fatalf("decode pre state: %v", err)
	}

	w := apiRequest(t, s, http.MethodPost, "/api/sync-result",
		fmt.Sprintf(`{"version":%d,"unscheduled":true}`, version))
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/sync-result: %d %q", w.Code, w.Body.String())
	}

	var post Snapshot
	if err := json.Unmarshal(apiRequest(t, s, http.MethodGet, "/api/state", "").Body.Bytes(), &post); err != nil {
		t.Fatalf("decode post state: %v", err)
	}
	if post.Version != pre.Version {
		t.Errorf("version moved from %d to %d on a sync report: only POST /api/code increments it", pre.Version, post.Version)
	}
	if len(post.History) != len(pre.History) {
		t.Errorf("history grew from %d to %d entries on a sync report: an observation is not a revision", len(pre.History), len(post.History))
	}
	if post.LastSync == nil || !post.LastSync.Unscheduled {
		t.Errorf("lastSync = %+v, want the stored unscheduled observation", post.LastSync)
	}
}

// TestSyncResultBroadcastsItsOwnFrameAndNothingOnRefusal proves both halves of
// the broadcast contract with a real listener attached.
func TestSyncResultBroadcastsItsOwnFrameAndNothingOnRefusal(t *testing.T) {
	s := New()
	version := syncTestPublish(t, s)

	_, conn := wsListener(t, s)

	w := apiRequest(t, s, http.MethodPost, "/api/sync-result",
		fmt.Sprintf(`{"version":%d,"driftMs":9}`, version))
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/sync-result: %d %q", w.Code, w.Body.String())
	}

	ev := wsReadNextEvent(t, conn, EventSync)
	if ev.Snapshot.LastSync == nil || ev.Snapshot.LastSync.DriftMS == nil || *ev.Snapshot.LastSync.DriftMS != 9 {
		t.Errorf("the %q frame carries lastSync = %+v, want the accepted observation", EventSync, ev.Snapshot.LastSync)
	}
	// The frame and the read path must agree: one encoder, one snapshot value.
	// A listener and an agent reading different snapshots is the defect this
	// uniform envelope exists to prevent.
	var state Snapshot
	if err := json.Unmarshal(apiRequest(t, s, http.MethodGet, "/api/state", "").Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.LastSync == nil || state.LastSync.DriftMS == nil || *state.LastSync.DriftMS != 9 {
		t.Errorf("the frame says drift 9 but GET /api/state says %+v: a listener and an agent must read the same snapshot", state.LastSync)
	}

	// Now two REFUSED reports. Neither may broadcast a sync frame.
	apiRequest(t, s, http.MethodPost, "/api/sync-result", fmt.Sprintf(`{"version":%d,"driftMs":1,"unscheduled":true}`, version))
	apiRequest(t, s, http.MethodPost, "/api/sync-result", `{"version":9999,"driftMs":2}`)

	if data, ok := wsNextIsSyncFrame(t, conn); ok {
		t.Errorf("a REFUSED sync report broadcast a %q frame: %s", EventSync, data)
	}
}

// wsNextIsSyncFrame reports whether the next frame on conn is a sync frame.
//
// It exists because "nothing was broadcast" is the property under test and there
// is no positive way to read an absence. The read is bounded: on timeout it
// reports false, which is the PASS direction for "no frame arrived". That is
// safe here precisely because the preceding assertion already proved the socket
// carries frames -- an always-empty socket would pass this check vacuously, and
// the wsReadNextEvent above is what rules that out.
func wsNextIsSyncFrame(t *testing.T, conn *websocket.Conn) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wsReadTimeout)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageText {
		return "", false
	}
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return string(data), false
	}
	if ev.Kind != EventSync {
		return "", false
	}
	return string(data), true
}

// TestSyncResultRefusalsAre400s walks the body rules through the route tree, so
// the documented 400s are asserted against the live server rather than against
// the store they happen to share.
func TestSyncResultRefusalsAre400s(t *testing.T) {
	s := New()
	version := syncTestPublish(t, s)

	for _, tc := range []struct{ name, body, needle string }{
		{"neither drift nor unscheduled", fmt.Sprintf(`{"version":%d}`, version), "exactly one"},
		{"both drift and unscheduled", fmt.Sprintf(`{"version":%d,"driftMs":0,"unscheduled":true}`, version), "exactly one"},
		{"unpublished version", `{"version":9999,"driftMs":3}`, "unknown version:"},
		{"unknown field", fmt.Sprintf(`{"version":%d,"driftMs":3,"drift":3}`, version), "unknown field"},
		{"wrong type for driftMs", fmt.Sprintf(`{"version":%d,"driftMs":"3"}`, version), ""},
		{"trailing JSON", fmt.Sprintf(`{"version":%d,"driftMs":3}{"version":%d}`, version, version), "unexpected data after JSON body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := apiRequest(t, s, http.MethodPost, "/api/sync-result", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("POST /api/sync-result %s: %d %q, want 400", tc.body, w.Code, w.Body.String())
			}
			if tc.needle != "" && !strings.Contains(w.Body.String(), tc.needle) {
				t.Errorf("the error %q does not name %q: an agent has to be told which rule it broke", w.Body.String(), tc.needle)
			}
		})
	}

	// The wrong verb is a 405 with Allow, like every other endpoint.
	w := apiRequest(t, s, http.MethodGet, "/api/sync-result", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/sync-result: %d, want 405", w.Code)
	}
	if got := w.Header().Get("Allow"); got != http.MethodPost {
		t.Errorf("GET /api/sync-result: Allow = %q, want POST", got)
	}
}

// TestSyncResultRejectsAnOversizedBody keeps the 64 KiB cap universal: this
// endpoint is the newest /api route, and a cap that forgot it would buffer an
// arbitrary body without bound.
func TestSyncResultRejectsAnOversizedBody(t *testing.T) {
	s := New()
	version := syncTestPublish(t, s)
	oversized := fmt.Sprintf(`{"version":%d,"driftMs":1,"pad":"%s"}`, version, strings.Repeat("x", APIMaxBodyBytes+16))
	w := apiRequest(t, s, http.MethodPost, "/api/sync-result", oversized)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized sync report: %d %q, want 413", w.Code, w.Body.String())
	}
}

// PLACEHOLDER_SYNC_API
