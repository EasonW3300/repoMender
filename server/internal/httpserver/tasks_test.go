package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

type taskMemoryStore struct {
	mu      sync.Mutex
	tasks   map[string]tasks.Task
	audit   []tasks.AuditEvent
	created int
}

func newTaskMemoryStore() *taskMemoryStore {
	return &taskMemoryStore{tasks: map[string]tasks.Task{}}
}

func (m *taskMemoryStore) CreateTask(_ context.Context, input tasks.CreateInput) (tasks.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.tasks {
		if current.SourceKey == input.SourceKey {
			return current, nil
		}
	}
	m.created++
	now := time.Now().UTC()
	task := tasks.Task{ID: "task-" + string(rune('0'+m.created)), Kind: input.Kind, Status: tasks.StatusQueued, Title: input.Title, RepositoryName: input.RepositoryName, SourceKey: input.SourceKey, Payload: input.Payload, MaxAttempts: 3, CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now, AvailableAt: now}
	m.tasks[task.ID] = task
	return task, nil
}

func (m *taskMemoryStore) GetTask(_ context.Context, id string) (tasks.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return tasks.Task{}, tasks.ErrTaskNotFound
	}
	return task, nil
}

func (m *taskMemoryStore) ListTasks(_ context.Context, _ tasks.ListFilter) ([]tasks.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]tasks.Task, 0, len(m.tasks))
	for _, task := range m.tasks {
		result = append(result, task)
	}
	return result, nil
}

func (m *taskMemoryStore) CancelTask(_ context.Context, id, reason string) (tasks.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return tasks.Task{}, tasks.ErrTaskNotFound
	}
	if !tasks.CanTransition(task.Status, tasks.StatusCancelled) {
		return tasks.Task{}, tasks.ErrInvalidTransition
	}
	task.Status, task.LastError = tasks.StatusCancelled, reason
	m.tasks[id] = task
	return task, nil
}

func (m *taskMemoryStore) RetryTask(_ context.Context, id, reason string) (tasks.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return tasks.Task{}, tasks.ErrTaskNotFound
	}
	if !tasks.CanTransition(task.Status, tasks.StatusQueued) {
		return tasks.Task{}, tasks.ErrInvalidTransition
	}
	task.Status, task.LastError = tasks.StatusQueued, reason
	m.tasks[id] = task
	return task, nil
}

func (m *taskMemoryStore) ClaimTask(context.Context, string, time.Duration) (tasks.Task, tasks.Run, error) {
	return tasks.Task{}, tasks.Run{}, tasks.ErrTaskNotFound
}
func (m *taskMemoryStore) Heartbeat(context.Context, string, string, time.Duration) error { return nil }
func (m *taskMemoryStore) CompleteTask(context.Context, string, string, tasks.Status, string, string) error {
	return nil
}
func (m *taskMemoryStore) GetRun(context.Context, string) (tasks.Run, error) {
	return tasks.Run{}, tasks.ErrRunNotFound
}
func (m *taskMemoryStore) ListRuns(context.Context, string) ([]tasks.Run, error) {
	return []tasks.Run{}, nil
}
func (m *taskMemoryStore) ListAllRuns(context.Context, int) ([]tasks.Run, error) {
	return []tasks.Run{}, nil
}
func (m *taskMemoryStore) ListRunEvents(context.Context, string, int64) ([]tasks.RunEvent, error) {
	return []tasks.RunEvent{}, nil
}
func (m *taskMemoryStore) AppendRunEvent(context.Context, string, tasks.RunEventInput) (tasks.RunEvent, error) {
	return tasks.RunEvent{}, nil
}

func (m *taskMemoryStore) ListAuditEvents(_ context.Context, filter tasks.AuditFilter) ([]tasks.AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]tasks.AuditEvent, 0)
	for _, event := range m.audit {
		if filter.ResourceType != "" && event.ResourceType != filter.ResourceType {
			continue
		}
		if filter.ResourceID != "" && event.ResourceID != filter.ResourceID {
			continue
		}
		result = append(result, event)
	}
	return result, nil
}

func (m *taskMemoryStore) RecordAudit(_ context.Context, input tasks.AuditInput) (tasks.AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	event := tasks.AuditEvent{ID: "audit-1", ActorID: input.ActorID, Action: input.Action, ResourceType: input.ResourceType, ResourceID: input.ResourceID, Outcome: input.Outcome, Metadata: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}
	m.audit = append(m.audit, event)
	return event, nil
}

func (m *taskMemoryStore) CreateFinding(_ context.Context, input tasks.FindingInput) (tasks.Finding, error) {
	return tasks.Finding{ID: "finding-1", TaskID: input.TaskID, Severity: input.Severity, Category: input.Category, Path: input.Path, Explanation: input.Explanation, Evidence: input.Evidence}, nil
}

func (m *taskMemoryStore) ListFindings(context.Context, string) ([]tasks.Finding, error) {
	return []tasks.Finding{}, nil
}

func (m *taskMemoryStore) CreateEvidence(_ context.Context, input tasks.EvidenceInput) (tasks.Evidence, error) {
	return tasks.Evidence{ID: "evidence-1", TaskID: input.TaskID, Kind: input.Kind, Title: input.Title, Content: input.Content, Digest: input.Digest}, nil
}

func (m *taskMemoryStore) ListEvidence(context.Context, string) ([]tasks.Evidence, error) {
	return []tasks.Evidence{}, nil
}

func (m *taskMemoryStore) SupersedeCodeReviews(context.Context, string, string, int, string) error {
	return nil
}

func TestTaskAPIEnforcesCSRFAndPersistsAudit(t *testing.T) {
	authService := auth.NewService(newAuthMemoryStore(), time.Hour)
	if _, err := authService.Bootstrap(context.Background(), "admin@example.com", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	_, sessionToken, csrfToken, err := authService.LoginLocal(context.Background(), "admin@example.com", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	taskStore := newTaskMemoryStore()
	handler := NewWithTaskServices(fakeChecker{}, authService, nil, nil, tasks.NewService(taskStore), false).Handler()
	cookies := []*http.Cookie{{Name: sessionCookie, Value: sessionToken}}
	request := map[string]any{"kind": "code_review", "title": "Review API", "sourceKey": "manual:review-1", "repositoryName": "repoMender", "payload": map[string]string{"commit": "abc"}}
	rejected := requestJSON(t, handler, http.MethodPost, "/api/v1/tasks", request, cookies, "")
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", rejected.Code)
	}
	created := requestJSON(t, handler, http.MethodPost, "/api/v1/tasks", request, cookies, csrfToken)
	if created.Code != http.StatusAccepted || !strings.Contains(created.Body.String(), "task-1") {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	list := requestJSON(t, handler, http.MethodGet, "/api/v1/tasks", nil, cookies, "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Review API") {
		t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
	}
	if len(taskStore.audit) != 1 || taskStore.audit[0].Action != "task.create" {
		t.Fatalf("audit = %+v", taskStore.audit)
	}
}

func TestTaskAPIAllowsAuditorReadButRejectsMutation(t *testing.T) {
	store := newAuthMemoryStore()
	authService := auth.NewService(store, time.Hour)
	_, sessionToken, csrfToken, err := authService.LoginOIDC(context.Background(), auth.Identity{Subject: "auditor", Email: "auditor@example.com", EmailVerified: true, Name: "Auditor"})
	if err != nil {
		t.Fatal(err)
	}
	store.users["auditor@example.com"] = auth.User{ID: store.users["auditor@example.com"].ID, Email: "auditor@example.com", DisplayName: "Auditor", Role: auth.RoleAuditor, AuthSource: "oidc", Active: true}
	taskStore := newTaskMemoryStore()
	handler := NewWithTaskServices(fakeChecker{}, authService, nil, nil, tasks.NewService(taskStore), false).Handler()
	cookies := []*http.Cookie{{Name: sessionCookie, Value: sessionToken}}
	read := requestJSON(t, handler, http.MethodGet, "/api/v1/tasks", nil, cookies, "")
	if read.Code != http.StatusOK {
		t.Fatalf("auditor read status = %d", read.Code)
	}
	write := requestJSON(t, handler, http.MethodPost, "/api/v1/tasks", map[string]string{"kind": "code_review", "title": "forbidden", "sourceKey": "manual:auditor"}, cookies, csrfToken)
	if write.Code != http.StatusForbidden {
		t.Fatalf("auditor write status = %d body=%s", write.Code, write.Body.String())
	}
}
