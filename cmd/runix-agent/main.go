package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/runix/runix/internal/agent"
	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/platform/config"
	"github.com/runix/runix/internal/platform/logger"
	"github.com/runix/runix/internal/platform/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "runix-agent:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.AgentFromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log, err := logger.New(logger.Options{Level: cfg.Log.Level, Format: cfg.Log.Format})
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}

	info := version.Get()
	log.Info("starting runix agent",
		"version", info.Version,
		"commit", info.Commit,
		"go", info.GoVersion,
		"platform", info.Platform,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	registry := rt.NewRegistry()
	if err := registerProviders(registry, cfg, log); err != nil {
		return fmt.Errorf("register providers: %w", err)
	}
	return agent.New(cfg, log, registry).Run(ctx)
}
