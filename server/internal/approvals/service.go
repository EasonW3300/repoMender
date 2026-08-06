package approvals

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// auth supplies role and user identity for policy checks; tasks owns the
// immutable audit event schema shared by all M4-M7 mutations.

type AuditRecorder interface {
	RecordAudit(context.Context, tasks.AuditInput) (tasks.AuditEvent, error)
}

type Service struct {
	store Store
	audit AuditRecorder
	now   func() time.Time
}

func NewService(store Store, audit AuditRecorder) *Service {
	return &Service{store: store, audit: audit, now: time.Now}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Request, error) {
	now := s.now().UTC()
	if err := ValidateCreate(input, now); err != nil {
		return Request{}, err
	}
	if input.RequestedAt.IsZero() {
		input.RequestedAt = now
	}
	request, err := s.store.Create(ctx, input)
	if err != nil {
		return Request{}, err
	}
	s.record(ctx, input.RequesterID, "approval.request", request.ID, "accepted", map[string]any{
		"action": request.Action, "risk": request.Risk, "actionDigest": request.ActionDigest,
	})
	return request, nil
}

func (s *Service) Get(ctx context.Context, id string) (Request, error) {
	return s.store.Get(ctx, strings.TrimSpace(id))
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Request, error) {
	if filter.State != "" && !ValidState(filter.State) {
		return nil, ErrInvalidState
	}
	return s.store.List(ctx, filter)
}

func (s *Service) Decide(ctx context.Context, input DecisionInput) (Request, error) {
	if input.Decision != StateApproved && input.Decision != StateRejected {
		return Request{}, ErrInvalidState
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return Request{}, err
	}
	if input.Now.IsZero() {
		input.Now = s.now().UTC()
	}
	request, err := s.store.Get(ctx, strings.TrimSpace(input.ApprovalID))
	if err != nil {
		return Request{}, err
	}
	if !canDecide(input.Actor, request) {
		s.record(ctx, input.Actor.ID, "approval.decision", request.ID, "denied", map[string]any{
			"decision": input.Decision, "reason": "role_not_eligible",
		})
		return Request{}, ErrForbidden
	}
	decided, err := s.store.Decide(ctx, input)
	if err != nil {
		s.record(ctx, input.Actor.ID, "approval.decision", request.ID, auditOutcome(err), map[string]any{
			"decision": input.Decision, "reason": input.Reason,
		})
		return Request{}, err
	}
	s.record(ctx, input.Actor.ID, "approval.decision", decided.ID, string(decided.State), map[string]any{
		"decision": input.Decision, "reason": input.Reason,
	})
	return decided, nil
}

// Consume is the protected-action gate. Callers must successfully consume the
// exact digest immediately before executing the action; a changed plan or
// patch therefore cannot reuse an earlier approval.
func (s *Service) Consume(ctx context.Context, input ConsumeInput) (Request, error) {
	if strings.TrimSpace(input.ActorID) == "" {
		return Request{}, ErrForbidden
	}
	if !ValidateActionDigest(input.ActionDigest) {
		return Request{}, ErrInvalidDigest
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return Request{}, err
	}
	if input.Now.IsZero() {
		input.Now = s.now().UTC()
	}
	request, err := s.store.Consume(ctx, input)
	if err != nil {
		s.record(ctx, input.ActorID, "approval.consume", input.ApprovalID, auditOutcome(err), map[string]any{
			"actionDigest": input.ActionDigest,
		})
		return Request{}, err
	}
	s.record(ctx, input.ActorID, "approval.consume", request.ID, "consumed", map[string]any{
		"actionDigest": input.ActionDigest,
	})
	return request, nil
}

func (s *Service) Cancel(ctx context.Context, input CancelInput) (Request, error) {
	if strings.TrimSpace(input.Actor.ID) == "" {
		return Request{}, ErrForbidden
	}
	if input.Now.IsZero() {
		input.Now = s.now().UTC()
	}
	request, err := s.store.Get(ctx, strings.TrimSpace(input.ApprovalID))
	if err != nil {
		return Request{}, err
	}
	if input.Actor.ID != request.RequesterID && !isPrivileged(input.Actor.Role) {
		s.record(ctx, input.Actor.ID, "approval.cancel", request.ID, "denied", map[string]any{"reason": "role_not_eligible"})
		return Request{}, ErrForbidden
	}
	cancelled, err := s.store.Cancel(ctx, input)
	if err != nil {
		s.record(ctx, input.Actor.ID, "approval.cancel", request.ID, auditOutcome(err), map[string]any{"reason": input.Reason})
		return Request{}, err
	}
	s.record(ctx, input.Actor.ID, "approval.cancel", cancelled.ID, "cancelled", map[string]any{"reason": input.Reason})
	return cancelled, nil
}

func canDecide(actor auth.User, request Request) bool {
	return isPrivileged(actor.Role) && containsRole(request.EligibleRoles, actor.Role)
}

func isPrivileged(role auth.Role) bool {
	return role == auth.RoleAdmin || role == auth.RoleMaintainer
}

func containsRole(roles []auth.Role, wanted auth.Role) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}

func auditOutcome(err error) string {
	switch {
	case err == nil:
		return "accepted"
	case err == ErrExpired:
		return "expired"
	case err == ErrForbidden:
		return "denied"
	case err == ErrRejected:
		return "rejected"
	case err == ErrCancelled:
		return "cancelled"
	case err == ErrConsumed:
		return "already_consumed"
	case err == ErrDigestMismatch:
		return "digest_mismatch"
	default:
		return "failed"
	}
}

func (s *Service) record(ctx context.Context, actorID, action, resourceID, outcome string, metadata map[string]any) {
	if s.audit == nil {
		return
	}
	payload, _ := json.Marshal(metadata)
	_, _ = s.audit.RecordAudit(ctx, tasks.AuditInput{
		ActorID: actorID, Action: action, ResourceType: "approval_request", ResourceID: resourceID,
		Outcome: outcome, Metadata: payload,
	})
}
