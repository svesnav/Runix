// Package metrics exposes persisted heartbeat history and a live per-server
// metrics feed.
package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/servers"
	"github.com/runix/runix/internal/platform/bus"
	"github.com/runix/runix/internal/platform/httpx"
	"github.com/runix/runix/internal/protocol"
)

// History is the slice of the servers module this module reads.
type History interface {
	Metrics(ctx context.Context, serverID string, from, to time.Time, limit int) ([]servers.MetricsPoint, error)
}

type Handler struct {
	history History
	bus     *bus.Bus
}

func NewHandler(history History, eventBus *bus.Bus) *Handler {
	return &Handler{history: history, bus: eventBus}
}

func (h *Handler) Query(c *gin.Context) {
	var from, to time.Time
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.BadRequest(c, "from must be RFC3339")
			return
		}
		from = t
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.BadRequest(c, "to must be RFC3339")
			return
		}
		to = t
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "1000"))
	points, err := h.history.Metrics(c.Request.Context(), c.Param("id"), from, to, limit)
	if err != nil {
		_ = c.Error(err)
		httpx.Internal(c)
		return
	}
	if points == nil {
		points = []servers.MetricsPoint{}
	}
	c.JSON(http.StatusOK, gin.H{"points": points})
}

// LiveWS pushes each incoming heartbeat of one server to the client.
func (h *Handler) LiveWS(c *gin.Context) {
	serverID := c.Param("id")
	sub := h.bus.Subscribe("agent.heartbeat")
	defer sub.Close()

	// Origin is enforced centrally by the server CORS middleware.
	ws, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx := c.Request.Context()

	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				sub.Close()
				return
			}
		}
	}()

	for {
		select {
		case event, ok := <-sub.C:
			if !ok {
				_ = ws.Close(websocket.StatusNormalClosure, "")
				return
			}
			if event.ServerID != serverID {
				continue
			}
			hb, isHB := event.Payload.(protocol.Heartbeat)
			if !isHB {
				continue
			}
			raw, err := json.Marshal(gin.H{
				"type":     "metrics",
				"at":       event.At,
				"metrics":  hb.Metrics,
				"runtimes": hb.Runtimes,
			})
			if err != nil {
				continue
			}
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = ws.Write(writeCtx, websocket.MessageText, raw)
			cancel()
			if err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// RegisterRoutes mounts under server-scoped metrics permission middleware
// supplied by the app.
func RegisterRoutes(r gin.IRouter, h *Handler, requireServerPerm func(string) gin.HandlerFunc) {
	r.GET("/servers/:id/metrics", requireServerPerm("server.metrics"), h.Query)
	r.GET("/servers/:id/metrics/live", requireServerPerm("server.metrics"), h.LiveWS)
}
