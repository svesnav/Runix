package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/runix/runix/internal/modules/rbac"
	"github.com/runix/runix/internal/modules/users"
	"github.com/runix/runix/internal/platform/config"
	"github.com/runix/runix/internal/platform/crypto"
)

// seedAdmin creates the initial administrator on an empty installation.
// The password comes from RUNIX_ADMIN_PASSWORD or is generated and printed
// once; either way the first login forces a change.
func seedAdmin(ctx context.Context, cfg config.Server, repo users.Repository,
	userSvc *users.Service, rbacSvc *rbac.Service, log *slog.Logger) error {

	exists, err := repo.Any(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	password := cfg.Auth.AdminPassword
	generated := false
	if password == "" {
		password, err = crypto.RandomToken(16)
		if err != nil {
			return fmt.Errorf("seed admin: generate password: %w", err)
		}
		generated = true
	}

	admin, err := userSvc.Create(ctx, users.CreateInput{
		Username:           "admin",
		Email:              "admin@runix.local",
		DisplayName:        "Administrator",
		Password:           password,
		MustChangePassword: true,
	})
	if err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}

	role, err := rbacSvc.ListRoles(ctx)
	if err != nil {
		return err
	}
	for _, r := range role {
		if r.Key == rbac.RoleAdmin {
			if err := rbacSvc.SetUserRoles(ctx, admin.ID, []string{r.ID}); err != nil {
				return fmt.Errorf("seed admin: assign role: %w", err)
			}
			break
		}
	}

	if generated {
		log.Warn("created initial admin user with a GENERATED password — change it on first login",
			"username", "admin", "password", password)
	} else {
		log.Info("created initial admin user", "username", "admin")
	}
	return nil
}
