package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

// Config is the single validated configuration contract shared by API and worker.
type Config struct {
	HTTPAddress     string
	DatabaseURL     string
	ShutdownTimeout time.Duration
	WorkerPoll      time.Duration
}

func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		HTTPAddress:     valueOrDefault(lookup, "REPOMENDER_HTTP_ADDRESS", ":8080"),
		DatabaseURL:     valueOrDefault(lookup, "REPOMENDER_DATABASE_URL", ""),
		ShutdownTimeout: durationOrDefault(lookup, "REPOMENDER_SHUTDOWN_TIMEOUT", 10*time.Second),
		WorkerPoll:      durationOrDefault(lookup, "REPOMENDER_WORKER_POLL_INTERVAL", 2*time.Second),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("REPOMENDER_DATABASE_URL is required")
	}
	if cfg.ShutdownTimeout <= 0 || cfg.WorkerPoll <= 0 {
		return Config{}, errors.New("timeouts and polling intervals must be positive")
	}

	return cfg, nil
}

func valueOrDefault(lookup func(string) (string, bool), key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}

func durationOrDefault(lookup func(string) (string, bool), key string, fallback time.Duration) time.Duration {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return time.Duration(seconds) * time.Second
}
