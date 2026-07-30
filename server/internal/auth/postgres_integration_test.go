//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/jackc/pgx/v5"
)

// database.Open and MigrateUp provide a real PostgreSQL boundary for validating
// the identity store's transactions, uniqueness rules, and expiry predicates.

func TestPostgreSQLIdentityLifecycle(t *testing.T) {
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
	if _, err := db.Exec(ctx, "TRUNCATE oidc_flows, sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate identity tables: %v", err)
	}

	store := NewPostgreSQLStore(db)
	service := NewService(store, time.Hour)
	admin, err := service.Bootstrap(ctx, "Admin@Example.com", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if admin.Role != RoleAdmin || admin.Email != "admin@example.com" {
		t.Fatalf("bootstrap user = %#v", admin)
	}
	if _, err := service.Bootstrap(ctx, "second@example.com", "correct-horse-battery"); !errors.Is(err, ErrBootstrapClosed) {
		t.Fatalf("second Bootstrap() error = %v, want ErrBootstrapClosed", err)
	}

	_, sessionToken, csrfToken, err := service.LoginLocal(ctx, admin.Email, "correct-horse-battery")
	if err != nil {
		t.Fatalf("LoginLocal() error = %v", err)
	}
	session, err := service.Authenticate(ctx, sessionToken)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !VerifyCSRF(session, csrfToken) || VerifyCSRF(session, "wrong-token") {
		t.Fatal("CSRF verification did not enforce the bound session token")
	}
	if err := service.Logout(ctx, sessionToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := service.Authenticate(ctx, sessionToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate() after logout error = %v, want ErrUnauthenticated", err)
	}

	state, _, _, err := service.BeginOIDC(ctx, "/settings")
	if err != nil {
		t.Fatalf("BeginOIDC() error = %v", err)
	}
	flow, err := service.ConsumeOIDC(ctx, state)
	if err != nil || flow.ReturnTo != "/settings" {
		t.Fatalf("ConsumeOIDC() = %#v, %v", flow, err)
	}
	if _, err := service.ConsumeOIDC(ctx, state); !errors.Is(err, ErrInvalidFlow) {
		t.Fatalf("second ConsumeOIDC() error = %v, want ErrInvalidFlow", err)
	}

	developer, _, _, err := service.LoginOIDC(ctx, Identity{
		Subject:       "subject-1",
		Email:         "developer@example.com",
		EmailVerified: true,
		Name:          "Developer",
	})
	if err != nil {
		t.Fatalf("LoginOIDC() error = %v", err)
	}
	if developer.Role != RoleDeveloper || developer.AuthSource != "oidc" {
		t.Fatalf("OIDC user = %#v", developer)
	}
	users, err := service.ListUsers(ctx, admin)
	if err != nil || len(users) != 2 {
		t.Fatalf("ListUsers() count = %d, error = %v", len(users), err)
	}
	if _, err := service.ListUsers(ctx, developer); !errors.Is(err, ErrForbidden) {
		t.Fatalf("developer ListUsers() error = %v, want ErrForbidden", err)
	}
}

func TestPostgreSQLExpiredSessionAndOIDCFlowAreRejected(t *testing.T) {
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
	if _, err := db.Exec(ctx, "TRUNCATE oidc_flows, sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate identity tables: %v", err)
	}

	store := NewPostgreSQLStore(db)
	now := time.Now().UTC()
	user := User{
		ID: "00000000-0000-4000-8000-000000000001", Email: "admin@example.com",
		DisplayName: "admin", Role: RoleAdmin, AuthSource: "local", Active: true, CreatedAt: now,
	}
	if err := store.BootstrapAdmin(ctx, user, []byte("unused-test-hash")); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	if err := store.CreateSession(ctx, []byte("expired-session"), []byte("csrf"), user.ID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if _, err := store.FindSession(ctx, []byte("expired-session"), now); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("FindSession() error = %v, want pgx.ErrNoRows", err)
	}
	if err := store.CreateOIDCFlow(ctx, []byte("expired-flow"), "verifier", "/", now.Add(-time.Minute)); err != nil {
		t.Fatalf("CreateOIDCFlow() error = %v", err)
	}
	if _, err := store.ConsumeOIDCFlow(ctx, []byte("expired-flow"), now); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("ConsumeOIDCFlow() error = %v, want pgx.ErrNoRows", err)
	}
}

func TestPostgreSQLOIDCDeveloperDoesNotCloseBootstrap(t *testing.T) {
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
	if _, err := db.Exec(ctx, "TRUNCATE oidc_flows, sessions, users CASCADE"); err != nil {
		t.Fatalf("truncate identity tables: %v", err)
	}

	service := NewService(NewPostgreSQLStore(db), time.Hour)
	if _, _, _, err := service.LoginOIDC(ctx, Identity{
		Subject: "first-subject", Email: "first@example.com",
		EmailVerified: true, Name: "First User",
	}); err != nil {
		t.Fatalf("LoginOIDC() error = %v", err)
	}
	if _, err := service.Bootstrap(ctx, "admin@example.com", "correct-horse-battery"); err != nil {
		t.Fatalf("Bootstrap() after OIDC developer error = %v", err)
	}
	if _, err := service.Bootstrap(ctx, "second@example.com", "correct-horse-battery"); !errors.Is(err, ErrBootstrapClosed) {
		t.Fatalf("second Bootstrap() error = %v, want ErrBootstrapClosed", err)
	}
}
