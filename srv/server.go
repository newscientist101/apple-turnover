package srv

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"runtime"
	"time"
)

// HistoryLimit is how many recent code versions the Conductor retains for the
// external agent to read back.
const HistoryLimit = 32

// pageData is the value handed to the shell template. It is deliberately a
// struct with at least one field rather than nil: html/template only raises an
// error for an unresolvable field reference when the data is a struct. Against
// nil, a template naming a field that no longer exists renders an empty string
// and Execute returns nil, so HandleRoot would serve a silently truncated page
// with a 200 and the render-completion tests could not see it. Keep the struct,
// and keep passing it — see HandleRoot.
type pageData struct {
	Now string
}

// Server is the HTTP front end. It owns the single in-memory Conductor for the
// live performance and the Hub that fans the encoded performance out to every
// connected listener; there is no persistence.
type Server struct {
	Conductor    *Conductor
	Hub          *Hub
	TemplatesDir string
	StaticDir    string

	// wsWriteTimeout bounds one frame write to one listener (see handleWS). It is
	// a field rather than a constant for one reason: the tests must be able to
	// prove the bound exists without spending the production timeout in every
	// run. It is read by handlers and written only by New, before the server
	// serves anything.
	wsWriteTimeout time.Duration

	// wsPingInterval is how often a listener is pinged, and wsPongTimeout how
	// long it has to answer before it is declared dead (see wsPingListener and
	// handleWS). They are fields for the same reason wsWriteTimeout is: the
	// reaping tests must run in tens of milliseconds rather than the production
	// 30s, and neither value may be baked into a test as a literal.
	wsPingInterval time.Duration
	wsPongTimeout  time.Duration
}

// New builds a Server with its Conductor and its Hub, and the wiring between
// them: the Hub reports its live subscriber count to the Conductor, which is
// what makes Snapshot.ListenerCount (and so GET /api/state.listenerCount) a real
// number rather than a permanently-0 field, and hands back the frame that tells
// the listeners already watching (issue .3.7).
//
// The hook is the hub's, and it is a func(int) []byte precisely so this wiring
// lives here instead of inside the hub: the hub is handed a number and answers
// with opaque bytes it does not interpret, so it still knows nothing about a
// Conductor or about the wire format. Note the direction of travel: hub →
// conductor, on the hub goroutine, and the Conductor never calls into the Hub
// while holding its own lock (the API handlers broadcast only after the
// Conductor call returns). That makes the pair acyclic — a Conductor method that
// called back into the Hub from inside its critical section would deadlock
// against the command the hub goroutine is currently serving, which is exactly
// why listenerCountFrame does not call Hub.Broadcast either.
func New() *Server {
	_, thisFile, _, _ := runtime.Caller(0)
	baseDir := filepath.Dir(thisFile)
	conductor := NewConductor(HistoryLimit)
	s := &Server{
		Conductor:    conductor,
		TemplatesDir: filepath.Join(baseDir, "templates"),
		StaticDir:    filepath.Join(baseDir, "static"),

		wsWriteTimeout: defaultWSWriteTimeout,
		wsPingInterval: defaultWSPingInterval,
		wsPongTimeout:  defaultWSPongTimeout,
	}
	s.Hub = NewHubWithCountHook(HubDefaultSendBuffer, s.listenerCountFrame)
	return s
}

// listenerCountFrame is the hub's count hook: it publishes the count into the
// Conductor and answers with the frame the hub then delivers to every listener
// EXCEPT the one that caused the change.
//
// The order inside is load-bearing and is the same contract the hook has always
// had (issue .3.5): the count is stored FIRST, so the snapshot this very call
// encodes already includes the new value. A frame built from the pre-change
// count would tell every listener a number it already knows to be wrong.
//
// This runs on the hub goroutine, which is why it must stay cheap. It takes the
// Conductor lock twice and marshals one small object — bounded work, no waiting
// on anything — and it must never call into the Hub, which would deadlock
// against the command the hub goroutine is serving (see NewHubWithCountHook).
func (s *Server) listenerCountFrame(n int) []byte {
	s.Conductor.SetListenerCount(n)
	return s.encodeEvent(EventListenerCount, s.Conductor.Snapshot())
}

// HandleRoot renders the shell. It passes a pageData struct (not nil) on
// purpose: see the pageData comment for why the type of this value is load
// bearing for the render-not-truncate guard.
func (s *Server) HandleRoot(w http.ResponseWriter, r *http.Request) {
	data := pageData{Now: time.Now().Format(time.RFC3339)}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.renderTemplate(w, "welcome.html", data); err != nil {
		slog.Warn("render template", "url", r.URL.Path, "error", err)
	}
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) error {
	path := filepath.Join(s.TemplatesDir, name)
	tmpl, err := template.ParseFiles(path)
	if err != nil {
		return fmt.Errorf("parse template %q: %w", name, err)
	}
	if err := tmpl.Execute(w, data); err != nil {
		return fmt.Errorf("execute template %q: %w", name, err)
	}
	return nil
}

// Serve starts the HTTP server with the configured routes. All routing lives in
// routes() so tests can drive the exact same handler tree over httptest.
func (s *Server) Serve(addr string) error {
	slog.Info("starting server", "addr", addr)
	return http.ListenAndServe(addr, s.routes())
}
