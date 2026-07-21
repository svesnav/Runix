package auth

import "github.com/gin-gonic/gin"

// RegisterPublicRoutes mounts the endpoints reachable without a session.
func RegisterPublicRoutes(r gin.IRouter, h *Handler) {
	r.POST("/auth/login", h.Login)
	r.POST("/auth/mfa/verify", h.VerifyMFA)
	r.POST("/auth/refresh", h.Refresh)
	r.POST("/auth/logout", h.Logout)
}

// RegisterProtectedRoutes mounts endpoints behind the auth middleware.
func RegisterProtectedRoutes(r gin.IRouter, h *Handler) {
	r.GET("/me", h.Me)
	r.POST("/me/mfa/setup", h.SetupMFA)
	r.POST("/me/mfa/enable", h.EnableMFA)
	r.POST("/me/mfa/disable", h.DisableMFA)
	r.POST("/me/mfa/recovery-codes", h.RegenerateRecoveryCodes)
	r.GET("/me/sessions", h.ListSessions)
	r.DELETE("/me/sessions/:id", h.RevokeSession)
	r.GET("/me/tokens", h.ListPATs)
	r.POST("/me/tokens", h.CreatePAT)
	r.DELETE("/me/tokens/:id", h.RevokePAT)
}
