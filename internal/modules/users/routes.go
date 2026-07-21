package users

import "github.com/gin-gonic/gin"

func RegisterRoutes(r gin.IRouter, h *Handler, requirePerm func(string) gin.HandlerFunc) {
	r.GET("/users", requirePerm("users.manage"), h.List)
	r.POST("/users", requirePerm("users.manage"), h.Create)
	r.GET("/users/:id", requirePerm("users.manage"), h.Get)
	r.PUT("/users/:id", requirePerm("users.manage"), h.Update)
	r.DELETE("/users/:id", requirePerm("users.manage"), h.Delete)
	r.PUT("/users/:id/password", requirePerm("users.manage"), h.SetPassword)

	// Self-service; only authentication required.
	r.PUT("/me/password", h.ChangeOwnPassword)
}
