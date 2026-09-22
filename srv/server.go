package srv

import (
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// HistoryLimit is how many recent code versions the Conductor retains for the
// external agent to read back.
const HistoryLimit = 32

// Server is the HTTP front end. It owns the single in-memory Conductor for the
// live performance and the Hub that fans the encoded performance out to every
// connected listener; there is no persistence.
type Server struct {
	Conductor    *Conductor
	Hub          *Hub
	Hostname     string
	TemplatesDir string
	StaticDir    string

	// wsWriteTimeout bounds one frame write to one listener (see handleWS). It is
	// a field rather than a constant for one reason: the tests must be able to
	// prove the bound exists without spending the production timeout in every
	// run. It is read by handlers and written only by New, before the server
	// serves anything.
	wsWriteTimeout time.Duration
}

type pageData struct {
	Hostname  string
	Now       string
	UserEmail string
	LoginURL  string
	LogoutURL string
	Headers   []headerEntry
}

type headerEntry struct {
	Name       string
	Values     []string
	AddedByExe bool
}

func New(hostname string) *Server {
	_, thisFile, _, _ := runtime.Caller(0)
	baseDir := filepath.Dir(thisFile)
	return &Server{
		Conductor:    NewConductor(HistoryLimit),
		Hub:          NewHub(HubDefaultSendBuffer),
		Hostname:     hostname,
		TemplatesDir: filepath.Join(baseDir, "templates"),
		StaticDir:    filepath.Join(baseDir, "static"),

		wsWriteTimeout: defaultWSWriteTimeout,
	}
}

func (s *Server) HandleRoot(w http.ResponseWriter, r *http.Request) {
	// Identity from proxy headers (if present). The live performance is
	// shared and anonymous; identity is only used to greet the viewer.
	userEmail := strings.TrimSpace(r.Header.Get("X-ExeDev-Email"))
	now := time.Now()

	data := pageData{
		Hostname:  s.Hostname,
		Now:       now.Format(time.RFC3339),
		UserEmail: userEmail,
		LoginURL:  loginURLForRequest(r),
		LogoutURL: "/__exe.dev/logout",
		Headers:   buildHeaderEntries(r),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.renderTemplate(w, "welcome.html", data); err != nil {
		slog.Warn("render template", "url", r.URL.Path, "error", err)
	}
}

func loginURLForRequest(r *http.Request) string {
	// Send the user back to this page after login, but use only the path and
	// drop the query. The query can carry exe.dev's reserved "redirect" param;
	// folding that back into a new redirect param would let a crawler following
	// the login link nest (and re-encode) the whole URL each hop, growing it
	// without bound. A bare path can't loop.
	v := url.Values{}
	v.Set("redirect", r.URL.Path)
	return "/__exe.dev/login?" + v.Encode()
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

func mainDomainFromHost(h string) string {
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		host = strings.TrimSpace(h)
	}
	if port != "" {
		port = ":" + port
	}
	// Check for exe.cloud-based domains (dev mode)
	if strings.HasSuffix(host, ".exe.cloud") || host == "exe.cloud" {
		return "exe.cloud" + port
	}
	// Check for exe.dev-based domains (production)
	if strings.HasSuffix(host, ".exe.dev") || host == "exe.dev" {
		return "exe.dev"
	}
	// Return as-is for custom domains
	return host
}

// Serve starts the HTTP server with the configured routes. All routing lives in
// routes() so tests can drive the exact same handler tree over httptest.
func (s *Server) Serve(addr string) error {
	slog.Info("starting server", "addr", addr)
	return http.ListenAndServe(addr, s.routes())
}

func buildHeaderEntries(r *http.Request) []headerEntry {
	if r == nil {
		return nil
	}

	headers := make([]headerEntry, 0, len(r.Header)+1)
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		headers = append(headers, headerEntry{
			Name:       name,
			Values:     values,
			AddedByExe: strings.HasPrefix(lower, "x-exedev-") || strings.HasPrefix(lower, "x-forwarded-"),
		})
	}
	if r.Host != "" {
		headers = append(headers, headerEntry{
			Name:   "Host",
			Values: []string{r.Host},
		})
	}

	sort.Slice(headers, func(i, j int) bool {
		return strings.ToLower(headers[i].Name) < strings.ToLower(headers[j].Name)
	})
	return headers
}
