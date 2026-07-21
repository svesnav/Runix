package audit

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/platform/httpx"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) List(c *gin.Context) {
	f := Filter{
		ActorID:    c.Query("actorId"),
		Action:     c.Query("action"),
		TargetType: c.Query("targetType"),
		TargetID:   c.Query("targetId"),
	}
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.BadRequest(c, "from must be RFC3339")
			return
		}
		f.From = t
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.BadRequest(c, "to must be RFC3339")
			return
		}
		f.To = t
	}
	page := httpx.Pagination(c)
	items, total, err := h.svc.List(c.Request.Context(), f, page)
	if err != nil {
		httpx.ServiceError(c, err, nil)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListResponse(items, total, page))
}
