package dashboard

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/servers"
	"github.com/runix/runix/internal/platform/httpx"
)

// ServerCounts is the slice of the servers module the summary needs.
type ServerCounts interface {
	CountByStatus(ctx context.Context) (map[servers.ConnectionStatus]int, error)
}

// Presence reports live agent connections (the hub).
type Presence interface {
	ConnectedIDs() []string
}

type Handler struct {
	svc      *Service
	counts   ServerCounts
	presence Presence
}

func NewHandler(svc *Service, counts ServerCounts, presence Presence) *Handler {
	return &Handler{svc: svc, counts: counts, presence: presence}
}

type summaryResponse struct {
	Servers         map[string]int            `json:"servers"`
	ConnectedAgents int                       `json:"connectedAgents"`
	Runtimes        map[string]map[string]int `json:"runtimes"`
	RecentEvents    []Event                   `json:"recentEvents"`
}

func (h *Handler) Summary(c *gin.Context) {
	byStatus, err := h.counts.CountByStatus(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		httpx.Internal(c)
		return
	}
	serverCounts := map[string]int{"total": 0}
	for status, n := range byStatus {
		serverCounts[string(status)] = n
		serverCounts["total"] += n
	}
	events := h.svc.RecentEvents()
	if events == nil {
		events = []Event{}
	}
	c.JSON(http.StatusOK, summaryResponse{
		Servers:         serverCounts,
		ConnectedAgents: len(h.presence.ConnectedIDs()),
		Runtimes:        h.svc.RuntimeSummary(),
		RecentEvents:    events,
	})
}

func RegisterRoutes(r gin.IRouter, h *Handler, requirePerm func(string) gin.HandlerFunc) {
	r.GET("/dashboard", requirePerm("server.view"), h.Summary)
}
