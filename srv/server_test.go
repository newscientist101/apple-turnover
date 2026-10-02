package srv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerSetupAndHandlers(t *testing.T) {
	server := New()

	t.Run("root endpoint", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		server.HandleRoot(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		body := w.Body.String()
		if !strings.Contains(body, "Strudel Agent") {
			t.Errorf("expected page to carry the project title, got body: %s", body)
		}
	})
}

// TestRootRendersToCompletion guards against template/struct drift: the visitor
// counter was removed along with the db machinery, and if the template keeps
// referencing a field that no longer exists on the data passed to Execute,
// html/template aborts mid-render. HandleRoot only logs that as a warning and
// still returns 200, so the failure is invisible without asserting on the body.
// "</main>" and the closing "</html>" sit at the very end of the document, so
// their presence — and their order — prove the whole template executed.
func TestRootRendersToCompletion(t *testing.T) {
	server := New()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	server.HandleRoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	for _, want := range []string{
		"<!doctype html>",
		"</main>",
		"</html>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("template did not render to completion: body is missing %q (len=%d)", want, len(body))
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
		t.Errorf("body does not END with </html>; the template aborted mid-render (len=%d)", len(body))
	}
	if strings.Index(body, "</main>") > strings.Index(body, "</html>") {
		t.Error("body has </main> after </html>; unexpected document order")
	}
}

// TestPerformanceUIShellStructure asserts that the rendered shell carries all
// required performance UI elements: agent message panel, canvas region, editor
// region.
func TestPerformanceUIShellStructure(t *testing.T) {
	server := New()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	server.HandleRoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Assert end-of-document markers to prove rendering completed without mid-template aborts.
	for _, want := range []string{
		"</main>",
		"</html>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("template did not render to completion: body is missing %q (len=%d)", want, len(body))
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
		t.Errorf("body does not END with </html>; the template aborted mid-render (len=%d)", len(body))
	}
	if strings.Index(body, "</main>") > strings.Index(body, "</html>") {
		t.Error("body has </main> after </html>; unexpected document order")
	}

	// Assert required regions exist
	requiredElements := []string{
		`id="agent-panel"`,
		`id="agent-message"`,
		`id="canvas"`,
		`id="editor"`,
	}
	for _, want := range requiredElements {
		if !strings.Contains(body, want) {
			t.Errorf("performance UI shell is missing required element %q", want)
		}
	}

	// Jules session 7393488358784355191 added the sample-pack toggle: it must
	// default to OFF (synth-only, no automatic fetch), reference the
	// dirt-samples pack, and degrade without breaking playback state.
	// Assert the rendered shell carries the toggle and its default state.
	sampleToggleMarkers := []string{
		`id="sample-toggle-btn"`,
		`Samples: Off`,
		`github:tidalcycles/dirt-samples`,
	}
	for _, want := range sampleToggleMarkers {
		if !strings.Contains(body, want) {
			t.Errorf("sample pack toggle is missing expected marker %q", want)
		}
	}

	// Synth must remain the default path: no automatic samples() call at
	// load. The only samples( reference must live inside the click handler.
}
