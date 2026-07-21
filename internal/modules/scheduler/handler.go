package scheduler

import (
	"net/http"
	"strconv"

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

type taskRequest struct {
	Name        string  `json:"name" binding:"required,max=128"`
	Description string  `json:"description" binding:"max=512"`
	ServerID    string  `json:"serverId" binding:"required,uuid"`
	Kind        string  `json:"kind" binding:"required"`
	Payload     Payload `json:"payload"`
	Cron        string  `json:"cron" binding:"required,max=128"`
	Enabled     *bool   `json:"enabled"`
}

func (r taskRequest) input() Input {
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return Input{
		Name: r.Name, Description: r.Description, ServerID: r.ServerID,
		Kind: r.Kind, Payload: r.Payload, Cron: r.Cron, Enabled: enabled,
	}
}

func (h *Handler) List(c *gin.Context) {
	tasks, err := h.svc.List(c.Request.Context(), c.Query("serverId"))
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if tasks == nil {
		tasks = []Task{}
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func (h *Handler) Create(c *gin.Context) {
	var req taskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	var createdBy string
	if p, ok := authn.FromContext(c.Request.Context()); ok {
		createdBy = p.UserID
	}
	task, err := h.svc.Create(c.Request.Context(), req.input(), createdBy)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "scheduler.create", TargetType: "scheduled_task", TargetID: req.Name,
		New: req, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusCreated, task)
}

func (h *Handler) Update(c *gin.Context) {
	var req taskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	task, err := h.svc.Update(c.Request.Context(), c.Param("id"), req.input())
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "scheduler.update", TargetType: "scheduled_task", TargetID: c.Param("id"),
		New: req, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, task)
}

func (h *Handler) Delete(c *gin.Context) {
	err := h.svc.Delete(c.Request.Context(), c.Param("id"))
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "scheduler.delete", TargetType: "scheduled_task", TargetID: c.Param("id"), Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.Status(http.StatusNoContent)
}

// RunNow executes the task immediately; the schedule is untouched.
func (h *Handler) RunNow(c *gin.Context) {
	run, err := h.svc.RunNow(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, run)
}

func (h *Handler) Runs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	runs, err := h.svc.Runs(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if runs == nil {
		runs = []Run{}
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
}

func RegisterRoutes(r gin.IRouter, h *Handler, requirePerm func(string) gin.HandlerFunc) {
	const manage = "scheduler.manage"
	r.GET("/scheduled-tasks", requirePerm("server.view"), h.List)
	r.POST("/scheduled-tasks", requirePerm(manage), h.Create)
	r.PUT("/scheduled-tasks/:id", requirePerm(manage), h.Update)
	r.DELETE("/scheduled-tasks/:id", requirePerm(manage), h.Delete)
	r.POST("/scheduled-tasks/:id/run", requirePerm(manage), h.RunNow)
	r.GET("/scheduled-tasks/:id/runs", requirePerm("server.view"), h.Runs)
}
