package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/config"
)

// The worker depends only on a database health contract in M0. Later modules
// will add transactional outbox claiming behind this stable process boundary.

type HealthChecker interface {
	Ping(context.Context) error
}

func Run(ctx context.Context, cfg config.Config, checker HealthChecker) error {
	ticker := time.NewTicker(cfg.WorkerPoll)
	defer ticker.Stop()

	slog.Info("RepoMender worker started", "poll_interval", cfg.WorkerPoll)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := checker.Ping(ctx); err != nil {
				return fmt.Errorf("worker database health check: %w", err)
			}
		}
	}
}
