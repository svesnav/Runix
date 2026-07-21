package audit

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the audit API under the authenticated group;
// requirePerm is the RBAC middleware factory supplied during wiring.
func RegisterRoutes(r gin.IRouter, h *Handler, requirePerm func(string) gin.HandlerFunc) {
	r.GET("/audit", requirePerm("audit.view"), h.List)
}
