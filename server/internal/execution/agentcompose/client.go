package agentcompose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/execution"
)

// json and net/http implement the stable AC transport, while sync tracks
// per-run deadlines without importing Agent Compose internals into RepoMender.

var (
	ErrUnavailable       = errors.New("ac_unavailable")
	ErrRejected          = errors.New("ac_rejected")
	ErrIncompatible      = errors.New("ac_incompatible")
	ErrTimeout           = errors.New("ac_timeout")
	ErrCancelled         = errors.New("ac_cancelled")
	ErrSandboxFailed     = errors.New("ac_sandbox_failed")
	ErrAgentFailed       = errors.New("ac_agent_failed")
	ErrMalformedResponse = errors.New("ac_malformed_response")
	ErrStreamDropped     = errors.New("ac_stream_dropped")
)

type Config struct {
	BaseURL           string
	AuthToken         string
	RequiredVersion   string
	RequiredDriver    string
	RequestTimeout    time.Duration
	SensitivePatterns []string
}

type daemonVersionInfo struct {
	Version         string   `json:"version"`
	Timestamp       float64  `json:"timestamp"`
	Timezone        string   `json:"timezone"`
	TimezoneOffset  int      `json:"timezone_offset"`
	OS              string   `json:"os"`
	Arch            string   `json:"arch"`
	CompiledDrivers []string `json:"compiled_drivers"`
}

type versionEnvelope struct {
	Error json.RawMessage   `json:"err"`
	Msg   string            `json:"msg"`
	Data  daemonVersionInfo `json:"data"`
}

type Client struct {
	baseURL         string
	authToken       string
	requiredVersion string
	requiredDriver  string
	httpClient      *http.Client
	streamClient    *http.Client
	redactor        *redactor
	deadlines       sync.Map
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("Agent Compose base URL is required")
	}
	if cfg.RequestTimeout <= 0 {
		return nil, errors.New("Agent Compose request timeout must be positive")
	}
	return &Client{
		baseURL:         strings.TrimRight(cfg.BaseURL, "/"),
		authToken:       cfg.AuthToken,
		requiredVersion: strings.TrimSpace(cfg.RequiredVersion),
		requiredDriver:  strings.TrimSpace(cfg.RequiredDriver),
		httpClient:      &http.Client{Timeout: cfg.RequestTimeout},
		streamClient:    &http.Client{},
		redactor:        newRedactor(append([]string{cfg.AuthToken}, cfg.SensitivePatterns...)),
	}, nil
}

// Health verifies transport, authentication, the documented version envelope,
// and explicitly configured compatibility constraints. Agent Compose currently
// reports a build version rather than an API version, so an empty version
// constraint intentionally accepts development builds such as version "0".
func (c *Client) Health(ctx context.Context) (execution.VersionInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return execution.VersionInfo{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return execution.VersionInfo{}, fmt.Errorf("%w: %v", ErrCancelled, err)
		}
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
			return execution.VersionInfo{}, fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return execution.VersionInfo{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return execution.VersionInfo{}, fmt.Errorf("%w: HTTP %d", ErrRejected, response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return execution.VersionInfo{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return execution.VersionInfo{}, fmt.Errorf("%w: %v", ErrMalformedResponse, err)
	}
	var envelope versionEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return execution.VersionInfo{}, fmt.Errorf("%w: %v", ErrMalformedResponse, err)
	}
	if !isNullJSON(envelope.Error) {
		return execution.VersionInfo{}, fmt.Errorf("%w: daemon returned %s", ErrRejected, strings.TrimSpace(string(envelope.Error)))
	}
	if strings.TrimSpace(envelope.Data.Version) == "" {
		return execution.VersionInfo{}, fmt.Errorf("%w: version is missing", ErrMalformedResponse)
	}
	if c.requiredVersion != "" && envelope.Data.Version != c.requiredVersion {
		return execution.VersionInfo{}, fmt.Errorf("%w: version %q does not match %q", ErrIncompatible, envelope.Data.Version, c.requiredVersion)
	}
	if c.requiredDriver != "" && !contains(envelope.Data.CompiledDrivers, c.requiredDriver) {
		return execution.VersionInfo{}, fmt.Errorf("%w: required driver %q is unavailable", ErrIncompatible, c.requiredDriver)
	}
	return execution.VersionInfo{
		Version: envelope.Data.Version, OS: envelope.Data.OS, Arch: envelope.Data.Arch,
		CompiledDrivers: append([]string(nil), envelope.Data.CompiledDrivers...),
	}, nil
}

// Check lets the AC client participate in the API readiness dependency list.
func (c *Client) Check(ctx context.Context) error {
	_, err := c.Health(ctx)
	return err
}

func isNullJSON(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed == "" || trimmed == "null"
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
