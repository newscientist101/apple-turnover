package srv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSessionClientValidateThenCommit is the TDD anchor for issue .3vo.5
// (browser session client: validate-then-commit). The live-change safety
// contract lives in the browser: unvalidated code must never reach the live
// repl, because repl.evaluate() hushes BEFORE parsing. The Go side of that
// contract is small — serve the shell referencing the session client, and
// serve the client itself with the load-bearing markers — but it must be
// pinned so a later shell edit cannot silently drop the client.
func TestSessionClientValidateThenCommit(t *testing.T) {
	server := New()

	// 1. The shell must load the session client after initStrudel exists.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	server.HandleRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"</main>",
		"</html>",
		`<script src="/static/session.js"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shell is missing %q: validate-then-commit client is not wired", want)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
		t.Error("shell did not render to completion: missing </html> tail")
	}

	// 2. The client itself must be served intact.
	req = httptest.NewRequest(http.MethodGet, "/static/session.js", nil)
	w = httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/session.js status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET /static/session.js served as JSON (%q): the API layer swallowed a static asset", ct)
	}
	js := w.Body.String()
	if len(js) == 0 {
		t.Fatal("GET /static/session.js returned an empty body")
	}

	// 3. Load-bearing markers: the exact calls the bead names.
	for _, want := range []string{
		"sandbox repl",
		"getTime",
		"defaultOutput",
		".evaluate(",
		"evalError",
		"setPattern(pattern, false)",
		"queryArc",
		"/api/eval-result",
		`"/ws"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("session.js is missing load-bearing marker %q", want)
		}
	}

	// 4. The safety invariant in code form: the LIVE repl is never evaluated.
	// Only the sandbox validates (sandbox.evaluate); the live repl is only
	// ever hot-swapped with an already-validated Pattern (live.setPattern).
	// A live.evaluate call would hush before parsing and silence every
	// listener on bad code.
	if strings.Contains(js, "live.evaluate(") {
		t.Error("session.js calls live.evaluate(): unvalidated code would hush the live repl before parsing")
	}
	if strings.Contains(js, "repl.evaluate(") && !strings.Contains(js, "sandbox.evaluate(") {
		t.Error("session.js evaluates on an unscoped repl: validation must go through sandbox.evaluate()")
	}
}
