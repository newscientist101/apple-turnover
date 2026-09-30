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
}

func New() *Server {
	_, thisFile, _, _ := runtime.Caller(0)
	baseDir := filepath.Dir(thisFile)
	return &Server{
		Conductor:    NewConductor(HistoryLimit),
		Hub:          NewHub(HubDefaultSendBuffer),
		TemplatesDir: filepath.Join(baseDir, "templates"),
		StaticDir:    filepath.Join(baseDir, "static"),

		wsWriteTimeout: defaultWSWriteTimeout,
	}
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
