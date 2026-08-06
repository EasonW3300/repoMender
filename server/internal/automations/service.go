package automations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// Service owns lifecycle validation and audit recording; the store owns the
// transactional version, deduplication, and concurrency guarantees.
type Service struct {
	store Store
	audit AuditRecorder
	now   func() time.Time
}

func NewService(store Store, audit AuditRecorder) *Service {
	return &Service{store: store, audit: audit, now: time.Now}
}

func (s *Service) Create(ctx context.Context, input Input, actorID string) (Template, error) {
	if err := Validate(input); err != nil {
		return Template{}, err
	}
	template, err := s.store.Create(ctx, input, actorID)
	if err != nil {
		return Template{}, err
	}
	s.recordAudit(ctx, actorID, "automation.create", template.ID, map[string]any{"revision": template.Revision})
	return template, nil
}

func (s *Service) Get(ctx context.Context, id string) (Template, error) {
	return s.store.Get(ctx, strings.TrimSpace(id))
}

func (s *Service) List(ctx context.Context, limit int) ([]Template, error) {
	return s.store.List(ctx, limit)
}

func (s *Service) UpdateDraft(ctx context.Context, id string, expectedRevision int, input Input, actorID string) (Template, error) {
	if err := Validate(input); err != nil {
		return Template{}, err
	}
	template, err := s.store.UpdateDraft(ctx, strings.TrimSpace(id), expectedRevision, input, actorID)
	if err != nil {
		return Template{}, err
	}
	s.recordAudit(ctx, actorID, "automation.edit", template.ID, map[string]any{"revision": template.Revision})
	return template, nil
}

func (s *Service) Publish(ctx context.Context, id string, expectedRevision int, actorID string) (Template, error) {
	template, err := s.store.Publish(ctx, strings.TrimSpace(id), expectedRevision, actorID)
	if err != nil {
		return Template{}, err
	}
	s.recordAudit(ctx, actorID, "automation.publish", template.ID, map[string]any{"version": template.Version})
	return template, nil
}

func (s *Service) SetEnabled(ctx context.Context, id string, expectedRevision int, enabled bool, actorID string) (Template, error) {
	template, err := s.store.SetEnabled(ctx, strings.TrimSpace(id), expectedRevision, enabled, actorID)
	if err != nil {
		return Template{}, err
	}
	action := "automation.disable"
	if enabled {
		action = "automation.activate"
	}
	s.recordAudit(ctx, actorID, action, template.ID, map[string]any{"version": template.Version, "enabled": enabled})
	return template, nil
}

func (s *Service) Delete(ctx context.Context, id string, expectedRevision int, actorID string) error {
	if err := s.store.Delete(ctx, strings.TrimSpace(id), expectedRevision, actorID); err != nil {
		return err
	}
	s.recordAudit(ctx, actorID, "automation.delete", strings.TrimSpace(id), nil)
	return nil
}

func (s *Service) History(ctx context.Context, id string, limit int) ([]Version, error) {
	return s.store.History(ctx, strings.TrimSpace(id), limit)
}

func (s *Service) Trigger(ctx context.Context, input TriggerInput) (TriggerResult, error) {
	if strings.TrimSpace(input.TemplateID) == "" || strings.TrimSpace(input.TriggerKey) == "" ||
		strings.TrimSpace(input.Provider) == "" || strings.TrimSpace(input.Event) == "" ||
		strings.TrimSpace(input.Action) == "" || strings.TrimSpace(input.Repository) == "" || strings.TrimSpace(input.ActorID) == "" {
		return TriggerResult{}, ErrInvalidTrigger
	}
	if len(input.TriggerKey) > 512 || strings.ContainsAny(input.TriggerKey, "\r\n") {
		return TriggerResult{}, ErrInvalidTrigger
	}
	if len(input.TaskPayload) == 0 || !json.Valid(input.TaskPayload) {
		return TriggerResult{}, ErrInvalidTrigger
	}
	if len(input.SourcePayload) == 0 {
		input.SourcePayload = json.RawMessage(`{}`)
	}
	if !json.Valid(input.SourcePayload) {
		return TriggerResult{}, ErrInvalidTrigger
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = s.now().UTC()
	}
	input.TriggerKey = strings.TrimSpace(input.TriggerKey)
	input.TaskPayload = decoratePayload(input.TaskPayload, input.TemplateID, input.TriggerKey)
	result, err := s.store.Trigger(ctx, input)
	if err != nil {
		if errors.Is(err, ErrDuplicateTrigger) {
			return result, err
		}
		return TriggerResult{}, err
	}
	s.recordAudit(ctx, input.ActorID, "automation.trigger", input.TemplateID, map[string]any{
		"runId": result.Run.ID, "taskId": result.Task.ID, "version": result.Run.TemplateVersion,
		"triggerDigest": triggerDigest(input.TriggerKey),
	})
	return result, nil
}

func (s *Service) Runs(ctx context.Context, templateID string, limit int) ([]Run, error) {
	return s.store.Runs(ctx, strings.TrimSpace(templateID), limit)
}

// Matching returns only enabled templates whose provider, event, action,
// branch, and exact repository scope all match the verified webhook envelope.
func (s *Service) Matching(ctx context.Context, provider, event, action, branch, repository string) ([]Template, error) {
	items, err := s.store.List(ctx, 200)
	if err != nil {
		return nil, err
	}
	matched := make([]Template, 0, len(items))
	for _, item := range items {
		if item.Matches(provider, event, action, branch, repository) {
			matched = append(matched, item)
		}
	}
	return matched, nil
}

func (s *Service) recordAudit(ctx context.Context, actorID, action, resourceID string, metadata map[string]any) {
	if s.audit == nil {
		return
	}
	encoded, _ := json.Marshal(metadata)
	_, _ = s.audit.RecordAudit(ctx, tasks.AuditInput{ActorID: actorID, Action: action,
		ResourceType: "automation", ResourceID: resourceID, Outcome: "accepted", Metadata: encoded})
}

func decoratePayload(raw json.RawMessage, templateID, triggerKey string) json.RawMessage {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		return raw
	}
	payload["automation"] = map[string]string{"templateId": templateID, "triggerKey": triggerKey}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return encoded
}

func triggerDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (s *Service) Validate(input Input) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("%w: automation service unavailable", ErrValidation)
	}
	return Validate(input)
}
