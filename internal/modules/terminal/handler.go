// Package terminal proxies interactive shell sessions between browser
// WebSockets and agent PTY streams.
package terminal

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/coder/websocket"
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

// clientMessage is what the browser sends: keystrokes or resizes.
type clientMessage struct {
	Type string `json:"type"` // "input" | "resize"
	Data []byte `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// serverMessage is what the browser receives.
type serverMessage struct {
	Type  string `json:"type"` // "output" | "end"
	Data  []byte `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// WS opens a terminal. Host terminals require terminal.admin; runtime
// terminals require terminal.open.
func (h *Handler) WS(c *gin.Context) {
	p, ok := authn.FromContext(c.Request.Context())
	if !ok {
		httpx.Unauthorized(c, "authentication required")
		return
	}
	target := c.DefaultQuery("target", protocol.TerminalTargetHost)
	perm := "terminal.open"
	if target == protocol.TerminalTargetHost {
		perm = "terminal.admin"
	}
	// A terminal into a runtime is checked against that runtime, so a
	// runtime-scoped grant is enough to open it.
	// Convert Target to rbac.Scope for proper permission checking
	var scope rbac.Scope
	if c.Query("type") != "" && c.Query("rid") != "" {
		scope = rbac.RuntimeScope(c.Param("id"), c.Query("type"), c.Query("rid"))
	} else {
		scope = rbac.ServerScope(c.Param("id"))
	}

	allowed, err := h.check(c.Request.Context(), p.UserID, perm, scope)
	if err != nil {
		httpx.Internal(c)
		return
	}
	if !allowed {
		httpx.Forbidden(c)
		return
	}

	cols, _ := strconv.Atoi(c.DefaultQuery("cols", "120"))
	rows, _ := strconv.Atoi(c.DefaultQuery("rows", "32"))
	params := protocol.TerminalParams{
		Target: target,
		Type:   c.Query("type"),
		ID:     c.Query("rid"),
		Cols:   uint16(cols), // #nosec G115 -- bounded by terminal geometry
		Rows:   uint16(rows), // #nosec G115
	}

	ctx := c.Request.Context()
	stream, err := h.hub.OpenStream(ctx, c.Param("id"), protocol.MethodTerminalOpen, params)
	if err != nil {
		runtimes.ProxyError(c, err)
		return
	}
	defer stream.Close()

	h.auditor.Write(ctx, audit.Record{
		Action: "terminal.open", TargetType: "server", TargetID: c.Param("id"),
		New: gin.H{"target": target, "type": params.Type, "rid": params.ID},
	})

	// Origin is enforced centrally by the server CORS middleware.
	ws, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()

	// Browser → agent.
	go func() {
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				stream.Close()
				return
			}
			var msg clientMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "input":
				if err := stream.SendData(ctx, msg.Data); err != nil {
					return
				}
			case "resize":
				_ = stream.SendCtrl(ctx, protocol.TerminalCtrl{
					Resize: &protocol.TerminalResize{Cols: msg.Cols, Rows: msg.Rows},
				})
			}
		}
	}()

	// Agent → browser.
	for {
		frame, err := stream.Recv(ctx)
		if err != nil {
			writeServerMessage(ctx, ws, serverMessage{Type: "end"})
			return
		}
		switch frame.Op {
		case protocol.StreamData:
			if err := writeServerMessage(ctx, ws, serverMessage{Type: "output", Data: frame.Data}); err != nil {
				return
			}
		case protocol.StreamClose:
			end := serverMessage{Type: "end"}
			if frame.Error != nil {
				end.Error = frame.Error.Message
			}
			writeServerMessage(ctx, ws, end)
			_ = ws.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
}

func writeServerMessage(ctx context.Context, ws *websocket.Conn, msg serverMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, raw)
}

func RegisterRoutes(r gin.IRouter, h *Handler) {
	r.GET("/servers/:id/terminal", h.WS)
}
