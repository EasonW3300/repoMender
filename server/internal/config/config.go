package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

// Config is the single validated configuration contract shared by API and worker.
type Config struct {
	HTTPAddress      string
	PublicURL        string
	DatabaseURL      string
	ShutdownTimeout  time.Duration
	WorkerPoll       time.Duration
	SessionTTL       time.Duration
	CookieSecure     bool
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
}

func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup func(string) (string, bool)) (Config, error) {
	cookieSecure, err := boolOrDefault(lookup, "REPOMENDER_COOKIE_SECURE", false)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		HTTPAddress:      valueOrDefault(lookup, "REPOMENDER_HTTP_ADDRESS", ":8080"),
		PublicURL:        valueOrDefault(lookup, "REPOMENDER_PUBLIC_URL", "http://localhost:8088"),
		DatabaseURL:      valueOrDefault(lookup, "REPOMENDER_DATABASE_URL", ""),
		ShutdownTimeout:  durationOrDefault(lookup, "REPOMENDER_SHUTDOWN_TIMEOUT", 10*time.Second),
		WorkerPoll:       durationOrDefault(lookup, "REPOMENDER_WORKER_POLL_INTERVAL", 2*time.Second),
		SessionTTL:       durationOrDefault(lookup, "REPOMENDER_SESSION_TTL", 8*time.Hour),
		CookieSecure:     cookieSecure,
		OIDCIssuer:       valueOrDefault(lookup, "REPOMENDER_OIDC_ISSUER", ""),
		OIDCClientID:     valueOrDefault(lookup, "REPOMENDER_OIDC_CLIENT_ID", ""),
		OIDCClientSecret: valueOrDefault(lookup, "REPOMENDER_OIDC_CLIENT_SECRET", ""),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("REPOMENDER_DATABASE_URL is required")
	}
	if cfg.ShutdownTimeout <= 0 || cfg.WorkerPoll <= 0 || cfg.SessionTTL <= 0 {
		return Config{}, errors.New("timeouts and polling intervals must be positive")
	}
	oidcValues := []string{cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret}
	configured := 0
	for _, value := range oidcValues {
		if value != "" {
			configured++
		}
	}
	if configured != 0 && configured != len(oidcValues) {
		return Config{}, errors.New("OIDC issuer, client ID, and client secret must be configured together")
	}

	return cfg, nil
}

func boolOrDefault(lookup func(string) (string, bool), key string, fallback bool) (bool, error) {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, errors.New(key + " must be true or false")
	}
	return parsed, nil
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
