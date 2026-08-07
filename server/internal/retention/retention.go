package retention

import (
	"context"
	"errors"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
)

// PostgreSQL transactions make cleanup atomic across audit, webhook, outbox,
// and run-event history; time provides a single UTC cutoff for all tables.

type Result struct {
	RunEvents         int64
	AuditEvents       int64
	WebhookDeliveries int64
	CompletedOutbox   int64
}

func Cutoff(now time.Time, days int) (time.Time, error) {
	if days <= 0 {
		return time.Time{}, errors.New("retention days must be positive")
	}
	return now.UTC().AddDate(0, 0, -days), nil
}

func Prune(ctx context.Context, db *database.DB, cutoff time.Time) (Result, error) {
	if db == nil || cutoff.IsZero() {
		return Result{}, errors.New("retention requires a database and cutoff")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)
	var result Result
	if tag, err := tx.Exec(ctx, "DELETE FROM run_events WHERE created_at < $1", cutoff); err != nil {
		return Result{}, err
	} else {
		result.RunEvents = tag.RowsAffected()
	}
	if tag, err := tx.Exec(ctx, "DELETE FROM audit_events WHERE created_at < $1", cutoff); err != nil {
		return Result{}, err
	} else {
		result.AuditEvents = tag.RowsAffected()
	}
	if tag, err := tx.Exec(ctx, "DELETE FROM webhook_deliveries WHERE received_at < $1", cutoff); err != nil {
		return Result{}, err
	} else {
		result.WebhookDeliveries = tag.RowsAffected()
	}
	// Pending and failed outbox work is operational state, not historical data;
	// only completed deliveries are eligible for automatic retention cleanup.
	if tag, err := tx.Exec(ctx, "DELETE FROM outbox_events WHERE state = 'completed' AND created_at < $1", cutoff); err != nil {
		return Result{}, err
	} else {
		result.CompletedOutbox = tag.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return result, nil
}
