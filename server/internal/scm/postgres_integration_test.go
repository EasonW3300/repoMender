//go:build integration

package scm

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/jackc/pgx/v5"
)

// database migrations and the auth store create a real actor boundary before
// SCM transaction, snapshot, dedupe, and Outbox invariants are exercised.

func TestPostgreSQLSCMConnectionSnapshotAndWebhookDedupe(t *testing.T) {
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
	if _, err := db.Exec(ctx, `
		TRUNCATE webhook_deliveries, scm_oauth_flows, repositories, scm_connections,
		         outbox_events, sessions, users CASCADE`); err != nil {
		t.Fatalf("truncate M2 tables: %v", err)
	}
	authService := auth.NewService(auth.NewPostgreSQLStore(db), time.Hour)
	admin, err := authService.Bootstrap(ctx, "admin@example.com", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	store := NewPostgreSQLStore(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	stateHash := []byte("state-hash")
	if err := store.CreateFlow(ctx, stateHash, Flow{
		Provider: ProviderGitLab, UserID: admin.ID, Verifier: "verifier",
		ReturnTo: "/repositories",
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("CreateFlow() error = %v", err)
	}
	flow, err := store.ConsumeFlow(ctx, stateHash, now)
	if err != nil || flow.Provider != ProviderGitLab {
		t.Fatalf("ConsumeFlow() = %+v, %v", flow, err)
	}
	if _, err := store.ConsumeFlow(ctx, stateHash, now); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second ConsumeFlow() error = %v, want pgx.ErrNoRows", err)
	}

	remote := RemoteConnection{
		Provider: ProviderGitLab, Name: "acme", ExternalAccountID: "12",
		BaseURL: "https://gitlab.com",
	}
	first, err := store.UpsertConnection(ctx, admin.ID, remote, []byte("encrypted-token"), []RemoteRepository{
		{ProviderRepositoryID: "1", FullName: "acme/one", CloneURL: "https://gitlab.com/acme/one.git", WebURL: "https://gitlab.com/acme/one", DefaultBranch: "main", Visibility: "private"},
		{ProviderRepositoryID: "2", FullName: "acme/two", CloneURL: "https://gitlab.com/acme/two.git", WebURL: "https://gitlab.com/acme/two", DefaultBranch: "main", Visibility: "private"},
	}, now)
	if err != nil {
		t.Fatalf("first UpsertConnection() error = %v", err)
	}
	second, err := store.UpsertConnection(ctx, admin.ID, remote, []byte("rotated-token"), []RemoteRepository{
		{ProviderRepositoryID: "1", FullName: "acme/one-renamed", CloneURL: "https://gitlab.com/acme/one.git", WebURL: "https://gitlab.com/acme/one", DefaultBranch: "trunk", Visibility: "private"},
	}, now.Add(time.Minute))
	if err != nil || second.ID != first.ID {
		t.Fatalf("second UpsertConnection() = %+v, %v", second, err)
	}
	repositories, err := store.ListRepositories(ctx)
	if err != nil || len(repositories) != 2 {
		t.Fatalf("ListRepositories() count = %d, error = %v", len(repositories), err)
	}
	enabled, disabled := 0, 0
	for _, repository := range repositories {
		if repository.Enabled {
			enabled++
			if repository.FullName != "acme/one-renamed" || repository.DefaultBranch != "trunk" {
				t.Fatalf("updated repository = %+v", repository)
			}
		} else {
			disabled++
		}
	}
	if enabled != 1 || disabled != 1 {
		t.Fatalf("snapshot enabled=%d disabled=%d", enabled, disabled)
	}
	stored, err := store.GetConnection(ctx, first.ID)
	if err != nil || string(stored.CredentialCiphertext) != "rotated-token" {
		t.Fatalf("GetConnection() credential = %q, error = %v", stored.CredentialCiphertext, err)
	}

	event := WebhookEvent{
		DeliveryID: "delivery-1", EventType: "push",
		Normalized: map[string]any{"repository": "acme/one-renamed"},
	}
	created, err := store.RecordWebhook(ctx, ProviderGitLab, event, []byte("payload-hash"), now)
	if err != nil || !created {
		t.Fatalf("first RecordWebhook() = %v, %v", created, err)
	}
	created, err = store.RecordWebhook(ctx, ProviderGitLab, event, []byte("payload-hash"), now)
	if err != nil || created {
		t.Fatalf("duplicate RecordWebhook() = %v, %v", created, err)
	}
	var deliveries, outbox int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM webhook_deliveries").Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE topic = 'scm.webhook.gitlab'").Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 || outbox != 1 {
		t.Fatalf("delivery count=%d outbox count=%d", deliveries, outbox)
	}
}
