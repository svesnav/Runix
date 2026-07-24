// Package plugins exposes the external runtime providers an agent loaded,
// and drives agent self-update.
package plugins

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/runix/runix/internal/modules/rbac"

	"github.com/runix/runix/internal/modules/agents"
	"github.com/runix/runix/internal/modules/runtimes"
	"github.com/runix/runix/internal/platform/authn"
	"github.com/runix/runix/internal/platform/httpx"
	"github.com/runix/runix/internal/protocol"
)

type Handler struct {
	hub   *agents.Hub
	check runtimes.PermissionCheck
}

func NewHandler(hub *agents.Hub, check runtimes.PermissionCheck) *Handler {
	return &Handler{hub: hub, check: check}
}

func (h *Handler) authorize(c *gin.Context, perm string) bool {
	p, ok := authn.FromContext(c.Request.Context())
	if !ok {
		httpx.Unauthorized(c, "authentication required")
		return false
	}

	// Convert Target to rbac.Scope for proper permission checking
	scope := rbac.ServerScope(c.Param("id"))
	allowed, err := h.check(c.Request.Context(), p.UserID, perm, scope)
	if err != nil {
		_ = c.Error(err)
		httpx.Internal(c)
		return false
	}
	if !allowed {
		httpx.Forbidden(c)
		return false
	}
	return true
}

func (h *Handler) List(c *gin.Context) {
	if !h.authorize(c, "plugins.manage") {
		return
	}
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodAgentPlugins, nil)
	if err != nil {
		runtimes.ProxyError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

type updateRequest struct {
	URL     string `json:"url" binding:"required"`
	SHA256  string `json:"sha256" binding:"required,len=64"`
	Version string `json:"version"`
	Restart *bool  `json:"restart"`
}

// UpdateAgent asks the agent to replace its own binary. The checksum is
// required by the agent as well; it is the only thing standing between this
// endpoint and arbitrary code execution on a managed host.
func (h *Handler) UpdateAgent(c *gin.Context) {
	if !h.authorize(c, "server.update") {
		return
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	restart := true
	if req.Restart != nil {
		restart = *req.Restart
	}
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodAgentUpdate,
		protocol.AgentUpdateParams{
			URL: req.URL, SHA256: req.SHA256, Version: req.Version, Restart: restart,
		})
	if err != nil {
		runtimes.ProxyError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

func RegisterRoutes(r gin.IRouter, h *Handler, requirePerm func(string) gin.HandlerFunc) {
	r.GET("/servers/:id/plugins", h.List)
	r.POST("/servers/:id/agent/update", h.UpdateAgent)
}
