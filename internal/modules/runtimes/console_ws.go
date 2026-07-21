package runtimes

import (
	"encoding/json"
	"strconv"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/protocol"
)

// consoleClientMessage is what the browser sends: a line (or raw bytes) for
// the process's stdin.
type consoleClientMessage struct {
	Type string `json:"type"` // "input"
	Data []byte `json:"data"`
}

type consoleServerMessage struct {
	Type  string `json:"type"` // "output" | "end"
	Data  []byte `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// ConsoleWS bridges a browser to the runtime's main process streams, so
// operators can both read output and type commands into programs that only
// speak on stdin.
func (h *Handler) ConsoleWS(c *gin.Context) {
	// Writing to a process's stdin is executing something inside it, so it
	// needs the same authority as exec — reading logs alone is not enough.
	if !h.authorize(c, "runtime.execute", "runtime.manage", managePermFor(c.Param("type"))) {
		return
	}
	tail, _ := strconv.Atoi(c.DefaultQuery("tail", "200"))

	stream, err := h.hub.OpenStream(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeConsole,
		protocol.RuntimeConsoleParams{
			Type: c.Param("type"),
			ID:   c.Param("rid"),
			Tail: tail,
		})
	if err != nil {
		h.proxyError(c, err)
		return
	}
	defer stream.Close()

	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "runtime.console", TargetType: "runtime",
		TargetID: c.Param("id") + "/" + c.Param("type") + "/" + c.Param("rid"),
	})

	// Origin is enforced centrally by the server CORS middleware.
	ws, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx := c.Request.Context()

	send := func(msg consoleServerMessage) error {
		raw, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		writeCtx, cancel := context5s(ctx)
		defer cancel()
		return ws.Write(writeCtx, websocket.MessageText, raw)
	}

	// Browser → process stdin.
	go func() {
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				stream.Close()
				return
			}
			var msg consoleClientMessage
			if err := json.Unmarshal(data, &msg); err != nil || msg.Type != "input" {
				continue
			}
			if err := stream.SendData(ctx, msg.Data); err != nil {
				return
			}
		}
	}()

	// Process output → browser.
	for {
		frame, err := stream.Recv(ctx)
		if err != nil {
			_ = send(consoleServerMessage{Type: "end"})
			return
		}
		switch frame.Op {
		case protocol.StreamData:
			if err := send(consoleServerMessage{Type: "output", Data: frame.Data}); err != nil {
				return
			}
		case protocol.StreamClose:
			end := consoleServerMessage{Type: "end"}
			if frame.Error != nil {
				end.Error = frame.Error.Message
			}
			_ = send(end)
			_ = ws.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
}
