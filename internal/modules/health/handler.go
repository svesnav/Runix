package health

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/platform/version"
)

const readinessTimeout = 5 * time.Second

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Liveness reports that the process is up. It must stay dependency-free so
// orchestrators never restart a healthy process because a dependency blinked.
func (h *Handler) Liveness(c *gin.Context) {
	c.JSON(http.StatusOK, LivenessResponse{
		Status:        statusOK,
		Version:       version.Get(),
		UptimeSeconds: h.svc.Uptime().Seconds(),
	})
}

// Readiness runs every registered dependency check and reports 503 until all
// of them pass.
func (h *Handler) Readiness(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
	defer cancel()

	resp := ReadinessResponse{
		Status: statusOK,
		Checks: make(map[string]CheckResult),
	}
	code := http.StatusOK
	for name, err := range h.svc.Run(ctx) {
		result := CheckResult{Status: statusOK}
		if err != nil {
			result = CheckResult{Status: statusFail, Error: err.Error()}
			resp.Status = statusFail
			code = http.StatusServiceUnavailable
		}
		resp.Checks[name] = result
	}
	c.JSON(code, resp)
}
