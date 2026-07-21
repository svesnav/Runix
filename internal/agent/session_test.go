package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/platform/config"
	"github.com/runix/runix/internal/protocol"
)

// fakeControlPlane accepts one agent, answers the hello with a welcome
// carrying the given heartbeat interval, and then behaves like a real
// control plane with nobody using it: it reads whatever arrives and sends
// nothing back.
type fakeControlPlane struct {
	heartbeatSeconds int
	heartbeats       atomic.Int32
	pings            atomic.Int32
	// silent drops incoming pings on the floor, standing in for a control
	// plane that has died without closing the socket.
	silent atomic.Bool
}

func (f *fakeControlPlane) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.CloseNow()
		ctx := r.Context()

		// hello
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil || env.Type != protocol.TypeHello {
			t.Errorf("expected hello, got %q (%v)", env.Type, err)
			return
		}

		welcome, err := protocol.Marshal(protocol.TypeWelcome, "", protocol.Welcome{
			ServerID:         "test-server",
			HeartbeatSeconds: f.heartbeatSeconds,
		})
		if err != nil {
			t.Errorf("marshal welcome: %v", err)
			return
		}
		out, _ := json.Marshal(welcome)
		if err := ws.Write(ctx, websocket.MessageText, out); err != nil {
			return
		}

		// Then stay quiet, exactly like an idle control plane.
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var in protocol.Envelope
			if json.Unmarshal(data, &in) == nil && in.Type == protocol.TypeHeartbeat {
				f.heartbeats.Add(1)
			}
		}
	}
}

func testAgent(t *testing.T, url string, heartbeat time.Duration) *Agent {
	t.Helper()
	return New(config.Agent{
		ServerURL:         url,
		Token:             "test-token",
		HeartbeatInterval: heartbeat,
		DataDir:           t.TempDir(),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), rt.NewRegistry())
}

// TestIdleSessionSurvives is the regression guard for agents flapping
// online/offline every couple of minutes.
//
// Nothing flows from the control plane to an agent unless an operator asks
// for something, so the agent used to hit its own read deadline on every
// idle connection, drop a perfectly healthy socket and reconnect. The
// session must outlive that deadline — which was three heartbeats.
func TestIdleSessionSurvives(t *testing.T) {
	fake := &fakeControlPlane{heartbeatSeconds: 0} // fall back to the agent's own interval
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	const heartbeat = 150 * time.Millisecond
	agent := testAgent(t, srv.URL, heartbeat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.runSession(ctx) }()

	// Well past the old 3×heartbeat deadline.
	select {
	case err := <-done:
		t.Fatalf("session ended while idle after less than the observation window: %v", err)
	case <-time.After(10 * heartbeat):
	}

	if got := fake.heartbeats.Load(); got < 2 {
		t.Errorf("control plane saw %d heartbeats, want at least 2", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("session did not stop when its context was canceled")
	}
}

// TestSessionEndsWhenControlPlaneStopsAnswering is the other half: dropping
// the read deadline must not cost the agent its ability to notice a control
// plane that went away without closing the socket.
func TestSessionEndsWhenControlPlaneStopsAnswering(t *testing.T) {
	// A raw listener that completes the WebSocket handshake and then never
	// reads or writes again: pings go unanswered, and the TCP connection
	// stays open, so only the keepalive can detect it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			return
		}
		ctx := r.Context()
		if _, _, err := ws.Read(ctx); err != nil { // hello
			return
		}
		welcome, _ := protocol.Marshal(protocol.TypeWelcome, "", protocol.Welcome{
			ServerID: "test-server", HeartbeatSeconds: 0,
		})
		out, _ := json.Marshal(welcome)
		_ = ws.Write(ctx, websocket.MessageText, out)
		// Never read again: the library only answers pings while a read is
		// in flight, so this peer now looks alive at the TCP level and dead
		// at the protocol level.
		<-ctx.Done()
	}))
	defer srv.Close()

	agent := testAgent(t, srv.URL, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- agent.runSession(ctx) }()

	select {
	case <-done: // any error is fine; what matters is that it gave up
	case <-time.After(pingDeadline(100*time.Millisecond) + 5*time.Second):
		t.Fatal("session hung on a control plane that stopped answering")
	}
}

func TestPingDeadlineIsBounded(t *testing.T) {
	cases := []struct {
		interval time.Duration
		want     time.Duration
	}{
		{10 * time.Millisecond, minPingTimeout}, // floored
		{2 * time.Second, 4 * time.Second},      // proportional
		{30 * time.Second, maxPingTimeout},      // capped
	}
	for _, c := range cases {
		if got := pingDeadline(c.interval); got != c.want {
			t.Errorf("pingDeadline(%s) = %s, want %s", c.interval, got, c.want)
		}
	}
}

// TestWelcomeIntervalIsHonoured guards the negotiation: the control plane
// picks the heartbeat cadence, and both the heartbeat and the keepalive run
// on it.
func TestWelcomeIntervalIsHonoured(t *testing.T) {
	fake := &fakeControlPlane{heartbeatSeconds: 1}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	// A wildly different local default, to prove the server's value wins.
	agent := testAgent(t, srv.URL, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = agent.runSession(ctx) }()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if fake.heartbeats.Load() >= 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("only %d heartbeats within 4s; the welcome interval was ignored",
		fake.heartbeats.Load())
}

func TestWSURLRejectsGarbage(t *testing.T) {
	if _, err := wsURL("://nope"); err == nil {
		t.Error("expected an error for a malformed url")
	}
	got, err := wsURL("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "wss://") {
		t.Errorf("wsURL(https) = %q, want a wss:// url", got)
	}
}
