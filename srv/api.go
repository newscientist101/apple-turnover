package srv

import (
	"bytes"
	"context"
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

	mux.HandleFunc("POST /api/anchor", s.handleAPIAnchor)
	mux.HandleFunc("/api/anchor", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /api/hush", s.handleAPIHush)
	mux.HandleFunc("/api/hush", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /api/play", s.handleAPIPlay)
	mux.HandleFunc("/api/play", methodNotAllowed(http.MethodPost))

	// Agent liveness. Argument-free like hush/play, and deliberately silent on
	// the listener socket: a heartbeat renews a lease every listener already
	// believes is held, so there is nothing for them to be told. The ONE thing
	// they must be told is that the lease lapsed, and that comes from the lease
	// sweeper rather than from here (see Server.startAgentSweeper).
	mux.HandleFunc("POST /api/heartbeat", s.handleAPIHeartbeat)
	mux.HandleFunc("/api/heartbeat", methodNotAllowed(http.MethodPost))

	// Dry-run: validate a candidate without publishing it (strudel-agent-uvj.16).
	//
	// These two are the only endpoints that make an agent WAIT. Every other
	// write is validate -> commit -> broadcast -> return, and dry-run has no
	// commit at all: it publishes nothing, so there is no state to return. What
	// it returns is somebody else's answer, which is why it needs both halves —
	// a request that asks, and a report that replies.
	mux.HandleFunc("POST /api/dry-run", s.handleAPIDryRun)
	mux.HandleFunc("/api/dry-run", methodNotAllowed(http.MethodPost))

	mux.HandleFunc("POST /api/dry-run-result", s.handleAPIDryRunResult)
	mux.HandleFunc("/api/dry-run-result", methodNotAllowed(http.MethodPost))

	// Drift reporting: how a listener tells the server where its commit landed
	// (strudel-agent-uvj.15). Like /api/dry-run-result this is a report only a
	// browser sends, and like /api/eval-result it answers with an ack rather than
	// a snapshot -- but unlike either, an agent READS the stored observation back
	// from GET /api/state, because drift is what an agent uses to decide whether
	// to re-anchor.
	mux.HandleFunc("POST /api/sync-result", s.handleAPISyncResult)
	mux.HandleFunc("/api/sync-result", methodNotAllowed(http.MethodPost))

	// Per-listener sample state (strudel-agent-f79). It is a report only a
	// browser sends, keyed by the connection id the server handed it on connect,
	// and an agent READS the stored result back from GET /api/state — because the
	// question it answers ("can anyone actually hear this?") is not about the
	// document and so cannot live on a verdict.
	mux.HandleFunc("POST /api/samples", s.handleAPISamples)
	mux.HandleFunc("/api/samples", methodNotAllowed(http.MethodPost))

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
// the new document, bumps the version by exactly one, broadcasts the result to
// every listener, and returns the resulting snapshot. The server never parses
// or evaluates the JavaScript.
//
// The broadcast happens AFTER the write succeeded and before the response, so a
// listener is never told about a version that was rejected. The same snapshot
// is both broadcast and returned, so the agent and the listeners can never
// disagree about which version is live.
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

	snap := s.Conductor.Publish(req.Code, req.Message)
	s.broadcast(EventCode, snap)
	writeJSON(w, http.StatusOK, snap)
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
	snap := s.Conductor.Snapshot()
	s.broadcast(EventMessage, snap)
	writeJSON(w, http.StatusOK, snap)
}

// evalResultRequest is the POST /api/eval-result body. Stats arrive as raw
// JSON because their KEYS are opaque — the browser decides what to report and
// the server never interprets them — but they must be a JSON OBJECT, which is
// not something this struct can enforce: json.RawMessage accepts any JSON
// value. RecordEvalResult is where that is checked, because the Conductor owns
// the invariant rather than this one entry point into it.
type evalResultRequest struct {
	Version int64           `json:"version"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error"`
	Stats   json.RawMessage `json:"stats"`

	// SamplesResolved is the tri-state from strudel-agent-uvj.18. It is a POINTER
	// so that "no registry to check" arrives as nil rather than as false: the
	// difference between "I looked and it is missing" and "I could not look" is
	// the difference between a finding and a silence, and decodeBody runs with
	// DisallowUnknownFields, so a browser that sends this field against a server
	// that does not declare it gets a 400 rather than a stored verdict.
	SamplesResolved *bool `json:"samplesResolved"`
}

// handleAPIEvalResult lets a browser tell the server whether a pushed version
// actually evaluated in its sandbox repl, which closes the agent's feedback
// loop: the agent reads the verdict back from GET /api/state instead of
// assuming its code worked. The server never evaluates JavaScript itself, so
// this is pure bookkeeping. A report for a version that was never published is
// rejected rather than stored, because accepting it would tell the agent a
// version succeeded when no listener could have seen it.
//
// The broadcast is gated on `stored`, not on the error. A stale report — one
// naming a version OLDER than the verdict already held — is understood and
// deliberately discarded, so it answers 200 with accepted:true (the browser's
// report was well-formed) but must NOT be broadcast: the state did not change,
// and telling every listener a new verdict exists when none was stored is a lie
// they cannot detect on the wire.
func (s *Server) handleAPIEvalResult(w http.ResponseWriter, r *http.Request) {
	var req evalResultRequest
	if err := decodeBody(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}

	res := EvalResult{
		Version:         req.Version,
		OK:              req.OK,
		Error:           req.Error,
		Stats:           req.Stats,
		SamplesResolved: req.SamplesResolved,
	}
	stored, err := s.Conductor.RecordEvalResult(res)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if stored {
		s.broadcast(EventEvalResult, s.Conductor.Snapshot())
	}

	writeJSON(w, http.StatusOK, apiEvalAck{Accepted: true, Version: req.Version})
}

// anchorRequest is the POST /api/anchor body: the shared timeline every listener
// maps its scheduler position onto. Both fields are required — a re-anchor is a
// complete replacement of the timeline, not a patch, so an agent changing only
// the rate must resend the current epochMs rather than leave it to be guessed.
type anchorRequest struct {
	EpochMS int64   `json:"epochMs"`
	CPS     float64 `json:"cps"`
}

// handleAPIAnchor republishes the shared timeline anchor (issue .3vo.8.1) so an
// agent can move every listener onto one clock — for instance after changing
// tempo, or to pull a listener whose clock had drifted onto the shared bar
// grid.
//
// It deliberately does NOT bump the version and does NOT touch the code
// document: a re-anchor changes where the shared timeline starts, not what is
// playing, so putting it in the code history would make a tempo change look
// like a new revision to every client.
//
// The broadcast is gated on acceptance, exactly like every other accepted write
// here: a refused anchor (a non-positive rate, or an epoch too far from the
// server's clock) leaves the stored anchor untouched and broadcasts NOTHING.
// Telling listeners to adopt a timeline the server refused to store would leave
// every client scheduling against a bar grid the server does not hold.
func (s *Server) handleAPIAnchor(w http.ResponseWriter, r *http.Request) {
	var req anchorRequest
	if err := decodeBody(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}

	if err := s.Conductor.SetAnchor(req.EpochMS, req.CPS); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	snap := s.Conductor.Snapshot()
	s.broadcast(EventAnchor, snap)
	writeJSON(w, http.StatusOK, snap)
}

// dryRunRequest is the POST /api/dry-run body: the same candidate document
// POST /api/code takes, and nothing else. `message` is deliberately absent --
// narration describes a published change, and a dry-run publishes nothing, so
// there is nothing for a listener to be told about it.
type dryRunRequest struct {
	Code string `json:"code"`
}

// handleAPIDryRun evaluates a candidate without publishing it.
//
// It is the one endpoint that BLOCKS, and everything about its shape follows
// from that. There is no commit and no snapshot to return, because a dry-run
// changes nothing; what it returns is a verdict somebody else produced. So the
// order is: validate, confirm somebody can actually answer, register an id,
// broadcast the candidate, then wait -- each step before the one that depends on
// it.
//
// The no-listener check comes BEFORE registering, and it is a 409 rather than a
// wait. With nobody connected the outcome is already known -- there is no
// evaluator -- so blocking until the timeout would spend the agent's whole
// budget to arrive at "unknown" for a condition the server could see at once,
// and the caller could not tell a missing evaluator from a broken candidate.
//
// Nothing here calls into the Conductor. That is not an oversight to be fixed
// later: the version, the history and the stored verdict must all survive a
// dry-run untouched, and the cheapest way to guarantee that is for the code path
// to have no way to reach them.
func (s *Server) handleAPIDryRun(w http.ResponseWriter, r *http.Request) {
	var req dryRunRequest
	if err := decodeBody(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		writeError(w, http.StatusBadRequest, "code must not be empty: send the strudel pattern to validate")
		return
	}

	if s.Conductor.Snapshot().ListenerCount == 0 {
		writeError(w, http.StatusConflict,
			"no listeners connected: cannot dry-run, because the browser is the only evaluator. Open the page in a browser, or publish with POST /api/code and read the verdict back.")
		return
	}

	dryReq, answer := s.dryRuns.Register(req.Code)

	// The snapshot is the current state, not the candidate: a dry-run frame tells
	// a listener what the performance looks like RIGHT NOW, so a client can keep
	// its panels current while it evaluates something that is not live.
	s.broadcastDryRun(dryReq)

	verdict, err := s.awaitRegisteredDryRun(r.Context(), dryReq, answer)
	if err != nil {
		switch {
		case errors.Is(err, errDryRunTimeout):
			writeError(w, http.StatusGatewayTimeout,
				fmt.Sprintf("%v: no listener answered POST /api/dry-run for id %d within %s. The candidate was not evaluated and nothing was published.",
					err, dryReq.ID, s.dryRunTimeout))
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			// The caller gave up first. Its own deadline is what it will report,
			// so this only has to be honest and brief.
			writeError(w, http.StatusGatewayTimeout,
				fmt.Sprintf("the caller stopped waiting for dry-run %d: %v", dryReq.ID, err))
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// The verdict is returned verbatim, with the dry-run's own id echoed so a
	// caller running several can tell which answer it is holding. It is NOT
	// stored and NOT broadcast: no listener needs to learn what a candidate that
	// was never published evaluated to.
	writeJSON(w, http.StatusOK, verdict)
}

// dryRunResultRequest is the POST /api/dry-run-result body: the browser's answer
// to one dry-run. Stats obeys the same JSON-object rule as EvalResult.Stats and
// is normalized by the same helper, so the three spellings of "no stats" mean
// the same thing here as they do there.
type dryRunResultRequest struct {
	DryRunID int64           `json:"dryRunId"`
	OK       bool            `json:"ok"`
	Error    string          `json:"error"`
	Stats    json.RawMessage `json:"stats"`

	// SamplesResolved is the same tri-state as on /api/eval-result, and the same
	// pointer for the same reason: the two endpoints are the two ways an agent
	// asks whether code evaluates, so a resolver that answered on one and stayed
	// silent on the other would give an agent two answers to one question.
	SamplesResolved *bool `json:"samplesResolved"`
}

// handleAPIDryRunResult accepts a browser's verdict on one dry-run and hands it
// to the waiting request.
//
// It never touches the Conductor, and that is the point: this verdict describes
// code that was never published, so storing it would overwrite the browser's
// real verdict for the current version with a statement about a candidate. The
// stored verdict is the agent's only feedback signal, and a verdict about code
// that is not live is worse than none, because read back through /api/state it
// is indistinguishable from a real one.
//
// An unknown id is a 404 rather than a silent 200. Every connected listener
// evaluates and reports, so the losers are expected and normal, but a browser
// whose report vanished has learned nothing from a quiet success -- and "nobody
// is waiting for this any more" is worth saying out loud.
func (s *Server) handleAPIDryRunResult(w http.ResponseWriter, r *http.Request) {
	var req dryRunResultRequest
	if err := decodeBody(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}

	stats, err := normalizeStats(req.Stats)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	verdict := DryRunVerdict{
		ID:              req.DryRunID,
		OK:              req.OK,
		Error:           req.Error,
		Stats:           stats,
		SamplesResolved: req.SamplesResolved,
	}
	if err := s.dryRuns.Resolve(verdict); err != nil {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("%s: %d (it was never registered, has already been answered, or has expired)", err, req.DryRunID))
		return
	}

	// An ack, deliberately NOT a snapshot: nothing changed, so there is no state
	// to report. It mirrors apiEvalAck's shape for the same reason -- a browser
	// needs to know its report landed, not what the performance now looks like.
	writeJSON(w, http.StatusOK, apiEvalAck{Accepted: true, Version: req.DryRunID})
}

// syncResultRequest is the POST /api/sync-result body: how a browser reports
// where its commit of a version actually landed (strudel-agent-uvj.15).
//
// It mirrors SyncObservation except for epochMs, which the SERVER stamps on
// store. A browser-supplied receipt time would be unreadable: the browser's
// clock is precisely what disagrees with the server's, and freshness is only
// comparable against the clock the agent reads /api/state with.
//
// DriftMS is *int64 rather than int64 so "no drift measured" and "measured zero"
// stay apart on the wire; see the SyncObservation type comment for why that
// distinction is the whole point of the field.
type syncResultRequest struct {
	Version     int64  `json:"version"`
	DriftMS     *int64 `json:"driftMs,omitempty"`
	Unscheduled bool   `json:"unscheduled,omitempty"`
	TargetMS    int64  `json:"targetMs,omitempty"`
	ActualMS    int64  `json:"actualMs,omitempty"`
}

// handleAPISyncResult records a listener's drift observation and publishes it.
//
// The endpoint exists because measured drift never left the browser: the client
// computed it, painted it into #sync-status and dropped it, so the drift
// visibility README and AGENT_API.md promised was reachable only by a human
// looking at the tab. An agent could not tell when listeners had drifted apart
// and so could not know when to re-anchor, which is the documented recovery
// mechanism.
//
// It is deliberately its own report rather than a field on /api/eval-result.
// applyVersion POSTs the eval verdict immediately after ARMING the boundary
// timer, so at that instant the drift does not exist yet; folding it in would
// mean stalling the agent's feedback loop behind a bar line, or POSTing one
// version twice and letting the second POST overwrite the verdict. Drift is
// measured about a COMMIT, at a bar, at a different instant from an evaluation.
//
// An ack, not a snapshot, for the same reason as the dry-run report: the browser
// needs to know its measurement landed. The observation itself is read from
// GET /api/state, which is the agent's path.
func (s *Server) handleAPISyncResult(w http.ResponseWriter, r *http.Request) {
	var req syncResultRequest
	if err := decodeBody(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}

	obs := SyncObservation{
		Version:     req.Version,
		DriftMS:     req.DriftMS,
		Unscheduled: req.Unscheduled,
		TargetMS:    req.TargetMS,
		ActualMS:    req.ActualMS,
	}
	if err := s.Conductor.RecordSyncObservation(obs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Broadcast only after the store accepted, like every other accepted
	// mutation here. A listener told about a measurement the server refused would
	// be describing drift that does not exist.
	s.broadcast(EventSync, s.Conductor.Snapshot())

	writeJSON(w, http.StatusOK, apiEvalAck{Accepted: true, Version: req.Version})
}

// samplesRequest is the POST /api/samples body: one listener's claim about its
// own sample registry (strudel-agent-f79).
//
// ListenerID is REQUIRED, and it is the whole reason this endpoint can exist. It
// is the connection id the server minted and handed to that browser on its
// `listener` frame; without it the server has no way to tell this report from any
// other, which is the defect the bead describes.
//
// Loaded is a POINTER so "I have no registry to check" arrives as absent rather
// than as false. The difference is the same one EvalResult.SamplesResolved keeps,
// and it is the original uvj.18 defect arriving one layer out: a false there would
// invent a missing-sample defect nobody found, and a true would be success for a
// check that never ran.
//
// EpochMS is absent from the request on purpose: the SERVER stamps receipt, for
// the reason it does so on a sync observation and an eval result. The browser's
// clock is precisely what disagrees with the server's.
type samplesRequest struct {
	ListenerID string `json:"listenerId"`
	Loaded     *bool  `json:"loaded"`
	Count      int    `json:"count"`
}

// handleAPISamples records what one listener says its sample registry holds.
//
// It exists because `lastEvalResult.samplesResolved` is verdict-scoped and the
// audience is not. Every listener evaluates and reports, the server stored
// whichever answer arrived last, and with listeners anonymous it could not say
// which — so an agent could read "resolved" from the one tab that had the pack
// while every other listener heard silence. This endpoint makes the audience
// answerable by keying every report to a connection.
//
// Validation order within one body is BODY-THEN-IDENTITY, matching
// /api/eval-result: a report wrong in two ways is diagnosed by the check its
// sender can act on, and a sender who typed a bad field can fix that without
// first having to know it has the wrong id.
//
// An ack, not a snapshot, for the same reason as the sync report: a browser needs
// to know its claim landed, not what the performance now looks like. The stored
// result is read from GET /api/state, which is the agent's path.
//
// THE BROADCAST IS CONDITIONAL and this is the load-bearing part of the handler.
// A `samples` frame is sent for a TRANSITION, not for an accepted write: a browser
// re-reporting an unchanged registry has told the server nothing new, and
// publishing that would put a frame on the wire every poll, filling listeners'
// bounded queues and evicting peers on a busy tab. The store decides, because it
// is the only place that knows the previous value — a handler that broadcast on
// every 200 would re-introduce the defect the transition rule exists to prevent.
//
// A REFUSED report broadcasts nothing, like every other accepted-mutation path:
// a listener told about sample state the server refused to store would be
// describing a claim that does not exist.
func (s *Server) handleAPISamples(w http.ResponseWriter, r *http.Request) {
	var req samplesRequest
	if err := decodeBody(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}

	changed, err := s.Conductor.RecordListenerSamples(req.ListenerID, ListenerSamples{
		Loaded: req.Loaded,
		Count:  req.Count,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if changed {
		s.broadcast(EventSamples, s.Conductor.Snapshot())
	}

	writeJSON(w, http.StatusOK, apiEvalAck{Accepted: true, Version: s.Conductor.Snapshot().Version})
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
// handleAPIHeartbeat renews the agent's liveness lease.
//
// It is the only way an agent can be "connected" at all: the agent speaks plain
// request/response HTTP and holds no persistent connection, so the server cannot
// observe a socket and this lease is the whole of what it knows (see
// AgentPresence).
//
// It follows validate -> commit -> broadcast like every other write, with one
// deliberate omission: there is NO broadcast. A heartbeat renews a lease that
// every listener already believes is held — they received the active flag in
// whichever frame last carried it — so the event that listeners need is the one
// that CANNOT be triggered from here, namely the lease lapsing. Announcing each
// renewal would be indistinguishable, on the wire, from a stream of no-ops, and
// because a listener's queue is bounded at 64 messages a heartbeat loop running
// faster than a listener drains could evict that listener on its own.
//
// A heartbeat is therefore accepted, committed, and returned — but silent. The
// expiry sweeper owns the EventAgent broadcast.
//
// The endpoint is argument-free and idempotent, for the reasons hush and play
// are: an agent may heartbeat on a timer without reading /api/state first, and
// a body is still parsed and refused so a client cannot smuggle a "code" field
// through it believing it changed the music.
func (s *Server) handleAPIHeartbeat(w http.ResponseWriter, r *http.Request) {
	if err := rejectUnexpectedBody(r); err != nil {
		writeDecodeError(w, err)
		return
	}
	// The snapshot is returned rather than a bare ack so the agent can confirm
	// what the server believes without a second round trip — the same reason
	// every other accepted write answers with the state it produced.
	writeJSON(w, http.StatusOK, s.Conductor.RecordAgentPresence())
}

func (s *Server) handleAPITransport(w http.ResponseWriter, r *http.Request, playing bool) {
	if err := rejectUnexpectedBody(r); err != nil {
		writeDecodeError(w, err)
		return
	}
	s.Conductor.SetPlaying(playing)
	snap := s.Conductor.Snapshot()
	s.broadcast(EventTransport, snap)
	writeJSON(w, http.StatusOK, snap)
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
