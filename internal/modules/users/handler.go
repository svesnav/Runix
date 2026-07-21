package users

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
	ErrNotFound:      http.StatusNotFound,
	ErrConflict:      http.StatusConflict,
	ErrInvalid:       http.StatusBadRequest,
	ErrWrongPassword: http.StatusForbidden,
	ErrLastAdmin:     http.StatusConflict,
}

func (h *Handler) List(c *gin.Context) {
	page := httpx.Pagination(c)
	items, total, err := h.svc.List(c.Request.Context(), page)
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	public := make([]Public, 0, len(items))
	for _, u := range items {
		public = append(public, u.Public())
	}
	c.JSON(http.StatusOK, httpx.NewListResponse(public, total, page))
}

func (h *Handler) Get(c *gin.Context) {
	u, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, u.Public())
}

func (h *Handler) Create(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	u, err := h.svc.Create(c.Request.Context(), CreateInput{
		Username: req.Username, Email: req.Email, DisplayName: req.DisplayName,
		Password: req.Password, MustChangePassword: req.MustChangePassword,
	})
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "user.create", TargetType: "user", TargetID: req.Username,
		New: gin.H{"username": req.Username, "email": req.Email}, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusCreated, u.Public())
}

func (h *Handler) Update(c *gin.Context) {
	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	u, err := h.svc.Update(c.Request.Context(), c.Param("id"), UpdateInput{
		Email: req.Email, DisplayName: req.DisplayName, IsActive: *req.IsActive,
	})
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "user.update", TargetType: "user", TargetID: c.Param("id"), New: req, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, u.Public())
}

func (h *Handler) Delete(c *gin.Context) {
	err := h.svc.Delete(c.Request.Context(), c.Param("id"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "user.delete", TargetType: "user", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

// ChangeOwnPassword lets any authenticated user rotate their own password.
func (h *Handler) ChangeOwnPassword(c *gin.Context) {
	p, ok := authn.FromContext(c.Request.Context())
	if !ok {
		httpx.Unauthorized(c, "authentication required")
		return
	}
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	err := h.svc.ChangePassword(c.Request.Context(), p.UserID, req.CurrentPassword, req.NewPassword)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "user.password.change", TargetType: "user", TargetID: p.UserID, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

// SetPassword is the administrative reset path.
func (h *Handler) SetPassword(c *gin.Context) {
	var req setPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	err := h.svc.AdminSetPassword(c.Request.Context(), c.Param("id"), req.NewPassword)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "user.password.reset", TargetType: "user", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}
