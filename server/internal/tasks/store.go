package tasks

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/jackc/pgx/v5"
)

// PostgreSQLStore keeps task state, leases, run events, and audit records in
// one database so claiming and state changes remain transactional across API
// and worker processes.
type PostgreSQLStore struct {
	db *database.DB
}

func NewPostgreSQLStore(db *database.DB) *PostgreSQLStore {
	return &PostgreSQLStore{db: db}
}

func (s *PostgreSQLStore) CreateTask(ctx context.Context, input CreateInput) (Task, error) {
	if !ValidKind(input.Kind) {
		return Task{}, ErrInvalidKind
	}
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.SourceKey) == "" || input.CreatedBy == "" {
		return Task{}, errors.New("task title, source key, and creator are required")
	}
	if input.MaxAttempts <= 0 {
		input.MaxAttempts = 3
	}
	if input.Payload == nil {
		input.Payload = json.RawMessage(`{}`)
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	id, err := newID()
	if err != nil {
		return Task{}, err
	}
	var task Task
	err = s.db.QueryRow(ctx, `
		INSERT INTO tasks(id, kind, title, repository_id, repository_name, source_key, payload,
		                 priority, max_attempts, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9, $10, $11, $11)
		ON CONFLICT (source_key) DO UPDATE SET source_key = tasks.source_key
		RETURNING id, kind, status, title, COALESCE(repository_id::text, ''), repository_name,
		          source_key, payload, priority, attempts, max_attempts, available_at,
		          lease_owner, lease_until, last_error, COALESCE(superseded_by::text, ''),
		          created_by, created_at, updated_at, started_at, finished_at`,
		id, input.Kind, strings.TrimSpace(input.Title), input.RepositoryID, input.RepositoryName,
		input.SourceKey, input.Payload, input.Priority, input.MaxAttempts, input.CreatedBy, input.CreatedAt).
		Scan(taskFields(&task)...)
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *PostgreSQLStore) GetTask(ctx context.Context, id string) (Task, error) {
	var task Task
	err := s.db.QueryRow(ctx, taskSelect+" WHERE id = $1", id).Scan(taskFields(&task)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrTaskNotFound
	}
	return task, err
}

func (s *PostgreSQLStore) ListTasks(ctx context.Context, filter ListFilter) ([]Task, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	where := []string{"1 = 1"}
	args := make([]any, 0, 3)
	if filter.Status != "" {
		where = append(where, fmt.Sprintf("status = $%d", len(args)+1))
		args = append(args, filter.Status)
	}
	if filter.Kind != "" {
		where = append(where, fmt.Sprintf("kind = $%d", len(args)+1))
		args = append(args, filter.Kind)
	}
	args = append(args, limit)
	query := taskSelect + " WHERE " + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY updated_at DESC LIMIT $%d", len(args))
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Task, 0)
	for rows.Next() {
		var task Task
		if err := rows.Scan(taskFields(&task)...); err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	return result, rows.Err()
}

func (s *PostgreSQLStore) CancelTask(ctx context.Context, id, reason string) (Task, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	var task Task
	if err := tx.QueryRow(ctx, taskSelect+" WHERE id = $1 FOR UPDATE", id).Scan(taskFields(&task)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Task{}, ErrTaskNotFound
		}
		return Task{}, err
	}
	if !CanTransition(task.Status, StatusCancelled) {
		return Task{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, task.Status, StatusCancelled)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE tasks SET status = 'cancelled', last_error = $2, lease_owner = '', lease_until = NULL,
		                 finished_at = $3, updated_at = $3 WHERE id = $1`, id, strings.TrimSpace(reason), now); err != nil {
		return Task{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE runs SET status = 'cancelled', error_code = 'cancelled', error_message = $2,
		                finished_at = $3
		WHERE task_id = $1 AND status IN ('queued', 'running', 'awaiting_approval')`, id, strings.TrimSpace(reason), now); err != nil {
		return Task{}, err
	}
	task.Status, task.LastError, task.LeaseOwner, task.LeaseUntil, task.FinishedAt, task.UpdatedAt = StatusCancelled, strings.TrimSpace(reason), "", nil, &now, now
	return task, tx.Commit(ctx)
}

func (s *PostgreSQLStore) RetryTask(ctx context.Context, id, reason string) (Task, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	var task Task
	if err := tx.QueryRow(ctx, taskSelect+" WHERE id = $1 FOR UPDATE", id).Scan(taskFields(&task)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Task{}, ErrTaskNotFound
		}
		return Task{}, err
	}
	if !CanTransition(task.Status, StatusQueued) {
		return Task{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, task.Status, StatusQueued)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE tasks SET status = 'queued', last_error = $2, available_at = $3,
		                 lease_owner = '', lease_until = NULL, finished_at = NULL, updated_at = $3
		WHERE id = $1`, id, strings.TrimSpace(reason), now); err != nil {
		return Task{}, err
	}
	task.Status, task.LastError, task.AvailableAt, task.LeaseOwner, task.LeaseUntil, task.FinishedAt, task.UpdatedAt = StatusQueued, strings.TrimSpace(reason), now, "", nil, nil, now
	return task, tx.Commit(ctx)
}

func (s *PostgreSQLStore) ClaimTask(ctx context.Context, owner string, lease time.Duration) (Task, Run, error) {
	if strings.TrimSpace(owner) == "" {
		return Task{}, Run{}, errors.New("worker owner is required")
	}
	if lease <= 0 {
		return Task{}, Run{}, errors.New("lease must be positive")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Task{}, Run{}, err
	}
	defer tx.Rollback(ctx)
	var task Task
	err = tx.QueryRow(ctx, taskSelect+`
		 WHERE ((status IN ('queued', 'failed') AND available_at <= now())
		     OR (status = 'running' AND lease_until IS NOT NULL AND lease_until < now()))
		   AND attempts < max_attempts
		 ORDER BY priority DESC, available_at, created_at
		 FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(taskFields(&task)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, Run{}, ErrTaskNotFound
	}
	if err != nil {
		return Task{}, Run{}, err
	}
	now := time.Now().UTC()
	leaseUntil := now.Add(lease)
	attempt := task.Attempts + 1
	// An expired lease is a failed attempt. Marking its run terminal before
	// creating the replacement prevents a stale worker from completing it later.
	if task.Status == StatusRunning {
		if _, err := tx.Exec(ctx, `
			UPDATE runs SET status = 'failed', error_code = 'lease_expired',
			                error_message = 'worker lease expired', finished_at = $2
			WHERE task_id = $1 AND status = 'running'`, task.ID, now); err != nil {
			return Task{}, Run{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks SET status = 'running', attempts = $2, lease_owner = $3, lease_until = $4,
		                 started_at = COALESCE(started_at, $5), updated_at = $5
		WHERE id = $1`, task.ID, attempt, owner, leaseUntil, now); err != nil {
		return Task{}, Run{}, err
	}
	runID, err := newID()
	if err != nil {
		return Task{}, Run{}, err
	}
	correlationID, err := newID()
	if err != nil {
		return Task{}, Run{}, err
	}
	var run Run
	if err := tx.QueryRow(ctx, `
		INSERT INTO runs(id, task_id, attempt, status, correlation_id, created_at, started_at)
		VALUES ($1, $2, $3, 'running', $4, $5, $5)
		RETURNING id, task_id, attempt, status, correlation_id, external_run_id, error_code,
		          error_message, created_at, started_at, finished_at`,
		runID, task.ID, attempt, correlationID, now).Scan(runFields(&run)...); err != nil {
		return Task{}, Run{}, err
	}
	task.Status, task.Attempts, task.LeaseOwner, task.LeaseUntil, task.StartedAt, task.UpdatedAt = StatusRunning, attempt, owner, &leaseUntil, &now, now
	return task, run, tx.Commit(ctx)
}

func (s *PostgreSQLStore) Heartbeat(ctx context.Context, id, owner string, lease time.Duration) error {
	if lease <= 0 {
		return errors.New("lease must be positive")
	}
	result, err := s.db.Exec(ctx, `
		UPDATE tasks SET lease_until = $3, updated_at = $3
		WHERE id = $1 AND status = 'running' AND lease_owner = $2`, id, owner, time.Now().UTC().Add(lease))
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgreSQLStore) CompleteTask(ctx context.Context, taskID, runID string, status Status, code, message string) error {
	if status != StatusSucceeded && status != StatusFailed && status != StatusCancelled && status != StatusAwaitingApproval {
		return ErrInvalidStatus
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current Status
	if err := tx.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1 FOR UPDATE", taskID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTaskNotFound
		}
		return err
	}
	if runID != "" {
		var runStatus Status
		if err := tx.QueryRow(ctx, "SELECT status FROM runs WHERE id = $1 AND task_id = $2 FOR UPDATE", runID, taskID).Scan(&runStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRunNotFound
			}
			return err
		}
		if runStatus != StatusRunning {
			if current == status && runStatus == status {
				return tx.Commit(ctx)
			}
			return ErrLeaseLost
		}
	}
	if current == status && (status == StatusSucceeded || status == StatusFailed || status == StatusCancelled) {
		return tx.Commit(ctx)
	}
	if !CanTransition(current, status) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, status)
	}
	now := time.Now().UTC()
	finished := status == StatusSucceeded || status == StatusFailed || status == StatusCancelled
	if _, err := tx.Exec(ctx, `
		UPDATE tasks SET status = $2, last_error = $3, lease_owner = '', lease_until = NULL,
		                 finished_at = CASE WHEN $4 THEN $5 ELSE finished_at END, updated_at = $5
		WHERE id = $1`, taskID, status, strings.TrimSpace(message), finished, now); err != nil {
		return err
	}
	if runID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE runs SET status = $2, error_code = $3, error_message = $4,
			                finished_at = CASE WHEN $5 THEN $6 ELSE finished_at END
			WHERE id = $1 AND task_id = $7`, runID, status, strings.TrimSpace(code), strings.TrimSpace(message), finished, now, taskID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgreSQLStore) GetRun(ctx context.Context, id string) (Run, error) {
	var run Run
	err := s.db.QueryRow(ctx, runSelect+" WHERE id = $1", id).Scan(runFields(&run)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	return run, err
}

func (s *PostgreSQLStore) ListRuns(ctx context.Context, taskID string) ([]Run, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM tasks WHERE id = $1)", taskID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrTaskNotFound
	}
	rows, err := s.db.Query(ctx, runSelect+" WHERE task_id = $1 ORDER BY attempt DESC", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Run, 0)
	for rows.Next() {
		var run Run
		if err := rows.Scan(runFields(&run)...); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (s *PostgreSQLStore) ListAllRuns(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, runSelect+" ORDER BY created_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Run, 0)
	for rows.Next() {
		var run Run
		if err := rows.Scan(runFields(&run)...); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (s *PostgreSQLStore) ListRunEvents(ctx context.Context, runID string, after int64) ([]RunEvent, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE id = $1)", runID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrRunNotFound
	}
	rows, err := s.db.Query(ctx, `
		SELECT run_id, sequence, kind, stream, message, payload, terminal, created_at
		FROM run_events WHERE run_id = $1 AND sequence > $2 ORDER BY sequence`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RunEvent, 0)
	for rows.Next() {
		var event RunEvent
		if err := rows.Scan(&event.RunID, &event.Sequence, &event.Kind, &event.Stream, &event.Message, &event.Payload, &event.Terminal, &event.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *PostgreSQLStore) AppendRunEvent(ctx context.Context, runID string, input RunEventInput) (RunEvent, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	var lockedRunID string
	if err := tx.QueryRow(ctx, "SELECT id FROM runs WHERE id = $1 FOR UPDATE", runID).Scan(&lockedRunID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RunEvent{}, ErrRunNotFound
		}
		return RunEvent{}, err
	}
	if lockedRunID == "" {
		return RunEvent{}, ErrRunNotFound
	}
	if input.Payload == nil {
		input.Payload = json.RawMessage(`{}`)
	}
	var event RunEvent
	err = tx.QueryRow(ctx, `
		INSERT INTO run_events(run_id, sequence, kind, stream, message, payload, terminal)
		SELECT $1, COALESCE(MAX(sequence), 0) + 1, $2, $3, $4, $5, $6
		FROM run_events WHERE run_id = $1
		RETURNING run_id, sequence, kind, stream, message, payload, terminal, created_at`,
		runID, input.Kind, input.Stream, input.Message, input.Payload, input.Terminal).Scan(
		&event.RunID, &event.Sequence, &event.Kind, &event.Stream, &event.Message, &event.Payload, &event.Terminal, &event.CreatedAt)
	if err != nil {
		return RunEvent{}, err
	}
	return event, tx.Commit(ctx)
}

func (s *PostgreSQLStore) ListAuditEvents(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	where := []string{"1 = 1"}
	args := make([]any, 0, 3)
	if filter.ResourceType != "" {
		where = append(where, fmt.Sprintf("resource_type = $%d", len(args)+1))
		args = append(args, filter.ResourceType)
	}
	if filter.ResourceID != "" {
		where = append(where, fmt.Sprintf("resource_id = $%d", len(args)+1))
		args = append(args, filter.ResourceID)
	}
	args = append(args, limit)
	query := `SELECT id, COALESCE(actor_id::text, ''), action, resource_type, resource_id, outcome,
	                 correlation_id, metadata, created_at FROM audit_events WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		if err := rows.Scan(&event.ID, &event.ActorID, &event.Action, &event.ResourceType, &event.ResourceID, &event.Outcome, &event.CorrelationID, &event.Metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *PostgreSQLStore) RecordAudit(ctx context.Context, input AuditInput) (AuditEvent, error) {
	if input.Metadata == nil {
		input.Metadata = json.RawMessage(`{}`)
	}
	id, err := newID()
	if err != nil {
		return AuditEvent{}, err
	}
	var event AuditEvent
	err = s.db.QueryRow(ctx, `
		INSERT INTO audit_events(id, actor_id, action, resource_type, resource_id, outcome, correlation_id, metadata)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8)
		RETURNING id, COALESCE(actor_id::text, ''), action, resource_type, resource_id, outcome,
		          correlation_id, metadata, created_at`, id, input.ActorID, input.Action, input.ResourceType,
		input.ResourceID, input.Outcome, input.CorrelationID, input.Metadata).Scan(&event.ID, &event.ActorID,
		&event.Action, &event.ResourceType, &event.ResourceID, &event.Outcome, &event.CorrelationID,
		&event.Metadata, &event.CreatedAt)
	return event, err
}

const taskSelect = `SELECT id, kind, status, title, COALESCE(repository_id::text, ''), repository_name,
                           source_key, payload, priority, attempts, max_attempts, available_at,
                           lease_owner, lease_until, last_error, COALESCE(superseded_by::text, ''),
                           created_by, created_at, updated_at, started_at, finished_at
                    FROM tasks`

const runSelect = `SELECT id, task_id, attempt, status, correlation_id, external_run_id, error_code,
                          error_message, created_at, started_at, finished_at FROM runs`

func taskFields(task *Task) []any {
	return []any{&task.ID, &task.Kind, &task.Status, &task.Title, &task.RepositoryID, &task.RepositoryName,
		&task.SourceKey, &task.Payload, &task.Priority, &task.Attempts, &task.MaxAttempts, &task.AvailableAt,
		&task.LeaseOwner, &task.LeaseUntil, &task.LastError, &task.SupersededBy, &task.CreatedBy,
		&task.CreatedAt, &task.UpdatedAt, &task.StartedAt, &task.FinishedAt}
}

func runFields(run *Run) []any {
	return []any{&run.ID, &run.TaskID, &run.Attempt, &run.Status, &run.CorrelationID, &run.ExternalRunID,
		&run.ErrorCode, &run.ErrorMessage, &run.CreatedAt, &run.StartedAt, &run.FinishedAt}
}

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
