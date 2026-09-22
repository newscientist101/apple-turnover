package srv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// APIMaxBodyBytes caps the size of an /api request body. The server only stores
// and broadcasts code, so anything larger than a generous strudel pattern is a
// client bug or an attack: it is rejected with 413 rather than buffered. 64 KiB
// is far more than a livecoding pattern needs (a whole 4-voice track is a few
// hundred bytes) while still being cheap to hold in memory per request.
const APIMaxBodyBytes = 64 << 10

// Status codes across the API are deliberately uniform: 200 for an accepted
// command (which always returns the resulting snapshot), 400 for a malformed or
// invalid body, 405 for the wrong verb, 404 for an unregistered /api path, 413
// for an oversized body. 201 is not used: a push does not create a resource at
// a new URL, it updates the one live document, and the caller consumes the
// returned snapshot immediately rather than following a Location header.

// routes builds the server's whole handler tree: the HTML shell, the static
// assets and the agent API. Serve mounts exactly this, and tests drive it
// directly so routing and method handling are exercised without opening a
// listener.
//
// The API is JSON in and JSON out, including failures. Rather than letting
// net/http emit its plain-text "405 Method Not Allowed" / "404 page not
// found" bodies, every /api route has an explicit non-GET and catch-all
// fallback so the agent always gets a machine-readable {"error":"..."} object.
//
// The whole tree is wrapped in the payload cap, which only applies to /api
// paths, so an oversized push cannot be buffered into memory.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.HandleRoot)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.StaticDir))))

	mux.HandleFunc("GET /api/state", s.handleAPIState)
	mux.HandleFunc("/api/state", methodNotAllowed(http.MethodGet))

	mux.HandleFunc("POST /api/code", s.handleAPICode)
	mux.HandleFunc("/api/code", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /api/message", s.handleAPIMessage)
	mux.HandleFunc("/api/message", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /api/eval-result", s.handleAPIEvalResult)
	mux.HandleFunc("/api/eval-result", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /api/hush", s.handleAPIHush)
	mux.HandleFunc("/api/hush", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /api/play", s.handleAPIPlay)
	mux.HandleFunc("/api/play", methodNotAllowed(http.MethodPost))

	// The listener WebSocket. It follows the /api idiom above rather than
	// letting net/http answer: the bare pattern is the wrong-verb fallback (405
	// plus Allow), while a WebSocket request without valid upgrade headers is
	// answered by the upgrade library itself with a specific error (426 Upgrade
	// Required, 400, or 501 if the writer cannot be hijacked) instead of a
	// panic or a 500. Only /ws is matched: /ws/ and /ws/anything are plain
	// 404s, exactly like every other unregistered non-/api path.
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("/ws", methodNotAllowed(http.MethodGet))

	// Anything else under /api (including bare /api) is a JSON 404.
	mux.HandleFunc("/api", handleAPINotFound)
	mux.HandleFunc("/api/", handleAPINotFound)

	return limitAPIRequestBody(mux)
}

// limitAPIRequestBody caps the body of every /api request before any handler
// sees it, so no endpoint can forget the limit. Handlers detect the trip via
// *http.MaxBytesError and answer 413.
func limitAPIRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			r.Body = http.MaxBytesReader(w, r.Body, APIMaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// methodNotAllowed answers any verb other than want with 405 and a JSON error
// naming the permitted method.
func methodNotAllowed(want string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", want)
		writeError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("method %s not allowed on %s, use %s", r.Method, r.URL.Path, want))
	}
}

// handleAPINotFound answers unregistered paths under /api as JSON.
func handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, fmt.Sprintf("no such API endpoint: %s %s", r.Method, r.URL.Path))
}

// handleAPIState serves the agent's read path. The Go server never evaluates
// JavaScript: the snapshot is stored state only.
func (s *Server) handleAPIState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Conductor.Snapshot())
}

// codeRequest is the POST /api/code body. message is optional narration that is
// shown to listeners alongside the new code; omitting it keeps the previous
// narration.
type codeRequest struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// handleAPICode is the agent's only way to change what is playing: it stores
// the new document, bumps the version by exactly one, and returns the resulting
// snapshot, which the WebSocket hub (issue .3) fans out to every listener. The
// server never parses or evaluates the JavaScript.
func (s *Server) handleAPICode(w http.ResponseWriter, r *http.Request) {
	var req codeRequest
	if err := decodeBody(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		writeError(w, http.StatusBadRequest, "code must not be empty: send the strudel pattern to play")
		return
	}

	writeJSON(w, http.StatusOK, s.Conductor.Publish(req.Code, req.Message))
}

// messageRequest is the POST /api/message body. An empty message is rejected
// rather than treated as a no-op: a silent no-op in the agent's loop is
// indistinguishable from a lost request.
type messageRequest struct {
	Message string `json:"message"`
}

// handleAPIMessage records agent narration without touching the code document,
// the version or the history. Listeners see the message change while the music
// keeps playing.
func (s *Server) handleAPIMessage(w http.ResponseWriter, r *http.Request) {
	var req messageRequest
	if err := decodeBody(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest,
			"message must not be empty: use POST /api/code to change the music, or send narration text")
		return
	}

	s.Conductor.SetMessage(req.Message)
	writeJSON(w, http.StatusOK, s.Conductor.Snapshot())
}

// evalResultRequest is the POST /api/eval-result body. Stats are opaque
// client-supplied JSON (hap counts and similar) and are stored verbatim.
type evalResultRequest struct {
	Version int64           `json:"version"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error"`
	Stats   json.RawMessage `json:"stats"`
}

// handleAPIEvalResult lets a browser tell the server whether a pushed version
// actually evaluated in its sandbox repl, which closes the agent's feedback
// loop: the agent reads the verdict back from GET /api/state instead of
// assuming its code worked. The server never evaluates JavaScript itself, so
// this is pure bookkeeping. A report for a version that was never published is
// rejected rather than stored, because accepting it would tell the agent a
// version succeeded when no listener could have seen it.
func (s *Server) handleAPIEvalResult(w http.ResponseWriter, r *http.Request) {
	var req evalResultRequest
	if err := decodeBody(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}

	res := EvalResult{Version: req.Version, OK: req.OK, Error: req.Error, Stats: req.Stats}
	if err := s.Conductor.RecordEvalResult(res); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, apiEvalAck{Accepted: true, Version: req.Version})
}

// apiEvalAck is the response to an accepted eval report. The field is named
// "accepted" (not "ok") so it cannot be mistaken for the eval verdict itself,
// which is the body's own ok field and is read back from GET /api/state.
type apiEvalAck struct {
	Accepted bool  `json:"accepted"`
	Version  int64 `json:"version"`
}

// handleAPIHush records transport intent only. There is no audio in Go and no
// JavaScript is evaluated here: the hushed flag is published so every browser
// knows to stop its own repl, and the code document is left untouched so the
// agent (or a listener) can resume exactly where the performance was.
func (s *Server) handleAPIHush(w http.ResponseWriter, r *http.Request) {
	s.handleAPITransport(w, r, false)
}

// handleAPIPlay is the counterpart to hush: resume intent, no audio.
func (s *Server) handleAPIPlay(w http.ResponseWriter, r *http.Request) {
	s.handleAPITransport(w, r, true)
}

// handleAPITransport shares the hush/play body handling. Both are
// argument-free and idempotent: re-asserting the current transport state
// succeeds, because the agent may not have read /api/state first. A body is
// still parsed when present so a client cannot smuggle a "code" field through
// these endpoints believing it changed the music.
func (s *Server) handleAPITransport(w http.ResponseWriter, r *http.Request, playing bool) {
	if err := rejectUnexpectedBody(r); err != nil {
		writeDecodeError(w, err)
		return
	}
	s.Conductor.SetPlaying(playing)
	writeJSON(w, http.StatusOK, s.Conductor.Snapshot())
}

// errBodyNotAllowed reports a body where none is accepted.
var errBodyNotAllowed = errors.New("this endpoint takes no arguments")

// rejectUnexpectedBody enforces an argument-free endpoint: an empty body is
// fine, and `{}` is fine, but any field at all is an error naming the offending
// field.
func rejectUnexpectedBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if len(probe) > 0 {
		names := make([]string, 0, len(probe))
		for name := range probe {
			names = append(names, name)
		}
		sort.Strings(names)
		return fmt.Errorf("%w: unexpected field(s) %s (use POST /api/code to change the music)",
			errBodyNotAllowed, strings.Join(names, ", "))
	}
	return nil
}

// errTrailingJSON is returned when a valid JSON value is followed by more data.
var errTrailingJSON = errors.New("unexpected data after JSON body")

// decodeBody reads exactly one JSON object from the request body into dst. It
// rejects unknown fields (a typo like "messge" must not be silently ignored),
// trailing content, and — via the middleware-installed MaxBytesReader —
// oversized payloads.
//
// The whole (capped) body is drained before decoding on purpose: the stream
// decoder can hit a JSON *syntax* error within the first few bytes of an
// oversized body and return that instead of the MaxBytesError, which would
// report a payload-cap violation as a 400. Draining first makes the cap
// authoritative no matter how malformed the body is.
func decodeBody(r *http.Request, dst any) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errTrailingJSON
	}
	return nil
}

// writeDecodeError turns a body-decoding failure into the right status: 413 for
// a payload over the documented cap, 400 for anything malformed.
func writeDecodeError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("request body too large: limit is %d bytes", APIMaxBodyBytes))
		return
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body: empty or truncated request body")
		return
	}
	writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
}

// apiError is the single error shape every failing /api request returns, so a
// client can branch on "error" without parsing prose. The message is meant to
// be specific enough to act on (which field, which limit, which version).
type apiError struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apiError{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write json response", "error", err)
	}
}
