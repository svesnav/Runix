package main

import (
	"log/slog"
	"path/filepath"

	"github.com/runix/runix/internal/agent/providers/compose"
	"github.com/runix/runix/internal/agent/providers/daemon"
	"github.com/runix/runix/internal/agent/providers/docker"
	"github.com/runix/runix/internal/agent/providers/plugin"
	"github.com/runix/runix/internal/agent/providers/systemd"
	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/platform/config"
)

// registerProviders wires every runtime provider compiled into this agent.
// Providers self-report availability, so registering one on a host without
// the underlying technology is correct: the API explains why it is unusable.
func registerProviders(registry *rt.Registry, cfg config.Agent, log *slog.Logger) error {
	providers := []rt.Provider{
		docker.NewProvider(""),
		compose.NewProvider(filepath.Join(cfg.DataDir, "compose")),
		systemd.NewProvider(),
	}
	daemonProvider, err := daemon.NewProvider(cfg.DataDir, log)
	if err != nil {
		return err
	}
	providers = append(providers, daemonProvider)

	// Plugins are external programs: a bad manifest disables that plugin,
	// it never prevents the agent from starting.
	manifests, err := plugin.Discover(filepath.Join(cfg.DataDir, "plugins"))
	if err != nil {
		log.Error("loading plugins failed", "err", err)
	}
	for _, manifest := range manifests {
		log.Info("loaded plugin", "name", manifest.Name,
			"runtime_type", manifest.RuntimeType, "version", manifest.Version)
		providers = append(providers, plugin.NewProvider(manifest))
	}

	for _, p := range providers {
		if err := registry.Register(p); err != nil {
			log.Error("registering provider failed", "type", p.Type(), "err", err)
		}
	}
	return nil
}
