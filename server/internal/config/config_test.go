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
	if cfg.FeatureM3ACExecution {
		t.Fatal("M3 execution must remain hidden until its feature flag is enabled")
	}
	if cfg.ACRequestTimeout != 10*time.Second {
		t.Fatalf("AC request timeout = %s, want 10s", cfg.ACRequestTimeout)
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

func TestLoadRequiresAgentComposeEndpointWhenM3IsEnabled(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL":            "postgres://example",
		"REPOMENDER_FEATURE_M3_AC_EXECUTION": "true",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected enabled M3 feature to require an Agent Compose endpoint")
	}
}

func TestLoadM4TasksFeatureFlag(t *testing.T) {
	lookup := map[string]string{
		"REPOMENDER_DATABASE_URL":     "postgres://localhost/repomender",
		"REPOMENDER_FEATURE_M4_TASKS": "true",
	}
	cfg, err := load(func(key string) (string, bool) {
		value, ok := lookup[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if !cfg.FeatureM4Tasks {
		t.Fatal("expected M4 task feature flag to be enabled")
	}
}

func TestLoadM5RequiresPriorModules(t *testing.T) {
	lookup := map[string]string{
		"REPOMENDER_DATABASE_URL":           "postgres://localhost/repomender",
		"REPOMENDER_FEATURE_M5_CODE_REVIEW": "true",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := lookup[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected M5 to require M2, M3, and M4")
	}
}

func TestLoadM6RequiresPriorModules(t *testing.T) {
	lookup := map[string]string{
		"REPOMENDER_DATABASE_URL":            "postgres://localhost/repomender",
		"REPOMENDER_FEATURE_M6_CI_DIAGNOSIS": "true",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := lookup[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected M6 to require M2, M3, M4, and M5")
	}
}

func TestLoadM7RequiresM4AndM6(t *testing.T) {
	lookup := map[string]string{
		"REPOMENDER_DATABASE_URL":         "postgres://localhost/repomender",
		"REPOMENDER_FEATURE_M7_APPROVALS": "true",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := lookup[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected M7 to require M4 tasks and M6 diagnosis")
	}
}

func TestLoadAcceptsAgentComposeConfiguration(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL":            "postgres://example",
		"REPOMENDER_FEATURE_M3_AC_EXECUTION": "true",
		"REPOMENDER_AC_BASE_URL":             "http://agent-compose:7410/",
		"REPOMENDER_AC_AUTH_TOKEN":           "secret",
		"REPOMENDER_AC_REQUIRED_VERSION":     "0",
		"REPOMENDER_AC_REQUIRED_DRIVER":      "docker",
		"REPOMENDER_AC_REQUEST_TIMEOUT":      "7",
	}
	cfg, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.ACBaseURL != "http://agent-compose:7410" {
		t.Fatalf("AC base URL = %q", cfg.ACBaseURL)
	}
	if cfg.ACRequestTimeout != 7*time.Second {
		t.Fatalf("AC request timeout = %s", cfg.ACRequestTimeout)
	}
}

func TestLoadRejectsInvalidAgentComposeEndpoint(t *testing.T) {
	values := map[string]string{
		"REPOMENDER_DATABASE_URL":            "postgres://example",
		"REPOMENDER_FEATURE_M3_AC_EXECUTION": "true",
		"REPOMENDER_AC_BASE_URL":             "file:///tmp/ac.sock",
	}
	_, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected non-HTTP Agent Compose endpoint to be rejected")
	}
}
