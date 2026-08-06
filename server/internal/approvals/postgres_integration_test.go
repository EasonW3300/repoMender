//go:build integration

package approvals

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/database"
)

func TestPostgreSQLApprovalLifecycleIsExactAndIdempotent(t *testing.T) {
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
	requesterID := "00000000-0000-4000-8000-000000000006"
	approverID := "00000000-0000-4000-8000-000000000007"
	ensureApprovalUser(t, ctx, db, requesterID, "m7-requester@example.com", "developer")
	ensureApprovalUser(t, ctx, db, approverID, "m7-approver@example.com", "maintainer")
	now := time.Now().UTC().Truncate(time.Microsecond)
	service := NewService(NewPostgreSQLStore(db), nil)
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	request, err := service.Create(ctx, CreateInput{
		Action: ActionPublishPatch, Risk: RiskHigh, ActionDigest: digest,
		RequesterID: requesterID, EligibleRoles: []auth.Role{auth.RoleAdmin, auth.RoleMaintainer},
		RequestedAt: now, ExpiresAt: now.Add(time.Hour), Metadata: []byte(`{"source":"m7-integration"}`),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	approved, err := service.Decide(ctx, DecisionInput{
		ApprovalID: request.ID, Actor: auth.User{ID: approverID, Role: auth.RoleMaintainer},
		Decision: StateApproved, Reason: "integration approval", IdempotencyKey: "m7-decision-001", Now: now,
	})
	if err != nil || approved.State != StateApproved {
		t.Fatalf("Decide() = %+v, %v", approved, err)
	}
	consumed, err := service.Consume(ctx, ConsumeInput{
		ApprovalID: request.ID, ActorID: approverID, ActionDigest: digest,
		IdempotencyKey: "m7-consume-001", Now: now,
	})
	if err != nil || consumed.State != StateConsumed {
		t.Fatalf("Consume() = %+v, %v", consumed, err)
	}
	repeated, err := service.Consume(ctx, ConsumeInput{
		ApprovalID: request.ID, ActorID: approverID, ActionDigest: digest,
		IdempotencyKey: "m7-consume-001", Now: now,
	})
	if err != nil || repeated.ID != consumed.ID {
		t.Fatalf("repeated Consume() = %+v, %v", repeated, err)
	}
	if _, err := service.Consume(ctx, ConsumeInput{
		ApprovalID: request.ID, ActorID: approverID,
		ActionDigest:   "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		IdempotencyKey: "m7-consume-002", Now: now,
	}); err != ErrConsumed {
		t.Fatalf("changed digest after consumption error = %v, want ErrConsumed", err)
	}
}

func ensureApprovalUser(t *testing.T, ctx context.Context, db *database.DB, id, email, role string) {
	t.Helper()
	if _, err := db.Exec(ctx, `
		INSERT INTO users(id, email, display_name, role, auth_source, subject, active)
		VALUES ($1, $2, 'M7 approval test', $3, 'oidc', $4, true)
		ON CONFLICT (id) DO UPDATE SET role = EXCLUDED.role, active = true`, id, email, role, "m7-"+role+"-subject"); err != nil {
		t.Fatalf("insert approval test user: %v", err)
	}
}
