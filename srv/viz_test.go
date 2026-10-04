package srv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestVisualizationWiring proves the custom pattern visualization wiring (issue strudel-agent-3vo.7).
func TestVisualizationWiring(t *testing.T) {
	server := New()

	// (a) GET / through httptest + server.HandleRoot, assert rendered shell contains <script src="/static/viz.js"
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	server.HandleRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<script src="/static/viz.js"`) {
		t.Errorf("shell is missing <script src=\"/static/viz.js\": visualization module is not wired")
	}

	// (b) GET /static/viz.js through server.routes(), assert 200, non-JSON Content-Type, non-empty body, and load-bearing markers.
	req = httptest.NewRequest(http.MethodGet, "/static/viz.js", nil)
	w = httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/viz.js status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET /static/viz.js served as JSON (%q): static asset swallowed by API", ct)
	}
	vizJS := w.Body.String()
	if len(vizJS) == 0 {
		t.Fatal("GET /static/viz.js returned empty body")
	}

	requiredVizMarkers := []string{
		".queryArc(",
		"value.s",
		"value.note",
		"epochMs",
		"anchor.cps",
		"requestAnimationFrame",
		"playhead",
	}
	for _, marker := range requiredVizMarkers {
		if !strings.Contains(vizJS, marker) {
			t.Errorf("viz.js is missing load-bearing marker %q", marker)
		}
	}

	// (c) GET /static/session.js and assert the session.js hooks landed (its body contains strudelViz.setPattern and strudelViz.onSnapshot)
	req = httptest.NewRequest(http.MethodGet, "/static/session.js", nil)
	w = httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/session.js status = %d, want 200", w.Code)
	}
	sessionJS := w.Body.String()
	for _, hook := range []string{
		"strudelViz.setPattern",
		"strudelViz.onSnapshot",
	} {
		if !strings.Contains(sessionJS, hook) {
			t.Errorf("session.js is missing visualization hook %q", hook)
		}
	}
}
