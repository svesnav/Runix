package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/runix/runix/internal/platform/config"
	"github.com/runix/runix/internal/webui"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Server{
		Env:             config.EnvTest,
		HTTPAddr:        "127.0.0.1:0",
		ShutdownTimeout: time.Second,
		Log:             config.Log{Level: "info", Format: "text"},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, log)
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestHealthEndpoint(t *testing.T) {
	w := get(t, newTestServer(t), "/healthz")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", w.Code)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID header")
	}
}

func TestVersionEndpoint(t *testing.T) {
	w := get(t, newTestServer(t), "/api/v1/version")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/version = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["version"] == "" {
		t.Errorf("version missing from body: %v", body)
	}
}

// An unknown API path must stay JSON: clients parse these, and handing
// them a page of HTML turns a typo into a confusing decode error.
func TestUnknownAPIRouteReturnsJSON404(t *testing.T) {
	w := get(t, newTestServer(t), "/api/v1/nope")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/nope = %d, want 404", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("404 body is not JSON: %v", err)
	}
}

// The regression this guards: the control plane used to answer 404 at /,
// so operators who opened the URL after installing saw nothing at all.
func TestRootServesTheWebUI(t *testing.T) {
	w := get(t, newTestServer(t), "/")
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), "<html") {
		t.Errorf("body is not HTML: %.120s", w.Body.String())
	}
}

// A non-API path that does not exist is a UI URL, so it gets the UI's own
// 404 page rather than a JSON error.
func TestUnknownUIRouteServesHTML(t *testing.T) {
	w := get(t, newTestServer(t), "/nope")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /nope = %d, want 404", w.Code)
	}
	// The exported 404 page only exists in a build that embedded the UI;
	// a bare checkout compiles against the placeholder, and CI runs the
	// tests without building the frontend first.
	if !webui.Built() {
		t.Skip("no UI embedded in this build")
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}
