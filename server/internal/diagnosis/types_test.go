package diagnosis

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EasonW3300/repoMender/server/internal/scm"
)

func TestEventFromWebhookAcceptsOnlyFailedWorkflowRuns(t *testing.T) {
	event, err := EventFromWebhook(scm.WebhookEvent{
		DeliveryID: "delivery-ci-1", EventType: "workflow_run",
		Normalized: map[string]any{
			"action": "completed", "conclusion": "failure", "repositoryId": int64(7),
			"repository": "acme/payments", "cloneURL": "https://github.com/acme/payments.git",
			"webURL": "https://github.com/acme/payments/actions/runs/99", "workflowRunId": int64(99),
			"headSHA": "0123456789abcdef0123456789abcdef01234567", "workflowName": "CI",
			"installationId": int64(42),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.WorkflowRunID != 99 || event.HeadSHA == "" || event.RepositoryName != "acme/payments" {
		t.Fatalf("unexpected workflow event: %+v", event)
	}

	_, err = EventFromWebhook(scm.WebhookEvent{EventType: "workflow_run", Normalized: map[string]any{
		"action": "completed", "conclusion": "success", "repositoryId": 7,
		"repository": "acme/payments", "workflowRunId": 99,
		"headSHA": "0123456789abcdef0123456789abcdef01234567",
	}})
	if err != ErrUnsupportedEvent {
		t.Fatalf("successful workflow run error = %v, want ErrUnsupportedEvent", err)
	}
}

func TestRedactAndChunkLogsPreserveLineReferences(t *testing.T) {
	logs := "build started\nTOKEN=super-secret\ncompiler failed\n"
	chunks, err := RedactAndChunkLogs(logs, []string{"super-secret"}, 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 || chunks[0].StartLine != 1 || chunks[len(chunks)-1].EndLine < 3 {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
	for _, chunk := range chunks {
		if chunk.Text == "" || containsSecret(chunk.Text, "super-secret") {
			t.Fatalf("secret leaked in chunk: %+v", chunk)
		}
	}
}

func TestRedactAndChunkLogsRejectsBinaryAndOversizedInput(t *testing.T) {
	if _, err := RedactAndChunkLogs("ok\x00binary", nil, 100); err != ErrBinaryLogs {
		t.Fatalf("binary logs error = %v, want ErrBinaryLogs", err)
	}
	if _, err := RedactAndChunkLogs(strings.Repeat("a", MaxLogBytes+1), nil, 4<<10); err != ErrLogsTooLarge {
		t.Fatalf("oversized logs error = %v, want ErrLogsTooLarge", err)
	}
}

func TestValidateResultRejectsUnknownFieldsAndAcceptsEvidence(t *testing.T) {
	valid := []byte(`{"schemaVersion":"v1","summary":"root cause","hypotheses":[{"rootCause":"missing migration","component":"database","confidence":0.9,"evidence":[{"source":"ci.log","startLine":4,"endLine":5,"excerpt":"relation missing"}],"reproduction":"confirmed","recommendedAction":"run migrations"}]}`)
	result, err := ValidateResult(valid)
	if err != nil || len(result.Hypotheses) != 1 {
		t.Fatalf("valid result = %+v, %v", result, err)
	}
	unknown := append(valid[:len(valid)-1], []byte(`,"unexpected":true}`)...)
	if _, err := ValidateResult(unknown); err == nil {
		t.Fatal("unknown result field accepted")
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(OutputSchema), &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatal("root schema must reject additional properties")
	}
	hypotheses := schema["properties"].(map[string]any)["hypotheses"].(map[string]any)
	hypothesis := hypotheses["items"].(map[string]any)
	if hypothesis["additionalProperties"] != false {
		t.Fatal("hypothesis schema must reject additional properties")
	}
	evidence := hypothesis["properties"].(map[string]any)["evidence"].(map[string]any)["items"].(map[string]any)
	if evidence["additionalProperties"] != false {
		t.Fatal("evidence schema must reject additional properties")
	}
}

func TestValidateResultAcceptsControlledReproductionOutcomes(t *testing.T) {
	for _, reproduction := range []string{"confirmed", "not_reproducible", "not_attempted", "blocked"} {
		t.Run(reproduction, func(t *testing.T) {
			raw := []byte(`{"schemaVersion":"v1","summary":"diagnosis","hypotheses":[{"rootCause":"root cause","component":"test","confidence":0.5,"evidence":[],"reproduction":"` + reproduction + `","recommendedAction":"inspect"}]}`)
			if _, err := ValidateResult(raw); err != nil {
				t.Fatalf("ValidateResult() error = %v", err)
			}
		})
	}
}

func TestDecodeTaskPayloadRequiresSafeRepositoryAndImmutableCommit(t *testing.T) {
	valid := taskPayload{RepositoryName: "acme/payments", CloneURL: "https://github.com/acme/payments.git", WorkflowRunID: 99, HeadSHA: "0123456789abcdef0123456789abcdef01234567"}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeTaskPayload(raw); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	valid.RepositoryName = "acme/../payments"
	raw, _ = json.Marshal(valid)
	if _, err := decodeTaskPayload(raw); err == nil {
		t.Fatal("unsafe repository payload accepted")
	}
}

func containsSecret(value, secret string) bool {
	return len(secret) > 0 && len(value) >= len(secret) && stringContains(value, secret)
}

func stringContains(value, needle string) bool {
	for index := 0; index+len(needle) <= len(value); index++ {
		if value[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
