//go:build integration

package retention

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
)

func TestPruneRetainsRecentAndPendingData(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.MigrateUp(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "TRUNCATE run_events, runs, tasks, audit_events, outbox_events, webhook_deliveries, users CASCADE"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC().Add(-2 * time.Hour)
	actorID := "00000000-0000-4000-8000-000000000010"
	taskID := "00000000-0000-4000-8000-000000000011"
	runID := "00000000-0000-4000-8000-000000000012"
	if _, err := db.Exec(ctx, `INSERT INTO users(id, email, display_name, role, auth_source, subject)
		VALUES ($1, 'm10-retention@example.com', 'M10 retention', 'admin', 'oidc', 'm10-retention')`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO tasks(id, kind, title, source_key, created_by, created_at)
		VALUES ($1, 'code_review', 'M10 retention', 'm10-retention', $2, $3)`, taskID, actorID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO runs(id, task_id, attempt, status, correlation_id, created_at)
		VALUES ($1, $2, 1, 'succeeded', 'm10-retention-run', $3)`, runID, taskID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO run_events(run_id, sequence, kind, message, created_at)
		VALUES ($1, 1, 'status', 'old', $2), ($1, 2, 'status', 'recent', $3)`, runID, old, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO audit_events(id, action, resource_type, resource_id, outcome, created_at)
		VALUES ('00000000-0000-4000-8000-000000000013', 'old', 'test', 'old', 'success', $1),
		       ('00000000-0000-4000-8000-000000000014', 'recent', 'test', 'recent', 'success', $2)`, old, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO outbox_events(id, topic, payload, state, created_at, updated_at)
		VALUES ('00000000-0000-4000-8000-000000000015', 'old.completed', '{}', 'completed', $1, $1),
		       ('00000000-0000-4000-8000-000000000016', 'old.pending', '{}', 'pending', $1, $1)`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO webhook_deliveries(id, provider, delivery_id, event_type, payload_sha256, normalized_event, received_at)
		VALUES ('00000000-0000-4000-8000-000000000017', 'github', 'old-delivery', 'push', decode(repeat('00', 32), 'hex'), '{}', $1)`, old); err != nil {
		t.Fatal(err)
	}
	result, err := Prune(ctx, db, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.RunEvents != 1 || result.AuditEvents != 1 || result.CompletedOutbox != 1 || result.WebhookDeliveries != 1 {
		t.Fatalf("unexpected prune result: %+v", result)
	}
	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE state = 'pending'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("pending outbox count = %d, err=%v", count, err)
	}
}
