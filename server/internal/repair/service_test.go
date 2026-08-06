package repair

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/approvals"
	"github.com/EasonW3300/repoMender/server/internal/execution"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

type memoryStore struct {
	items  map[string]Repair
	byTask map[string]string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{items: map[string]Repair{}, byTask: map[string]string{}}
}
func (m *memoryStore) Create(_ context.Context, input CreateInput) (Repair, error) {
	for _, existing := range m.items {
		if existing.SourceKey == input.SourceKey {
			return existing, nil
		}
	}
	item := Repair{ID: "repair-1", TaskID: input.TaskID, SourceKey: input.SourceKey, Trigger: input.Trigger, RepositoryID: input.Issue.RepositoryID,
		Repository: input.Issue.Repository, CloneURL: input.Issue.CloneURL, WebURL: input.Issue.WebURL, InstallationID: input.Issue.InstallationID,
		IssueNumber: input.Issue.Number, IssueTitle: input.Issue.Title, IssueBody: input.Issue.Body, BaseBranch: input.Issue.BaseBranch,
		BaseSHA: input.Issue.BaseSHA, State: StatePlanning, CreatedBy: input.CreatedBy, CreatedAt: input.CreatedAt, UpdatedAt: input.CreatedAt}
	m.items[item.ID], m.byTask[item.TaskID] = item, item.ID
	return item, nil
}
func (m *memoryStore) Get(_ context.Context, id string) (Repair, error) {
	item, ok := m.items[id]
	if !ok {
		return Repair{}, ErrNotFound
	}
	return item, nil
}
func (m *memoryStore) GetByTask(_ context.Context, taskID string) (Repair, error) {
	return m.Get(context.Background(), m.byTask[taskID])
}
func (m *memoryStore) List(_ context.Context, state State, _ int) ([]Repair, error) {
	result := []Repair{}
	for _, item := range m.items {
		if state == "" || item.State == state {
			result = append(result, item)
		}
	}
	return result, nil
}
func (m *memoryStore) Update(_ context.Context, item Repair) (Repair, error) {
	if _, ok := m.items[item.ID]; !ok {
		return Repair{}, ErrNotFound
	}
	item.UpdatedAt = time.Now()
	m.items[item.ID] = item
	return item, nil
}

type fakeTasks struct {
	next    int
	items   map[string]tasks.Task
	retries int
}

func newFakeTasks() *fakeTasks { return &fakeTasks{items: map[string]tasks.Task{}} }
func (f *fakeTasks) Create(_ context.Context, input tasks.CreateInput) (tasks.Task, error) {
	f.next++
	item := tasks.Task{ID: "task-" + string(rune('0'+f.next)), Kind: input.Kind, Status: tasks.StatusQueued, Title: input.Title, RepositoryID: input.RepositoryID, RepositoryName: input.RepositoryName, SourceKey: input.SourceKey, Payload: input.Payload, CreatedBy: input.CreatedBy}
	f.items[item.ID] = item
	return item, nil
}
func (f *fakeTasks) Get(_ context.Context, id string) (tasks.Task, error) {
	item, ok := f.items[id]
	if !ok {
		return tasks.Task{}, tasks.ErrTaskNotFound
	}
	return item, nil
}
func (f *fakeTasks) Retry(_ context.Context, id, _ string) (tasks.Task, error) {
	item, ok := f.items[id]
	if !ok {
		return tasks.Task{}, tasks.ErrTaskNotFound
	}
	f.retries++
	item.Status = tasks.StatusQueued
	f.items[id] = item
	return item, nil
}
func (f *fakeTasks) AppendEvent(_ context.Context, _ string, _ tasks.RunEventInput) (tasks.RunEvent, error) {
	return tasks.RunEvent{}, nil
}

type fakeApprovals struct {
	next     int
	requests map[string]approvals.Request
}

func newFakeApprovals() *fakeApprovals {
	return &fakeApprovals{requests: map[string]approvals.Request{}}
}
func (f *fakeApprovals) Create(_ context.Context, input approvals.CreateInput) (approvals.Request, error) {
	f.next++
	id := "approval-" + string(rune('0'+f.next))
	item := approvals.Request{ID: id, Action: input.Action, State: approvals.StatePending, ActionDigest: input.ActionDigest, RequesterID: input.RequesterID, Metadata: input.Metadata}
	f.requests[id] = item
	return item, nil
}
func (f *fakeApprovals) Consume(_ context.Context, input approvals.ConsumeInput) (approvals.Request, error) {
	item, ok := f.requests[input.ApprovalID]
	if !ok {
		return approvals.Request{}, approvals.ErrNotFound
	}
	if item.State == approvals.StateConsumed && item.ActionDigest == input.ActionDigest {
		return item, nil
	}
	if item.State != approvals.StatePending {
		return approvals.Request{}, approvals.ErrAlreadyDecided
	}
	if item.ActionDigest != input.ActionDigest {
		return approvals.Request{}, approvals.ErrDigestMismatch
	}
	item.State = approvals.StateConsumed
	f.requests[item.ID] = item
	return item, nil
}

type fakeProvider struct{ published int }

func (f *fakeProvider) ResolveIssue(context.Context, string, string, int) (Issue, error) {
	return Issue{}, errors.New("not used")
}
func (f *fakeProvider) ResolveBaseSHA(context.Context, string, string, string) (string, error) {
	return strings.Repeat("a", 40), nil
}
func (f *fakeProvider) PublishDraft(context.Context, PublishInput) (DraftPullRequest, error) {
	f.published++
	return DraftPullRequest{Number: 99, URL: "https://github.com/example/repo/pull/99"}, nil
}

type fakeAC struct{ calls int }

func (f *fakeAC) Health(context.Context) (execution.VersionInfo, error) {
	return execution.VersionInfo{}, nil
}
func (f *fakeAC) Start(context.Context, execution.Request) (execution.Run, error) {
	f.calls++
	return execution.Run{ID: "run-ac", CorrelationID: "corr"}, nil
}
func (f *fakeAC) Events(context.Context, string, uint64) (<-chan execution.Event, <-chan error) {
	events := make(chan execution.Event)
	errs := make(chan error, 1)
	close(events)
	errs <- nil
	close(errs)
	return events, errs
}
func (f *fakeAC) Cancel(context.Context, string, string) error { return nil }
func (f *fakeAC) Result(context.Context, string) (execution.Result, error) {
	if f.calls == 1 {
		raw, _ := json.Marshal(validPlan())
		return execution.Result{Output: raw}, nil
	}
	raw, _ := json.Marshal(validPatch())
	return execution.Result{Output: raw}, nil
}

func TestRepairWorkflowRequiresPlanAndPatchApprovals(t *testing.T) {
	ctx := context.Background()
	store, queue, gate, provider, adapter := newMemoryStore(), newFakeTasks(), newFakeApprovals(), &fakeProvider{}, &fakeAC{}
	service := NewService(store, queue, gate, provider)
	created, err := service.CreateFromIssue(ctx, validIssue(), "admin-1")
	if err != nil {
		t.Fatalf("CreateFromIssue() error = %v", err)
	}
	processor := NewProcessor(service, queue, adapter, time.Minute)
	status, _, _ := processor.Process(ctx, tasks.Task{ID: created.TaskID, RepositoryID: "repo"}, tasks.Run{ID: "run-1", CorrelationID: "corr-1"})
	if status != tasks.StatusAwaitingApproval {
		t.Fatalf("plan status = %s", status)
	}
	item, _ := store.Get(ctx, created.ID)
	if item.State != StateAwaitingPlanApproval || item.PlanApprovalID == "" {
		t.Fatalf("plan approval state = %+v", item)
	}
	planDigest := item.PlanDigest
	if _, err := service.ConsumeApproval(ctx, created.ID, "plan", planDigest, "plan-key-123", "admin-1"); err != nil {
		t.Fatalf("consume plan error = %v", err)
	}
	status, _, _ = processor.Process(ctx, tasks.Task{ID: created.TaskID, RepositoryID: "repo"}, tasks.Run{ID: "run-2", CorrelationID: "corr-2"})
	if status != tasks.StatusAwaitingApproval {
		t.Fatalf("patch status = %s", status)
	}
	item, _ = store.Get(ctx, created.ID)
	if item.State != StateAwaitingPatchApproval || item.PatchApprovalID == "" {
		t.Fatalf("patch approval state = %+v", item)
	}
	if _, err := service.ConsumeApproval(ctx, created.ID, "patch", item.PatchDigest, "patch-key-123", "admin-1"); err != nil {
		t.Fatalf("consume patch error = %v", err)
	}
	status, _, _ = processor.Process(ctx, tasks.Task{ID: created.TaskID, RepositoryID: "repo"}, tasks.Run{ID: "run-3", CorrelationID: "corr-3"})
	if status != tasks.StatusSucceeded {
		t.Fatalf("publish status = %s", status)
	}
	item, _ = store.Get(ctx, created.ID)
	if item.State != StatePublished || item.DraftPR.Number != 99 || provider.published != 1 {
		t.Fatalf("published repair = %+v", item)
	}
	if queue.retries != 2 {
		t.Fatalf("approval retries = %d, want 2", queue.retries)
	}
}
