package srv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerSetupAndHandlers(t *testing.T) {
	server := New("test-hostname")

	// Test root endpoint without auth
	t.Run("root endpoint unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		server.HandleRoot(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		body := w.Body.String()
		if !strings.Contains(body, "test-hostname") {
			t.Errorf("expected page to show hostname, got body: %s", body)
		}
		if !strings.Contains(body, "Go Template Project") {
			t.Errorf("expected page to contain headline, got body: %s", body)
		}
		if strings.Contains(body, "Signed in as") {
			t.Errorf("expected page to not be logged in, got body: %s", body)
		}
		if !strings.Contains(body, "Not signed in") {
			t.Errorf("expected page to show 'Not signed in', got body: %s", body)
		}
	})

	// Test root endpoint with auth headers
	t.Run("root endpoint authenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-ExeDev-UserID", "user123")
		req.Header.Set("X-ExeDev-Email", "test@example.com")
		w := httptest.NewRecorder()

		server.HandleRoot(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		body := w.Body.String()
		if !strings.Contains(body, "Signed in as") {
			t.Errorf("expected page to show logged in state, got body: %s", body)
		}
		if !strings.Contains(body, "test@example.com") {
			t.Error("expected page to show user email")
		}
	})

}

// TestRootRendersToCompletion guards against template/struct drift: the visitor
// counter was removed along with the db machinery, and if the template keeps
// referencing a field that no longer exists on pageData, html/template aborts
// mid-render. HandleRoot only logs that as a warning and still returns 200, so
// the failure is invisible without asserting on the body. "Copied to clipboard!"
// and the Shelley ribbon sit at the very end of the document, so their presence
// proves the whole template executed.
func TestRootRendersToCompletion(t *testing.T) {
	server := New("render-hostname")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	server.HandleRoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	for _, want := range []string{
		"Copied to clipboard!",
		"Edit with Shelley",
		"</html>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("template did not render to completion: body is missing %q (len=%d)", want, len(body))
		}
	}
	if strings.Contains(body, "VisitCount") {
		t.Error("body still references removed VisitCount field")
	}
}

func TestUtilityFunctions(t *testing.T) {
	t.Run("mainDomainFromHost function", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"example.exe.cloud:8080", "exe.cloud:8080"},
			{"example.exe.dev", "exe.dev"},
			{"example.exe.cloud", "exe.cloud"},
		}

		for _, test := range tests {
			result := mainDomainFromHost(test.input)
			if result != test.expected {
				t.Errorf("mainDomainFromHost(%q) = %q, expected %q", test.input, result, test.expected)
			}
		}
	})

	t.Run("loginURLForRequest redirects to bare path", func(t *testing.T) {
		tests := []struct {
			name     string
			target   string
			expected string
		}{
			{
				name:     "plain path",
				target:   "/dashboard",
				expected: "/__exe.dev/login?redirect=%2Fdashboard",
			},
			{
				// The query is dropped entirely, so the login link points at the
				// bare path regardless of what params were present.
				name:     "query is dropped",
				target:   "/search?q=cats",
				expected: "/__exe.dev/login?redirect=%2Fsearch",
			},
			{
				// Dropping the query also drops any existing redirect param, so
				// following the login link repeatedly can never nest/grow the URL.
				name:     "existing redirect cannot nest",
				target:   "/page?redirect=%2Flogin%3Fredirect%3D%252Flogin",
				expected: "/__exe.dev/login?redirect=%2Fpage",
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, test.target, nil)
				if got := loginURLForRequest(req); got != test.expected {
					t.Errorf("loginURLForRequest(%q) = %q, want %q", test.target, got, test.expected)
				}
			})
		}
	})
}
