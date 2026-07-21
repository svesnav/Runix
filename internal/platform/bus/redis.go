package bus

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Redis fan-out makes the in-process bus cluster-wide: every control-plane
// instance publishes to one channel and mirrors what it receives onto its
// local bus, so a browser connected to instance A sees events raised by an
// agent attached to instance B.
//
// A minimal RESP client is used rather than a Redis driver: the bus needs
// exactly PUBLISH and SUBSCRIBE, and this keeps the dependency surface (and
// the audit surface) small.

const (
	redisChannel   = "runix:events"
	redisDialWait  = 5 * time.Second
	redisRetryWait = 3 * time.Second
)

type RedisOptions struct {
	Addr     string
	Password string
	DB       int
}

// wireEvent is the serialized form; Payload is kept raw so subscribers can
// decode it into the type they expect.
type wireEvent struct {
	Topic    string          `json:"topic"`
	ServerID string          `json:"serverId,omitempty"`
	At       time.Time       `json:"at"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Origin   string          `json:"origin"`
}

// RedisBridge connects a local Bus to Redis.
type RedisBridge struct {
	bus    *Bus
	opts   RedisOptions
	log    *slog.Logger
	origin string

	mu  sync.Mutex
	pub net.Conn
}

// NewRedisBridge returns a bridge; call Run to start it. instanceID must be
// unique per control-plane process so an instance ignores its own echoes.
func NewRedisBridge(b *Bus, opts RedisOptions, instanceID string, log *slog.Logger) *RedisBridge {
	return &RedisBridge{bus: b, opts: opts, log: log, origin: instanceID}
}

// Run publishes local events to Redis and mirrors remote ones back, until
// ctx is canceled. Connection failures are retried; the platform keeps
// working single-instance while Redis is unavailable.
func (r *RedisBridge) Run(ctx context.Context) {
	go r.publishLoop(ctx)
	for ctx.Err() == nil {
		if err := r.subscribeOnce(ctx); err != nil && ctx.Err() == nil {
			r.log.Warn("redis subscribe dropped, retrying", "err", err, "retry_in", redisRetryWait)
			select {
			case <-time.After(redisRetryWait):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (r *RedisBridge) publishLoop(ctx context.Context) {
	sub := r.bus.Subscribe()
	defer sub.Close()
	for {
		select {
		case event, ok := <-sub.C:
			if !ok {
				return
			}
			// Mirrored events carry an origin already; do not re-publish.
			if event.Origin != "" {
				continue
			}
			if err := r.publish(ctx, event); err != nil {
				r.log.Debug("redis publish failed", "err", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (r *RedisBridge) publish(ctx context.Context, e Event) error {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		payload = nil
	}
	body, err := json.Marshal(wireEvent{
		Topic: e.Topic, ServerID: e.ServerID, At: e.At, Payload: payload, Origin: r.origin,
	})
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pub == nil {
		conn, err := r.dial(ctx)
		if err != nil {
			return err
		}
		r.pub = conn
	}
	if err := writeCommand(r.pub, "PUBLISH", redisChannel, string(body)); err != nil {
		_ = r.pub.Close()
		r.pub = nil
		return err
	}
	// Drain the reply so the connection stays in sync.
	if _, err := bufio.NewReader(r.pub).ReadString('\n'); err != nil {
		_ = r.pub.Close()
		r.pub = nil
		return err
	}
	return nil
}

func (r *RedisBridge) subscribeOnce(ctx context.Context) error {
	conn, err := r.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if err := writeCommand(conn, "SUBSCRIBE", redisChannel); err != nil {
		return err
	}
	r.log.Info("redis event bridge connected", "addr", r.opts.Addr, "channel", redisChannel)

	reader := bufio.NewReader(conn)
	for {
		message, err := readPubSubMessage(reader)
		if err != nil {
			return err
		}
		if message == "" {
			continue
		}
		var we wireEvent
		if err := json.Unmarshal([]byte(message), &we); err != nil {
			continue
		}
		if we.Origin == r.origin {
			continue // our own publish echoed back
		}
		r.bus.Publish(Event{
			Topic: we.Topic, ServerID: we.ServerID, At: we.At,
			Payload: we.Payload, Origin: we.Origin,
		})
	}
}

func (r *RedisBridge) dial(ctx context.Context) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, redisDialWait)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", r.opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("bus: dial redis: %w", err)
	}
	reader := bufio.NewReader(conn)
	if r.opts.Password != "" {
		if err := writeCommand(conn, "AUTH", r.opts.Password); err != nil {
			conn.Close()
			return nil, err
		}
		if err := expectOK(reader); err != nil {
			conn.Close()
			return nil, fmt.Errorf("bus: redis auth: %w", err)
		}
	}
	if r.opts.DB != 0 {
		if err := writeCommand(conn, "SELECT", strconv.Itoa(r.opts.DB)); err != nil {
			conn.Close()
			return nil, err
		}
		if err := expectOK(reader); err != nil {
			conn.Close()
			return nil, fmt.Errorf("bus: redis select: %w", err)
		}
	}
	return conn, nil
}

// writeCommand emits a RESP array of bulk strings.
func writeCommand(w net.Conn, args ...string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(arg), arg)
	}
	_ = w.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := w.Write([]byte(b.String()))
	_ = w.SetWriteDeadline(time.Time{})
	return err
}

func expectOK(r *bufio.Reader) error {
	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "+OK") {
		return fmt.Errorf("unexpected reply: %s", strings.TrimSpace(line))
	}
	return nil
}

// readPubSubMessage reads one RESP push frame and returns the payload of a
// "message" delivery (empty string for subscribe confirmations).
func readPubSubMessage(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return "", nil
	}
	count, err := strconv.Atoi(line[1:])
	if err != nil || count < 0 {
		return "", fmt.Errorf("bus: malformed array header %q", line)
	}
	parts := make([]string, 0, count)
	for i := 0; i < count; i++ {
		value, err := readBulkString(r)
		if err != nil {
			return "", err
		}
		parts = append(parts, value)
	}
	// message frames are: ["message", channel, payload]
	if len(parts) == 3 && strings.EqualFold(parts[0], "message") {
		return parts[2], nil
	}
	return "", nil
}

func readBulkString(r *bufio.Reader) (string, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	header = strings.TrimRight(header, "\r\n")
	if len(header) == 0 {
		return "", fmt.Errorf("bus: empty bulk header")
	}
	switch header[0] {
	case '$':
		n, err := strconv.Atoi(header[1:])
		if err != nil {
			return "", fmt.Errorf("bus: malformed bulk header %q", header)
		}
		if n < 0 {
			return "", nil
		}
		buf := make([]byte, n+2) // payload + CRLF
		if _, err := readFull(r, buf); err != nil {
			return "", err
		}
		return string(buf[:n]), nil
	case ':', '+':
		return header[1:], nil
	default:
		return "", fmt.Errorf("bus: unexpected reply %q", header)
	}
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
