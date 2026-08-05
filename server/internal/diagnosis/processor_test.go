package diagnosis

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/execution"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

type fakeWorkflowProvider struct {
	published  string
	logs       string
	fetchErr   error
	publishErr error
}

func (p *fakeWorkflowProvider) FetchGitHubWorkflowLogs(context.Context, string, string, int64) (string, error) {
	if p.fetchErr != nil {
		return "", p.fetchErr
	}
	if p.logs != "" {
		return p.logs, nil
	}
	return "TOKEN=super-secret\ncompiler failed\n", nil
}

func (p *fakeWorkflowProvider) PublishGitHubDiagnosis(_ context.Context, _, _, _, summary string) error {
	if p.publishErr != nil {
		return p.publishErr
	}
	p.published = summary
	return nil
}

type fakeExecutionAdapter struct {
	prompt string
}

func (*fakeExecutionAdapter) Health(context.Context) (execution.VersionInfo, error) {
	return execution.VersionInfo{}, nil
}

func (a *fakeExecutionAdapter) Start(_ context.Context, request execution.Request) (execution.Run, error) {
	a.prompt = request.Prompt
	return execution.Run{ID: "external-diagnosis-1", CorrelationID: request.CorrelationID}, nil
}

func (*fakeExecutionAdapter) Events(context.Context, string, uint64) (<-chan execution.Event, <-chan error) {
	events := make(chan execution.Event, 1)
	errors := make(chan error, 1)
	events <- execution.Event{Kind: execution.EventCompleted, Message: "done", Terminal: true}
	close(events)
	close(errors)
	return events, errors
}

func (*fakeExecutionAdapter) Cancel(context.Context, string, string) error { return nil }

func (*fakeExecutionAdapter) Result(context.Context, string) (execution.Result, error) {
	return execution.Result{Output: json.RawMessage(`{"schemaVersion":"v1","summary":"compiler input was invalid","hypotheses":[{"rootCause":"compiler received TOKEN=super-secret","component":"compiler","confidence":0.94,"evidence":[{"source":"workflow.log","startLine":2,"endLine":2,"excerpt":"compiler failed"}],"reproduction":"confirmed","recommendedAction":"inspect generated input"}]}`)}, nil
}

func TestProcessorRedactsBeforeEvidenceAndPrompt(t *testing.T) {
	store := &diagnosisTaskStore{}
	taskService := tasks.NewService(store)
	workflowProvider := &fakeWorkflowProvider{}
	adapter := &fakeExecutionAdapter{}
	event := WorkflowRunEvent{
		RepositoryID: "7", RepositoryName: "acme/payments", CloneURL: "https://github.com/acme/payments.git",
		WorkflowRunID: 99, WorkflowName: "CI", Conclusion: "failure", HeadSHA: "0123456789abcdef0123456789abcdef01234567", InstallationID: "42",
	}
	input, err := buildTaskInput(event, "repository-1")
	if err != nil {
		t.Fatal(err)
	}
	task := tasks.Task{ID: "task-diagnosis-1", Kind: tasks.KindCIDiagnosis, RepositoryID: "repository-1", RepositoryName: event.RepositoryName, Payload: input.Payload}
	run := tasks.Run{ID: "run-diagnosis-1", CorrelationID: "correlation-1"}
	processor := NewProcessor(taskService, adapter, workflowProvider, time.Minute, []string{"super-secret"})
	status, code, message := processor.Process(context.Background(), task, run)
	if status != tasks.StatusSucceeded || code != "" || !strings.Contains(message, "1 hypotheses") {
		t.Fatalf("Process() = %s, %s, %s", status, code, message)
	}
	if strings.Contains(adapter.prompt, "super-secret") || strings.Contains(workflowProvider.published, "super-secret") {
		t.Fatal("secret reached AC prompt or provider publication")
	}
	if len(store.evidence) == 0 || strings.Contains(string(store.evidence[0].Content), "super-secret") {
		t.Fatalf("redacted evidence missing or leaked: %+v", store.evidence)
	}
	if len(store.findings) != 1 || store.findings[0].Severity != "high" || strings.Contains(store.findings[0].Explanation, "super-secret") {
		t.Fatalf("unexpected findings: %+v", store.findings)
	}
}

func TestProcessorMapsControlledLogAndPublicationFailures(t *testing.T) {
	tests := []struct {
		name     string
		provider *fakeWorkflowProvider
		wantCode string
	}{
		{name: "missing logs", provider: &fakeWorkflowProvider{fetchErr: errors.New("workflow logs are empty")}, wantCode: "m6_logs_fetch_failed"},
		{name: "binary logs", provider: &fakeWorkflowProvider{logs: "ok\x00binary"}, wantCode: "m6_logs_binary"},
		{name: "oversized logs", provider: &fakeWorkflowProvider{logs: strings.Repeat("x", MaxLogBytes+1)}, wantCode: "m6_logs_too_large"},
		{name: "publication failure", provider: &fakeWorkflowProvider{publishErr: errors.New("GitHub API returned 429")}, wantCode: "m6_publication_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &diagnosisTaskStore{}
			service := tasks.NewService(store)
			adapter := &fakeExecutionAdapter{}
			event := WorkflowRunEvent{RepositoryID: "7", RepositoryName: "acme/payments", CloneURL: "https://github.com/acme/payments.git", WorkflowRunID: 99, WorkflowName: "CI", Conclusion: "failure", HeadSHA: "0123456789abcdef0123456789abcdef01234567", InstallationID: "42"}
			input, err := buildTaskInput(event, "repository-1")
			if err != nil {
				t.Fatal(err)
			}
			task := tasks.Task{ID: "task-diagnosis-1", Kind: tasks.KindCIDiagnosis, RepositoryID: "repository-1", RepositoryName: event.RepositoryName, Payload: input.Payload}
			status, code, _ := NewProcessor(service, adapter, test.provider, time.Minute, []string{"super-secret"}).Process(context.Background(), task, tasks.Run{ID: "run-1", CorrelationID: "correlation-1"})
			if status != tasks.StatusFailed || code != test.wantCode {
				t.Fatalf("Process() = %s, %s; want failed/%s", status, code, test.wantCode)
			}
		})
	}
}
