package config

import (
	"testing"
	"time"
)

func TestLoadUsesValidatedDefaults(t *testing.T) {
	t.Setenv("REPOMENDER_DATABASE_URL", "postgres://example")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddress != ":8080" || cfg.ShutdownTimeout != 10*time.Second || cfg.WorkerPoll != 2*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadRejectsMissingDatabase(t *testing.T) {
	_, err := load(func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("expected missing database error")
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL":         "postgres://example",
		"REPOMENDER_WORKER_POLL_INTERVAL": "not-a-number",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected invalid duration error")
	}
}
