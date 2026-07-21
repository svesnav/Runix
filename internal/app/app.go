// Package app is the composition root of the control plane: it connects
// infrastructure, wires every module's repository/service/handler chain,
// mounts routes, seeds baseline data and runs background workers. No
// business logic lives here — only assembly.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/runix/runix/internal/modules/agents"
	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/modules/auth"
	"github.com/runix/runix/internal/modules/backup"
	"github.com/runix/runix/internal/modules/dashboard"
	"github.com/runix/runix/internal/modules/dockerres"
	"github.com/runix/runix/internal/modules/files"
	"github.com/runix/runix/internal/modules/metrics"
	"github.com/runix/runix/internal/modules/notifications"
	"github.com/runix/runix/internal/modules/plugins"
	"github.com/runix/runix/internal/modules/rbac"
	"github.com/runix/runix/internal/modules/runtimes"
	"github.com/runix/runix/internal/modules/scheduler"
	"github.com/runix/runix/internal/modules/servers"
	"github.com/runix/runix/internal/modules/settings"
	"github.com/runix/runix/internal/modules/terminal"
	"github.com/runix/runix/internal/modules/users"
	"github.com/runix/runix/internal/platform/bus"
	"github.com/runix/runix/internal/platform/config"
	"github.com/runix/runix/internal/platform/crypto"
	"github.com/runix/runix/internal/platform/database"
	"github.com/runix/runix/internal/platform/token"
	"github.com/runix/runix/internal/server"
	"github.com/runix/runix/migrations"
)

// Run assembles and serves the control plane until ctx is canceled.
func Run(ctx context.Context, cfg config.Server, log *slog.Logger) error {
	if cfg.Auth.GeneratedSecrets {
		log.Warn("using generated ephemeral secrets; set RUNIX_JWT_SECRET and RUNIX_ENCRYPTION_KEY for persistent sessions")
	}
	if cfg.Database.DSN == "" {
		return errors.New("RUNIX_DATABASE_DSN is required")
	}

	pool, err := database.Connect(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool, migrations.FS, log); err != nil {
		return err
	}

	// Platform services.
	eventBus := bus.New()
	tokens, err := token.NewManager(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenTTL)
	if err != nil {
		return err
	}
	sealer, err := crypto.NewSealer(cfg.Auth.EncryptionKey)
	if err != nil {
		return err
	}

	// Repositories.
	userRepo := users.NewRepository(pool)
	rbacRepo := rbac.NewRepository(pool)
	auditRepo := audit.NewRepository(pool)
	authRepo := auth.NewRepository(pool)
	serverRepo := servers.NewRepository(pool)
	settingsRepo := settings.NewRepository(pool)

	// Services.
	auditSvc := audit.NewService(auditRepo, log)
	serverSvc := servers.NewService(serverRepo, eventBus, log)
	rbacSvc := rbac.NewService(rbacRepo, serverSvc, log)
	userSvc := users.NewService(userRepo, rbacSvc)
	settingsSvc := settings.NewService(settingsRepo)
	authSvc, err := auth.NewService(authRepo, userRepo, rbacSvc, tokens, sealer, auth.Config{
		RefreshTTL:  cfg.Auth.RefreshTokenTTL,
		RememberTTL: cfg.Auth.RememberTokenTTL,
	}, log)
	if err != nil {
		return err
	}
	hub := agents.NewHub(serverDirectory{svc: serverSvc}, eventBus, log)
	dashSvc := dashboard.NewService(eventBus)
	schedulerSvc := scheduler.NewService(scheduler.NewRepository(pool), hub, auditSvc, log)
	backupSvc := backup.NewService(serverSvc, rbacSvc, schedulerSvc, settingsSvc, log)

	// Seeding and reconciliation.
	if err := rbacSvc.Seed(ctx); err != nil {
		return err
	}
	if err := seedAdmin(ctx, cfg, userRepo, userSvc, rbacSvc, log); err != nil {
		return err
	}
	if err := serverSvc.ReconcilePresence(ctx); err != nil {
		return fmt.Errorf("reconcile presence: %w", err)
	}

	// HTTP assembly.
	srv := server.New(cfg, log)
	if err := srv.Health().Register("database", pool.Ping); err != nil {
		return err
	}

	api := srv.API()

	authHandler := auth.NewHandler(authSvc, rbacSvc, userRepo, auditSvc)
	auth.RegisterPublicRoutes(api, authHandler)
	agents.RegisterRoutes(api, hub) // agents authenticate with their own tokens

	protected := api.Group("", auth.Middleware(authSvc))
	auth.RegisterProtectedRoutes(protected, authHandler)
	users.RegisterRoutes(protected, users.NewHandler(userSvc, auditSvc), rbacSvc.Require)
	rbac.RegisterRoutes(protected, rbac.NewHandler(rbacSvc, auditSvc), rbacSvc)
	audit.RegisterRoutes(protected, audit.NewHandler(auditSvc), rbacSvc.Require)
	settings.RegisterRoutes(protected, settings.NewHandler(settingsSvc, auditSvc), rbacSvc.Require)
	servers.RegisterRoutes(protected, servers.NewHandler(serverSvc, auditSvc),
		rbacSvc.Require, rbacSvc.RequireServer)

	// A request addressing one runtime is checked against that runtime
	// first, then falls back to the server. Without the first check a
	// runtime-scoped grant would be stored and never honored.
	serverPermCheck := func(ctx context.Context, userID, perm string, target runtimes.Target) (bool, error) {
		if target.RuntimeType != "" && target.RuntimeID != "" {
			allowed, err := rbacSvc.Check(ctx, userID, perm,
				rbac.RuntimeScope(target.ServerID, target.RuntimeType, target.RuntimeID))
			if err != nil || allowed {
				return allowed, err
			}
		}
		return rbacSvc.Check(ctx, userID, perm, rbac.ServerScope(target.ServerID))
	}
	runtimes.RegisterRoutes(protected, runtimes.NewHandler(hub, serverPermCheck, auditSvc))
	dockerres.RegisterRoutes(protected, dockerres.NewHandler(hub, serverPermCheck, auditSvc))
	files.RegisterRoutes(protected, files.NewHandler(hub, serverPermCheck, auditSvc))
	terminal.RegisterRoutes(protected, terminal.NewHandler(hub, serverPermCheck, auditSvc))
	metrics.RegisterRoutes(protected, metrics.NewHandler(serverSvc, eventBus), rbacSvc.RequireServer)
	dashboard.RegisterRoutes(protected, dashboard.NewHandler(dashSvc, serverSvc, hub), rbacSvc.Require)
	notifications.RegisterRoutes(protected, notifications.NewHandler(eventBus), rbacSvc.Require)
	scheduler.RegisterRoutes(protected, scheduler.NewHandler(schedulerSvc, auditSvc), rbacSvc.Require)
	backup.RegisterRoutes(protected, backup.NewHandler(backupSvc, auditSvc), rbacSvc.Require)
	plugins.RegisterRoutes(protected, plugins.NewHandler(hub, serverPermCheck), rbacSvc.Require)

	// Background workers.
	go dashSvc.Run(ctx)
	go runWorkers(ctx, authSvc, serverSvc, settingsSvc)
	go schedulerSvc.Run(ctx)

	// With Redis configured the event bus spans every control-plane
	// instance, so browsers see events raised wherever an agent is attached.
	if cfg.Redis.Addr != "" {
		bridge := bus.NewRedisBridge(eventBus, bus.RedisOptions{
			Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB,
		}, uuid.NewString(), log)
		go bridge.Run(ctx)
	} else {
		log.Info("RUNIX_REDIS_ADDR is not set; events stay local to this instance")
	}

	return srv.Run(ctx)
}
