package srv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLiveCodeEditorView is the TDD anchor for issue .3vo.6 (CodeMirror live
// code view). The view contract lives in the browser: the agent is the sole
// writer, so the editor is read-only by default; new versions replace the
// document in place with a flash cue, and failed versions highlight the
// failing region without touching the playing document. The Go side of that
// contract is small — serve the shell referencing the pinned CodeMirror CDN
// bundle plus the editor adapter, and serve the adapter itself with the
// load-bearing markers — but it must be pinned so a later shell edit cannot
// silently drop the view.
func TestLiveCodeEditorView(t *testing.T) {
	server := New()

	// 1. The shell must load the pinned CodeMirror bundle and the adapter
	// after initStrudel exists, and the textarea must be read-only.
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
		`codemirror@5.65.16`,
		`codemirror@5.65.16/lib/codemirror.css`,
		`codemirror@5.65.16/lib/codemirror.js`,
		`codemirror@5.65.16/mode/javascript/javascript.js`,
		`<script src="/static/editor.js" defer></script>`,
		`id="editor-status"`,
		// The view must be read-only: assert the exact attribute pair, not
		// just the word "readonly" (which `aria-readonly` would satisfy even
		// after the real read-only attribute was dropped).
		`spellcheck="false" readonly aria-readonly="true"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shell is missing %q: live code view is not wired", want)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
		t.Error("shell did not render to completion: missing </html> tail")
	}

	// 2. The adapter itself must be served intact.
	req = httptest.NewRequest(http.MethodGet, "/static/editor.js", nil)
	w = httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/editor.js status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET /static/editor.js served as JSON (%q): the API layer swallowed a static asset", ct)
	}
	js := w.Body.String()
	if len(js) == 0 {
		t.Fatal("GET /static/editor.js returned an empty body")
	}

	// 3. Load-bearing markers: the exact surface session.js drives. Assert the
	// exact call statements, not just the method names, so dropping a call
	// (for instance the landed-version flash) is a failure rather than a
	// substring that still matches a `typeof` guard.
	for _, want := range []string{
		"window.CodeMirror.fromTextArea(el, {",
		"readOnly: readOnly,",
		"cm.setOption('readOnly', readOnly);",
		"cm.addLineClass(line, 'background', 'editor-error-line');",
		"cm.removeLineClass(errorLine, 'background', 'editor-error-line');",
		"root.classList.add('editor-flash');",
		"window.strudelEditor = api;",
		"5.65.16",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("editor.js is missing load-bearing marker %q", want)
		}
	}

	// 4. session.js must drive versions through the adapter, with the exact
	// call statements the view contract names.
	req = httptest.NewRequest(http.MethodGet, "/static/session.js", nil)
	w = httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/session.js status = %d, want 200", w.Code)
	}
	session := w.Body.String()
	for _, want := range []string{
		"window.strudelEditor",
		"ed.setCode(snapshot.code);",
		"ed.flashUpdate();",
		"ed.markError(message);",
		"ed.clearError();",
	} {
		if !strings.Contains(session, want) {
			t.Errorf("session.js is missing live-code-view marker %q", want)
		}
	}

	// 5. The safety invariant still holds: the LIVE repl is never evaluated.
	if strings.Contains(session, "live.evaluate(") {
		t.Error("session.js calls live.evaluate(): unvalidated code would hush the live repl before parsing")
	}

	// 6. The stylesheet must carry the flash + error cues.
	req = httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	w = httptest.NewRecorder()
	server.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css status = %d, want 200", w.Code)
	}
	css := w.Body.String()
	for _, want := range []string{
		"editor-flash",
		"editor-error-line",
		"editor-status",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css is missing live-code-view marker %q", want)
		}
	}
}
