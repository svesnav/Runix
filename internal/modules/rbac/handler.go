package rbac

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/platform/authn"
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

// ListPermissions returns the catalog with human-readable names; the dotted
// key remains the identifier clients send back.
func (h *Handler) ListPermissions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"permissions": PermissionCatalog()})
}

func (h *Handler) ListRoles(c *gin.Context) {
	roles, err := h.svc.ListRoles(c.Request.Context())
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

func (h *Handler) CreateRole(c *gin.Context) {
	var req createRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	role, err := h.svc.CreateRole(c.Request.Context(), req.Key, req.Name, req.Description, req.Permissions)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "role.create", TargetType: "role", TargetID: req.Key, New: req, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusCreated, role)
}

func (h *Handler) UpdateRole(c *gin.Context) {
	var req updateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	role, err := h.svc.UpdateRole(c.Request.Context(), Role{
		ID: c.Param("id"), Name: req.Name, Description: req.Description, Permissions: req.Permissions,
	})
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "role.update", TargetType: "role", TargetID: c.Param("id"), New: req, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, role)
}

func (h *Handler) DeleteRole(c *gin.Context) {
	err := h.svc.DeleteRole(c.Request.Context(), c.Param("id"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "role.delete", TargetType: "role", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) SetUserRoles(c *gin.Context) {
	var req setUserRolesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	err := h.svc.SetUserRoles(c.Request.Context(), c.Param("id"), req.RoleIDs)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "user.roles.set", TargetType: "user", TargetID: c.Param("id"), New: req, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) GetUserRoles(c *gin.Context) {
	keys, err := h.svc.RoleKeysOfUser(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if keys == nil {
		keys = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"roles": keys})
}

func (h *Handler) ListGroups(c *gin.Context) {
	groups, err := h.svc.ListGroups(c.Request.Context())
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

func (h *Handler) CreateGroup(c *gin.Context) {
	var req createGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	group, err := h.svc.CreateGroup(c.Request.Context(), req.Name, req.Description)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "group.create", TargetType: "group", TargetID: req.Name, New: req, Err: err,
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
		Action: "group.delete", TargetType: "group", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) AddGroupMember(c *gin.Context) {
	var req groupMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	err := h.svc.AddGroupMember(c.Request.Context(), c.Param("id"), req.UserID)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "group.member.add", TargetType: "group", TargetID: c.Param("id"), New: req, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) RemoveGroupMember(c *gin.Context) {
	err := h.svc.RemoveGroupMember(c.Request.Context(), c.Param("id"), c.Param("userId"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "group.member.remove", TargetType: "group", TargetID: c.Param("id"),
		New: gin.H{"userId": c.Param("userId")}, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ListGroupMembers(c *gin.Context) {
	ids, err := h.svc.GroupMemberIDs(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if ids == nil {
		ids = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"userIds": ids})
}

func (h *Handler) ListGrants(c *gin.Context) {
	grants, err := h.svc.ListGrants(c.Request.Context())
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if grants == nil {
		grants = []Grant{}
	}
	c.JSON(http.StatusOK, gin.H{"grants": grants})
}

func (h *Handler) CreateGrant(c *gin.Context) {
	var req createGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	grant := Grant{
		SubjectType: req.SubjectType, SubjectID: req.SubjectID,
		Permission: req.Permission, ScopeType: req.ScopeType, ScopeID: req.ScopeID,
	}
	if p, ok := authn.FromContext(c.Request.Context()); ok {
		grant.CreatedBy = p.UserID
	}
	created, err := h.svc.CreateGrant(c.Request.Context(), grant)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "grant.create", TargetType: "grant", New: req, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (h *Handler) DeleteGrant(c *gin.Context) {
	err := h.svc.DeleteGrant(c.Request.Context(), c.Param("id"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "grant.delete", TargetType: "grant", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}
