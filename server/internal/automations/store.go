package automations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
	"github.com/jackc/pgx/v5"
)

// PostgreSQLStore keeps templates, immutable published versions, and trigger
// reservations in one transaction so concurrent API requests cannot bypass
// RBAC-visible revision checks or create work past a concurrency limit.
type PostgreSQLStore struct {
	db *database.DB
}

func NewPostgreSQLStore(db *database.DB) *PostgreSQLStore {
	return &PostgreSQLStore{db: db}
}

func (s *PostgreSQLStore) Create(ctx context.Context, input Input, actorID string) (Template, error) {
	if err := Validate(input); err != nil {
		return Template{}, err
	}
	id, err := newID()
	if err != nil {
		return Template{}, err
	}
	filters, err := json.Marshal(input.EventFilters)
	if err != nil {
		return Template{}, err
	}
	now := time.Now().UTC()
	var template Template
	err = s.db.QueryRow(ctx, `
		INSERT INTO automation_templates(
			id, name, description, kind, provider, enabled, repository_scope,
			event_filters, agent_template, execution_budget, timeout_seconds,
			concurrency_limit, risk, approval_policy, version, revision,
			created_by, updated_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, false, $6, $7, $8, $9, $10, $11, $12, $13,
			0, 1, NULLIF($14, '')::uuid, NULLIF($14, '')::uuid, $15, $15)
		RETURNING `+templateColumns+`
	`, id, strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), input.Kind,
		input.Provider, input.RepositoryScope, filters, strings.TrimSpace(input.AgentTemplate),
		input.ExecutionBudget, input.TimeoutSeconds, input.ConcurrencyLimit, input.Risk,
		input.ApprovalPolicy, actorID, now).Scan(templateFields(&template)...)
	if err != nil {
		return Template{}, err
	}
	return template, nil
}

func (s *PostgreSQLStore) Get(ctx context.Context, id string) (Template, error) {
	var template Template
	err := s.db.QueryRow(ctx, templateSelect+" WHERE id = $1", strings.TrimSpace(id)).Scan(templateFields(&template)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	return template, err
}

func (s *PostgreSQLStore) List(ctx context.Context, limit int) ([]Template, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, templateSelect+" ORDER BY updated_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Template, 0)
	for rows.Next() {
		var template Template
		if err := rows.Scan(templateFields(&template)...); err != nil {
			return nil, err
		}
		result = append(result, template)
	}
	return result, rows.Err()
}

func (s *PostgreSQLStore) UpdateDraft(ctx context.Context, id string, expectedRevision int, input Input, actorID string) (Template, error) {
	if err := Validate(input); err != nil {
		return Template{}, err
	}
	filters, err := json.Marshal(input.EventFilters)
	if err != nil {
		return Template{}, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback(ctx)
	if expectedRevision <= 0 {
		return Template{}, ErrConflict
	}
	var current Template
	if err := tx.QueryRow(ctx, templateSelect+" WHERE id = $1 FOR UPDATE", id).Scan(templateFields(&current)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Template{}, ErrTemplateNotFound
		}
		return Template{}, err
	}
	if current.Revision != expectedRevision {
		return Template{}, ErrConflict
	}
	if current.Enabled {
		return Template{}, ErrPublished
	}
	now := time.Now().UTC()
	var updated Template
	if err := tx.QueryRow(ctx, `
		UPDATE automation_templates SET name = $2, description = $3, kind = $4,
			provider = $5, repository_scope = $6, event_filters = $7,
			agent_template = $8, execution_budget = $9, timeout_seconds = $10,
			concurrency_limit = $11, risk = $12, approval_policy = $13,
			updated_by = NULLIF($14, '')::uuid, revision = revision + 1, updated_at = $15
		WHERE id = $1
		RETURNING `+templateColumns,
		id, strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), input.Kind, input.Provider,
		input.RepositoryScope, filters, strings.TrimSpace(input.AgentTemplate), input.ExecutionBudget,
		input.TimeoutSeconds, input.ConcurrencyLimit, input.Risk, input.ApprovalPolicy, actorID, now).
		Scan(templateFields(&updated)...); err != nil {
		return Template{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return updated, nil
}

func (s *PostgreSQLStore) Publish(ctx context.Context, id string, expectedRevision int, actorID string) (Template, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback(ctx)
	template, err := lockTemplate(ctx, tx, id, expectedRevision)
	if err != nil {
		return Template{}, err
	}
	if template.Version > 0 && template.Enabled {
		return Template{}, ErrPublished
	}
	if err := Validate(template.Input()); err != nil {
		return Template{}, err
	}
	updated, err := publishLocked(ctx, tx, template, actorID, false)
	if err != nil {
		return Template{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return updated, nil
}

func (s *PostgreSQLStore) SetEnabled(ctx context.Context, id string, expectedRevision int, enabled bool, actorID string) (Template, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback(ctx)
	template, err := lockTemplate(ctx, tx, id, expectedRevision)
	if err != nil {
		return Template{}, err
	}
	if template.Version == 0 {
		return Template{}, fmt.Errorf("%w: publish the template before activation", ErrValidation)
	}
	if template.Enabled == enabled {
		return template, tx.Commit(ctx)
	}
	updated, err := publishLocked(ctx, tx, template, actorID, enabled)
	if err != nil {
		return Template{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return updated, nil
}

func (s *PostgreSQLStore) Delete(ctx context.Context, id string, expectedRevision int, _ string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var revision int
	var enabled bool
	err = tx.QueryRow(ctx, "SELECT revision, enabled FROM automation_templates WHERE id = $1 FOR UPDATE", id).Scan(&revision, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTemplateNotFound
	}
	if err != nil {
		return err
	}
	if revision != expectedRevision {
		return ErrConflict
	}
	if enabled {
		return fmt.Errorf("%w: disable the template before deleting it", ErrConflict)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM automation_templates WHERE id = $1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgreSQLStore) History(ctx context.Context, id string, limit int) ([]Version, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT template_id::text, version, enabled, config, COALESCE(changed_by::text, ''), changed_at
		FROM automation_template_versions WHERE template_id = $1
		ORDER BY version DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Version, 0)
	for rows.Next() {
		var item Version
		if err := rows.Scan(&item.TemplateID, &item.Version, &item.Enabled, &item.Config, &item.ChangedBy, &item.ChangedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *PostgreSQLStore) Trigger(ctx context.Context, input TriggerInput) (TriggerResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TriggerResult{}, err
	}
	defer tx.Rollback(ctx)
	template, err := lockTemplate(ctx, tx, input.TemplateID, 0)
	if err != nil {
		return TriggerResult{}, err
	}
	if !template.Enabled {
		return TriggerResult{}, ErrDisabled
	}
	if !template.Matches(input.Provider, input.Event, input.Action, input.Branch, input.Repository) {
		return TriggerResult{}, ErrInvalidTrigger
	}
	var result TriggerResult
	var existing Run
	var existingTask tasks.Task
	err = tx.QueryRow(ctx, runSelect+" WHERE ar.template_id = $1 AND ar.trigger_key = $2", input.TemplateID, input.TriggerKey).
		Scan(runFields(&existing)...)
	if err == nil {
		if err := tx.QueryRow(ctx, taskSelectForRun+" WHERE t.id = $1", existing.TaskID).Scan(taskFields(&existingTask)...); err != nil {
			return TriggerResult{}, err
		}
		result.Run, result.Task, result.Duplicate = existing, existingTask, true
		return result, ErrDuplicateTrigger
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return TriggerResult{}, err
	}
	var active int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM automation_runs ar JOIN tasks t ON t.id = ar.task_id
		WHERE ar.template_id = $1 AND t.status IN ('queued', 'running', 'awaiting_approval')`, input.TemplateID).Scan(&active); err != nil {
		return TriggerResult{}, err
	}
	if active >= template.ConcurrencyLimit {
		return TriggerResult{}, ErrConcurrencyLimit
	}
	if strings.TrimSpace(input.ActorID) == "" {
		return TriggerResult{}, ErrInvalidTrigger
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	taskID, err := newID()
	if err != nil {
		return TriggerResult{}, err
	}
	runID, err := newID()
	if err != nil {
		return TriggerResult{}, err
	}
	sourceKey := "automation:" + template.ID + ":" + triggerDigest(input.TriggerKey)
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = template.Name + " · " + input.Repository
	}
	maxAttempts := 3
	if template.Kind == tasks.KindIssueRepair {
		maxAttempts = 5
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO tasks(id, kind, title, repository_id, repository_name, source_key, payload,
			priority, max_attempts, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9, $10::uuid, $11, $11)
		RETURNING `+taskColumns, taskID, template.Kind, title, input.RepositoryID, input.Repository,
		sourceKey, input.TaskPayload, priorityFor(template.Kind), maxAttempts, input.ActorID, input.CreatedAt).
		Scan(taskFields(&result.Task)...); err != nil {
		return TriggerResult{}, err
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO automation_runs(id, template_id, template_version, trigger_key, provider, event,
			action, repository, task_id, status, source_payload, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'scheduled', $10, $11::uuid, $12, $12)
		RETURNING `+runReturningColumns, runID, template.ID, template.Version, input.TriggerKey, input.Provider,
		input.Event, input.Action, input.Repository, taskID, input.SourcePayload, input.ActorID, input.CreatedAt).
		Scan(runFields(&result.Run)...); err != nil {
		return TriggerResult{}, err
	}
	result.Run.TaskStatus = tasks.StatusQueued
	if err := tx.Commit(ctx); err != nil {
		return TriggerResult{}, err
	}
	return result, nil
}

func (s *PostgreSQLStore) Runs(ctx context.Context, templateID string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, runSelect+` WHERE ar.template_id = $1 ORDER BY ar.created_at DESC LIMIT $2`, templateID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Run, 0)
	for rows.Next() {
		var item Run
		if err := rows.Scan(runFields(&item)...); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

const templateColumns = `id::text, name, description, kind, provider, enabled, repository_scope,
event_filters, agent_template, execution_budget, timeout_seconds, concurrency_limit, risk,
approval_policy, version, revision, COALESCE(created_by::text, ''), COALESCE(updated_by::text, ''),
COALESCE(published_by::text, ''), created_at, updated_at, published_at`

const templateSelect = "SELECT " + templateColumns + " FROM automation_templates"

const taskColumns = `id::text, kind, status, title, COALESCE(repository_id::text, ''), repository_name,
source_key, payload, priority, attempts, max_attempts, available_at, lease_owner, lease_until,
last_error, COALESCE(superseded_by::text, ''), COALESCE(created_by::text, ''), created_at,
updated_at, started_at, finished_at`

const taskSelectForRun = "SELECT " + taskColumns + " FROM tasks t"

const runColumns = `ar.id::text, ar.template_id::text, ar.template_version, ar.trigger_key, ar.provider,
ar.event, ar.action, ar.repository, ar.task_id::text, ar.status, ar.source_payload, ar.created_at,
ar.updated_at, t.status`

const runReturningColumns = `id::text, template_id::text, template_version, trigger_key, provider, event,
action, repository, task_id::text, status, source_payload, created_at, updated_at, status`

const runSelect = "SELECT " + runColumns + " FROM automation_runs ar JOIN tasks t ON t.id = ar.task_id"

func templateFields(template *Template) []any {
	return []any{&template.ID, &template.Name, &template.Description, &template.Kind, &template.Provider,
		&template.Enabled, &template.RepositoryScope, &template.EventFilters, &template.AgentTemplate,
		&template.ExecutionBudget, &template.TimeoutSeconds, &template.ConcurrencyLimit, &template.Risk,
		&template.ApprovalPolicy, &template.Version, &template.Revision, &template.CreatedBy, &template.UpdatedBy,
		&template.PublishedBy, &template.CreatedAt, &template.UpdatedAt, &template.PublishedAt}
}

func taskFields(task *tasks.Task) []any {
	return []any{&task.ID, &task.Kind, &task.Status, &task.Title, &task.RepositoryID, &task.RepositoryName,
		&task.SourceKey, &task.Payload, &task.Priority, &task.Attempts, &task.MaxAttempts, &task.AvailableAt,
		&task.LeaseOwner, &task.LeaseUntil, &task.LastError, &task.SupersededBy, &task.CreatedBy,
		&task.CreatedAt, &task.UpdatedAt, &task.StartedAt, &task.FinishedAt}
}

func runFields(run *Run) []any {
	return []any{&run.ID, &run.TemplateID, &run.TemplateVersion, &run.TriggerKey, &run.Provider,
		&run.Event, &run.Action, &run.Repository, &run.TaskID, &run.Status, &run.SourcePayload,
		&run.CreatedAt, &run.UpdatedAt, &run.TaskStatus}
}

func lockTemplate(ctx context.Context, tx pgx.Tx, id string, expectedRevision int) (Template, error) {
	var template Template
	err := tx.QueryRow(ctx, templateSelect+" WHERE id = $1 FOR UPDATE", strings.TrimSpace(id)).Scan(templateFields(&template)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	if err != nil {
		return Template{}, err
	}
	if expectedRevision > 0 && template.Revision != expectedRevision {
		return Template{}, ErrConflict
	}
	return template, nil
}

func publishLocked(ctx context.Context, tx pgx.Tx, template Template, actorID string, enabled bool) (Template, error) {
	now := time.Now().UTC()
	var updated Template
	if err := tx.QueryRow(ctx, `
		UPDATE automation_templates SET enabled = $2, version = version + 1, revision = revision + 1,
			updated_by = NULLIF($3, '')::uuid, published_by = NULLIF($3, '')::uuid,
			published_at = $4, updated_at = $4
		WHERE id = $1 RETURNING `+templateColumns, template.ID, enabled, actorID, now).Scan(templateFields(&updated)...); err != nil {
		return Template{}, err
	}
	config, err := json.Marshal(map[string]any{"name": updated.Name, "description": updated.Description, "kind": updated.Kind,
		"provider": updated.Provider, "enabled": updated.Enabled, "repositoryScope": updated.RepositoryScope,
		"eventFilters": updated.EventFilters, "agentTemplate": updated.AgentTemplate, "executionBudget": updated.ExecutionBudget,
		"timeoutSeconds": updated.TimeoutSeconds, "concurrencyLimit": updated.ConcurrencyLimit, "risk": updated.Risk,
		"approvalPolicy": updated.ApprovalPolicy})
	if err != nil {
		return Template{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO automation_template_versions(template_id, version, enabled, config, changed_by, changed_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, $6)`, updated.ID, updated.Version, updated.Enabled, config, actorID, now); err != nil {
		return Template{}, err
	}
	return updated, nil
}

func priorityFor(kind tasks.Kind) int {
	switch kind {
	case tasks.KindCIDiagnosis:
		return 60
	case tasks.KindIssueRepair:
		return 40
	default:
		return 50
	}
}

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(value[:4]), hex.EncodeToString(value[4:6]),
		hex.EncodeToString(value[6:8]), hex.EncodeToString(value[8:10]), hex.EncodeToString(value[10:])), nil
}
