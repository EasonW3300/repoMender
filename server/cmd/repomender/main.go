package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/EasonW3300/repoMender/server/internal/config"
	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/EasonW3300/repoMender/server/internal/httpserver"
	"github.com/EasonW3300/repoMender/server/internal/worker"
)

// The internal packages separate configuration, persistence, HTTP delivery, and
// background execution so later business modules can evolve behind stable boundaries.

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("repomender stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: repomender <api|worker|migrate|healthcheck>")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer db.Close()

	switch args[0] {
	case "api":
		if err := database.MigrateUp(ctx, db); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}
		return httpserver.Run(ctx, cfg, db)
	case "worker":
		if err := database.MigrateUp(ctx, db); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}
		return worker.Run(ctx, cfg, db)
	case "migrate":
		direction := "up"
		if len(args) > 1 {
			direction = args[1]
		}
		if direction == "up" {
			return database.MigrateUp(ctx, db)
		}
		if direction == "down" {
			return database.MigrateDown(ctx, db)
		}
		return fmt.Errorf("unknown migration direction %q", direction)
	case "healthcheck":
		return db.Ping(ctx)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
