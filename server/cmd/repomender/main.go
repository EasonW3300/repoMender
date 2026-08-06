package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/config"
	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/EasonW3300/repoMender/server/internal/diagnosis"
	"github.com/EasonW3300/repoMender/server/internal/execution/agentcompose"
	"github.com/EasonW3300/repoMender/server/internal/httpserver"
	"github.com/EasonW3300/repoMender/server/internal/review"
	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
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
		if cfg.FeatureM4Tasks {
			service := tasks.NewService(tasks.NewPostgreSQLStore(db))
			lease := cfg.WorkerPoll * 3
			if lease < 5*time.Second {
				lease = 5 * time.Second
			}
			processor := worker.NewMultiplexProcessor(worker.NewCoreProcessor(service))
			if cfg.FeatureM5CodeReview || cfg.FeatureM6CIDiagnosis {
				if !cfg.FeatureM3ACExecution {
					return errors.New("M5/M6 execution requires M3 AC execution")
				}
				acClient, err := agentcompose.New(agentcompose.Config{
					BaseURL: cfg.ACBaseURL, AuthToken: cfg.ACAuthToken,
					RequiredVersion: cfg.ACRequiredVersion, RequiredDriver: cfg.ACRequiredDriver,
					RequestTimeout: cfg.ACRequestTimeout, SensitivePatterns: cfg.ACSensitivePatterns,
				})
				if err != nil {
					return err
				}
				githubService, err := newGitHubService(cfg, db)
				if err != nil {
					return err
				}
				if cfg.FeatureM5CodeReview {
					processor.Register(tasks.KindCodeReview,
						review.NewProcessor(service, acClient, 15*time.Minute, githubService).
							WithProjectID(cfg.ACProjectID).WithAgentName(cfg.ACAgentName))
				}
				if cfg.FeatureM6CIDiagnosis {
					processor.Register(tasks.KindCIDiagnosis,
						diagnosis.NewProcessor(service, acClient, githubService, 15*time.Minute, cfg.ACSensitivePatterns).
							WithProjectID(cfg.ACProjectID).WithAgentName(cfg.ACAgentName))
				}
			}
			return worker.RunQueue(ctx, cfg.WorkerPoll, lease, workerOwner(), service, processor)
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

func newGitHubService(cfg config.Config, db *database.DB) (*scm.Service, error) {
	box, err := scm.NewSecretBox(cfg.SCMMasterKey)
	if err != nil {
		return nil, err
	}
	github, err := scm.NewGitHubAdapter(scm.GitHubConfig{
		AppID: cfg.GitHubAppID, Slug: cfg.GitHubAppSlug,
		PrivateKeyPEM: cfg.GitHubPrivateKey, WebhookSecret: cfg.GitHubWebhookSecret,
		APIBaseURL: cfg.GitHubAPIBaseURL, WebBaseURL: cfg.GitHubWebBaseURL,
	}, nil)
	if err != nil {
		return nil, err
	}
	return scm.NewService(scm.NewPostgreSQLStore(db), box, github), nil
}

func workerOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}
