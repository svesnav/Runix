// Package agent implements the on-host process: it dials the control plane,
// reports host facts and metrics, and executes runtime/file/terminal
// operations against the providers available on its machine. It holds no
// business logic — every decision is made by the control plane.
package agent

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/platform/config"
)

type Agent struct {
	cfg      config.Agent
	log      *slog.Logger
	registry *rt.Registry
	rpc      *rpcRegistry
}

func New(cfg config.Agent, log *slog.Logger, registry *rt.Registry) *Agent {
	a := &Agent{cfg: cfg, log: log, registry: registry, rpc: newRPCRegistry()}
	registerCoreHandlers(a.rpc, registry)
	registerFSHandlers(a.rpc)
	registerArchiveHandlers(a.rpc)
	registerTransferHandlers(a.rpc)
	registerDockerResourceHandlers(a.rpc, registry)
	registerTerminalHandlers(a.rpc, registry)
	registerConsoleHandlers(a.rpc, registry)
	registerUpdateHandlers(a.rpc, log)
	registerPluginHandlers(a.rpc, registry)
	return a
}

// Run maintains the control-plane connection until ctx is canceled,
// reconnecting with capped exponential backoff.
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("agent started", "server_url", a.cfg.ServerURL)
	backoff := time.Second
	for {
		started := time.Now()
		err := a.runSession(ctx)
		if ctx.Err() != nil {
			a.log.Info("agent stopping")
			return nil
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		a.log.Warn("control-plane connection lost", "err", err, "retry_in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			a.log.Info("agent stopping")
			return nil
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// wsURL converts the configured server URL into the agent WebSocket URL.
func wsURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/v1/agents/ws"
	return u.String(), nil
}
