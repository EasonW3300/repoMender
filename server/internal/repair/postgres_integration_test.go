//go:build integration

package repair

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
)

func TestPostgreSQLRepairRecordPersistsImmutableInputsAndState(t *testing.T) {
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
	userID := "00000000-0000-4000-8000-000000000008"
	taskID := "00000000-0000-4000-8000-000000000009"
	if _, err := db.Exec(ctx, `INSERT INTO users(id, email, display_name, role, auth_source, subject, active) VALUES ($1, $2, 'M8 test', 'admin', 'oidc', $3, true) ON CONFLICT (id) DO NOTHING`, userID, "m8@example.com", "m8-subject"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO tasks(id, kind, title, source_key, payload, max_attempts, created_by) VALUES ($1, 'issue_repair', 'M8 test', $2, '{}'::jsonb, 5, $3) ON CONFLICT (id) DO NOTHING`, taskID, "m8:integration", userID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	store := NewPostgreSQLStore(db)
	created, err := store.Create(ctx, CreateInput{Trigger: TriggerGitHubIssue, SourceKey: "m8:integration", TaskID: taskID, CreatedBy: userID, CreatedAt: time.Now().UTC(), Issue: Issue{Provider: "github", Repository: "example/repo", CloneURL: "https://github.com/example/repo.git", InstallationID: "42", Number: 12, Title: "repair", State: "open", BaseBranch: "main", BaseSHA: strings.Repeat("a", 40)}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	created.State = StateAwaitingPlanApproval
	created.Plan = []byte(`{"schemaVersion":"v1"}`)
	updated, err := store.Update(ctx, created)
	if err != nil || updated.State != StateAwaitingPlanApproval {
		t.Fatalf("Update() = %+v, %v", updated, err)
	}
	byTask, err := store.GetByTask(ctx, taskID)
	if err != nil || byTask.ID != created.ID || byTask.BaseSHA != strings.Repeat("a", 40) {
		t.Fatalf("GetByTask() = %+v, %v", byTask, err)
	}
}
