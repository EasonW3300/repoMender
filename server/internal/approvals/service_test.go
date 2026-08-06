package approvals

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

type memoryStore struct {
	mu       sync.Mutex
	requests map[string]Request
	nextID   int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{requests: map[string]Request{}}
}

func (m *memoryStore) Create(_ context.Context, input CreateInput) (Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	now := input.RequestedAt
	request := Request{ID: "approval-" + string(rune('0'+m.nextID)), Action: input.Action, State: StatePending, Risk: input.Risk, ActionDigest: input.ActionDigest, RequesterID: input.RequesterID, EligibleRoles: input.EligibleRoles, RequestedAt: now, ExpiresAt: input.ExpiresAt, Metadata: input.Metadata, UpdatedAt: now}
	m.requests[request.ID] = request
	return request, nil
}

func (m *memoryStore) Get(_ context.Context, id string) (Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request, ok := m.requests[id]
	if !ok {
		return Request{}, ErrNotFound
	}
	return request, nil
}

func (m *memoryStore) List(_ context.Context, filter ListFilter) ([]Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Request, 0)
	for _, request := range m.requests {
		if filter.State == "" || filter.State == request.State {
			result = append(result, request)
		}
	}
	return result, nil
}

func (m *memoryStore) Decide(_ context.Context, input DecisionInput) (Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request, ok := m.requests[input.ApprovalID]
	if !ok {
		return Request{}, ErrNotFound
	}
	if request.DecisionIdempotencyKey == input.IdempotencyKey {
		if request.State == input.Decision {
			return request, nil
		}
		return Request{}, ErrIdempotencyConflict
	}
	if request.IsExpired(input.Now) {
		request.State, request.UpdatedAt = StateExpired, input.Now
		m.requests[request.ID] = request
		return request, ErrExpired
	}
	if request.State != StatePending {
		return Request{}, stateDecisionError(request.State)
	}
	request.State, request.DecisionIdempotencyKey, request.DecisionActorID = input.Decision, input.IdempotencyKey, input.Actor.ID
	request.DecisionReason, request.UpdatedAt = input.Reason, input.Now
	m.requests[request.ID] = request
	return request, nil
}

func (m *memoryStore) Consume(_ context.Context, input ConsumeInput) (Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request, ok := m.requests[input.ApprovalID]
	if !ok {
		return Request{}, ErrNotFound
	}
	if request.ConsumptionIdempotencyKey == input.IdempotencyKey && request.ActionDigest == input.ActionDigest {
		return request, nil
	}
	if err := request.CanConsume(input.Now); err != nil {
		return Request{}, err
	}
	if request.ActionDigest != input.ActionDigest {
		return Request{}, ErrDigestMismatch
	}
	request.State, request.ConsumptionIdempotencyKey, request.ConsumedBy, request.UpdatedAt = StateConsumed, input.IdempotencyKey, input.ActorID, input.Now
	m.requests[request.ID] = request
	return request, nil
}

func (m *memoryStore) Cancel(_ context.Context, input CancelInput) (Request, error) {
	return Request{}, errors.New("not used")
}

type auditMemory struct {
	events []tasks.AuditInput
}

func (a *auditMemory) RecordAudit(_ context.Context, input tasks.AuditInput) (tasks.AuditEvent, error) {
	a.events = append(a.events, input)
	return tasks.AuditEvent{ID: "audit-1"}, nil
}

func TestServiceEnforcesRolePolicyAndDecisionIdempotency(t *testing.T) {
	now := time.Date(2026, time.August, 6, 1, 0, 0, 0, time.UTC)
	store, audit := newMemoryStore(), &auditMemory{}
	service := NewService(store, audit)
	service.now = func() time.Time { return now }
	request, err := service.Create(context.Background(), CreateInput{
		Action: ActionRepairPlan, Risk: RiskHigh,
		ActionDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		RequesterID:  "developer-1", EligibleRoles: []auth.Role{auth.RoleAdmin, auth.RoleMaintainer},
		ExpiresAt: now.Add(time.Hour), RequestedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Decide(context.Background(), DecisionInput{ApprovalID: request.ID, Actor: auth.User{ID: "developer-1", Role: auth.RoleDeveloper}, Decision: StateApproved, IdempotencyKey: "decision-001", Now: now})
	if err != ErrForbidden {
		t.Fatalf("developer decision error = %v", err)
	}
	approved, err := service.Decide(context.Background(), DecisionInput{ApprovalID: request.ID, Actor: auth.User{ID: "maintainer-1", Role: auth.RoleMaintainer}, Decision: StateApproved, IdempotencyKey: "decision-001", Now: now})
	if err != nil || approved.State != StateApproved {
		t.Fatalf("maintainer decision = %+v, %v", approved, err)
	}
	repeated, err := service.Decide(context.Background(), DecisionInput{ApprovalID: request.ID, Actor: auth.User{ID: "maintainer-1", Role: auth.RoleMaintainer}, Decision: StateApproved, IdempotencyKey: "decision-001", Now: now})
	if err != nil || repeated.ID != approved.ID {
		t.Fatalf("repeated decision = %+v, %v", repeated, err)
	}
	if len(audit.events) < 3 || audit.events[1].Outcome != "denied" {
		t.Fatalf("audit events = %+v", audit.events)
	}
}

func TestServiceConsumesOnlyExactApprovedAction(t *testing.T) {
	now := time.Now().UTC()
	store := newMemoryStore()
	service := NewService(store, nil)
	service.now = func() time.Time { return now }
	request, err := service.Create(context.Background(), CreateInput{
		Action: ActionSandboxNetwork, Risk: RiskMedium,
		ActionDigest: "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		RequesterID:  "developer-1", EligibleRoles: []auth.Role{auth.RoleAdmin},
		ExpiresAt: now.Add(time.Hour), RequestedAt: now,
		Metadata: []byte(`{"destinations":["packages.acme.dev"],"networkPolicy":"allowlist"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Consume(context.Background(), ConsumeInput{ApprovalID: request.ID, ActorID: "worker-1", ActionDigest: request.ActionDigest, IdempotencyKey: "consume-001", Now: now})
	if !errors.Is(err, ErrInvalidState) && !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("pending consumption error = %v", err)
	}
	_, err = service.Decide(context.Background(), DecisionInput{ApprovalID: request.ID, Actor: auth.User{ID: "admin-1", Role: auth.RoleAdmin}, Decision: StateApproved, IdempotencyKey: "decision-002", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Consume(context.Background(), ConsumeInput{ApprovalID: request.ID, ActorID: "worker-1", ActionDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", IdempotencyKey: "consume-001", Now: now})
	if err != ErrDigestMismatch {
		t.Fatalf("changed digest error = %v", err)
	}
	consumed, err := service.Consume(context.Background(), ConsumeInput{ApprovalID: request.ID, ActorID: "worker-1", ActionDigest: request.ActionDigest, IdempotencyKey: "consume-001", Now: now})
	if err != nil || consumed.State != StateConsumed {
		t.Fatalf("consumption = %+v, %v", consumed, err)
	}
}
