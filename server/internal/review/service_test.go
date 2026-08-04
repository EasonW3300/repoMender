package review

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

type reviewTaskStore struct {
	created    tasks.CreateInput
	superseded bool
}

func (s *reviewTaskStore) CreateTask(_ context.Context, input tasks.CreateInput) (tasks.Task, error) {
	s.created = input
	now := time.Now().UTC()
	return tasks.Task{ID: "task-review-1", Kind: input.Kind, Status: tasks.StatusQueued,
		Title: input.Title, RepositoryID: input.RepositoryID, RepositoryName: input.RepositoryName,
		SourceKey: input.SourceKey, Payload: input.Payload, CreatedAt: now, UpdatedAt: now}, nil
}
func (*reviewTaskStore) GetTask(context.Context, string) (tasks.Task, error) {
	return tasks.Task{}, tasks.ErrTaskNotFound
}
func (*reviewTaskStore) ListTasks(context.Context, tasks.ListFilter) ([]tasks.Task, error) {
	return nil, nil
}
func (*reviewTaskStore) CancelTask(context.Context, string, string) (tasks.Task, error) {
	return tasks.Task{}, tasks.ErrTaskNotFound
}
func (*reviewTaskStore) RetryTask(context.Context, string, string) (tasks.Task, error) {
	return tasks.Task{}, tasks.ErrTaskNotFound
}
func (*reviewTaskStore) ClaimTask(context.Context, string, time.Duration) (tasks.Task, tasks.Run, error) {
	return tasks.Task{}, tasks.Run{}, tasks.ErrTaskNotFound
}
func (*reviewTaskStore) Heartbeat(context.Context, string, string, time.Duration) error { return nil }
func (*reviewTaskStore) CompleteTask(context.Context, string, string, tasks.Status, string, string) error {
	return nil
}
func (*reviewTaskStore) GetRun(context.Context, string) (tasks.Run, error) {
	return tasks.Run{}, tasks.ErrRunNotFound
}
func (*reviewTaskStore) ListRuns(context.Context, string) ([]tasks.Run, error) { return nil, nil }
func (*reviewTaskStore) ListAllRuns(context.Context, int) ([]tasks.Run, error) { return nil, nil }
func (*reviewTaskStore) ListRunEvents(context.Context, string, int64) ([]tasks.RunEvent, error) {
	return nil, nil
}
func (*reviewTaskStore) AppendRunEvent(context.Context, string, tasks.RunEventInput) (tasks.RunEvent, error) {
	return tasks.RunEvent{}, nil
}
func (*reviewTaskStore) ListAuditEvents(context.Context, tasks.AuditFilter) ([]tasks.AuditEvent, error) {
	return nil, nil
}
func (*reviewTaskStore) RecordAudit(context.Context, tasks.AuditInput) (tasks.AuditEvent, error) {
	return tasks.AuditEvent{}, nil
}
func (*reviewTaskStore) CreateFinding(context.Context, tasks.FindingInput) (tasks.Finding, error) {
	return tasks.Finding{}, nil
}
func (*reviewTaskStore) ListFindings(context.Context, string) ([]tasks.Finding, error) {
	return nil, nil
}
func (*reviewTaskStore) CreateEvidence(context.Context, tasks.EvidenceInput) (tasks.Evidence, error) {
	return tasks.Evidence{}, nil
}
func (*reviewTaskStore) ListEvidence(context.Context, string) ([]tasks.Evidence, error) {
	return nil, nil
}
func (s *reviewTaskStore) SupersedeCodeReviews(context.Context, string, string, int, string) error {
	s.superseded = true
	return nil
}

type reviewRepositoryResolver struct{}

func (reviewRepositoryResolver) FindRepository(context.Context, scm.Provider, string) (scm.Repository, error) {
	return scm.Repository{ID: "repository-1", Provider: scm.ProviderGitHub}, nil
}

func TestHandleWebhookCreatesImmutableReviewTask(t *testing.T) {
	store := &reviewTaskStore{}
	service := NewService(tasks.NewService(store), reviewRepositoryResolver{})
	event := scm.WebhookEvent{DeliveryID: "delivery-1", EventType: "pull_request", Normalized: map[string]any{
		"action": "opened", "repositoryId": int64(7), "repository": "acme/payments",
		"cloneURL": "https://github.com/acme/payments.git", "webURL": "https://github.com/acme/payments/pull/12",
		"pullRequestNumber": 12, "baseBranch": "main", "headBranch": "feature/fix",
		"headSHA": "0123456789abcdef0123456789abcdef01234567", "installationId": int64(42),
	}}
	task, actionable, err := service.HandleWebhook(context.Background(), event)
	if err != nil || !actionable {
		t.Fatalf("HandleWebhook() = %+v, %v, %v", task, actionable, err)
	}
	if task.Kind != tasks.KindCodeReview || store.created.SourceKey != "github:pull_request:7:12:0123456789abcdef0123456789abcdef01234567" || !store.superseded {
		t.Fatalf("unexpected task input: %+v, superseded=%v", store.created, store.superseded)
	}
	var payload taskPayload
	if err := json.Unmarshal(store.created.Payload, &payload); err != nil || payload.HeadSHA != "0123456789abcdef0123456789abcdef01234567" || payload.InstallationID != "42" {
		t.Fatalf("payload = %+v, err=%v", payload, err)
	}
}
