// Package notifications pushes platform events (agent presence, heartbeat
// summaries) to authenticated dashboard clients over WebSocket.
package notifications

import (
	"context"
	"encoding/json"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/platform/bus"
)

type Handler struct {
	bus *bus.Bus
}

func NewHandler(eventBus *bus.Bus) *Handler {
	return &Handler{bus: eventBus}
}

type eventMessage struct {
	Type     string    `json:"type"`
	Topic    string    `json:"topic"`
	ServerID string    `json:"serverId,omitempty"`
	At       time.Time `json:"at"`
}

// WS streams presence events. Heartbeats are intentionally excluded here;
// clients wanting metrics subscribe to the per-server live feed.
func (h *Handler) WS(c *gin.Context) {
	sub := h.bus.Subscribe("agent.online", "agent.offline")
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
			raw, err := json.Marshal(eventMessage{
				Type: "event", Topic: event.Topic, ServerID: event.ServerID, At: event.At,
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

func RegisterRoutes(r gin.IRouter, h *Handler, requirePerm func(string) gin.HandlerFunc) {
	r.GET("/events", requirePerm("server.view"), h.WS)
}
