package diagnosis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

type diagnosisTaskStore struct {
	created  tasks.CreateInput
	events   []tasks.RunEventInput
	evidence []tasks.EvidenceInput
	findings []tasks.FindingInput
}

func (s *diagnosisTaskStore) CreateTask(_ context.Context, input tasks.CreateInput) (tasks.Task, error) {
	s.created = input
	now := time.Now().UTC()
	return tasks.Task{ID: "task-diagnosis-1", Kind: input.Kind, Status: tasks.StatusQueued, Title: input.Title, RepositoryID: input.RepositoryID, RepositoryName: input.RepositoryName, SourceKey: input.SourceKey, Payload: input.Payload, CreatedAt: now, UpdatedAt: now}, nil
}
func (*diagnosisTaskStore) GetTask(context.Context, string) (tasks.Task, error) {
	return tasks.Task{}, tasks.ErrTaskNotFound
}
func (*diagnosisTaskStore) ListTasks(context.Context, tasks.ListFilter) ([]tasks.Task, error) {
	return nil, nil
}
func (*diagnosisTaskStore) CancelTask(context.Context, string, string) (tasks.Task, error) {
	return tasks.Task{}, tasks.ErrTaskNotFound
}
func (*diagnosisTaskStore) RetryTask(context.Context, string, string) (tasks.Task, error) {
	return tasks.Task{}, tasks.ErrTaskNotFound
}
func (*diagnosisTaskStore) ClaimTask(context.Context, string, time.Duration) (tasks.Task, tasks.Run, error) {
	return tasks.Task{}, tasks.Run{}, tasks.ErrTaskNotFound
}
func (*diagnosisTaskStore) Heartbeat(context.Context, string, string, time.Duration) error {
	return nil
}
func (*diagnosisTaskStore) CompleteTask(context.Context, string, string, tasks.Status, string, string) error {
	return nil
}
func (*diagnosisTaskStore) GetRun(context.Context, string) (tasks.Run, error) {
	return tasks.Run{}, tasks.ErrRunNotFound
}
func (*diagnosisTaskStore) ListRuns(context.Context, string) ([]tasks.Run, error) { return nil, nil }
func (*diagnosisTaskStore) ListAllRuns(context.Context, int) ([]tasks.Run, error) { return nil, nil }
func (*diagnosisTaskStore) ListRunEvents(context.Context, string, int64) ([]tasks.RunEvent, error) {
	return nil, nil
}
func (s *diagnosisTaskStore) AppendRunEvent(_ context.Context, _ string, input tasks.RunEventInput) (tasks.RunEvent, error) {
	s.events = append(s.events, input)
	return tasks.RunEvent{}, nil
}
func (*diagnosisTaskStore) ListAuditEvents(context.Context, tasks.AuditFilter) ([]tasks.AuditEvent, error) {
	return nil, nil
}
func (*diagnosisTaskStore) RecordAudit(context.Context, tasks.AuditInput) (tasks.AuditEvent, error) {
	return tasks.AuditEvent{}, nil
}
func (s *diagnosisTaskStore) CreateFinding(_ context.Context, input tasks.FindingInput) (tasks.Finding, error) {
	s.findings = append(s.findings, input)
	return tasks.Finding{}, nil
}
func (*diagnosisTaskStore) ListFindings(context.Context, string) ([]tasks.Finding, error) {
	return nil, nil
}
func (s *diagnosisTaskStore) CreateEvidence(_ context.Context, input tasks.EvidenceInput) (tasks.Evidence, error) {
	s.evidence = append(s.evidence, input)
	return tasks.Evidence{}, nil
}
func (*diagnosisTaskStore) ListEvidence(context.Context, string) ([]tasks.Evidence, error) {
	return nil, nil
}
func (*diagnosisTaskStore) SupersedeCodeReviews(context.Context, string, string, int, string) error {
	return nil
}

type diagnosisRepositoryResolver struct{}

func (diagnosisRepositoryResolver) FindRepository(context.Context, scm.Provider, string) (scm.Repository, error) {
	return scm.Repository{ID: "repository-1", Provider: scm.ProviderGitHub}, nil
}

func TestHandleWebhookCreatesIdempotentWorkflowTask(t *testing.T) {
	store := &diagnosisTaskStore{}
	service := NewService(tasks.NewService(store), diagnosisRepositoryResolver{})
	event := scm.WebhookEvent{DeliveryID: "delivery-ci-1", EventType: "workflow_run", Normalized: map[string]any{
		"action": "completed", "conclusion": "failure", "repositoryId": int64(7), "repository": "acme/payments",
		"cloneURL": "https://github.com/acme/payments.git", "webURL": "https://github.com/acme/payments/actions/runs/99",
		"workflowRunId": int64(99), "workflowName": "CI", "headSHA": "0123456789abcdef0123456789abcdef01234567", "installationId": int64(42),
	}}
	task, actionable, err := service.HandleWebhook(context.Background(), event)
	if err != nil || !actionable {
		t.Fatalf("HandleWebhook() = %+v, %v, %v", task, actionable, err)
	}
	if task.Kind != tasks.KindCIDiagnosis || store.created.SourceKey != "github:workflow_run:7:99:0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("unexpected task input: %+v", store.created)
	}
	var payload taskPayload
	if err := json.Unmarshal(store.created.Payload, &payload); err != nil || payload.WorkflowRunID != 99 || payload.InstallationID != "42" {
		t.Fatalf("payload = %+v, err=%v", payload, err)
	}
}
