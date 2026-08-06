//go:build integration

package automations

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

func TestPostgreSQLAutomationLifecycleDeduplicationAndConcurrency(t *testing.T) {
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
	if _, err := db.Exec(ctx, "TRUNCATE automation_runs, automation_template_versions, automation_templates, tasks, users CASCADE"); err != nil {
		t.Fatalf("truncate M9 tables: %v", err)
	}
	actorID := "00000000-0000-4000-8000-000000000009"
	if _, err := db.Exec(ctx, `
		INSERT INTO users(id, email, display_name, role, auth_source, subject, active)
		VALUES ($1, 'm9-admin@example.com', 'M9 admin', 'admin', 'oidc', 'm9-admin', true)`, actorID); err != nil {
		t.Fatalf("insert M9 actor: %v", err)
	}

	store := NewPostgreSQLStore(db)
	service := NewService(store, nil)
	input := Input{
		Name: "M9 review", Description: "integration", Kind: tasks.KindCodeReview, Provider: ProviderGitHub,
		RepositoryScope: []string{"acme/payments"}, EventFilters: []EventFilter{{Event: "pull_request", Actions: []string{"opened"}}},
		AgentTemplate: "codex-reviewer", ExecutionBudget: 100, TimeoutSeconds: 300, ConcurrencyLimit: 1,
		Risk: "high", ApprovalPolicy: ApprovalRequired,
	}
	template, err := service.Create(ctx, input, actorID)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	template, err = service.Publish(ctx, template.ID, template.Revision, actorID)
	if err != nil || template.Version != 1 {
		t.Fatalf("Publish() = %+v, %v", template, err)
	}
	template, err = service.SetEnabled(ctx, template.ID, template.Revision, true, actorID)
	if err != nil || !template.Enabled || template.Version != 2 {
		t.Fatalf("SetEnabled() = %+v, %v", template, err)
	}

	trigger := TriggerInput{
		TemplateID: template.ID, TriggerKey: "delivery-1", Provider: ProviderGitHub, Event: "pull_request", Action: "opened",
		Repository: "acme/payments", ActorID: actorID,
		TaskPayload:   []byte(`{"provider":"github","repositoryId":"10","repositoryName":"acme/payments","cloneURL":"https://github.com/acme/payments.git","pullRequestNumber":1,"headSHA":"0123456789abcdef0123456789abcdef01234567"}`),
		SourcePayload: []byte(`{"delivery":"delivery-1"}`), CreatedAt: time.Now().UTC(),
	}
	first, err := service.Trigger(ctx, trigger)
	if err != nil || first.Run.TemplateVersion != 2 || first.Task.Kind != tasks.KindCodeReview {
		t.Fatalf("Trigger() = %+v, %v", first, err)
	}
	duplicate, err := service.Trigger(ctx, trigger)
	if !errors.Is(err, ErrDuplicateTrigger) || !duplicate.Duplicate || duplicate.Run.ID != first.Run.ID {
		t.Fatalf("duplicate Trigger() = %+v, %v", duplicate, err)
	}
	trigger.TriggerKey = "delivery-2"
	if _, err := service.Trigger(ctx, trigger); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("concurrent Trigger() error = %v, want ErrConcurrencyLimit", err)
	}
	template, err = service.SetEnabled(ctx, template.ID, template.Revision, false, actorID)
	if err != nil || template.Enabled {
		t.Fatalf("disable = %+v, %v", template, err)
	}
	trigger.TriggerKey = "delivery-3"
	if _, err := service.Trigger(ctx, trigger); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled Trigger() error = %v, want ErrDisabled", err)
	}
	history, err := service.History(ctx, template.ID, 10)
	if err != nil || len(history) != 3 {
		t.Fatalf("History() = %d, %v", len(history), err)
	}
	if _, err := service.UpdateDraft(ctx, template.ID, template.Revision-1, input, actorID); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateDraft() error = %v, want ErrConflict", err)
	}
}
