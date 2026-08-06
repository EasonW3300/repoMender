package approvals

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/jackc/pgx/v5"
)

// database.DB provides the shared PostgreSQL pool and transaction boundary;
// pgx supplies the no-row sentinel used to preserve the domain not-found error.

type PostgreSQLStore struct {
	db *database.DB
}

func NewPostgreSQLStore(db *database.DB) *PostgreSQLStore {
	return &PostgreSQLStore{db: db}
}

func (s *PostgreSQLStore) Create(ctx context.Context, input CreateInput) (Request, error) {
	if input.Metadata == nil {
		input.Metadata = []byte(`{}`)
	}
	if input.RequestedAt.IsZero() {
		input.RequestedAt = time.Now().UTC()
	}
	id, err := newID()
	if err != nil {
		return Request{}, err
	}
	roles := make([]string, 0, len(input.EligibleRoles))
	for _, role := range input.EligibleRoles {
		roles = append(roles, string(role))
	}
	request, err := scanApproval(s.db.QueryRow(ctx, `
		INSERT INTO approval_requests(
			id, action, state, risk, action_digest, requester_id, eligible_roles,
			requested_at, expires_at, metadata)
		VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+approvalColumns, id, input.Action, input.Risk, strings.TrimSpace(input.ActionDigest),
		input.RequesterID, roles, input.RequestedAt, input.ExpiresAt, input.Metadata))
	return request, err
}

func (s *PostgreSQLStore) Get(ctx context.Context, id string) (Request, error) {
	request, err := scanApproval(s.db.QueryRow(ctx, approvalSelect+" WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	return request, err
}

func (s *PostgreSQLStore) List(ctx context.Context, filter ListFilter) ([]Request, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	args := make([]any, 0, 2)
	where := "1 = 1"
	if filter.State != "" {
		args = append(args, filter.State)
		where += fmt.Sprintf(" AND state = $%d", len(args))
	}
	args = append(args, limit)
	query := approvalSelect + " WHERE " + where + fmt.Sprintf(" ORDER BY requested_at DESC LIMIT $%d", len(args))
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Request, 0)
	for rows.Next() {
		request, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, request)
	}
	return result, rows.Err()
}

func (s *PostgreSQLStore) Decide(ctx context.Context, input DecisionInput) (Request, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	request, err := scanApproval(tx.QueryRow(ctx, approvalSelect+" WHERE id = $1 FOR UPDATE", input.ApprovalID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Request{}, ErrNotFound
		}
		return Request{}, err
	}
	if request.DecisionIdempotencyKey == input.IdempotencyKey {
		if (request.State == StateApproved && input.Decision == StateApproved) || (request.State == StateRejected && input.Decision == StateRejected) {
			return commitRequest(ctx, tx, request)
		}
		return Request{}, ErrIdempotencyConflict
	}
	if request.IsExpired(input.Now) {
		now := input.Now.UTC()
		if _, err := tx.Exec(ctx, `UPDATE approval_requests SET state = 'expired', updated_at = $2 WHERE id = $1`, request.ID, now); err != nil {
			return Request{}, err
		}
		request.State, request.UpdatedAt = StateExpired, now
		if err := tx.Commit(ctx); err != nil {
			return Request{}, err
		}
		return request, ErrExpired
	}
	if request.State != StatePending {
		return Request{}, stateDecisionError(request.State)
	}
	now := input.Now.UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE approval_requests
		SET state = $2, decision_idempotency_key = $3, decision_actor_id = $4,
		    decision_reason = $5, decided_at = $6, updated_at = $6
		WHERE id = $1`, request.ID, input.Decision, input.IdempotencyKey, input.Actor.ID,
		strings.TrimSpace(input.Reason), now); err != nil {
		return Request{}, err
	}
	request.State, request.DecisionIdempotencyKey, request.DecisionActorID = input.Decision, input.IdempotencyKey, input.Actor.ID
	request.DecisionReason, request.DecidedAt, request.UpdatedAt = strings.TrimSpace(input.Reason), &now, now
	return commitRequest(ctx, tx, request)
}

func (s *PostgreSQLStore) Consume(ctx context.Context, input ConsumeInput) (Request, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	request, err := scanApproval(tx.QueryRow(ctx, approvalSelect+" WHERE id = $1 FOR UPDATE", input.ApprovalID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Request{}, ErrNotFound
		}
		return Request{}, err
	}
	if request.ConsumptionIdempotencyKey == input.IdempotencyKey {
		if request.ActionDigest == input.ActionDigest && request.State == StateConsumed {
			return commitRequest(ctx, tx, request)
		}
		return Request{}, ErrIdempotencyConflict
	}
	if request.IsExpired(input.Now) {
		now := input.Now.UTC()
		if _, err := tx.Exec(ctx, `UPDATE approval_requests SET state = 'expired', updated_at = $2 WHERE id = $1`, request.ID, now); err != nil {
			return Request{}, err
		}
		request.State, request.UpdatedAt = StateExpired, now
		if err := tx.Commit(ctx); err != nil {
			return Request{}, err
		}
		return request, ErrExpired
	}
	if err := request.CanConsume(input.Now); err != nil {
		return Request{}, err
	}
	if request.ActionDigest != input.ActionDigest {
		return Request{}, ErrDigestMismatch
	}
	now := input.Now.UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE approval_requests
		SET state = 'consumed', consumption_idempotency_key = $2,
		    consumed_by = $3, consumed_at = $4, updated_at = $4
		WHERE id = $1`, request.ID, input.IdempotencyKey, input.ActorID, now); err != nil {
		return Request{}, err
	}
	request.State, request.ConsumptionIdempotencyKey, request.ConsumedBy = StateConsumed, input.IdempotencyKey, input.ActorID
	request.ConsumedAt, request.UpdatedAt = &now, now
	return commitRequest(ctx, tx, request)
}

func (s *PostgreSQLStore) Cancel(ctx context.Context, input CancelInput) (Request, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	request, err := scanApproval(tx.QueryRow(ctx, approvalSelect+" WHERE id = $1 FOR UPDATE", input.ApprovalID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Request{}, ErrNotFound
		}
		return Request{}, err
	}
	if request.IsExpired(input.Now) {
		now := input.Now.UTC()
		if _, err := tx.Exec(ctx, `UPDATE approval_requests SET state = 'expired', updated_at = $2 WHERE id = $1`, request.ID, now); err != nil {
			return Request{}, err
		}
		request.State, request.UpdatedAt = StateExpired, now
		if err := tx.Commit(ctx); err != nil {
			return Request{}, err
		}
		return request, ErrExpired
	}
	if request.State != StatePending && request.State != StateApproved {
		return Request{}, stateDecisionError(request.State)
	}
	now := input.Now.UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE approval_requests
		SET state = 'cancelled', cancelled_by = $2, cancel_reason = $3,
		    cancelled_at = $4, updated_at = $4
		WHERE id = $1`, request.ID, input.Actor.ID, strings.TrimSpace(input.Reason), now); err != nil {
		return Request{}, err
	}
	request.State, request.CancelledBy, request.CancelReason = StateCancelled, input.Actor.ID, strings.TrimSpace(input.Reason)
	request.CancelledAt, request.UpdatedAt = &now, now
	return commitRequest(ctx, tx, request)
}

func commitRequest(ctx context.Context, tx pgx.Tx, request Request) (Request, error) {
	if err := tx.Commit(ctx); err != nil {
		return Request{}, err
	}
	return request, nil
}

func stateDecisionError(state State) error {
	switch state {
	case StateExpired:
		return ErrExpired
	case StateRejected:
		return ErrRejected
	case StateCancelled:
		return ErrCancelled
	case StateConsumed:
		return ErrConsumed
	default:
		return ErrAlreadyDecided
	}
}

type scanner interface {
	Scan(...any) error
}

func scanApproval(row scanner) (Request, error) {
	var request Request
	var roles []string
	err := row.Scan(&request.ID, &request.Action, &request.State, &request.Risk, &request.ActionDigest,
		&request.RequesterID, &roles, &request.RequestedAt, &request.ExpiresAt,
		&request.DecisionIdempotencyKey, &request.DecisionActorID, &request.DecisionReason,
		&request.DecidedAt, &request.ConsumptionIdempotencyKey, &request.ConsumedBy,
		&request.ConsumedAt, &request.CancelledBy, &request.CancelReason, &request.CancelledAt,
		&request.Metadata, &request.UpdatedAt)
	setRoles(&request, roles)
	return request, err
}

const approvalColumns = `id, action, state, risk, action_digest, requester_id::text,
                         eligible_roles, requested_at, expires_at,
                         COALESCE(decision_idempotency_key, ''), COALESCE(decision_actor_id::text, ''),
                         decision_reason, decided_at, COALESCE(consumption_idempotency_key, ''),
                         COALESCE(consumed_by::text, ''), consumed_at, COALESCE(cancelled_by::text, ''),
                         cancel_reason, cancelled_at, metadata, updated_at`

const approvalSelect = "SELECT " + approvalColumns + " FROM approval_requests"

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func setRoles(request *Request, values []string) {
	request.EligibleRoles = make([]auth.Role, 0, len(values))
	for _, value := range values {
		request.EligibleRoles = append(request.EligibleRoles, auth.Role(value))
	}
}
