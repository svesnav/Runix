// Package dockerres exposes Docker's object types — images, volumes and
// networks — which are Docker-specific resources rather than runtimes and
// therefore sit outside the runtime abstraction.
package dockerres

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/agents"
	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/modules/runtimes"
	"github.com/runix/runix/internal/platform/authn"
	"github.com/runix/runix/internal/platform/httpx"
	"github.com/runix/runix/internal/protocol"
)

// Image pulls are proxied synchronously and can be slow.
const pullTimeout = 16 * time.Minute

type Handler struct {
	hub     *agents.Hub
	check   runtimes.PermissionCheck
	auditor *audit.Service
}

func NewHandler(hub *agents.Hub, check runtimes.PermissionCheck, auditor *audit.Service) *Handler {
	return &Handler{hub: hub, check: check, auditor: auditor}
}

// authorize requires docker.manage (or the generic runtime.manage) on the
// target server.
func (h *Handler) authorize(c *gin.Context, readOnly bool) bool {
	p, ok := authn.FromContext(c.Request.Context())
	if !ok {
		httpx.Unauthorized(c, "authentication required")
		return false
	}
	perms := []string{"docker.manage", "runtime.manage"}
	if readOnly {
		perms = append(perms, "server.view")
	}
	for _, perm := range perms {
		allowed, err := h.check(c.Request.Context(), p.UserID, perm, runtimes.ServerTarget(c.Param("id")))
		if err != nil {
			_ = c.Error(err)
			httpx.Internal(c)
			return false
		}
		if allowed {
			return true
		}
	}
	httpx.Forbidden(c)
	return false
}

func validKind(kind string) bool {
	switch kind {
	case protocol.DockerImages, protocol.DockerVolumes, protocol.DockerNetworks:
		return true
	}
	return false
}

func (h *Handler) List(c *gin.Context) {
	if !h.authorize(c, true) {
		return
	}
	kind := c.Param("kind")
	if !validKind(kind) {
		httpx.NotFound(c, "unknown docker resource kind")
		return
	}
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodDockerResourceList,
		protocol.DockerResourceParams{Kind: kind})
	if err != nil {
		runtimes.ProxyError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

type createRequest struct {
	// Images: the reference to pull. Volumes/networks: name plus options.
	Image    string            `json:"image"`
	Name     string            `json:"name"`
	Driver   string            `json:"driver"`
	Internal bool              `json:"internal"`
	Labels   map[string]string `json:"labels"`
}

func (h *Handler) Create(c *gin.Context) {
	if !h.authorize(c, false) {
		return
	}
	kind := c.Param("kind")
	if !validKind(kind) {
		httpx.NotFound(c, "unknown docker resource kind")
		return
	}
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	ctx := c.Request.Context()
	if kind == protocol.DockerImages {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pullTimeout)
		defer cancel()
	}
	_, err := h.hub.Call(ctx, c.Param("id"), protocol.MethodDockerResourceCreate,
		protocol.DockerResourceParams{
			Kind: kind, Image: req.Image, Name: req.Name,
			Driver: req.Driver, Internal: req.Internal, Labels: req.Labels,
		})
	target := req.Name
	if target == "" {
		target = req.Image
	}
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "docker." + kind + ".create", TargetType: "docker_" + kind,
		TargetID: c.Param("id") + ":" + target, New: req, Err: err,
	})
	if err != nil {
		runtimes.ProxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Remove(c *gin.Context) {
	if !h.authorize(c, false) {
		return
	}
	kind := c.Param("kind")
	if !validKind(kind) {
		httpx.NotFound(c, "unknown docker resource kind")
		return
	}
	rid := c.Param("rid")
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodDockerResourceRemove,
		protocol.DockerResourceParams{Kind: kind, ID: rid, Force: c.Query("force") == "true"})
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "docker." + kind + ".remove", TargetType: "docker_" + kind,
		TargetID: c.Param("id") + ":" + rid, Err: err,
	})
	if err != nil {
		runtimes.ProxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Prune(c *gin.Context) {
	if !h.authorize(c, false) {
		return
	}
	kind := c.Param("kind")
	if !validKind(kind) {
		httpx.NotFound(c, "unknown docker resource kind")
		return
	}
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodDockerResourcePrune,
		protocol.DockerResourceParams{Kind: kind})
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "docker." + kind + ".prune", TargetType: "docker_" + kind,
		TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		runtimes.ProxyError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

func (h *Handler) DiskUsage(c *gin.Context) {
	if !h.authorize(c, true) {
		return
	}
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodDockerDiskUsage, nil)
	if err != nil {
		runtimes.ProxyError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.GET("/servers/:id/docker/usage", h.DiskUsage)
	r.GET("/servers/:id/docker/:kind", h.List)
	r.POST("/servers/:id/docker/:kind", h.Create)
	r.POST("/servers/:id/docker/:kind/prune", h.Prune)
	r.DELETE("/servers/:id/docker/:kind/:rid", h.Remove)
}
