package runtimes

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.GET("/servers/:id/runtimes", h.List)
	r.GET("/servers/:id/runtimes/:type/:rid", h.Get)
	r.GET("/servers/:id/runtimes/:type/:rid/inspect", h.Inspect)
	r.POST("/servers/:id/runtimes/:type", h.Create)
	r.PUT("/servers/:id/runtimes/:type/:rid", h.Update)
	r.POST("/servers/:id/runtimes/:type/:rid/actions", h.Action)
	r.DELETE("/servers/:id/runtimes/:type/:rid", h.Remove)
	r.POST("/servers/:id/runtimes/:type/:rid/exec", h.Exec)
	r.GET("/servers/:id/runtimes/:type/:rid/logs", h.LogsWS)
	r.GET("/servers/:id/runtimes/:type/:rid/console", h.ConsoleWS)
}

func secondsDuration(s int) time.Duration {
	return time.Duration(s) * time.Second
}

func context5s(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Second)
}
