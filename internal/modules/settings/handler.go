package settings

import (
	"encoding/json"
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
	ErrNotFound:   http.StatusNotFound,
	ErrUnknownKey: http.StatusNotFound,
	ErrInvalid:    http.StatusBadRequest,
}

func (h *Handler) List(c *gin.Context) {
	items, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	if items == nil {
		items = []Setting{}
	}
	// knownKeys is kept alongside the descriptors so an older client still
	// renders something usable against a newer control plane.
	c.JSON(http.StatusOK, gin.H{
		"settings":  items,
		"keys":      Descriptors(),
		"knownKeys": KnownKeys(),
	})
}

type setRequest struct {
	Value json.RawMessage `json:"value" binding:"required"`
}

func (h *Handler) Set(c *gin.Context) {
	var req setRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	key := c.Param("key")
	old, _ := h.svc.Get(c.Request.Context(), key)
	updated, err := h.svc.Set(c.Request.Context(), key, req.Value)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "settings.update", TargetType: "setting", TargetID: key,
		Old: old, New: updated, Err: err,
	})
	if err != nil {
		httpx.ServiceError(c, err, errStatus)
		return
	}
	c.JSON(http.StatusOK, updated)
}
