package auth

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/modules/users"
	"github.com/runix/runix/internal/platform/authn"
	"github.com/runix/runix/internal/platform/httpx"
)

// PermissionSource supplies the effective permission/role view for /me;
// rbac provides it at wiring time.
type PermissionSource interface {
	GlobalPermissions(ctx context.Context, userID string) ([]string, error)
	RoleKeysOfUser(ctx context.Context, userID string) ([]string, error)
}

// UserReader is the read-only user lookup for /me.
type UserReader interface {
	GetByID(ctx context.Context, id string) (users.User, error)
}

type Handler struct {
	svc     *Service
	perms   PermissionSource
	userss  UserReader
	auditor *audit.Service
}

func NewHandler(svc *Service, perms PermissionSource, userReader UserReader, auditor *audit.Service) *Handler {
	return &Handler{svc: svc, perms: perms, userss: userReader, auditor: auditor}
}

var errStatus = map[error]int{
	ErrBadCredentials: http.StatusUnauthorized,
	ErrUserDisabled:   http.StatusForbidden,
	ErrSessionInvalid: http.StatusUnauthorized,
	ErrTokenReuse:     http.StatusUnauthorized,
	ErrMFACode:        http.StatusUnauthorized,
	ErrMFAState:       http.StatusConflict,
	ErrRateLimited:    http.StatusTooManyRequests,
	ErrNotFound:       http.StatusNotFound,
	ErrConflict:       http.StatusConflict,
}

func client(c *gin.Context) ClientInfo {
	return ClientInfo{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}

func (h *Handler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	res, err := h.svc.Login(c.Request.Context(), req.Identifier, req.Password, req.Remember, client(c))
	h.auditor.Write(withMeta(c), audit.Record{
		Action: "auth.login", TargetType: "user", TargetID: req.Identifier, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	resp := loginResponse{MFARequired: res.MFARequired, MFAToken: res.MFAToken}
	if !res.MFARequired {
		pub := res.User.Public()
		resp.Tokens = &res.Tokens
		resp.User = &pub
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) VerifyMFA(c *gin.Context) {
	var req mfaVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	res, err := h.svc.VerifyMFA(c.Request.Context(), req.MFAToken, req.Code, req.Remember, client(c))
	h.auditor.Write(withMeta(c), audit.Record{Action: "auth.mfa.verify", Err: err})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	pub := res.User.Public()
	c.JSON(http.StatusOK, loginResponse{Tokens: &res.Tokens, User: &pub})
}

func (h *Handler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	pair, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken, client(c))
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, pair)
}

func (h *Handler) Logout(c *gin.Context) {
	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	if err := h.svc.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Me(c *gin.Context) {
	p, ok := authn.FromContext(c.Request.Context())
	if !ok {
		httpx.Unauthorized(c, "authentication required")
		return
	}
	u, err := h.userss.GetByID(c.Request.Context(), p.UserID)
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	perms, err := h.perms.GlobalPermissions(c.Request.Context(), p.UserID)
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	roles, err := h.perms.RoleKeysOfUser(c.Request.Context(), p.UserID)
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if roles == nil {
		roles = []string{}
	}
	c.JSON(http.StatusOK, meResponse{User: u.Public(), Permissions: perms, Roles: roles})
}

func (h *Handler) SetupMFA(c *gin.Context) {
	p, _ := authn.FromContext(c.Request.Context())
	setup, err := h.svc.SetupMFA(c.Request.Context(), p.UserID)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "auth.mfa.setup", TargetType: "user", TargetID: p.UserID, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, setup)
}

func (h *Handler) EnableMFA(c *gin.Context) {
	p, _ := authn.FromContext(c.Request.Context())
	var req mfaEnableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	codes, err := h.svc.EnableMFA(c.Request.Context(), p.UserID, req.Code)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "auth.mfa.enable", TargetType: "user", TargetID: p.UserID, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
}

func (h *Handler) DisableMFA(c *gin.Context) {
	p, _ := authn.FromContext(c.Request.Context())
	var req mfaDisableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	err := h.svc.DisableMFA(c.Request.Context(), p.UserID, req.Password, req.Code)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "auth.mfa.disable", TargetType: "user", TargetID: p.UserID, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) RegenerateRecoveryCodes(c *gin.Context) {
	p, _ := authn.FromContext(c.Request.Context())
	var req mfaEnableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	codes, err := h.svc.RegenerateRecoveryCodes(c.Request.Context(), p.UserID, req.Code)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "auth.mfa.recovery.regenerate", TargetType: "user", TargetID: p.UserID, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
}

func (h *Handler) ListSessions(c *gin.Context) {
	p, _ := authn.FromContext(c.Request.Context())
	sessions, err := h.svc.Sessions(c.Request.Context(), p.UserID)
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if sessions == nil {
		sessions = []Session{}
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (h *Handler) RevokeSession(c *gin.Context) {
	p, _ := authn.FromContext(c.Request.Context())
	err := h.svc.RevokeSessionByID(c.Request.Context(), p.UserID, c.Param("id"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "auth.session.revoke", TargetType: "session", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) CreatePAT(c *gin.Context) {
	p, _ := authn.FromContext(c.Request.Context())
	var req createPATRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	created, err := h.svc.CreatePAT(c.Request.Context(), p.UserID, req.Name, req.ExpiresAt)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "auth.pat.create", TargetType: "pat", TargetID: req.Name, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (h *Handler) ListPATs(c *gin.Context) {
	p, _ := authn.FromContext(c.Request.Context())
	pats, err := h.svc.PATs(c.Request.Context(), p.UserID)
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if pats == nil {
		pats = []PAT{}
	}
	c.JSON(http.StatusOK, gin.H{"tokens": pats})
}

func (h *Handler) RevokePAT(c *gin.Context) {
	p, _ := authn.FromContext(c.Request.Context())
	err := h.svc.RevokePAT(c.Request.Context(), p.UserID, c.Param("id"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "auth.pat.revoke", TargetType: "pat", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

// withMeta attaches transport facts for unauthenticated endpoints where the
// auth middleware has not run yet.
func withMeta(c *gin.Context) context.Context {
	return authn.WithRequestMeta(c.Request.Context(), authn.RequestMeta{
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
		RequestID: c.GetString("request_id"),
	})
}
