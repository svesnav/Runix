// Package files is the control-plane API of the remote file manager; all
// operations execute on the target server through its agent.
package files

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/runix/runix/internal/modules/rbac"

	"github.com/runix/runix/internal/modules/agents"
	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/modules/runtimes"
	"github.com/runix/runix/internal/platform/authn"
	"github.com/runix/runix/internal/platform/httpx"
	"github.com/runix/runix/internal/protocol"
)

type Handler struct {
	hub     *agents.Hub
	check   runtimes.PermissionCheck
	auditor *audit.Service
}

func NewHandler(hub *agents.Hub, check runtimes.PermissionCheck, auditor *audit.Service) *Handler {
	return &Handler{hub: hub, check: check, auditor: auditor}
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

func (h *Handler) call(c *gin.Context, method string, params any) ([]byte, bool) {
	raw, err := h.hub.Call(c.Request.Context(), c.Param("id"), method, params)
	if err != nil {
		proxyError(c, err)
		return nil, false
	}
	return raw, true
}

func (h *Handler) List(c *gin.Context) {
	if !h.authorize(c, "server.files.read") {
		return
	}
	raw, ok := h.call(c, protocol.MethodFSList, protocol.FSListParams{
		Path:       c.Query("path"),
		ShowHidden: c.Query("hidden") == "true",
	})
	if ok {
		c.Data(http.StatusOK, "application/json", raw)
	}
}

func (h *Handler) Stat(c *gin.Context) {
	if !h.authorize(c, "server.files.read") {
		return
	}
	raw, ok := h.call(c, protocol.MethodFSStat, protocol.FSStatParams{Path: c.Query("path")})
	if ok {
		c.Data(http.StatusOK, "application/json", raw)
	}
}

func (h *Handler) Read(c *gin.Context) {
	if !h.authorize(c, "server.files.read") {
		return
	}
	maxBytes, _ := strconv.ParseInt(c.Query("maxBytes"), 10, 64)
	raw, ok := h.call(c, protocol.MethodFSRead, protocol.FSReadParams{
		Path: c.Query("path"), MaxBytes: maxBytes,
	})
	if ok {
		c.Data(http.StatusOK, "application/json", raw)
	}
}

type writeRequest struct {
	Path    string `json:"path" binding:"required"`
	Content []byte `json:"content"`
	Mode    uint32 `json:"mode"`
	Append  bool   `json:"append"`
}

func (h *Handler) Write(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	var req writeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodFSWrite,
		protocol.FSWriteParams(req))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "files.write", TargetType: "file", TargetID: c.Param("id") + ":" + req.Path,
		New: gin.H{"bytes": len(req.Content), "append": req.Append}, Err: err,
	})
	if err != nil {
		proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type mkdirRequest struct {
	Path string `json:"path" binding:"required"`
}

func (h *Handler) Mkdir(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	var req mkdirRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodFSMkdir,
		protocol.FSMkdirParams(req))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "files.mkdir", TargetType: "file", TargetID: c.Param("id") + ":" + req.Path, Err: err,
	})
	if err != nil {
		proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type renameRequest struct {
	From string `json:"from" binding:"required"`
	To   string `json:"to" binding:"required"`
}

func (h *Handler) Rename(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	var req renameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodFSRename,
		protocol.FSRenameParams(req))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "files.rename", TargetType: "file",
		TargetID: c.Param("id") + ":" + req.From, New: gin.H{"to": req.To}, Err: err,
	})
	if err != nil {
		proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Delete(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	path := c.Query("path")
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodFSDelete,
		protocol.FSDeleteParams{Path: path, Recursive: c.Query("recursive") == "true"})
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "files.delete", TargetType: "file", TargetID: c.Param("id") + ":" + path, Err: err,
	})
	if err != nil {
		proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type createRequest struct {
	Path string `json:"path" binding:"required"`
}

func (h *Handler) Create(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodFSCreate,
		protocol.FSCreateParams(req))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "files.create", TargetType: "file", TargetID: c.Param("id") + ":" + req.Path, Err: err,
	})
	if err != nil {
		proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type copyRequest struct {
	From string `json:"from" binding:"required"`
	To   string `json:"to" binding:"required"`
}

func (h *Handler) Copy(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	var req copyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodFSCopy,
		protocol.FSCopyParams(req))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "files.copy", TargetType: "file", TargetID: c.Param("id") + ":" + req.From,
		New: gin.H{"to": req.To}, Err: err,
	})
	if err != nil {
		proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type chmodRequest struct {
	Path      string `json:"path" binding:"required"`
	Mode      string `json:"mode" binding:"required"`
	Recursive bool   `json:"recursive"`
}

func (h *Handler) Chmod(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	var req chmodRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	_, err := h.hub.Call(c.Request.Context(), c.Param("id"), protocol.MethodFSChmod,
		protocol.FSChmodParams(req))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "files.chmod", TargetType: "file", TargetID: c.Param("id") + ":" + req.Path,
		New: gin.H{"mode": req.Mode, "recursive": req.Recursive}, Err: err,
	})
	if err != nil {
		proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type archiveRequest struct {
	Paths  []string `json:"paths" binding:"required,min=1"`
	Target string   `json:"target" binding:"required"`
	Format string   `json:"format"`
}

func (h *Handler) Archive(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	var req archiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	// Archiving is CPU/IO bound on large trees; give it room beyond the
	// default RPC timeout.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	_, err := h.hub.Call(ctx, c.Param("id"), protocol.MethodFSArchive, protocol.FSArchiveParams(req))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "files.archive", TargetType: "file", TargetID: c.Param("id") + ":" + req.Target,
		New: gin.H{"paths": req.Paths, "format": req.Format}, Err: err,
	})
	if err != nil {
		proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type extractRequest struct {
	Path string `json:"path" binding:"required"`
	Dest string `json:"dest"`
}

func (h *Handler) Extract(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	var req extractRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	_, err := h.hub.Call(ctx, c.Param("id"), protocol.MethodFSExtract, protocol.FSExtractParams(req))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "files.extract", TargetType: "file", TargetID: c.Param("id") + ":" + req.Path,
		New: gin.H{"dest": req.Dest}, Err: err,
	})
	if err != nil {
		proxyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.GET("/servers/:id/files", h.List)
	r.GET("/servers/:id/files/stat", h.Stat)
	r.GET("/servers/:id/files/content", h.Read)
	r.PUT("/servers/:id/files/content", h.Write)
	r.POST("/servers/:id/files/mkdir", h.Mkdir)
	r.POST("/servers/:id/files/create", h.Create)
	r.POST("/servers/:id/files/rename", h.Rename)
	r.POST("/servers/:id/files/copy", h.Copy)
	r.POST("/servers/:id/files/chmod", h.Chmod)
	r.POST("/servers/:id/files/archive", h.Archive)
	r.POST("/servers/:id/files/extract", h.Extract)
	r.GET("/servers/:id/files/download", h.Download)
	r.POST("/servers/:id/files/upload", h.Upload)
	r.DELETE("/servers/:id/files", h.Delete)
}

func proxyError(c *gin.Context, err error) {
	runtimes.ProxyError(c, err)
}
