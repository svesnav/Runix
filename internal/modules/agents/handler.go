package agents

import (
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/platform/httpx"
	"github.com/runix/runix/internal/protocol"
)

const heartbeatSeconds = 30

// HandleWS upgrades an agent's connection. Agents authenticate with their
// per-server token in the Authorization header; this endpoint is never used
// by browsers.
func (h *Hub) HandleWS(c *gin.Context) {
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if token == "" {
		httpx.Unauthorized(c, "missing agent token")
		return
	}
	serverID, name, err := h.dir.AuthenticateAgent(c.Request.Context(), token)
	if err != nil {
		httpx.Unauthorized(c, "invalid agent token")
		return
	}

	ws, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		// Agents are native clients; browser origin checks do not apply.
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.log.Warn("ws accept failed", "server", serverID, "err", err)
		return
	}
	ws.SetReadLimit(maxFrameBytes)

	conn := newAgentConn(serverID, name, ws, h, h.log)

	// The first frame must be hello.
	env, err := conn.read(c.Request.Context(), helloTimeout)
	if err != nil || env.Type != protocol.TypeHello {
		conn.log.Warn("agent did not send hello", "err", err)
		_ = ws.Close(websocket.StatusPolicyViolation, "hello expected")
		return
	}
	hello, err := protocol.Decode[protocol.Hello](env.Payload)
	if err != nil {
		_ = ws.Close(websocket.StatusInvalidFramePayloadData, "bad hello")
		return
	}
	ctx := c.Request.Context()
	if err := h.dir.ApplyHello(ctx, serverID, hello); err != nil {
		conn.log.Error("apply hello failed", "err", err)
	}

	h.register(conn)
	h.dir.MarkOnline(ctx, serverID)
	conn.log.Info("agent connected",
		"hostname", hello.Info.Hostname, "version", hello.Info.AgentVersion)

	welcome, err := protocol.Marshal(protocol.TypeWelcome, "", protocol.Welcome{
		ServerID:         serverID,
		HeartbeatSeconds: heartbeatSeconds,
	})
	if err == nil {
		_ = conn.enqueue(ctx, welcome)
	}

	runErr := conn.run(ctx)
	conn.shutdown()
	h.unregister(conn)
	h.dir.MarkOffline(c.Request.Context(), serverID)
	conn.log.Info("agent disconnected", "reason", runErr)

	if websocket.CloseStatus(runErr) == -1 {
		_ = ws.Close(websocket.StatusNormalClosure, "")
	}
	// Response already hijacked by the websocket; nothing more to write.
	c.Status(http.StatusSwitchingProtocols)
}

func RegisterRoutes(r gin.IRouter, h *Hub) {
	r.GET("/agents/ws", h.HandleWS)
}
