package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runix/runix/internal/platform/config"
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

func TestUnknownRouteReturnsJSON404(t *testing.T) {
	w := get(t, newTestServer(t), "/nope")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /nope = %d, want 404", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("404 body is not JSON: %v", err)
	}
}
