package approvals

import (
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
)

func TestApprovalStateMachineOnlyAllowsForwardProgress(t *testing.T) {
	allowed := [][2]State{
		{StatePending, StateApproved}, {StatePending, StateRejected},
		{StatePending, StateExpired}, {StatePending, StateCancelled},
		{StateApproved, StateConsumed}, {StateApproved, StateCancelled},
	}
	for _, transition := range allowed {
		if !CanTransition(transition[0], transition[1]) {
			t.Fatalf("CanTransition(%q, %q) = false", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]State{
		{StateRejected, StateApproved}, {StateExpired, StateApproved},
		{StateCancelled, StateConsumed}, {StateConsumed, StatePending},
	} {
		if CanTransition(transition[0], transition[1]) {
			t.Fatalf("CanTransition(%q, %q) = true", transition[0], transition[1])
		}
	}
}

func TestValidateCreateRequiresExactDigestAndPrivilegedEligibleRoles(t *testing.T) {
	now := time.Date(2026, time.August, 6, 0, 0, 0, 0, time.UTC)
	base := CreateInput{
		Action: ActionPublishPatch, Risk: RiskHigh,
		ActionDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		RequesterID:  "requester-1", EligibleRoles: []auth.Role{auth.RoleAdmin}, ExpiresAt: now.Add(time.Hour),
	}
	if err := ValidateCreate(base, now); err != nil {
		t.Fatal(err)
	}
	base.ActionDigest = "not-a-digest"
	if err := ValidateCreate(base, now); err != ErrInvalidDigest {
		t.Fatalf("invalid digest error = %v", err)
	}
	base.ActionDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	base.EligibleRoles = []auth.Role{auth.RoleDeveloper}
	if err := ValidateCreate(base, now); err != ErrForbidden {
		t.Fatalf("developer eligible role error = %v", err)
	}
}

func TestRequestCanConsumeRejectsExpiredOrUnapprovedState(t *testing.T) {
	now := time.Now().UTC()
	request := Request{State: StatePending, ExpiresAt: now.Add(-time.Minute)}
	if err := request.CanConsume(now); err != ErrExpired {
		t.Fatalf("expired request error = %v", err)
	}
	request.State, request.ExpiresAt = StateRejected, now.Add(time.Hour)
	if err := request.CanConsume(now); err != ErrRejected {
		t.Fatalf("rejected request error = %v", err)
	}
}

func TestSandboxNetworkApprovalBindsDestinationsAndPolicy(t *testing.T) {
	now := time.Now().UTC()
	base := CreateInput{
		Action: ActionSandboxNetwork, Risk: RiskMedium,
		ActionDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		RequesterID:  "requester-1", EligibleRoles: []auth.Role{auth.RoleAdmin}, ExpiresAt: now.Add(time.Hour),
		Metadata: []byte(`{"destinations":["packages.acme.dev"],"networkPolicy":"allowlist"}`),
	}
	if err := ValidateCreate(base, now); err != nil {
		t.Fatal(err)
	}
	base.Metadata = []byte(`{"destinations":["*.acme.dev"],"networkPolicy":"allowlist"}`)
	if err := ValidateCreate(base, now); err == nil {
		t.Fatal("expected wildcard destination to be rejected")
	}
}
