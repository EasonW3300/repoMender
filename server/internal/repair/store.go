package repair

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

// PostgreSQL stores the repair state separately from the generic task queue so
// approvals, immutable inputs, patch evidence, and provider publication can be
// inspected after the worker run has completed.
type PostgreSQLStore struct {
	db *database.DB
}

func NewPostgreSQLStore(db *database.DB) *PostgreSQLStore { return &PostgreSQLStore{db: db} }

func (s *PostgreSQLStore) Create(ctx context.Context, input CreateInput) (Repair, error) {
	if err := validateCreateInput(input); err != nil {
		return Repair{}, err
	}
	id, err := newID()
	if err != nil {
		return Repair{}, err
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	var repair Repair
	err = s.db.QueryRow(ctx, `
		INSERT INTO issue_repairs(
			id, task_id, source_key, source_kind, repository_id, repository_name, clone_url, web_url,
			installation_id, issue_number, issue_title, issue_body, base_branch, base_sha, state,
			created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
		        NULLIF($16, '')::uuid, $17, $17)
		ON CONFLICT (source_key) DO UPDATE SET source_key = issue_repairs.source_key
		RETURNING `+repairColumns, id, input.TaskID, input.SourceKey, input.Trigger, input.Issue.RepositoryID,
		input.Issue.Repository, input.Issue.CloneURL, input.Issue.WebURL, input.Issue.InstallationID,
		input.Issue.Number, input.Issue.Title, input.Issue.Body, input.Issue.BaseBranch, input.Issue.BaseSHA,
		StatePlanning, input.CreatedBy, input.CreatedAt).Scan(repairFields(&repair)...)
	if err != nil {
		return Repair{}, err
	}
	return repair, nil
}

func (s *PostgreSQLStore) Get(ctx context.Context, id string) (Repair, error) {
	var repair Repair
	err := s.db.QueryRow(ctx, repairSelect+" WHERE id = $1", strings.TrimSpace(id)).Scan(repairFields(&repair)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Repair{}, ErrNotFound
	}
	return repair, err
}

func (s *PostgreSQLStore) GetByTask(ctx context.Context, taskID string) (Repair, error) {
	var repair Repair
	err := s.db.QueryRow(ctx, repairSelect+" WHERE task_id = $1", strings.TrimSpace(taskID)).Scan(repairFields(&repair)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Repair{}, ErrNotFound
	}
	return repair, err
}

func (s *PostgreSQLStore) List(ctx context.Context, state State, limit int) ([]Repair, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := repairSelect + " WHERE ($1 = '' OR state = $1) ORDER BY updated_at DESC LIMIT $2"
	rows, err := s.db.Query(ctx, query, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Repair, 0)
	for rows.Next() {
		var repair Repair
		if err := rows.Scan(repairFields(&repair)...); err != nil {
			return nil, err
		}
		result = append(result, repair)
	}
	return result, rows.Err()
}

func (s *PostgreSQLStore) Update(ctx context.Context, repair Repair) (Repair, error) {
	if !ValidState(repair.State) {
		return Repair{}, ErrInvalidState
	}
	now := time.Now().UTC()
	if repair.UpdatedAt.IsZero() {
		repair.UpdatedAt = now
	}
	_, err := s.db.Exec(ctx, `
		UPDATE issue_repairs SET state = $2, plan = $3, plan_digest = $4, plan_approval_id = NULLIF($5, '')::uuid,
			patch = $6, patch_digest = $7, patch_approval_id = NULLIF($8, '')::uuid,
			branch_name = $9, draft_pr_number = $10, draft_pr_url = $11,
			failure_code = $12, failure_message = $13, updated_at = $14 WHERE id = $1`,
		repair.ID, repair.State, normalizeJSON(repair.Plan), repair.PlanDigest, repair.PlanApprovalID,
		normalizeJSON(repair.Patch), repair.PatchDigest, repair.PatchApprovalID, repair.BranchName,
		repair.DraftPR.Number, repair.DraftPR.URL, repair.FailureCode, repair.FailureMessage, now)
	if err != nil {
		return Repair{}, err
	}
	return s.Get(ctx, repair.ID)
}

func validateCreateInput(input CreateInput) error {
	if input.Trigger != TriggerGitHubIssue && input.Trigger != TriggerM6Diagnosis {
		return ErrInvalidTrigger
	}
	if strings.TrimSpace(input.SourceKey) == "" || strings.TrimSpace(input.TaskID) == "" {
		return ErrInvalidTrigger
	}
	return ValidateIssue(input.Issue, time.Now().UTC())
}

func normalizeJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage(`{}`)
	}
	return raw
}

const repairColumns = `id, task_id, source_key, source_kind, COALESCE(repository_id::text, ''), repository_name,
                         clone_url, web_url, installation_id, issue_number, issue_title, issue_body,
                         base_branch, base_sha, state, plan, plan_digest, COALESCE(plan_approval_id::text, ''),
                         patch, patch_digest, COALESCE(patch_approval_id::text, ''), branch_name,
                         draft_pr_number, draft_pr_url, failure_code, failure_message,
                         COALESCE(created_by::text, ''), created_at, updated_at`

const repairSelect = "SELECT " + repairColumns + " FROM issue_repairs"

func repairFields(repair *Repair) []any {
	return []any{&repair.ID, &repair.TaskID, &repair.SourceKey, &repair.Trigger, &repair.RepositoryID,
		&repair.Repository, &repair.CloneURL, &repair.WebURL, &repair.InstallationID, &repair.IssueNumber,
		&repair.IssueTitle, &repair.IssueBody, &repair.BaseBranch, &repair.BaseSHA, &repair.State,
		&repair.Plan, &repair.PlanDigest, &repair.PlanApprovalID, &repair.Patch, &repair.PatchDigest,
		&repair.PatchApprovalID, &repair.BranchName, &repair.DraftPR.Number, &repair.DraftPR.URL,
		&repair.FailureCode, &repair.FailureMessage, &repair.CreatedBy, &repair.CreatedAt, &repair.UpdatedAt}
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
