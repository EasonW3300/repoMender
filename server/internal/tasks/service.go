package tasks

import (
	"context"
	"encoding/json"
	"time"
)

// Service is the provider-neutral M4 application boundary. It delegates
// durable mutations to Store so HTTP handlers and future webhook triggers use
// identical idempotency and state-machine behavior.
type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Task, error) {
	return s.store.CreateTask(ctx, input)
}

func (s *Service) Get(ctx context.Context, id string) (Task, error) {
	return s.store.GetTask(ctx, id)
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Task, error) {
	return s.store.ListTasks(ctx, filter)
}

func (s *Service) Cancel(ctx context.Context, id, reason string) (Task, error) {
	return s.store.CancelTask(ctx, id, reason)
}

func (s *Service) Retry(ctx context.Context, id, reason string) (Task, error) {
	return s.store.RetryTask(ctx, id, reason)
}

func (s *Service) Claim(ctx context.Context, owner string, lease time.Duration) (Task, Run, error) {
	return s.store.ClaimTask(ctx, owner, lease)
}

func (s *Service) Heartbeat(ctx context.Context, id, owner string, lease time.Duration) error {
	return s.store.Heartbeat(ctx, id, owner, lease)
}

func (s *Service) Complete(ctx context.Context, taskID, runID string, status Status, code, message string) error {
	return s.store.CompleteTask(ctx, taskID, runID, status, code, message)
}

func (s *Service) Run(ctx context.Context, id string) (Run, error) {
	return s.store.GetRun(ctx, id)
}

func (s *Service) Runs(ctx context.Context, taskID string) ([]Run, error) {
	return s.store.ListRuns(ctx, taskID)
}

func (s *Service) AllRuns(ctx context.Context, limit int) ([]Run, error) {
	return s.store.ListAllRuns(ctx, limit)
}

func (s *Service) Events(ctx context.Context, runID string, after int64) ([]RunEvent, error) {
	return s.store.ListRunEvents(ctx, runID, after)
}

func (s *Service) AppendEvent(ctx context.Context, runID string, input RunEventInput) (RunEvent, error) {
	return s.store.AppendRunEvent(ctx, runID, input)
}

func (s *Service) Audit(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	return s.store.ListAuditEvents(ctx, filter)
}

func (s *Service) RecordAudit(ctx context.Context, input AuditInput) (AuditEvent, error) {
	return s.store.RecordAudit(ctx, input)
}

func normalizePayload(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return json.RawMessage(`{}`)
	}
	return payload
}
