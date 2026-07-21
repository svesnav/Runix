package runtimes

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/protocol"
)

type logMessage struct {
	Type      string    `json:"type"` // "log" | "end"
	Timestamp time.Time `json:"timestamp,omitempty"`
	Source    string    `json:"source,omitempty"`
	Line      string    `json:"line,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// LogsWS streams runtime logs to a browser over WebSocket, proxying the
// agent stream frame by frame.
func (h *Handler) LogsWS(c *gin.Context) {
	if !h.authorize(c, "server.logs", "server.view") {
		return
	}
	tail, _ := strconv.Atoi(c.DefaultQuery("tail", "200"))
	params := protocol.RuntimeLogsParams{
		Type:       c.Param("type"),
		ID:         c.Param("rid"),
		Follow:     c.DefaultQuery("follow", "true") == "true",
		Tail:       tail,
		Timestamps: c.Query("timestamps") == "true",
	}

	stream, err := h.hub.OpenStream(c.Request.Context(), c.Param("id"), protocol.MethodRuntimeLogs, params)
	if err != nil {
		h.proxyError(c, err)
		return
	}
	defer stream.Close()

	// Origin is enforced centrally by the server CORS middleware.
	ws, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()

	ctx := c.Request.Context()
	writeJSON := func(msg logMessage) error {
		raw, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		writeCtx, cancel := context5s(ctx)
		defer cancel()
		return ws.Write(writeCtx, websocket.MessageText, raw)
	}

	// Drain client frames so pings/close are processed.
	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				stream.Close()
				return
			}
		}
	}()

	for {
		frame, err := stream.Recv(ctx)
		if err != nil {
			_ = writeJSON(logMessage{Type: "end"})
			_ = ws.Close(websocket.StatusNormalClosure, "")
			return
		}
		switch frame.Op {
		case protocol.StreamData:
			var line protocol.LogLine
			if err := json.Unmarshal(frame.Data, &line); err != nil {
				continue
			}
			if err := writeJSON(logMessage{
				Type: "log", Timestamp: line.Timestamp, Source: line.Source, Line: line.Line,
			}); err != nil {
				return
			}
		case protocol.StreamClose:
			end := logMessage{Type: "end"}
			if frame.Error != nil {
				end.Error = frame.Error.Message
			}
			_ = writeJSON(end)
			_ = ws.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
}
