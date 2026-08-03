package review

import (
	"encoding/json"
	"testing"

	"github.com/EasonW3300/repoMender/server/internal/scm"
)

func TestEventFromWebhookUsesImmutableHead(t *testing.T) {
	event, err := EventFromWebhook(scm.WebhookEvent{
		DeliveryID: "delivery-1", EventType: "pull_request",
		Normalized: map[string]any{
			"action": "synchronize", "repositoryId": int64(7), "repository": "acme/payments",
			"pullRequestNumber": 12, "headSHA": "0123456789abcdef0123456789abcdef01234567",
			"headBranch": "feature/fix", "baseBranch": "main", "installationId": int64(42),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.PullRequest != 12 || event.HeadSHA != "0123456789abcdef0123456789abcdef01234567" || event.InstallationID != "42" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestEventFromWebhookIgnoresNonActionableActions(t *testing.T) {
	_, err := EventFromWebhook(scm.WebhookEvent{EventType: "pull_request", Normalized: map[string]any{
		"action": "closed", "repositoryId": 7, "repository": "acme/payments",
		"pullRequestNumber": 12, "headSHA": "0123456789abcdef0123456789abcdef01234567",
	}})
	if err != ErrUnsupportedEvent {
		t.Fatalf("error = %v, want ErrUnsupportedEvent", err)
	}
}

func TestValidateResultRejectsUnknownAndUnsafeFields(t *testing.T) {
	valid, _ := json.Marshal(map[string]any{
		"schemaVersion": "v1", "summary": "one issue", "findings": []any{map[string]any{
			"severity": "high", "category": "bug", "path": "internal/api.go", "lineStart": 3,
			"lineEnd": 4, "explanation": "unsafe input", "evidence": map[string]any{"test": "fixture"},
			"confidence": 0.9,
		}},
	})
	if result, err := ValidateResult(valid); err != nil || len(result.Findings) != 1 {
		t.Fatalf("valid result = %+v, %v", result, err)
	}
	unknown := []byte(`{"schemaVersion":"v1","summary":"x","findings":[],"unexpected":true}`)
	if _, err := ValidateResult(unknown); err == nil {
		t.Fatal("unknown result field accepted")
	}
	unsafe := []byte(`{"schemaVersion":"v1","summary":"x","findings":[{"severity":"high","category":"bug","path":"../secret","explanation":"x"}]}`)
	if _, err := ValidateResult(unsafe); err == nil {
		t.Fatal("unsafe finding path accepted")
	}
}
