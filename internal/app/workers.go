package app

import (
	"context"
	"time"

	"github.com/runix/runix/internal/modules/auth"
	"github.com/runix/runix/internal/modules/servers"
	"github.com/runix/runix/internal/modules/settings"
)

const workerInterval = time.Hour

// runWorkers drives periodic maintenance: expired-session cleanup and
// metrics retention pruning (retention window is a runtime setting).
func runWorkers(ctx context.Context, authSvc *auth.Service, serverSvc *servers.Service,
	settingsSvc *settings.Service) {

	ticker := time.NewTicker(workerInterval)
	defer ticker.Stop()

	runOnce := func() {
		authSvc.CleanupExpiredSessions(ctx)
		retentionDays := settingsSvc.Int(ctx, "metrics.retention_days", 7)
		serverSvc.PruneMetrics(ctx, time.Duration(retentionDays)*24*time.Hour)
	}

	runOnce()
	for {
		select {
		case <-ticker.C:
			runOnce()
		case <-ctx.Done():
			return
		}
	}
}
