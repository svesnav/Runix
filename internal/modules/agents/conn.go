package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/runix/runix/internal/platform/bus"
	"github.com/runix/runix/internal/protocol"
)

const (
	helloTimeout = 10 * time.Second
	// Four missed heartbeats. Safe to deadline this side because the
	// traffic actually flows this way: the agent heartbeats on a timer
	// whether or not anything is happening. The reverse is not true, so
	// the agent must not deadline its own read — see keepaliveLoop in
	// internal/agent/session.go.
	readTimeout   = 4 * heartbeatSeconds * time.Second
	writeTimeout  = 15 * time.Second
	maxFrameBytes = 32 << 20
	sendQueueSize = 256
)

type agentConn struct {
	serverID string
	name     string
	ws       *websocket.Conn
	hub      *Hub
	log      *slog.Logger

	send   chan protocol.Envelope
	closed chan struct{}
	once   sync.Once

	mu      sync.Mutex
	pending map[string]chan protocol.Envelope
	streams map[string]*Stream
}

func newAgentConn(serverID, name string, ws *websocket.Conn, hub *Hub, log *slog.Logger) *agentConn {
	return &agentConn{
		serverID: serverID,
		name:     name,
		ws:       ws,
		hub:      hub,
		log:      log.With("server", serverID, "agent", name),
		send:     make(chan protocol.Envelope, sendQueueSize),
		closed:   make(chan struct{}),
		pending:  make(map[string]chan protocol.Envelope),
		streams:  make(map[string]*Stream),
	}
}

func (c *agentConn) shutdown() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.ws.CloseNow()
		c.mu.Lock()
		for id, s := range c.streams {
			s.closeLocal()
			delete(c.streams, id)
		}
		c.mu.Unlock()
	})
}

func (c *agentConn) enqueue(ctx context.Context, env protocol.Envelope) error {
	select {
	case c.send <- env:
		return nil
	case <-c.closed:
		return ErrAgentOffline
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *agentConn) addPending(id string) chan protocol.Envelope {
	ch := make(chan protocol.Envelope, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	return ch
}

func (c *agentConn) removePending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *agentConn) addStream(s *Stream) {
	c.mu.Lock()
	c.streams[s.ID] = s
	c.mu.Unlock()
}

func (c *agentConn) removeStream(id string) {
	c.mu.Lock()
	delete(c.streams, id)
	c.mu.Unlock()
}

// run drives the connection until it drops. Caller handles registration.
func (c *agentConn) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.writePump(ctx)

	for {
		env, err := c.read(ctx, readTimeout)
		if err != nil {
			return err
		}
		switch env.Type {
		case protocol.TypeHeartbeat:
			hb, err := protocol.Decode[protocol.Heartbeat](env.Payload)
			if err != nil {
				c.log.Warn("bad heartbeat payload", "err", err)
				continue
			}
			if err := c.hub.dir.RecordHeartbeat(ctx, c.serverID, hb); err != nil {
				c.log.Error("record heartbeat failed", "err", err)
			}
		case protocol.TypeResponse:
			c.mu.Lock()
			ch := c.pending[env.ID]
			c.mu.Unlock()
			if ch != nil {
				ch <- env
			}
		case protocol.TypeStream:
			c.mu.Lock()
			s := c.streams[env.ID]
			c.mu.Unlock()
			if s == nil {
				continue
			}
			s.deliver(env)
			if env.Op == protocol.StreamClose {
				c.removeStream(env.ID)
			}
		case protocol.TypeEvent:
			c.hub.bus.Publish(bus.Event{
				Topic:    "agent.event",
				ServerID: c.serverID,
				Payload:  json.RawMessage(env.Payload),
			})
		default:
			c.log.Warn("unexpected message type", "type", env.Type)
		}
	}
}

func (c *agentConn) read(ctx context.Context, timeout time.Duration) (protocol.Envelope, error) {
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, data, err := c.ws.Read(readCtx)
	if err != nil {
		return protocol.Envelope{}, err
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return protocol.Envelope{}, fmt.Errorf("agents: malformed frame: %w", err)
	}
	return env, nil
}

func (c *agentConn) writePump(ctx context.Context) {
	for {
		select {
		case env := <-c.send:
			data, err := json.Marshal(env)
			if err != nil {
				c.log.Error("marshal frame failed", "err", err)
				continue
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err = c.ws.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				c.log.Debug("write failed, dropping connection", "err", err)
				c.shutdown()
				return
			}
		case <-c.closed:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Stream is the server-side handle of one agent stream.
type Stream struct {
	ID     string
	conn   *agentConn
	frames chan protocol.Envelope
	done   chan struct{}
	once   sync.Once
}

func newStream(id string, conn *agentConn) *Stream {
	return &Stream{
		ID:     id,
		conn:   conn,
		frames: make(chan protocol.Envelope, sendQueueSize),
		done:   make(chan struct{}),
	}
}

func (s *Stream) deliver(env protocol.Envelope) {
	select {
	case s.frames <- env:
	case <-s.done:
	default:
		// Slow consumer: drop the stream rather than block the agent socket.
		s.closeLocal()
	}
}

// Recv returns the next frame. A StreamClose frame is delivered to the
// caller before ErrStreamClosed on subsequent calls.
var ErrStreamClosed = errors.New("agents: stream closed")

func (s *Stream) Recv(ctx context.Context) (protocol.Envelope, error) {
	select {
	case env := <-s.frames:
		return env, nil
	case <-s.done:
		select {
		case env := <-s.frames:
			return env, nil
		default:
			return protocol.Envelope{}, ErrStreamClosed
		}
	case <-s.conn.closed:
		return protocol.Envelope{}, ErrAgentOffline
	case <-ctx.Done():
		return protocol.Envelope{}, ctx.Err()
	}
}

// SendData forwards bytes to the agent (terminal input, file uploads). The
// payload is copied: the write pump marshals frames asynchronously, so a
// caller streaming from a reused buffer would otherwise corrupt the data.
func (s *Stream) SendData(ctx context.Context, data []byte) error {
	payload := make([]byte, len(data))
	copy(payload, data)
	return s.conn.enqueue(ctx, protocol.Envelope{
		V: protocol.Version, Type: protocol.TypeStream, ID: s.ID,
		Op: protocol.StreamData, Data: payload,
	})
}

// SendCtrl forwards a control payload (terminal resize).
func (s *Stream) SendCtrl(ctx context.Context, payload any) error {
	env, err := protocol.Marshal(protocol.TypeStream, s.ID, payload)
	if err != nil {
		return err
	}
	env.Op = protocol.StreamCtrl
	return s.conn.enqueue(ctx, env)
}

func (s *Stream) closeLocal() {
	s.once.Do(func() { close(s.done) })
}

// Close tells the agent to tear the stream down and releases it locally.
func (s *Stream) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.conn.enqueue(ctx, protocol.Envelope{
		V: protocol.Version, Type: protocol.TypeStream, ID: s.ID, Op: protocol.StreamClose,
	})
	s.conn.removeStream(s.ID)
	s.closeLocal()
}
