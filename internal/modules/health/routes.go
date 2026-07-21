package health

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the health endpoints at the root of the router;
// they intentionally live outside /api/v1 and outside authentication.
func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.GET("/healthz", h.Liveness)
	r.GET("/readyz", h.Readiness)
}
