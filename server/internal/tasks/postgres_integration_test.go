//go:build integration

package tasks

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
)

func TestPostgreSQLTaskQueueIsIdempotentAndExclusive(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.MigrateUp(ctx, db); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}

	creatorID := "00000000-0000-4000-8000-000000000004"
	ensureTestUser(t, ctx, db, creatorID)
	prefix := fmt.Sprintf("m4-integration-%d-", time.Now().UnixNano())
	if _, err := db.Exec(ctx, "DELETE FROM tasks WHERE source_key LIKE $1", prefix+"%"); err != nil {
		t.Fatalf("clean test tasks: %v", err)
	}
	store := NewPostgreSQLStore(db)
	created, err := store.CreateTask(ctx, CreateInput{
		Kind: KindCodeReview, Title: "Queue exclusivity", RepositoryName: "repoMender",
		SourceKey: prefix + "one", Payload: []byte(`{"commit":"abc"}`), CreatedBy: creatorID,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	duplicate, err := store.CreateTask(ctx, CreateInput{
		Kind: KindCodeReview, Title: "Different title", RepositoryName: "repoMender",
		SourceKey: created.SourceKey, CreatedBy: creatorID,
	})
	if err != nil {
		t.Fatalf("idempotent CreateTask() error = %v", err)
	}
	if duplicate.ID != created.ID {
		t.Fatalf("duplicate task ID = %s, want %s", duplicate.ID, created.ID)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, owner := range []string{"worker-a", "worker-b"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			_, _, err := store.ClaimTask(ctx, owner, time.Minute)
			results <- err
		}(owner)
	}
	wg.Wait()
	close(results)
	claimed := 0
	for claimErr := range results {
		if claimErr == nil {
			claimed++
		} else if claimErr != ErrTaskNotFound {
			t.Fatalf("unexpected claim error = %v", claimErr)
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed = %d, want exactly one", claimed)
	}
}

func TestPostgreSQLTaskStateAndAuditLifecycle(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.MigrateUp(ctx, db); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}
	creatorID := "00000000-0000-4000-8000-000000000004"
	ensureTestUser(t, ctx, db, creatorID)
	store := NewPostgreSQLStore(db)
	prefix := fmt.Sprintf("m4-lifecycle-%d-", time.Now().UnixNano())
	task, err := store.CreateTask(ctx, CreateInput{Kind: KindCIDiagnosis, Title: "Lifecycle", SourceKey: prefix + "one", CreatedBy: creatorID})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	claimed, run, err := store.ClaimTask(ctx, "worker-lifecycle", time.Minute)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("ClaimTask() = %s, %v", claimed.ID, err)
	}
	if _, err := store.AppendRunEvent(ctx, run.ID, RunEventInput{Kind: "log", Message: "started"}); err != nil {
		t.Fatalf("AppendRunEvent() error = %v", err)
	}
	if _, err := store.AppendRunEvent(ctx, run.ID, RunEventInput{Kind: "completed", Message: "done", Terminal: true}); err != nil {
		t.Fatalf("AppendRunEvent() terminal error = %v", err)
	}
	if err := store.CompleteTask(ctx, task.ID, run.ID, StatusSucceeded, "", "done"); err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}
	if _, err := store.CancelTask(ctx, task.ID, "too late"); err == nil {
		t.Fatal("expected illegal succeeded -> cancelled transition")
	}
	if _, err := store.RecordAudit(ctx, AuditInput{ActorID: creatorID, Action: "task.complete", ResourceType: "task", ResourceID: task.ID, Outcome: "accepted"}); err != nil {
		t.Fatalf("RecordAudit() error = %v", err)
	}
	audit, err := store.ListAuditEvents(ctx, AuditFilter{ResourceType: "task", ResourceID: task.ID})
	if err != nil || len(audit) != 1 {
		t.Fatalf("ListAuditEvents() = %d, %v", len(audit), err)
	}
}

func ensureTestUser(t *testing.T, ctx context.Context, db *database.DB, id string) {
	t.Helper()
	if _, err := db.Exec(ctx, `
		INSERT INTO users(id, email, display_name, role, auth_source, active)
		VALUES ($1, $2, 'M4 test', 'admin', 'local', true)
		ON CONFLICT (id) DO NOTHING`, id, "m4-task-test@example.com"); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
}
