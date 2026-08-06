package config

import (
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// base64 decodes secret-aware values, while net/url and strings normalize
// external service endpoints before they are shared by the API and worker.
type Config struct {
	HTTPAddress          string
	PublicURL            string
	DatabaseURL          string
	ShutdownTimeout      time.Duration
	WorkerPoll           time.Duration
	SessionTTL           time.Duration
	CookieSecure         bool
	OIDCIssuer           string
	OIDCClientID         string
	OIDCClientSecret     string
	FeatureM2SCM         bool
	FeatureM3ACExecution bool
	FeatureM4Tasks       bool
	FeatureM5CodeReview  bool
	FeatureM6CIDiagnosis bool
	FeatureM7Approvals   bool
	FeatureGitLab        bool
	SCMMasterKey         []byte
	GitHubAppID          int64
	GitHubAppSlug        string
	GitHubPrivateKey     []byte
	GitHubPrivateKeyFile string
	GitHubWebhookSecret  string
	GitHubAPIBaseURL     string
	GitHubWebBaseURL     string
	GitLabClientID       string
	GitLabClientSecret   string
	GitLabWebhookSecret  string
	GitLabAPIBaseURL     string
	GitLabWebBaseURL     string
	ACBaseURL            string
	ACProjectID          string
	ACAgentName          string
	ACAuthToken          string
	ACRequiredVersion    string
	ACRequiredDriver     string
	ACRequestTimeout     time.Duration
	ACSensitivePatterns  []string
}

func Load() (Config, error) {
	cfg, err := load(os.LookupEnv)
	if err != nil {
		return Config{}, err
	}
	if len(cfg.GitHubPrivateKey) == 0 && cfg.GitHubPrivateKeyFile != "" {
		privateKey, err := os.ReadFile(cfg.GitHubPrivateKeyFile)
		if err != nil {
			return Config{}, errors.New("read GitHub App private key file")
		}
		cfg.GitHubPrivateKey = privateKey
	}
	return cfg, nil
}

func load(lookup func(string) (string, bool)) (Config, error) {
	cookieSecure, err := boolOrDefault(lookup, "REPOMENDER_COOKIE_SECURE", false)
	if err != nil {
		return Config{}, err
	}
	featureM2SCM, err := boolOrDefault(lookup, "REPOMENDER_FEATURE_M2_SCM", false)
	if err != nil {
		return Config{}, err
	}
	featureGitLab, err := boolOrDefault(lookup, "REPOMENDER_FEATURE_GITLAB", false)
	if err != nil {
		return Config{}, err
	}
	featureM3ACExecution, err := boolOrDefault(lookup, "REPOMENDER_FEATURE_M3_AC_EXECUTION", false)
	if err != nil {
		return Config{}, err
	}
	featureM4Tasks, err := boolOrDefault(lookup, "REPOMENDER_FEATURE_M4_TASKS", false)
	if err != nil {
		return Config{}, err
	}
	featureM5CodeReview, err := boolOrDefault(lookup, "REPOMENDER_FEATURE_M5_CODE_REVIEW", false)
	if err != nil {
		return Config{}, err
	}
	featureM6CIDiagnosis, err := boolOrDefault(lookup, "REPOMENDER_FEATURE_M6_CI_DIAGNOSIS", false)
	if err != nil {
		return Config{}, err
	}
	featureM7Approvals, err := boolOrDefault(lookup, "REPOMENDER_FEATURE_M7_APPROVALS", false)
	if err != nil {
		return Config{}, err
	}
	masterKey, err := decodeBase64Value(lookup, "REPOMENDER_MASTER_KEY")
	if err != nil {
		return Config{}, err
	}
	githubPrivateKey, err := decodeBase64Value(lookup, "REPOMENDER_GITHUB_APP_PRIVATE_KEY_B64")
	if err != nil {
		return Config{}, err
	}
	githubAppID, err := int64OrDefault(lookup, "REPOMENDER_GITHUB_APP_ID", 0)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		HTTPAddress:          valueOrDefault(lookup, "REPOMENDER_HTTP_ADDRESS", ":8080"),
		PublicURL:            valueOrDefault(lookup, "REPOMENDER_PUBLIC_URL", "http://localhost:8088"),
		DatabaseURL:          valueOrDefault(lookup, "REPOMENDER_DATABASE_URL", ""),
		ShutdownTimeout:      durationOrDefault(lookup, "REPOMENDER_SHUTDOWN_TIMEOUT", 10*time.Second),
		WorkerPoll:           durationOrDefault(lookup, "REPOMENDER_WORKER_POLL_INTERVAL", 2*time.Second),
		SessionTTL:           durationOrDefault(lookup, "REPOMENDER_SESSION_TTL", 8*time.Hour),
		CookieSecure:         cookieSecure,
		OIDCIssuer:           valueOrDefault(lookup, "REPOMENDER_OIDC_ISSUER", ""),
		OIDCClientID:         valueOrDefault(lookup, "REPOMENDER_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:     valueOrDefault(lookup, "REPOMENDER_OIDC_CLIENT_SECRET", ""),
		FeatureM2SCM:         featureM2SCM,
		FeatureM3ACExecution: featureM3ACExecution,
		FeatureM4Tasks:       featureM4Tasks,
		FeatureM5CodeReview:  featureM5CodeReview,
		FeatureM6CIDiagnosis: featureM6CIDiagnosis,
		FeatureM7Approvals:   featureM7Approvals,
		FeatureGitLab:        featureGitLab,
		SCMMasterKey:         masterKey,
		GitHubAppID:          githubAppID,
		GitHubAppSlug:        valueOrDefault(lookup, "REPOMENDER_GITHUB_APP_SLUG", ""),
		GitHubPrivateKey:     githubPrivateKey,
		GitHubPrivateKeyFile: valueOrDefault(lookup, "REPOMENDER_GITHUB_APP_PRIVATE_KEY_FILE", ""),
		GitHubWebhookSecret:  valueOrDefault(lookup, "REPOMENDER_GITHUB_WEBHOOK_SECRET", ""),
		GitHubAPIBaseURL:     valueOrDefault(lookup, "REPOMENDER_GITHUB_API_BASE_URL", "https://api.github.com"),
		GitHubWebBaseURL:     valueOrDefault(lookup, "REPOMENDER_GITHUB_WEB_BASE_URL", "https://github.com"),
		GitLabClientID:       valueOrDefault(lookup, "REPOMENDER_GITLAB_CLIENT_ID", ""),
		GitLabClientSecret:   valueOrDefault(lookup, "REPOMENDER_GITLAB_CLIENT_SECRET", ""),
		GitLabWebhookSecret:  valueOrDefault(lookup, "REPOMENDER_GITLAB_WEBHOOK_SECRET", ""),
		GitLabAPIBaseURL:     valueOrDefault(lookup, "REPOMENDER_GITLAB_API_BASE_URL", "https://gitlab.com/api/v4"),
		GitLabWebBaseURL:     valueOrDefault(lookup, "REPOMENDER_GITLAB_WEB_BASE_URL", "https://gitlab.com"),
		ACBaseURL:            strings.TrimRight(valueOrDefault(lookup, "REPOMENDER_AC_BASE_URL", ""), "/"),
		ACProjectID:          valueOrDefault(lookup, "REPOMENDER_AC_PROJECT_ID", ""),
		ACAgentName:          valueOrDefault(lookup, "REPOMENDER_AC_AGENT_NAME", "codex"),
		ACAuthToken:          valueOrDefault(lookup, "REPOMENDER_AC_AUTH_TOKEN", ""),
		ACRequiredVersion:    valueOrDefault(lookup, "REPOMENDER_AC_REQUIRED_VERSION", ""),
		ACRequiredDriver:     valueOrDefault(lookup, "REPOMENDER_AC_REQUIRED_DRIVER", "docker"),
		ACRequestTimeout:     durationOrDefault(lookup, "REPOMENDER_AC_REQUEST_TIMEOUT", 10*time.Second),
		ACSensitivePatterns:  splitNonEmpty(valueOrDefault(lookup, "REPOMENDER_AC_SENSITIVE_PATTERNS", "")),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("REPOMENDER_DATABASE_URL is required")
	}
	if cfg.ShutdownTimeout <= 0 || cfg.WorkerPoll <= 0 || cfg.SessionTTL <= 0 || cfg.ACRequestTimeout <= 0 {
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
	if cfg.FeatureM2SCM && len(cfg.SCMMasterKey) != 32 {
		return Config{}, errors.New("REPOMENDER_MASTER_KEY must decode to 32 bytes when M2 SCM is enabled")
	}
	githubValues := []bool{
		cfg.GitHubAppID > 0, cfg.GitHubAppSlug != "",
		len(cfg.GitHubPrivateKey) != 0 || cfg.GitHubPrivateKeyFile != "",
		cfg.GitHubWebhookSecret != "",
	}
	if partiallyConfigured(githubValues) {
		return Config{}, errors.New("GitHub App ID, slug, private key, and webhook secret must be configured together")
	}
	if cfg.FeatureGitLab {
		// Dormant GitLab values are intentionally ignored while the provider is
		// deferred. Enabling the feature restores strict all-or-nothing validation.
		gitlabValues := []bool{
			cfg.GitLabClientID != "", cfg.GitLabClientSecret != "", cfg.GitLabWebhookSecret != "",
		}
		if partiallyConfigured(gitlabValues) {
			return Config{}, errors.New("GitLab client ID, client secret, and webhook secret must be configured together")
		}
	}
	if cfg.FeatureM3ACExecution {
		if cfg.ACBaseURL == "" {
			return Config{}, errors.New("REPOMENDER_AC_BASE_URL is required when M3 AC execution is enabled")
		}
		endpoint, err := url.Parse(cfg.ACBaseURL)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
			return Config{}, errors.New("REPOMENDER_AC_BASE_URL must be an absolute HTTP(S) URL")
		}
	}
	if cfg.FeatureM5CodeReview && (!cfg.FeatureM2SCM || !cfg.FeatureM3ACExecution || !cfg.FeatureM4Tasks) {
		return Config{}, errors.New("REPOMENDER_FEATURE_M5_CODE_REVIEW requires M2 SCM, M3 AC execution, and M4 tasks")
	}
	if cfg.FeatureM5CodeReview && !allConfigured(githubValues) {
		return Config{}, errors.New("GitHub App configuration is required when M5 code review is enabled")
	}
	if cfg.FeatureM5CodeReview && strings.TrimSpace(cfg.ACProjectID) == "" {
		return Config{}, errors.New("REPOMENDER_AC_PROJECT_ID is required when M5 code review is enabled")
	}
	if cfg.FeatureM6CIDiagnosis && (!cfg.FeatureM2SCM || !cfg.FeatureM3ACExecution || !cfg.FeatureM4Tasks || !cfg.FeatureM5CodeReview) {
		return Config{}, errors.New("REPOMENDER_FEATURE_M6_CI_DIAGNOSIS requires M2 SCM, M3 AC execution, M4 tasks, and M5 code review")
	}
	if cfg.FeatureM6CIDiagnosis && strings.TrimSpace(cfg.ACProjectID) == "" {
		return Config{}, errors.New("REPOMENDER_AC_PROJECT_ID is required when M6 CI diagnosis is enabled")
	}
	if cfg.FeatureM7Approvals && (!cfg.FeatureM4Tasks || !cfg.FeatureM6CIDiagnosis) {
		return Config{}, errors.New("REPOMENDER_FEATURE_M7_APPROVALS requires M4 tasks and M6 CI diagnosis")
	}

	return cfg, nil
}

func partiallyConfigured(values []bool) bool {
	configured := 0
	for _, value := range values {
		if value {
			configured++
		}
	}
	return configured != 0 && configured != len(values)
}

func allConfigured(values []bool) bool {
	for _, value := range values {
		if !value {
			return false
		}
	}
	return true
}

func decodeBase64Value(lookup func(string) (string, bool), key string) ([]byte, error) {
	value, ok := lookup(key)
	if !ok || value == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New(key + " must be standard base64")
	}
	return decoded, nil
}

func int64OrDefault(lookup func(string) (string, bool), key string, fallback int64) (int64, error) {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New(key + " must be a positive integer")
	}
	return parsed, nil
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

func splitNonEmpty(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}
