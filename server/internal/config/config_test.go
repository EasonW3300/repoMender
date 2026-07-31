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
	if cfg.HTTPAddress != ":8080" || cfg.ShutdownTimeout != 10*time.Second || cfg.WorkerPoll != 2*time.Second || cfg.SessionTTL != 8*time.Hour {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.FeatureGitLab {
		t.Fatal("GitLab must remain deferred unless its dedicated feature flag is enabled")
	}
}

func TestLoadRejectsPartialOIDCConfiguration(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL": "postgres://example",
		"REPOMENDER_OIDC_ISSUER":  "https://identity.example.com",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected partial OIDC configuration error")
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

func TestLoadRejectsInvalidCookieSecurityFlag(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL":  "postgres://example",
		"REPOMENDER_COOKIE_SECURE": "tru",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected invalid cookie security flag error")
	}
}

func TestLoadRequiresMasterKeyWhenM2SCMIsEnabled(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL":   "postgres://example",
		"REPOMENDER_FEATURE_M2_SCM": "true",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected missing SCM master key error")
	}
}

func TestLoadRejectsPartialSCMProviderConfiguration(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL":  "postgres://example",
		"REPOMENDER_GITHUB_APP_ID": "123",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected partial GitHub App configuration error")
	}
}

func TestLoadIgnoresDormantPartialGitLabConfiguration(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL":     "postgres://example",
		"REPOMENDER_GITLAB_CLIENT_ID": "preserved-client-id",
	}
	cfg, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("deferred GitLab configuration blocked startup: %v", err)
	}
	if cfg.FeatureGitLab {
		t.Fatal("GitLab unexpectedly enabled")
	}
}

func TestLoadValidatesGitLabConfigurationWhenFeatureIsEnabled(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL":     "postgres://example",
		"REPOMENDER_FEATURE_GITLAB":   "true",
		"REPOMENDER_GITLAB_CLIENT_ID": "partial-client-id",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected enabled GitLab feature to reject partial credentials")
	}
}
