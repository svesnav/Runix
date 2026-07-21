package servers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/platform/httpx"
)

type Handler struct {
	svc     *Service
	auditor *audit.Service
}

func NewHandler(svc *Service, auditor *audit.Service) *Handler {
	return &Handler{svc: svc, auditor: auditor}
}

var errStatus = map[error]int{
	ErrNotFound: http.StatusNotFound,
	ErrConflict: http.StatusConflict,
	ErrInvalid:  http.StatusBadRequest,
}

func (h *Handler) List(c *gin.Context) {
	page := httpx.Pagination(c)
	items, total, err := h.svc.List(c.Request.Context(), page)
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListResponse(items, total, page))
}

func (h *Handler) Get(c *gin.Context) {
	srv, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, srv)
}

func (h *Handler) Create(c *gin.Context) {
	var req upsertServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	created, err := h.svc.Create(c.Request.Context(), CreateInput{
		Name: req.Name, Description: req.Description, Address: req.Address, Location: req.Location,
		Tags: req.Tags, Labels: req.Labels, AgentToken: req.AgentToken,
	})
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "server.create", TargetType: "server", TargetID: req.Name,
		// The supplied token is a secret: record only whether one was given.
		New: gin.H{"name": req.Name, "address": req.Address, "suppliedToken": req.AgentToken != ""},
		Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (h *Handler) Update(c *gin.Context) {
	var req upsertServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	old, _ := h.svc.Get(c.Request.Context(), c.Param("id"))
	updated, err := h.svc.Update(c.Request.Context(), c.Param("id"), UpdateInput{
		Name: req.Name, Description: req.Description, Address: req.Address, Location: req.Location,
		Tags: req.Tags, Labels: req.Labels,
	})
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "server.update", TargetType: "server", TargetID: c.Param("id"),
		Old: old, New: updated, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *Handler) Delete(c *gin.Context) {
	err := h.svc.Delete(c.Request.Context(), c.Param("id"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "server.delete", TargetType: "server", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) RotateToken(c *gin.Context) {
	// Body is optional: an empty request generates a token.
	var req rotateTokenRequest
	_ = c.ShouldBindJSON(&req)
	token, err := h.svc.RotateToken(c.Request.Context(), c.Param("id"), req.AgentToken)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "server.token.rotate", TargetType: "server", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agentToken": token})
}

func (h *Handler) ListGroups(c *gin.Context) {
	groups, err := h.svc.ListGroups(c.Request.Context())
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if groups == nil {
		groups = []Group{}
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

func (h *Handler) CreateGroup(c *gin.Context) {
	var req upsertGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	group, err := h.svc.CreateGroup(c.Request.Context(), req.Name, req.Description)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "server_group.create", TargetType: "server_group", TargetID: req.Name, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusCreated, group)
}

func (h *Handler) DeleteGroup(c *gin.Context) {
	err := h.svc.DeleteGroup(c.Request.Context(), c.Param("id"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "server_group.delete", TargetType: "server_group", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ListGroupMembers(c *gin.Context) {
	ids, err := h.svc.ServerIDsOfGroup(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if ids == nil {
		ids = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"serverIds": ids})
}

func (h *Handler) AddGroupMember(c *gin.Context) {
	var req groupMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	err := h.svc.AddGroupMember(c.Request.Context(), c.Param("id"), req.ServerID)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "server_group.member.add", TargetType: "server_group", TargetID: c.Param("id"),
		New: req, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) RemoveGroupMember(c *gin.Context) {
	err := h.svc.RemoveGroupMember(c.Request.Context(), c.Param("id"), c.Param("serverId"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "server_group.member.remove", TargetType: "server_group", TargetID: c.Param("id"),
		New: gin.H{"serverId": c.Param("serverId")}, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}
