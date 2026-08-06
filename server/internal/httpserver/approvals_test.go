package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/approvals"
	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

type approvalHTTPStore struct {
	mu       sync.Mutex
	nextID   int
	requests map[string]approvals.Request
}

func newApprovalHTTPStore() *approvalHTTPStore {
	return &approvalHTTPStore{requests: map[string]approvals.Request{}}
}

func (m *approvalHTTPStore) Create(_ context.Context, input approvals.CreateInput) (approvals.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	now := input.RequestedAt
	request := approvals.Request{ID: "approval-http-" + string(rune('0'+m.nextID)), Action: input.Action, State: approvals.StatePending, Risk: input.Risk, ActionDigest: input.ActionDigest, RequesterID: input.RequesterID, EligibleRoles: input.EligibleRoles, RequestedAt: now, ExpiresAt: input.ExpiresAt, Metadata: input.Metadata, UpdatedAt: now}
	m.requests[request.ID] = request
	return request, nil
}

func (m *approvalHTTPStore) Get(_ context.Context, id string) (approvals.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request, ok := m.requests[id]
	if !ok {
		return approvals.Request{}, approvals.ErrNotFound
	}
	return request, nil
}

func (m *approvalHTTPStore) List(_ context.Context, filter approvals.ListFilter) ([]approvals.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]approvals.Request, 0)
	for _, request := range m.requests {
		if filter.State == "" || request.State == filter.State {
			result = append(result, request)
		}
	}
	return result, nil
}

func (m *approvalHTTPStore) Decide(_ context.Context, input approvals.DecisionInput) (approvals.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request, ok := m.requests[input.ApprovalID]
	if !ok {
		return approvals.Request{}, approvals.ErrNotFound
	}
	if request.DecisionIdempotencyKey == input.IdempotencyKey {
		if request.State == input.Decision {
			return request, nil
		}
		return approvals.Request{}, approvals.ErrIdempotencyConflict
	}
	if request.IsExpired(input.Now) {
		return approvals.Request{}, approvals.ErrExpired
	}
	if request.State != approvals.StatePending {
		return approvals.Request{}, approvals.ErrAlreadyDecided
	}
	request.State, request.DecisionIdempotencyKey, request.DecisionActorID = input.Decision, input.IdempotencyKey, input.Actor.ID
	request.DecisionReason, request.UpdatedAt = input.Reason, input.Now
	m.requests[request.ID] = request
	return request, nil
}

func (m *approvalHTTPStore) Consume(_ context.Context, input approvals.ConsumeInput) (approvals.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request, ok := m.requests[input.ApprovalID]
	if !ok {
		return approvals.Request{}, approvals.ErrNotFound
	}
	if request.ConsumptionIdempotencyKey == input.IdempotencyKey && request.ActionDigest == input.ActionDigest {
		return request, nil
	}
	if err := request.CanConsume(input.Now); err != nil {
		return approvals.Request{}, err
	}
	if request.ActionDigest != input.ActionDigest {
		return approvals.Request{}, approvals.ErrDigestMismatch
	}
	request.State, request.ConsumptionIdempotencyKey, request.ConsumedBy, request.UpdatedAt = approvals.StateConsumed, input.IdempotencyKey, input.ActorID, input.Now
	m.requests[request.ID] = request
	return request, nil
}

func (m *approvalHTTPStore) Cancel(_ context.Context, _ approvals.CancelInput) (approvals.Request, error) {
	return approvals.Request{}, errors.New("cancel not used in HTTP test")
}

func TestApprovalAPIEnforcesCSRFIdempotencyAndExactConsumption(t *testing.T) {
	authStore := newAuthMemoryStore()
	authService := auth.NewService(authStore, time.Hour)
	if _, err := authService.Bootstrap(context.Background(), "admin@example.com", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	_, sessionToken, csrfToken, err := authService.LoginLocal(context.Background(), "admin@example.com", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	taskStore := newTaskMemoryStore()
	approvalStore := newApprovalHTTPStore()
	approvalService := approvals.NewService(approvalStore, tasks.NewService(taskStore))
	handler := NewWithApprovalServices(fakeChecker{}, authService, nil, nil, tasks.NewService(taskStore), nil, nil, approvalService, false).Handler()
	cookies := []*http.Cookie{{Name: sessionCookie, Value: sessionToken}}
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	created := requestJSON(t, handler, http.MethodPost, "/api/v1/approvals", map[string]any{
		"action": "publish_patch", "risk": "high", "actionDigest": digest,
		"expiresAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}, cookies, csrfToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create approval status = %d body=%s", created.Code, created.Body.String())
	}
	requestID := "approval-http-1"
	decision := requestJSON(t, handler, http.MethodPost, "/api/v1/approvals/"+requestID+"/decision", map[string]any{
		"decision": "approved", "reason": "reviewed", "idempotencyKey": "decision-http-1",
	}, cookies, csrfToken)
	if decision.Code != http.StatusOK {
		t.Fatalf("decision status = %d body=%s", decision.Code, decision.Body.String())
	}
	wrong := requestJSON(t, handler, http.MethodPost, "/api/v1/approvals/"+requestID+"/consume", map[string]any{
		"actionDigest": "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd", "idempotencyKey": "consume-http-1",
	}, cookies, csrfToken)
	if wrong.Code != http.StatusConflict || !strings.Contains(wrong.Body.String(), "approval_digest_mismatch") {
		t.Fatalf("wrong digest status = %d body=%s", wrong.Code, wrong.Body.String())
	}
	consumed := requestJSON(t, handler, http.MethodPost, "/api/v1/approvals/"+requestID+"/consume", map[string]any{
		"actionDigest": digest, "idempotencyKey": "consume-http-1",
	}, cookies, csrfToken)
	if consumed.Code != http.StatusOK || !strings.Contains(consumed.Body.String(), "consumed") {
		t.Fatalf("consume status = %d body=%s", consumed.Code, consumed.Body.String())
	}
	repeated := requestJSON(t, handler, http.MethodPost, "/api/v1/approvals/"+requestID+"/consume", map[string]any{
		"actionDigest": digest, "idempotencyKey": "consume-http-1",
	}, cookies, csrfToken)
	if repeated.Code != http.StatusOK {
		t.Fatalf("repeated consume status = %d body=%s", repeated.Code, repeated.Body.String())
	}
	if len(taskStore.audit) < 3 {
		t.Fatalf("approval audit events = %+v", taskStore.audit)
	}
}

func TestApprovalAPIAuditorDecisionCreatesDenialAudit(t *testing.T) {
	authStore := newAuthMemoryStore()
	authService := auth.NewService(authStore, time.Hour)
	user, sessionToken, csrfToken, err := authService.LoginOIDC(context.Background(), auth.Identity{Subject: "auditor", Email: "auditor@example.com", EmailVerified: true, Name: "Auditor"})
	if err != nil {
		t.Fatal(err)
	}
	user.Role = auth.RoleAuditor
	authStore.users[user.Email] = user
	taskStore := newTaskMemoryStore()
	approvalStore := newApprovalHTTPStore()
	approvalService := approvals.NewService(approvalStore, tasks.NewService(taskStore))
	handler := NewWithApprovalServices(fakeChecker{}, authService, nil, nil, tasks.NewService(taskStore), nil, nil, approvalService, false).Handler()
	cookies := []*http.Cookie{{Name: sessionCookie, Value: sessionToken}}
	created := requestJSON(t, handler, http.MethodPost, "/api/v1/approvals", map[string]any{
		"action": "repair_plan", "risk": "critical", "actionDigest": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"expiresAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}, cookies, csrfToken)
	if created.Code != http.StatusForbidden {
		t.Fatalf("auditor create status = %d", created.Code)
	}
	// Seed a request as a developer would, then verify the auditor decision path
	// reaches the domain policy so the denial is durably auditable.
	request, err := approvalService.Create(context.Background(), approvals.CreateInput{
		Action: approvals.ActionRepairPlan, Risk: approvals.RiskCritical,
		ActionDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		RequesterID:  "developer-1", EligibleRoles: []auth.Role{auth.RoleAdmin}, ExpiresAt: time.Now().UTC().Add(time.Hour), RequestedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := requestJSON(t, handler, http.MethodPost, "/api/v1/approvals/"+request.ID+"/decision", map[string]any{
		"decision": "approved", "reason": "not allowed", "idempotencyKey": "auditor-decision-1",
	}, cookies, csrfToken)
	if decision.Code != http.StatusForbidden || !strings.Contains(decision.Body.String(), "approval_forbidden") {
		t.Fatalf("auditor decision status = %d body=%s", decision.Code, decision.Body.String())
	}
	if len(taskStore.audit) == 0 || taskStore.audit[len(taskStore.audit)-1].Outcome != "denied" {
		t.Fatalf("auditor denial audit = %+v", taskStore.audit)
	}
}
