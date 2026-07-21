package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/runix/runix/internal/platform/version"
	"github.com/runix/runix/internal/protocol"
)

const (
	welcomeTimeout = 15 * time.Second
	writeTimeout   = 15 * time.Second
	// How long a keepalive ping may go unanswered before the connection is
	// treated as dead. Scaled to the negotiated heartbeat so the check
	// stays proportional, but bounded: the ceiling keeps a busy control
	// plane under a large log stream from being mistaken for an absent
	// one, and the floor keeps a very short interval from turning a
	// scheduling hiccup into a disconnect.
	minPingTimeout = time.Second
	maxPingTimeout = 20 * time.Second
	maxFrameBytes  = 32 << 20
	sendQueueSize  = 256
)

type session struct {
	agent *Agent
	ws    *websocket.Conn
	send  chan protocol.Envelope

	mu      sync.Mutex
	streams map[string]*AgentStream
}

func (a *Agent) runSession(ctx context.Context) error {
	target, err := wsURL(a.cfg.ServerURL)
	if err != nil {
		return fmt.Errorf("agent: bad server url: %w", err)
	}
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.cfg.Token)
	ws, _, err := websocket.Dial(dialCtx, target, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return fmt.Errorf("agent: dial: %w", err)
	}
	ws.SetReadLimit(maxFrameBytes)

	s := &session{
		agent:   a,
		ws:      ws,
		send:    make(chan protocol.Envelope, sendQueueSize),
		streams: make(map[string]*AgentStream),
	}
	defer s.shutdown()

	sessCtx, cancelSess := context.WithCancel(ctx)
	defer cancelSess()
	go s.writePump(sessCtx)

	hello := protocol.Hello{
		Info:      collectHostInfo(sessCtx, version.Get().Version),
		Providers: collectProviders(sessCtx, a.registry),
	}
	env, err := protocol.Marshal(protocol.TypeHello, "", hello)
	if err != nil {
		return err
	}
	if err := s.enqueue(sessCtx, env); err != nil {
		return err
	}

	welcomeEnv, err := s.read(sessCtx, welcomeTimeout)
	if err != nil {
		return fmt.Errorf("agent: waiting for welcome: %w", err)
	}
	if welcomeEnv.Type != protocol.TypeWelcome {
		return fmt.Errorf("agent: expected welcome, got %s", welcomeEnv.Type)
	}
	welcome, err := protocol.Decode[protocol.Welcome](welcomeEnv.Payload)
	if err != nil {
		return err
	}
	interval := time.Duration(welcome.HeartbeatSeconds) * time.Second
	if interval < time.Second {
		interval = a.cfg.HeartbeatInterval
	}
	a.log.Info("connected to control plane", "server_id", welcome.ServerID, "heartbeat", interval)

	go s.heartbeatLoop(sessCtx, interval)
	go s.keepaliveLoop(sessCtx, cancelSess, interval)

	// No read deadline here. Traffic on this socket is one-directional
	// when nothing is happening: the agent heartbeats to the control
	// plane, which has nothing to say back until an operator asks for
	// something. A deadline on this read would therefore fire on every
	// idle connection — which it did, dropping and reconnecting agents
	// every couple of minutes. Liveness is keepaliveLoop's job.
	for {
		env, err := s.read(sessCtx, 0)
		if err != nil {
			return err
		}
		switch env.Type {
		case protocol.TypeRequest:
			go s.handleRequest(sessCtx, env)
		case protocol.TypeStream:
			s.handleStreamFrame(sessCtx, env)
		default:
			a.log.Warn("unexpected message from control plane", "type", env.Type)
		}
	}
}

func (s *session) shutdown() {
	_ = s.ws.CloseNow()
	s.mu.Lock()
	for id, st := range s.streams {
		st.cancel()
		delete(s.streams, id)
	}
	s.mu.Unlock()
}

// read waits for the next frame. A timeout of 0 means "however long the
// session lasts" — used for the main loop, where silence is normal.
func (s *session) read(ctx context.Context, timeout time.Duration) (protocol.Envelope, error) {
	readCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	_, data, err := s.ws.Read(readCtx)
	if err != nil {
		return protocol.Envelope{}, err
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return protocol.Envelope{}, fmt.Errorf("agent: malformed frame: %w", err)
	}
	return env, nil
}

func (s *session) enqueue(ctx context.Context, env protocol.Envelope) error {
	select {
	case s.send <- env:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) writePump(ctx context.Context) {
	for {
		select {
		case env := <-s.send:
			data, err := json.Marshal(env)
			if err != nil {
				s.agent.log.Error("marshal frame failed", "err", err)
				continue
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err = s.ws.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				_ = s.ws.CloseNow()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// keepaliveLoop proves the control plane is still there.
//
// Heartbeats only prove the agent can *write*, which a half-open socket
// happily accepts, so they cannot tell a live control plane from one that
// vanished. A WebSocket ping is answered by the peer's protocol layer, so
// a returning pong is positive evidence — and unlike a read deadline it
// does not depend on the control plane having something to say.
//
// Note that ping/pong is invisible to Read: the library handles those
// frames while the main loop is blocked, which is exactly why liveness
// belongs here rather than in a deadline on that read.
func pingDeadline(interval time.Duration) time.Duration {
	switch d := 2 * interval; {
	case d < minPingTimeout:
		return minPingTimeout
	case d > maxPingTimeout:
		return maxPingTimeout
	default:
		return d
	}
}

func (s *session) keepaliveLoop(ctx context.Context, cancel context.CancelFunc, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	timeout := pingDeadline(interval)
	for {
		select {
		case <-ticker.C:
			pingCtx, done := context.WithTimeout(ctx, timeout)
			err := s.ws.Ping(pingCtx)
			done()
			if err != nil {
				if ctx.Err() != nil {
					return // the session is going away anyway
				}
				s.agent.log.Warn("control plane did not answer keepalive", "err", err)
				cancel()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *session) heartbeatLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	send := func() {
		hb := protocol.Heartbeat{
			Metrics:   collectHostMetrics(ctx),
			Runtimes:  collectRuntimeCounts(ctx, s.agent.registry),
			Providers: collectProviders(ctx, s.agent.registry),
		}
		env, err := protocol.Marshal(protocol.TypeHeartbeat, "", hb)
		if err != nil {
			s.agent.log.Error("marshal heartbeat failed", "err", err)
			return
		}
		_ = s.enqueue(ctx, env)
	}
	send()
	for {
		select {
		case <-ticker.C:
			send()
		case <-ctx.Done():
			return
		}
	}
}

func (s *session) handleRequest(ctx context.Context, env protocol.Envelope) {
	resp := protocol.Envelope{V: protocol.Version, Type: protocol.TypeResponse, ID: env.ID}
	handler, ok := s.agent.rpc.calls[env.Method]
	if !ok {
		resp.Error = &protocol.Error{Code: protocol.CodeNotSupported,
			Message: fmt.Sprintf("unknown method %q", env.Method)}
		_ = s.enqueue(ctx, resp)
		return
	}
	result, perr := handler(ctx, env.Payload)
	if perr != nil {
		resp.Error = perr
	} else if result != nil {
		raw, err := json.Marshal(result)
		if err != nil {
			resp.Error = &protocol.Error{Code: protocol.CodeInternal, Message: "marshal result failed"}
		} else {
			resp.Payload = raw
		}
	}
	_ = s.enqueue(ctx, resp)
}

func (s *session) handleStreamFrame(ctx context.Context, env protocol.Envelope) {
	switch env.Op {
	case protocol.StreamOpen:
		handler, ok := s.agent.rpc.streams[env.Method]
		if !ok {
			s.closeStream(ctx, env.ID, &protocol.Error{
				Code: protocol.CodeNotSupported, Message: fmt.Sprintf("unknown stream method %q", env.Method)})
			return
		}
		streamCtx, cancel := context.WithCancel(ctx)
		st := &AgentStream{
			ID:      env.ID,
			sess:    s,
			In:      make(chan protocol.Envelope, 64),
			ctx:     streamCtx,
			cancelF: cancel,
		}
		s.mu.Lock()
		s.streams[env.ID] = st
		s.mu.Unlock()
		go func() {
			perr := handler(streamCtx, env.Payload, st)
			s.mu.Lock()
			delete(s.streams, env.ID)
			s.mu.Unlock()
			st.cancel()
			s.closeStream(ctx, env.ID, perr)
		}()
	case protocol.StreamData, protocol.StreamCtrl:
		s.mu.Lock()
		st := s.streams[env.ID]
		s.mu.Unlock()
		if st != nil {
			select {
			case st.In <- env:
			case <-st.ctx.Done():
			default: // drop input for a stuck stream instead of blocking reads
			}
		}
	case protocol.StreamClose:
		s.mu.Lock()
		st := s.streams[env.ID]
		delete(s.streams, env.ID)
		s.mu.Unlock()
		if st != nil {
			st.cancel()
		}
	}
}

func (s *session) closeStream(ctx context.Context, id string, perr *protocol.Error) {
	env := protocol.Envelope{
		V: protocol.Version, Type: protocol.TypeStream, ID: id,
		Op: protocol.StreamClose, Error: perr,
	}
	_ = s.enqueue(ctx, env)
}

// AgentStream is the agent-side handle of one open stream.
type AgentStream struct {
	ID      string
	sess    *session
	In      chan protocol.Envelope
	ctx     context.Context
	cancelF context.CancelFunc
}

func (s *AgentStream) cancel() { s.cancelF() }

// Send emits one data frame to the control plane. The payload is copied
// because frames are marshaled asynchronously by the write pump: callers
// streaming from a reused read buffer would otherwise see their data
// overwritten before it hits the wire.
func (s *AgentStream) Send(data []byte) error {
	payload := make([]byte, len(data))
	copy(payload, data)
	return s.sess.enqueue(s.ctx, protocol.Envelope{
		V: protocol.Version, Type: protocol.TypeStream, ID: s.ID,
		Op: protocol.StreamData, Data: payload,
	})
}

// SendJSON emits one data frame carrying a JSON payload.
func (s *AgentStream) SendJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Send(raw)
}

// SendCtrlJSON emits a control frame (out-of-band metadata that must not be
// mixed into the binary data frames, e.g. download headers).
func (s *AgentStream) SendCtrlJSON(v any) error {
	env, err := protocol.Marshal(protocol.TypeStream, s.ID, v)
	if err != nil {
		return err
	}
	env.Op = protocol.StreamCtrl
	return s.sess.enqueue(s.ctx, env)
}
