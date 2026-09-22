package srv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// apiRequest drives one request through the same handler tree Serve builds, so
// routing and method handling are exercised for real rather than by calling
// handlers directly.
func apiRequest(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w
}

// decodeJSON fails the test unless the whole body is valid JSON into a map.
func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	dec := json.NewDecoder(strings.NewReader(w.Body.String()))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("response body is not valid JSON object: %v (body=%q)", err, w.Body.String())
	}
	return got
}

func wantJSONContentType(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// TestAPIStateEmpty pins the read endpoint on a fresh performance: every field
// the agent contract depends on is present, listenerCount serialises as the
// number 0 (not omitted), history is an empty array rather than null, and
// lastEvalResult is explicitly null because nothing has been evaluated yet.
func TestAPIStateEmpty(t *testing.T) {
	s := New("api-host")
	w := apiRequest(t, s, http.MethodGet, "/api/state", "")

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/state status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	wantJSONContentType(t, w)

	raw := w.Body.String()
	if !strings.Contains(raw, `"history":[]`) {
		t.Errorf("GET /api/state body must carry an empty history array, got %q", raw)
	}
	if !strings.Contains(raw, `"lastEvalResult":null`) {
		t.Errorf("GET /api/state body must carry lastEvalResult:null, got %q", raw)
	}
	if strings.Contains(raw, `"listenerCount":null`) {
		t.Errorf("listenerCount must be a number, got null: %q", raw)
	}
	if strings.Contains(raw, `"playing":null`) {
		t.Errorf("playing must be a boolean, got null: %q", raw)
	}

	got := decodeJSON(t, w)
	if v := got["version"].(json.Number); v.String() != "0" {
		t.Errorf("version = %s, want 0", v)
	}
	if code, ok := got["code"].(string); !ok || code != "" {
		t.Errorf("code = %v, want empty string", got["code"])
	}
	if msg, ok := got["lastAgentMessage"].(string); !ok || msg != "" {
		t.Errorf("lastAgentMessage = %v, want empty string", got["lastAgentMessage"])
	}
	if playing, ok := got["playing"].(bool); !ok || !playing {
		t.Errorf("playing = %v, want true on a fresh conductor", got["playing"])
	}
	if n, ok := got["listenerCount"].(json.Number); !ok || n.String() != "0" {
		t.Errorf("listenerCount = %v, want the number 0", got["listenerCount"])
	}
	if lr, ok := got["lastEvalResult"]; !ok || lr != nil {
		t.Errorf("lastEvalResult = %v, want explicit null", lr)
	}
	hist, ok := got["history"].([]any)
	if !ok || len(hist) != 0 {
		t.Errorf("history = %v, want empty array", got["history"])
	}
	anchor, ok := got["anchor"].(map[string]any)
	if !ok {
		t.Fatalf("anchor = %v, want object", got["anchor"])
	}
	if cps := anchor["cps"].(json.Number); cps.String() != "0.5" {
		t.Errorf("anchor.cps = %s, want 0.5", cps)
	}
	if ep, ok := anchor["epochMs"].(json.Number); !ok || ep.String() == "0" {
		t.Errorf("anchor.epochMs = %v, want a real epoch timestamp", anchor["epochMs"])
	}
}

// TestAPIStateReflectsConductor pins that /api/state is a real read of the live
// Conductor rather than a stub: published code/version/narration/history, the
// transport flag, the eval feedback and the listener count all have to come
// back out of the JSON unchanged.
func TestAPIStateReflectsConductor(t *testing.T) {
	s := New("api-host")
	s.Conductor.Publish(`s("bd*4")`, "four on the floor")
	s.Conductor.Publish(`s("bd*4, ~ cp")`, "")
	s.Conductor.SetMessage("adding backbeat")
	s.Conductor.SetPlaying(false)
	s.Conductor.SetListenerCount(3)
	if err := s.Conductor.RecordEvalResult(EvalResult{
		Version: 2,
		OK:      false,
		Error:   "TypeError: x is not a function",
		Stats:   json.RawMessage(`{"haps":12}`),
	}); err != nil {
		t.Fatalf("RecordEvalResult: %v", err)
	}

	w := apiRequest(t, s, http.MethodGet, "/api/state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeJSON(t, w)

	if v := got["version"].(json.Number); v.String() != "2" {
		t.Errorf("version = %s, want 2", v)
	}
	if code := got["code"].(string); code != `s("bd*4, ~ cp")` {
		t.Errorf("code = %q, want the newest published code", code)
	}
	if msg := got["lastAgentMessage"].(string); msg != "adding backbeat" {
		t.Errorf("lastAgentMessage = %q, want %q", msg, "adding backbeat")
	}
	if playing := got["playing"].(bool); playing {
		t.Error("playing = true, want false after POST /api/hush state")
	}
	if n := got["listenerCount"].(json.Number); n.String() != "3" {
		t.Errorf("listenerCount = %s, want 3", n)
	}

	hist := got["history"].([]any)
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2", len(hist))
	}
	first := hist[0].(map[string]any)
	last := hist[1].(map[string]any)
	if v := first["version"].(json.Number); v.String() != "1" {
		t.Errorf("history[0].version = %s, want 1 (oldest first)", v)
	}
	if code := first["code"].(string); code != `s("bd*4")` {
		t.Errorf("history[0].code = %q, want the first version", code)
	}
	if msg := first["message"].(string); msg != "four on the floor" {
		t.Errorf("history[0].message = %q, want %q", msg, "four on the floor")
	}
	if v := last["version"].(json.Number); v.String() != "2" {
		t.Errorf("history[1].version = %s, want 2 (newest last)", v)
	}
	if msg := last["message"].(string); msg != "" {
		t.Errorf("history[1].message = %q, want empty (published without narration)", msg)
	}

	lr, ok := got["lastEvalResult"].(map[string]any)
	if !ok {
		t.Fatalf("lastEvalResult = %v, want object", got["lastEvalResult"])
	}
	if v := lr["version"].(json.Number); v.String() != "2" {
		t.Errorf("lastEvalResult.version = %s, want 2", v)
	}
	if lr["ok"].(bool) {
		t.Error("lastEvalResult.ok = true, want false")
	}
	if e := lr["error"].(string); e != "TypeError: x is not a function" {
		t.Errorf("lastEvalResult.error = %q, want the reported error", e)
	}
	stats, ok := lr["stats"].(map[string]any)
	if !ok {
		t.Fatalf("lastEvalResult.stats = %v, want object (opaque JSON echoed back)", lr["stats"])
	}
	if h := stats["haps"].(json.Number); h.String() != "12" {
		t.Errorf("lastEvalResult.stats.haps = %s, want 12", h)
	}
}

// TestAPIStateMethodNotAllowed pins that the read endpoint is GET-only; a write
// verb must not be silently accepted.
func TestAPIStateMethodNotAllowed(t *testing.T) {
	s := New("api-host")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		w := apiRequest(t, s, method, "/api/state", "")
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/state status = %d, want 405", method, w.Code)
		}
		wantJSONContentType(t, w)
		got := decodeJSON(t, w)
		if msg, ok := got["error"].(string); !ok || msg == "" {
			t.Errorf("%s /api/state error body = %q, want a non-empty error message", method, w.Body.String())
		}
	}
}

// TestAPIUnknownPath pins that an unregistered /api path 404s as JSON instead
// of falling through to the HTML shell.
func TestAPIUnknownPath(t *testing.T) {
	s := New("api-host")
	w := apiRequest(t, s, http.MethodGet, "/api/nope", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /api/nope status = %d, want 404", w.Code)
	}
	wantJSONContentType(t, w)
	if msg, ok := decodeJSON(t, w)["error"].(string); !ok || msg == "" {
		t.Errorf("GET /api/nope body = %q, want a JSON error object", w.Body.String())
	}
}

// TestAPICodePublishes covers the agent's write path: a valid push installs the
// code, bumps the version by exactly one, records the narration, and returns the
// new snapshot so the agent does not need a second round trip.
func TestAPICodePublishes(t *testing.T) {
	s := New("api-host")

	w := apiRequest(t, s, http.MethodPost, "/api/code", `{"code":"s(\"bd\")","message":"kick"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/code status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	wantJSONContentType(t, w)
	got := decodeJSON(t, w)
	if v := got["version"].(json.Number); v.String() != "1" {
		t.Errorf("response version = %s, want 1", v)
	}
	if code := got["code"].(string); code != `s("bd")` {
		t.Errorf("response code = %q, want the pushed code", code)
	}
	if msg := got["lastAgentMessage"].(string); msg != "kick" {
		t.Errorf("response lastAgentMessage = %q, want %q", msg, "kick")
	}
	if playing := got["playing"].(bool); !playing {
		t.Error("response playing = false, want true (publishing is not a hush)")
	}

	// The write must be visible through the read path.
	state := decodeJSON(t, apiRequest(t, s, http.MethodGet, "/api/state", ""))
	if v := state["version"].(json.Number); v.String() != "1" {
		t.Errorf("after push, GET /api/state version = %s, want 1", v)
	}
	if code := state["code"].(string); code != `s("bd")` {
		t.Errorf("after push, GET /api/state code = %q, want the pushed code", code)
	}
	if len(state["history"].([]any)) != 1 {
		t.Errorf("after push, history len = %d, want 1", len(state["history"].([]any)))
	}

	// A second push stores the code even when it carries no narration, and does
	// not wipe the narration already on record.
	got = decodeJSON(t, apiRequest(t, s, http.MethodPost, "/api/code", `{"code":"s(\"cp\")"}`))
	if v := got["version"].(json.Number); v.String() != "2" {
		t.Errorf("second push version = %s, want 2", v)
	}
	if code := got["code"].(string); code != `s("cp")` {
		t.Errorf("second push code = %q, want the pushed code", code)
	}
	if msg := got["lastAgentMessage"].(string); msg != "kick" {
		t.Errorf("second push wiped narration: lastAgentMessage = %q, want %q", msg, "kick")
	}

	// A body with no message key at all behaves the same way.
	got = decodeJSON(t, apiRequest(t, s, http.MethodPost, "/api/code", `{"code":"note(\"c4\")"}`))
	if v := got["version"].(json.Number); v.String() != "3" {
		t.Errorf("third push version = %s, want 3", v)
	}
	if lp := got["lastEvalResult"]; lp != nil {
		t.Errorf("pushing code fabricated an eval result: %v", lp)
	}
}

func TestAPICodeRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantSubstr string
	}{
		{"empty code", `{"code":""}`, "code"},
		{"missing code field", `{}`, "code"},
		{"whitespace-only code", `{"code":"   \n"}`, "code"},
		{"malformed json", `{"code":`, "invalid JSON"},
		{"trailing garbage", `{"code":"a"} and more`, "invalid JSON"},
		{"wrong field type", `{"code":42}`, "invalid JSON"},
		{"unknown field", `{"code":"a","bogus":1}`, "bogus"},
		{"empty body", ``, "invalid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New("api-host")
			w := apiRequest(t, s, http.MethodPost, "/api/code", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%q)", w.Code, w.Body.String())
			}
			wantJSONContentType(t, w)
			msg, ok := decodeJSON(t, w)["error"].(string)
			if !ok || msg == "" {
				t.Fatalf("error body = %q, want a non-empty JSON error", w.Body.String())
			}
			if !strings.Contains(strings.ToLower(msg), strings.ToLower(tc.wantSubstr)) {
				t.Errorf("error %q does not mention %q", msg, tc.wantSubstr)
			}
			// A rejected push must not have touched the live performance.
			if v := s.Conductor.Snapshot().Version; v != 0 {
				t.Errorf("rejected push bumped version to %d, want 0", v)
			}
		})
	}
}

func TestAPICodeMethodNotAllowed(t *testing.T) {
	s := New("api-host")
	w := apiRequest(t, s, http.MethodGet, "/api/code", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/code status = %d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != http.MethodPost {
		t.Errorf("Allow = %q, want POST", allow)
	}
	if msg, ok := decodeJSON(t, w)["error"].(string); !ok || !strings.Contains(msg, "POST") {
		t.Errorf("error body = %q, want it to name the permitted method", w.Body.String())
	}
}

// TestAPIPayloadLimit pins the body cap for every write endpoint: a body over
// the documented limit is refused with 413 and a message naming the limit, and
// nothing is stored. A body just under the limit is still accepted, so the cap
// is a limit and not an accidental hard-coded rejection.
func TestAPIPayloadLimit(t *testing.T) {
	huge := `{"code":"` + strings.Repeat("a", APIMaxBodyBytes) + `"}`
	if len(huge) <= APIMaxBodyBytes {
		t.Fatalf("test body len %d is not over the %d limit", len(huge), APIMaxBodyBytes)
	}

	s := New("api-host")
	w := apiRequest(t, s, http.MethodPost, "/api/code", huge)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413 (body=%q)", w.Code, w.Body.String())
	}
	wantJSONContentType(t, w)
	msg, ok := decodeJSON(t, w)["error"].(string)
	if !ok || !strings.Contains(msg, "too large") {
		t.Fatalf("oversized body error = %q, want it to say the payload is too large", w.Body.String())
	}
	if !strings.Contains(msg, "65536") && !strings.Contains(msg, "64") {
		t.Errorf("oversized body error = %q, want it to state the limit", msg)
	}
	if v := s.Conductor.Snapshot().Version; v != 0 {
		t.Errorf("oversized push bumped version to %d, want 0", v)
	}

	// Just under the cap: still accepted and stored.
	big := strings.Repeat("b", APIMaxBodyBytes-64)
	w = apiRequest(t, s, http.MethodPost, "/api/code", `{"code":"`+big+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("body just under the limit: status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	if got := s.Conductor.Snapshot().Code; got != big {
		t.Errorf("under-limit code stored as len %d, want len %d", len(got), len(big))
	}
}

// TestAPIPayloadLimitAppliesToEveryWriteEndpoint makes sure the cap is enforced
// by shared middleware rather than by one diligent handler.
func TestAPIPayloadLimitAppliesToEveryWriteEndpoint(t *testing.T) {
	huge := strings.Repeat("x", APIMaxBodyBytes+1024)
	for _, path := range []string{"/api/code", "/api/message", "/api/eval-result", "/api/hush", "/api/play"} {
		t.Run(path, func(t *testing.T) {
			s := New("api-host")
			w := apiRequest(t, s, http.MethodPost, path, huge)
			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("POST %s oversized status = %d, want 413 (body=%q)", path, w.Code, w.Body.String())
			}
			if msg, ok := decodeJSON(t, w)["error"].(string); !ok || !strings.Contains(msg, "too large") {
				t.Errorf("POST %s oversized error = %q, want a too-large message", path, w.Body.String())
			}
		})
	}
}

// TestAPIMessageNarratesWithoutBumpingVersion pins the difference between the
// two write paths: /api/message changes only the narration. The version, the
// code document and the history must all be untouched, which is what lets the
// agent talk to listeners without interrupting the music.
func TestAPIMessageNarratesWithoutBumpingVersion(t *testing.T) {
	s := New("api-host")
	s.Conductor.Publish(`s("bd")`, "kick")
	s.Conductor.SetListenerCount(2)

	w := apiRequest(t, s, http.MethodPost, "/api/message", `{"message":"now layering hats"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/message status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	wantJSONContentType(t, w)
	got := decodeJSON(t, w)
	if msg := got["lastAgentMessage"].(string); msg != "now layering hats" {
		t.Errorf("response lastAgentMessage = %q, want %q", msg, "now layering hats")
	}
	if v := got["version"].(json.Number); v.String() != "1" {
		t.Errorf("response version = %s, want 1 (narration must not bump it)", v)
	}
	if code := got["code"].(string); code != `s("bd")` {
		t.Errorf("response code = %q, want the unchanged document", code)
	}

	snap := s.Conductor.Snapshot()
	if snap.Version != 1 {
		t.Errorf("version after narration = %d, want 1", snap.Version)
	}
	if snap.Code != `s("bd")` {
		t.Errorf("code after narration = %q, want unchanged", snap.Code)
	}
	if snap.LastAgentMessage != "now layering hats" {
		t.Errorf("LastAgentMessage = %q, want the narration", snap.LastAgentMessage)
	}
	if len(snap.History) != 1 {
		t.Errorf("history len after narration = %d, want 1 (narration is not a version)", len(snap.History))
	}
	if snap.History[0].Message != "kick" {
		t.Errorf("history[0].Message = %q, want the publish-time message untouched", snap.History[0].Message)
	}
	if snap.ListenerCount != 2 {
		t.Errorf("narration reset listener count to %d, want 2", snap.ListenerCount)
	}

	// The read path agrees.
	state := decodeJSON(t, apiRequest(t, s, http.MethodGet, "/api/state", ""))
	if msg := state["lastAgentMessage"].(string); msg != "now layering hats" {
		t.Errorf("GET /api/state lastAgentMessage = %q, want the narration", msg)
	}
}

func TestAPIMessageRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantSubstr string
	}{
		{"empty message", `{"message":""}`, "message"},
		{"missing message field", `{}`, "message"},
		{"whitespace-only message", `{"message":"  \t "}`, "message"},
		{"malformed json", `{"message"`, "invalid JSON"},
		{"unknown field", `{"message":"hi","code":"s(\"bd\")"}`, "code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New("api-host")
			w := apiRequest(t, s, http.MethodPost, "/api/message", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%q)", w.Code, w.Body.String())
			}
			msg, ok := decodeJSON(t, w)["error"].(string)
			if !ok || !strings.Contains(strings.ToLower(msg), strings.ToLower(tc.wantSubstr)) {
				t.Errorf("error = %q, want it to mention %q", w.Body.String(), tc.wantSubstr)
			}
			if got := s.Conductor.Snapshot().LastAgentMessage; got != "" {
				t.Errorf("rejected narration was stored anyway: %q", got)
			}
		})
	}
}

func TestAPIMessageMethodNotAllowed(t *testing.T) {
	s := New("api-host")
	w := apiRequest(t, s, http.MethodGet, "/api/message", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/message status = %d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != http.MethodPost {
		t.Errorf("Allow = %q, want POST", allow)
	}
}

// TestAPIEvalResultSuccess covers the browser feedback loop: a report on a
// published version is stored and surfaced by GET /api/state, closing the
// agent's see-what-my-code-did loop. A report with no stats is fine; the
// optional fields simply stay absent.
func TestAPIEvalResultSuccess(t *testing.T) {
	s := New("api-host")
	s.Conductor.Publish(`s("bd*4")`, "kick")

	w := apiRequest(t, s, http.MethodPost, "/api/eval-result",
		`{"version":1,"ok":true,"stats":{"events":16,"notes":0}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/eval-result status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	wantJSONContentType(t, w)
	ack := decodeJSON(t, w)
	if accepted, has := ack["accepted"]; !has || accepted != true {
		t.Errorf("response = %q, want an acknowledgement that the report was accepted", w.Body.String())
	}
	if v := ack["version"].(json.Number); v.String() != "1" {
		t.Errorf("ack version = %s, want 1", v)
	}

	state := decodeJSON(t, apiRequest(t, s, http.MethodGet, "/api/state", ""))
	lr, isObj := state["lastEvalResult"].(map[string]any)
	if !isObj {
		t.Fatalf("GET /api/state lastEvalResult = %v, want an object once reported", state["lastEvalResult"])
	}
	if v := lr["version"].(json.Number); v.String() != "1" {
		t.Errorf("stored version = %s, want 1", v)
	}
	if !lr["ok"].(bool) {
		t.Error("stored ok = false, want true")
	}
	if _, present := lr["error"]; present {
		t.Errorf("stored error = %v, want it omitted on success", lr["error"])
	}
	stats, isObj := lr["stats"].(map[string]any)
	if !isObj {
		t.Fatalf("stored stats = %v, want the reported object", lr["stats"])
	}
	if n := stats["events"].(json.Number); n.String() != "16" {
		t.Errorf("stored stats.events = %s, want 16", n)
	}
	if ts, ok := lr["epochMs"].(json.Number); !ok || ts.String() == "0" {
		t.Errorf("stored epochMs = %v, want a server timestamp", lr["epochMs"])
	}
	// Reporting a result is bookkeeping, not a code change: the version, the
	// document and the history must all be untouched or every browser report
	// would re-trigger the hub's fan-out.
	if v := state["version"].(json.Number); v.String() != "1" {
		t.Errorf("eval report bumped version to %s, want 1", v)
	}
	if code := state["code"].(string); code != `s("bd*4")` {
		t.Errorf("eval report changed the document: code = %q", code)
	}
	if n := len(state["history"].([]any)); n != 1 {
		t.Errorf("eval report changed history len to %d, want 1", n)
	}

	// Failure reports carry the error text verbatim; stats are optional.
	w = apiRequest(t, s, http.MethodPost, "/api/eval-result",
		`{"version":1,"ok":false,"error":"SyntaxError: unexpected token ')'"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("failure report status = %d, want 200", w.Code)
	}
	lr = decodeJSON(t, apiRequest(t, s, http.MethodGet, "/api/state", ""))["lastEvalResult"].(map[string]any)
	if lr["ok"].(bool) {
		t.Error("stored ok = true, want false")
	}
	if e := lr["error"].(string); e != "SyntaxError: unexpected token ')'" {
		t.Errorf("stored error = %q, want the reported text verbatim", e)
	}
}

// TestAPIEvalResultUnknownVersion pins the error path that matters most: if the
// agent is told a version evaluated when that version does not exist, its
// feedback loop is lying to it. The report is refused with 400 and nothing is
// stored.
func TestAPIEvalResultUnknownVersion(t *testing.T) {
	s := New("api-host")
	s.Conductor.Publish(`s("bd")`, "")

	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"future version", `{"version":2,"ok":true}`, "unknown version"},
		{"far future version", `{"version":9999,"ok":true}`, "unknown version"},
		{"version zero", `{"version":0,"ok":true}`, "unknown version"},
		{"negative version", `{"version":-1,"ok":true}`, "unknown version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := apiRequest(t, s, http.MethodPost, "/api/eval-result", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%q)", w.Code, w.Body.String())
			}
			wantJSONContentType(t, w)
			msg, ok := decodeJSON(t, w)["error"].(string)
			if !ok || !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("error = %q, want it to mention %q", w.Body.String(), tc.wantMsg)
			}
			if lr := s.Conductor.Snapshot().LastEvalResult; lr != nil {
				t.Errorf("rejected report was stored anyway: %+v", lr)
			}
		})
	}

	// On a client that never saw any code (server still at version 0) every
	// report is unknown.
	fresh := New("api-host")
	w := apiRequest(t, fresh, http.MethodPost, "/api/eval-result", `{"version":1,"ok":true}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("report against version 0 conductor: status = %d, want 400", w.Code)
	}
}

func TestAPIEvalResultRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantSubstr string
	}{
		{"malformed json", `{"version":`, "invalid JSON"},
		{"missing version", `{"ok":true}`, "version"},
		{"wrong version type", `{"version":"1","ok":true}`, "invalid JSON"},
		{"unknown field", `{"version":1,"ok":true,"verdict":"yes"}`, "verdict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New("api-host")
			s.Conductor.Publish(`s("bd")`, "")
			w := apiRequest(t, s, http.MethodPost, "/api/eval-result", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%q)", w.Code, w.Body.String())
			}
			msg, ok := decodeJSON(t, w)["error"].(string)
			if !ok || !strings.Contains(strings.ToLower(msg), strings.ToLower(tc.wantSubstr)) {
				t.Errorf("error = %q, want it to mention %q", w.Body.String(), tc.wantSubstr)
			}
		})
	}
}

// TestAPITransportHushAndPlay pins the transport signals. The Go server has no
// audio and evaluates no JavaScript: hush/play only record and broadcast
// intent. They must not touch the code document, the version, the history or
// the narration, because a listener who hits hush must be able to resume to
// exactly the same pattern.
func TestAPITransportHushAndPlay(t *testing.T) {
	s := New("api-host")
	s.Conductor.Publish(`s("bd*4")`, "kick")
	s.Conductor.SetListenerCount(4)

	if !s.Conductor.Snapshot().Playing {
		t.Fatal("precondition: transport should start playing")
	}

	w := apiRequest(t, s, http.MethodPost, "/api/hush", "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/hush status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	wantJSONContentType(t, w)
	got := decodeJSON(t, w)
	if playing := got["playing"].(bool); playing {
		t.Errorf("hush response playing = true, want false")
	}
	if v := got["version"].(json.Number); v.String() != "1" {
		t.Errorf("hush bumped version to %s, want 1", v)
	}
	if code := got["code"].(string); code != `s("bd*4")` {
		t.Errorf("hush changed the document: code = %q", code)
	}

	snap := s.Conductor.Snapshot()
	if snap.Playing {
		t.Error("after hush, Snapshot().Playing = true, want false")
	}
	if snap.Version != 1 || snap.Code != `s("bd*4")` || snap.LastAgentMessage != "kick" {
		t.Errorf("hush mutated non-transport state: %+v", snap)
	}
	if len(snap.History) != 1 {
		t.Errorf("hush changed history len to %d, want 1", len(snap.History))
	}
	if snap.ListenerCount != 4 {
		t.Errorf("hush changed listener count to %d, want 4", snap.ListenerCount)
	}

	// The read path must show the hushed transport so browsers can react.
	if playing := decodeJSON(t, apiRequest(t, s, http.MethodGet, "/api/state", ""))["playing"].(bool); playing {
		t.Error("GET /api/state playing = true after hush, want false")
	}

	// Silence is the current state, so hush again must be accepted idempotently
	// (the agent may re-assert state it cannot see) rather than erroring.
	if w := apiRequest(t, s, http.MethodPost, "/api/hush", ""); w.Code != http.StatusOK {
		t.Errorf("second POST /api/hush status = %d, want 200 (idempotent)", w.Code)
	}

	// Play resumes the transport and still does not resurrect a version.
	w = apiRequest(t, s, http.MethodPost, "/api/play", "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/play status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	got = decodeJSON(t, w)
	if playing := got["playing"].(bool); !playing {
		t.Errorf("play response playing = false, want true")
	}
	if v := got["version"].(json.Number); v.String() != "1" {
		t.Errorf("play bumped version to %s, want 1", v)
	}
	if code := got["code"].(string); code != `s("bd*4")` {
		t.Errorf("play changed the document: code = %q", code)
	}
	if playing := decodeJSON(t, apiRequest(t, s, http.MethodGet, "/api/state", ""))["playing"].(bool); !playing {
		t.Error("GET /api/state playing = false after play, want true")
	}

	// Playing is the start state: play on an already-playing conductor is also a
	// no-op success.
	if w := apiRequest(t, s, http.MethodPost, "/api/play", ""); w.Code != http.StatusOK {
		t.Errorf("second POST /api/play status = %d, want 200 (idempotent)", w.Code)
	}
}

// TestAPITransportRejectsBodies pins that hush/play take no arguments: a body
// carrying a code document must not be mistaken for a push, and it must not be
// silently ignored either.
func TestAPITransportRejectsBodies(t *testing.T) {
	for _, path := range []string{"/api/hush", "/api/play"} {
		t.Run(path, func(t *testing.T) {
			s := New("api-host")
			s.Conductor.Publish(`s("bd")`, "")

			// A payload attempting to smuggle code through the transport
			// endpoint is refused outright.
			w := apiRequest(t, s, http.MethodPost, path, `{"code":"s(\"evil\")"}`)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%q)", w.Code, w.Body.String())
			}
			msg, ok := decodeJSON(t, w)["error"].(string)
			if !ok || !strings.Contains(strings.ToLower(msg), "code") {
				t.Errorf("error = %q, want it to name the rejected field", w.Body.String())
			}
			if code := s.Conductor.Snapshot().Code; code != `s("bd")` {
				t.Errorf("smuggled code reached the document: %q", code)
			}
			if v := s.Conductor.Snapshot().Version; v != 1 {
				t.Errorf("rejected transport bumped version to %d, want 1", v)
			}

			// An empty JSON object is a valid "no arguments" body.
			if w := apiRequest(t, s, http.MethodPost, path, `{}`); w.Code != http.StatusOK {
				t.Errorf("empty object body: status = %d, want 200 (body=%q)", w.Code, w.Body.String())
			}

			// Malformed JSON is a client bug, not something to ignore.
			if w := apiRequest(t, s, http.MethodPost, path, `{`); w.Code != http.StatusBadRequest {
				t.Errorf("malformed body: status = %d, want 400", w.Code)
			}

			// GET is not how you drive a transport.
			if w := apiRequest(t, s, http.MethodGet, path, ""); w.Code != http.StatusMethodNotAllowed {
				t.Errorf("GET %s status = %d, want 405", path, w.Code)
			}
		})
	}
}

// TestAPIDoesNotBreakShellRouting pins that adding the API did not disturb the
// existing page/static routes: / still renders the full HTML document (the
// renderTemplate gotcha means a 200 alone proves nothing), and the API paths
// are not swallowed by the shell catch-all.
func TestAPIDoesNotBreakShellRouting(t *testing.T) {
	s := New("shell-host")

	w := apiRequest(t, s, http.MethodGet, "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Copied to clipboard!", "Edit with Shelley", "</html>"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / did not render to completion: missing %q", want)
		}
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / Content-Type = %q, want text/html", ct)
	}

	// An unknown non-API path must not be answered by the JSON API layer.
	w = apiRequest(t, s, http.MethodGet, "/definitely-not-here", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /definitely-not-here status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Errorf("non-API 404 answered as JSON (%q): the API catch-all is too broad", ct)
	}
}

// TestAPIServesStaticAssets guards the /static/ mount against the API rewrite.
func TestAPIServesStaticAssets(t *testing.T) {
	s := New("shell-host")
	w := apiRequest(t, s, http.MethodGet, "/static/style.css", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css status = %d, want 200", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Error("GET /static/style.css returned an empty body")
	}
}

// TestAPIConcurrentWritesAreSerialised hammers every write endpoint alongside
// reads and must be clean under `go test -race`, mirroring the real traffic
// shape of an agent plus many browsers reporting eval results.
func TestAPIConcurrentWritesAreSerialised(t *testing.T) {
	s := New("api-host")
	const (
		pushes  = 60
		reports = 60
	)
	// Seed one version so reports name a real version for at least part of the
	// run; the rest race against pushes and are allowed to 400 as unknown.
	apiRequest(t, s, http.MethodPost, "/api/code", `{"code":"s(\"bd\")","message":"seed"}`)

	var wg sync.WaitGroup
	for i := 0; i < pushes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := `{"code":"s(\"bd\").gain(` + strconv.Itoa(i%4) + `)","message":"push ` + strconv.Itoa(i) + `"}`
			w := apiRequest(t, s, http.MethodPost, "/api/code", body)
			if w.Code != http.StatusOK {
				t.Errorf("concurrent push %d status = %d (body=%q)", i, w.Code, w.Body.String())
			}
		}(i)
	}
	for i := 0; i < reports; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := apiRequest(t, s, http.MethodPost, "/api/eval-result", `{"version":1,"ok":true,"stats":{"events":1}}`)
			if w.Code != http.StatusOK && w.Code != http.StatusBadRequest {
				t.Errorf("concurrent report status = %d (body=%q)", w.Code, w.Body.String())
			}
		}()
	}
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := apiRequest(t, s, http.MethodGet, "/api/state", ""); w.Code != http.StatusOK {
				t.Errorf("concurrent read status = %d", w.Code)
			}
		}()
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Conductor.SetListenerCount(i)
			if i%2 == 0 {
				apiRequest(t, s, http.MethodPost, "/api/hush", "")
			} else {
				apiRequest(t, s, http.MethodPost, "/api/play", "")
			}
		}(i)
	}
	wg.Wait()

	// The seed plus every push: lost updates would show up here.
	if got := s.Conductor.Snapshot().Version; got != pushes+1 {
		t.Fatalf("version after concurrent pushes = %d, want %d", got, pushes+1)
	}
}
