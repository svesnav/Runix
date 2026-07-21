package settings

import "github.com/gin-gonic/gin"

func RegisterRoutes(r gin.IRouter, h *Handler, requirePerm func(string) gin.HandlerFunc) {
	r.GET("/settings", requirePerm("settings.view"), h.List)
	r.PUT("/settings/:key", requirePerm("settings.edit"), h.Set)
}
