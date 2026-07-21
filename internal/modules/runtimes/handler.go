// Package runtimes is the control-plane API over the runtime abstraction:
// every endpoint proxies to the target server's agent through the hub and
// translates protocol errors into HTTP semantics.
package runtimes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	rtdomain "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/modules/agents"
	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/platform/authn"
	"github.com/runix/runix/internal/platform/httpx"
	"github.com/runix/runix/internal/protocol"
)

// Target is what a permission is being checked against. RuntimeType and
// RuntimeID are set when the request addresses one runtime, which is what
// makes runtime-scoped grants meaningful.
type Target struct {
	ServerID    string
	RuntimeType string
	RuntimeID   string
}

func ServerTarget(serverID string) Target { return Target{ServerID: serverID} }

// PermissionCheck answers a scoped permission question; the app wires it to
// the rbac service.
type PermissionCheck func(ctx context.Context, userID, perm string, target Target) (bool, error)

type Handler struct {
	hub     *agents.Hub
	check   PermissionCheck
	auditor *audit.Service
}

func NewHandler(hub *agents.Hub, check PermissionCheck, auditor *audit.Service) *Handler {
	return &Handler{hub: hub, check: check, auditor: auditor}
}

// managePermFor maps a runtime type to its technology permission; holding
// either the generic runtime.manage or the specific one authorizes.
func managePermFor(rtType string) string {
	switch rtType {
	case "docker":
		return "docker.manage"
	case "compose":
		return "compose.manage"
	case "systemd":
		return "systemd.manage"
	case "daemon":
		return "daemon.manage"
	}
	return "runtime.manage"
}

// authorize checks the request against the addressed runtime when the route
// names one, so a grant scoped to a single runtime is honored.
func (h *Handler) authorize(c *gin.Context, perms ...string) bool {
	target := Target{
		ServerID:    c.Param("id"),
		RuntimeType: c.Param("type"),
		RuntimeID:   c.Param("rid"),
	}
	return h.authorizeTarget(c, target, perms...)
}

func (h *Handler) authorizeTarget(c *gin.Context, target Target, perms ...string) bool {
	p, ok := authn.FromContext(c.Request.Context())
	if !ok {
		httpx.Unauthorized(c, "authentication required")
		return false
	}
	for _, perm := range perms {
		allowed, err := h.check(c.Request.Context(), p.UserID, perm, target)
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

func (h *Handler) proxyError(c *gin.Context, err error) {
	ProxyError(c, err)
}

// ProxyError maps agent/protocol failures onto HTTP semantics; other
// agent-proxying modules (files, terminal, metrics) reuse it.
func ProxyError(c *gin.Context, err error) {
	var perr *protocol.Error
	if errors.As(err, &perr) {
		switch perr.Code {
		case protocol.CodeNotFound:
			httpx.Error(c, http.StatusNotFound, perr.Code, perr.Message)
		case protocol.CodeNotSupported:
			httpx.Error(c, http.StatusUnprocessableEntity, perr.Code, perr.Message)
		case protocol.CodeInvalid:
			httpx.Error(c, http.StatusBadRequest, perr.Code, perr.Message)
		case protocol.CodeUnavailable:
			httpx.Error(c, http.StatusServiceUnavailable, perr.Code, perr.Message)
		case protocol.CodeTimeout:
			httpx.Error(c, http.StatusGatewayTimeout, perr.Code, perr.Message)
		default:
			httpx.Error(c, http.StatusBadGateway, perr.Code, perr.Message)
		}
		return
	}
	if errors.Is(err, agents.ErrAgentOffline) {
		httpx.Error(c, http.StatusServiceUnavailable, "agent_offline", "the server's agent is not connected")
		return
	}
	if errors.Is(err, agents.ErrCallTimeout) {
		httpx.Error(c, http.StatusGatewayTimeout, "timeout", "the agent did not answer in time")
		return
	}
	_ = c.Error(err)
	httpx.Internal(c)
}

func (h *Handler) List(c *gin.Context) {
	if !h.authorize(c, "server.view") {
		return
	}
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeList,
		protocol.RuntimeListParams{Type: c.Query("type")})
	if err != nil {
		h.proxyError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

func (h *Handler) Get(c *gin.Context) {
	if !h.authorize(c, "server.view") {
		return
	}
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeGet,
		protocol.RuntimeGetParams{Type: c.Param("type"), ID: c.Param("rid")})
	if err != nil {
		h.proxyError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

func (h *Handler) Inspect(c *gin.Context) {
	if !h.authorize(c, "server.view") {
		return
	}
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeInspect,
		protocol.RuntimeInspectParams{Type: c.Param("type"), ID: c.Param("rid")})
	if err != nil {
		h.proxyError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

type actionRequest struct {
	Action         string `json:"action" binding:"required"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	Signal         string `json:"signal"`
}

func (h *Handler) Action(c *gin.Context) {
	rtType := c.Param("type")
	if !h.authorize(c, "runtime.manage", managePermFor(rtType)) {
		return
	}
	var req actionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	params := protocol.RuntimeActionParams{
		Type: rtType, ID: c.Param("rid"), Action: req.Action, Signal: req.Signal,
	}
	if req.TimeoutSeconds > 0 {
		params.Stop.Timeout = secondsDuration(req.TimeoutSeconds)
	}
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeAction, params)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "runtime." + req.Action, TargetType: "runtime",
		TargetID: c.Param("id") + "/" + rtType + "/" + c.Param("rid"), Err: err,
	})
	if err != nil {
		h.proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type createRequest struct {
	Name   string            `json:"name" binding:"required,max=128"`
	Labels map[string]string `json:"labels"`
	Config json.RawMessage   `json:"config"`
}

func (h *Handler) Create(c *gin.Context) {
	rtType := c.Param("type")
	if !h.authorize(c, "runtime.manage", managePermFor(rtType)) {
		return
	}
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	params := protocol.RuntimeCreateParams{}
	params.Spec.Name = req.Name
	params.Spec.Type = rtdomain.Type(rtType)
	params.Spec.Labels = req.Labels
	params.Spec.Config = req.Config
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeCreate, params)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "runtime.create", TargetType: "runtime",
		TargetID: c.Param("id") + "/" + rtType + "/" + req.Name, New: req, Err: err,
	})
	if err != nil {
		h.proxyError(c, err)
		return
	}
	c.Data(http.StatusCreated, "application/json", raw)
}

type updateRequest struct {
	Labels map[string]string `json:"labels"`
	Config json.RawMessage   `json:"config"`
}

func (h *Handler) Update(c *gin.Context) {
	rtType := c.Param("type")
	if !h.authorize(c, "runtime.manage", managePermFor(rtType)) {
		return
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	params := protocol.RuntimeUpdateParams{Type: rtType, ID: c.Param("rid")}
	params.Spec.Name = c.Param("rid")
	params.Spec.Type = rtdomain.Type(rtType)
	params.Spec.Labels = req.Labels
	params.Spec.Config = req.Config
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeUpdate, params)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "runtime.update", TargetType: "runtime",
		TargetID: c.Param("id") + "/" + rtType + "/" + c.Param("rid"), New: req, Err: err,
	})
	if err != nil {
		h.proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Remove(c *gin.Context) {
	rtType := c.Param("type")
	if !h.authorize(c, "runtime.manage", managePermFor(rtType)) {
		return
	}
	params := protocol.RuntimeRemoveParams{
		Type: rtType, ID: c.Param("rid"),
		Force: c.Query("force") == "true",
		Purge: c.Query("purge") == "true",
	}
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeRemove, params)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "runtime.remove", TargetType: "runtime",
		TargetID: c.Param("id") + "/" + rtType + "/" + c.Param("rid"), Err: err,
	})
	if err != nil {
		h.proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type execRequest struct {
	Cmd            []string `json:"cmd" binding:"required,min=1"`
	Env            []string `json:"env"`
	WorkingDir     string   `json:"workingDir"`
	User           string   `json:"user"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

func (h *Handler) Exec(c *gin.Context) {
	if !h.authorize(c, "runtime.execute") {
		return
	}
	var req execRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeExec,
		protocol.RuntimeExecParams{
			Type: c.Param("type"), ID: c.Param("rid"), Cmd: req.Cmd, Env: req.Env,
			WorkingDir: req.WorkingDir, User: req.User, TimeoutSeconds: req.TimeoutSeconds,
		})
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "runtime.exec", TargetType: "runtime",
		TargetID: c.Param("id") + "/" + c.Param("type") + "/" + c.Param("rid"),
		New:      gin.H{"cmd": req.Cmd}, Err: err,
	})
	if err != nil {
		h.proxyError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}
