package agentcompose

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/execution"
)

// This opt-in test is the M3 real-provider gate. Credentials remain in Agent
// Compose; the test receives only the daemon URL and non-sensitive run inputs.

func TestRealAgentComposeCodexExecution(t *testing.T) {
	if os.Getenv("REPOMENDER_AC_REAL_SMOKE") != "1" {
		t.Skip("set REPOMENDER_AC_REAL_SMOKE=1 to run against a real AC daemon")
	}
	baseURL := os.Getenv("REPOMENDER_AC_BASE_URL")
	projectID := os.Getenv("REPOMENDER_AC_SMOKE_PROJECT_ID")
	repository := os.Getenv("REPOMENDER_AC_SMOKE_REPOSITORY")
	commit := os.Getenv("REPOMENDER_AC_SMOKE_COMMIT")
	if baseURL == "" || projectID == "" || repository == "" || commit == "" {
		t.Fatal("AC base URL, project ID, repository, and commit are required")
	}
	client, err := New(Config{
		BaseURL: baseURL, AuthToken: os.Getenv("REPOMENDER_AC_AUTH_TOKEN"),
		RequestTimeout: 15 * time.Second, RequiredDriver: "docker",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := client.Health(ctx); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	run, err := client.Start(ctx, execution.Request{
		CorrelationID: "m3-real-smoke-" + time.Now().UTC().Format("20060102T150405Z"),
		ProjectID:     projectID, AgentName: "codex", Repository: repository,
		CommitSHA: commit,
		Prompt:    `Return exactly {"schemaVersion":"v1","summary":"RepoMender M3 real AC smoke passed"}. Do not modify any repository.`,
		Timeout:   5 * time.Minute,
		Policy: execution.ResourcePolicy{
			Driver: "docker", Cleanup: "remove",
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	events, errs := client.Events(ctx, run.ID, 0)
	terminal := false
	for event := range events {
		t.Logf("event sequence=%d kind=%s terminal=%v", event.Sequence, event.Kind, event.Terminal)
		terminal = terminal || event.Terminal
	}
	if err := <-errs; err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if !terminal {
		t.Fatal("event stream omitted terminal event")
	}
	result, err := client.Result(ctx, run.ID)
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if result.Status != "succeeded" || result.SchemaVersion != "v1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
