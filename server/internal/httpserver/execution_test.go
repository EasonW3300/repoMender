package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/execution"
	"github.com/EasonW3300/repoMender/server/internal/execution/agentcompose"
)

// The fake execution adapter isolates HTTP security and SSE normalization from
// the Agent Compose wire-contract tests.

type fakeExecutionAdapter struct {
	started   execution.Request
	cancelled bool
}

func (f *fakeExecutionAdapter) Check(context.Context) error { return nil }
func (f *fakeExecutionAdapter) Health(context.Context) (execution.VersionInfo, error) {
	return execution.VersionInfo{Version: "0", CompiledDrivers: []string{"docker"}}, nil
}
func (f *fakeExecutionAdapter) Start(_ context.Context, request execution.Request) (execution.Run, error) {
	f.started = request
	return execution.Run{ID: "run-1", CorrelationID: request.CorrelationID}, nil
}
func (f *fakeExecutionAdapter) Events(context.Context, string, uint64) (<-chan execution.Event, <-chan error) {
	events := make(chan execution.Event, 2)
	errs := make(chan error, 1)
	events <- execution.Event{
		Sequence: 7, RunID: "run-1", Kind: execution.EventLog,
		Message: "checking repository", CreatedAt: time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC),
	}
	events <- execution.Event{
		Sequence: 8, RunID: "run-1", Kind: execution.EventCompleted,
		Message: "succeeded", Terminal: true,
	}
	close(events)
	errs <- nil
	close(errs)
	return events, errs
}
func (f *fakeExecutionAdapter) Cancel(context.Context, string, string) error {
	f.cancelled = true
	return nil
}
func (f *fakeExecutionAdapter) Result(context.Context, string) (execution.Result, error) {
	return execution.Result{
		SchemaVersion: "v1", RunID: "run-1", Status: "succeeded",
		Output: json.RawMessage(`{"schemaVersion":"v1","summary":"ok"}`),
	}, nil
}

func TestExecutionDiagnosticLifecycleRequiresSessionRBACAndCSRF(t *testing.T) {
	service := auth.NewService(newAuthMemoryStore(), time.Hour)
	_, _ = service.Bootstrap(context.Background(), "admin@example.com", "correct-horse-battery")
	_, sessionToken, csrfToken, err := service.LoginLocal(context.Background(), "admin@example.com", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeExecutionAdapter{}
	handler := NewWithServices(fakeChecker{}, service, nil, nil, false, adapter).Handler()
	cookies := []*http.Cookie{{Name: sessionCookie, Value: sessionToken}}
	request := map[string]any{
		"correlationId": "corr-1", "projectId": "project-1", "agentName": "codex",
		"repository":     "https://github.com/EasonW3300/repomender-sandbox.git",
		"commitSha":      "0123456789012345678901234567890123456789",
		"prompt":         "Run the M3 diagnostic.",
		"timeoutSeconds": 300,
		"driver":         "docker",
	}

	unauthenticated := requestJSON(t, handler, http.MethodPost, "/api/v1/internal/executions", request, nil, csrfToken)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}
	withoutCSRF := requestJSON(t, handler, http.MethodPost, "/api/v1/internal/executions", request, cookies, "")
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("without CSRF status = %d", withoutCSRF.Code)
	}
	started := requestJSON(t, handler, http.MethodPost, "/api/v1/internal/executions", request, cookies, csrfToken)
	if started.Code != http.StatusAccepted || !strings.Contains(started.Body.String(), `"run-1"`) {
		t.Fatalf("start status = %d body=%s", started.Code, started.Body.String())
	}
	if adapter.started.CommitSHA != request["commitSha"] {
		t.Fatalf("adapter request = %+v", adapter.started)
	}

	stream := requestJSON(t, handler, http.MethodGet, "/api/v1/internal/executions/run-1/events?after=6", nil, cookies, "")
	if stream.Code != http.StatusOK ||
		!strings.Contains(stream.Body.String(), "event: log") ||
		!strings.Contains(stream.Body.String(), "event: completed") {
		t.Fatalf("SSE status = %d body=%s", stream.Code, stream.Body.String())
	}

	result := requestJSON(t, handler, http.MethodGet, "/api/v1/internal/executions/run-1/result", nil, cookies, "")
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"schemaVersion":"v1"`) {
		t.Fatalf("result status = %d body=%s", result.Code, result.Body.String())
	}
	cancelled := requestJSON(t, handler, http.MethodPost, "/api/v1/internal/executions/run-1/cancel",
		map[string]string{"reason": "operator requested"}, cookies, csrfToken)
	if cancelled.Code != http.StatusNoContent || !adapter.cancelled {
		t.Fatalf("cancel status = %d cancelled=%v", cancelled.Code, adapter.cancelled)
	}
}

func TestExecutionDiagnosticRejectsDeveloper(t *testing.T) {
	store := newAuthMemoryStore()
	service := auth.NewService(store, time.Hour)
	_, sessionToken, csrfToken, err := service.LoginOIDC(context.Background(), auth.Identity{
		Subject: "dev", Email: "dev@example.com", EmailVerified: true, Name: "Developer",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewWithServices(fakeChecker{}, service, nil, nil, false, &fakeExecutionAdapter{}).Handler()
	response := requestJSON(t, handler, http.MethodPost, "/api/v1/internal/executions",
		map[string]string{"prompt": "forbidden"}, []*http.Cookie{{Name: sessionCookie, Value: sessionToken}}, csrfToken)
	if response.Code != http.StatusForbidden {
		t.Fatalf("developer status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestExecutionSSEEmitsStableErrorWithoutLeakingProviderMessage(t *testing.T) {
	adapter := &errorExecutionAdapter{fakeExecutionAdapter: fakeExecutionAdapter{}}
	service := auth.NewService(newAuthMemoryStore(), time.Hour)
	_, _ = service.Bootstrap(context.Background(), "admin@example.com", "correct-horse-battery")
	_, sessionToken, _, _ := service.LoginLocal(context.Background(), "admin@example.com", "correct-horse-battery")
	handler := NewWithServices(fakeChecker{}, service, nil, nil, false, adapter).Handler()
	response := requestJSON(t, handler, http.MethodGet, "/api/v1/internal/executions/run-1/events", nil,
		[]*http.Cookie{{Name: sessionCookie, Value: sessionToken}}, "")
	if !strings.Contains(response.Body.String(), `"error":"ac_stream_dropped"`) ||
		strings.Contains(response.Body.String(), "provider secret") {
		t.Fatalf("SSE error body = %s", response.Body.String())
	}
}

type errorExecutionAdapter struct {
	fakeExecutionAdapter
}

func (f *errorExecutionAdapter) Events(context.Context, string, uint64) (<-chan execution.Event, <-chan error) {
	events := make(chan execution.Event)
	errs := make(chan error, 1)
	close(events)
	errs <- errors.Join(agentcompose.ErrStreamDropped, errors.New("provider secret"))
	close(errs)
	return events, errs
}
