package agents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/runix/runix/internal/platform/bus"
	"github.com/runix/runix/internal/protocol"
)

var (
	ErrAgentOffline = errors.New("agents: agent is not connected")
	ErrCallTimeout  = errors.New("agents: call timed out")
)

// ServerDirectory is the slice of the servers module the hub needs; the app
// wires servers.Service in through a thin adapter.
type ServerDirectory interface {
	AuthenticateAgent(ctx context.Context, token string) (serverID, name string, err error)
	ApplyHello(ctx context.Context, serverID string, hello protocol.Hello) error
	MarkOnline(ctx context.Context, serverID string)
	MarkOffline(ctx context.Context, serverID string)
	RecordHeartbeat(ctx context.Context, serverID string, hb protocol.Heartbeat) error
}

// Hub owns every live agent connection. It is the only path between the
// control plane and managed hosts: request/response calls and byte streams
// all multiplex over the agent's single authenticated WebSocket.
type Hub struct {
	dir ServerDirectory
	bus *bus.Bus
	log *slog.Logger

	callTimeout time.Duration

	mu    sync.RWMutex
	conns map[string]*agentConn
}

func NewHub(dir ServerDirectory, eventBus *bus.Bus, log *slog.Logger) *Hub {
	return &Hub{
		dir:         dir,
		bus:         eventBus,
		log:         log,
		callTimeout: 30 * time.Second,
		conns:       make(map[string]*agentConn),
	}
}

func (h *Hub) register(conn *agentConn) {
	h.mu.Lock()
	prev := h.conns[conn.serverID]
	h.conns[conn.serverID] = conn
	h.mu.Unlock()
	if prev != nil {
		h.log.Warn("replacing existing agent connection", "server", conn.serverID)
		prev.shutdown()
	}
}

func (h *Hub) unregister(conn *agentConn) {
	h.mu.Lock()
	if h.conns[conn.serverID] == conn {
		delete(h.conns, conn.serverID)
	}
	h.mu.Unlock()
}

func (h *Hub) conn(serverID string) (*agentConn, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.conns[serverID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAgentOffline, serverID)
	}
	return c, nil
}

// Connected reports whether the server's agent currently holds a live
// connection.
func (h *Hub) Connected(serverID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[serverID]
	return ok
}

func (h *Hub) ConnectedIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.conns))
	for id := range h.conns {
		out = append(out, id)
	}
	return out
}

// Call performs one RPC against the server's agent.
func (h *Hub) Call(ctx context.Context, serverID, method string, params any) ([]byte, error) {
	conn, err := h.conn(serverID)
	if err != nil {
		return nil, err
	}
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.callTimeout)
		defer cancel()
	}

	id := uuid.NewString()
	env, err := protocol.Marshal(protocol.TypeRequest, id, params)
	if err != nil {
		return nil, err
	}
	env.Method = method

	ch := conn.addPending(id)
	defer conn.removePending(id)

	if err := conn.enqueue(ctx, env); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Payload, nil
	case <-conn.closed:
		return nil, ErrAgentOffline
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrCallTimeout
		}
		return nil, ctx.Err()
	}
}

// OpenStream starts a streaming method (logs, terminal) on the agent.
func (h *Hub) OpenStream(ctx context.Context, serverID, method string, params any) (*Stream, error) {
	conn, err := h.conn(serverID)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	env, err := protocol.Marshal(protocol.TypeStream, id, params)
	if err != nil {
		return nil, err
	}
	env.Method = method
	env.Op = protocol.StreamOpen

	stream := newStream(id, conn)
	conn.addStream(stream)
	if err := conn.enqueue(ctx, env); err != nil {
		conn.removeStream(id)
		return nil, err
	}
	return stream, nil
}
