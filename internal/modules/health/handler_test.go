package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newRouter(t *testing.T, svc *Service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r, NewHandler(svc))
	return r
}

func TestLiveness(t *testing.T) {
	r := newRouter(t, NewService())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp LivenessResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != statusOK || resp.Version.GoVersion == "" {
		t.Errorf("unexpected body: %+v", resp)
	}
}

func TestReadinessAllPassing(t *testing.T) {
	svc := NewService()
	if err := svc.Register("database", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := newRouter(t, svc)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestReadinessFailingCheck(t *testing.T) {
	svc := NewService()
	if err := svc.Register("database", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := svc.Register("redis", func(context.Context) error {
		return errors.New("connection refused")
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := newRouter(t, svc)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var resp ReadinessResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != statusFail {
		t.Errorf("overall status = %q, want fail", resp.Status)
	}
	if resp.Checks["redis"].Status != statusFail || resp.Checks["redis"].Error == "" {
		t.Errorf("redis check = %+v, want failure with message", resp.Checks["redis"])
	}
	if resp.Checks["database"].Status != statusOK {
		t.Errorf("database check = %+v, want ok", resp.Checks["database"])
	}
}

func TestRegisterValidation(t *testing.T) {
	svc := NewService()
	if err := svc.Register("", func(context.Context) error { return nil }); err == nil {
		t.Error("empty name accepted")
	}
	if err := svc.Register("db", nil); err == nil {
		t.Error("nil check accepted")
	}
	if err := svc.Register("db", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := svc.Register("db", func(context.Context) error { return nil }); err == nil {
		t.Error("duplicate name accepted")
	}
}
