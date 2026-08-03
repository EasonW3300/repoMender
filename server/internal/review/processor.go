package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/execution"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// Processor is the M5 worker boundary. It executes one immutable commit in AC,
// records the complete event stream, validates the structured response, and
// persists normalized findings before the queue commits the task status.
type Processor struct {
	tasks     *tasks.Service
	adapter   execution.Adapter
	publisher Publisher
	timeout   time.Duration
}

type Publisher interface {
	PublishGitHubReview(context.Context, string, string, int, string, json.RawMessage) error
}

func NewProcessor(taskService *tasks.Service, adapter execution.Adapter, timeout time.Duration, publishers ...Publisher) Processor {
	var publisher Publisher
	if len(publishers) > 0 {
		publisher = publishers[0]
	}
	return Processor{tasks: taskService, adapter: adapter, publisher: publisher, timeout: timeout}
}

func (p Processor) Process(ctx context.Context, task tasks.Task, run tasks.Run) (tasks.Status, string, string) {
	if p.tasks == nil || p.adapter == nil {
		return tasks.StatusFailed, "m5_ac_unavailable", "AC execution is required for code review"
	}
	payload, err := decodeTaskPayload(task.Payload)
	if err != nil {
		return tasks.StatusFailed, "m5_payload_invalid", err.Error()
	}
	if _, err := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{
		Kind: "status", Message: fmt.Sprintf("starting AC review for %s", payload.HeadSHA),
	}); err != nil {
		return tasks.StatusFailed, "m5_event_persist_failed", err.Error()
	}
	requestTimeout := p.timeout
	if requestTimeout <= 0 {
		requestTimeout = 15 * time.Minute
	}
	request, err := p.adapter.Start(ctx, execution.Request{
		CorrelationID: run.CorrelationID, ProjectID: task.RepositoryID,
		AgentName: "repomender-code-review", Repository: payload.CloneURL,
		CommitSHA: payload.HeadSHA, Prompt: reviewPrompt(task, payload),
		Timeout: requestTimeout, OutputSchemaJSON: OutputSchema,
		Policy: execution.ResourcePolicy{Driver: "docker", Cleanup: "remove", NetworkEnabled: false},
	})
	if err != nil {
		return tasks.StatusFailed, "m5_ac_start_failed", err.Error()
	}
	if _, err := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{
		Kind: "external_run", Message: "AC review started", Payload: json.RawMessage(fmt.Sprintf(`{"externalRunId":%q}`, request.ID)),
	}); err != nil {
		return tasks.StatusFailed, "m5_event_persist_failed", err.Error()
	}
	events, errs := p.adapter.Events(ctx, request.ID, 0)
	for event := range events {
		payload, _ := json.Marshal(event)
		if _, appendErr := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{
			Kind: string(event.Kind), Stream: event.Stream, Message: event.Message,
			Payload: payload, Terminal: event.Terminal,
		}); appendErr != nil {
			return tasks.StatusFailed, "m5_event_persist_failed", appendErr.Error()
		}
	}
	if streamErr := <-errs; streamErr != nil {
		return tasks.StatusFailed, "m5_ac_stream_failed", streamErr.Error()
	}
	result, err := p.adapter.Result(ctx, request.ID)
	if err != nil {
		return tasks.StatusFailed, "m5_ac_result_failed", err.Error()
	}
	validated, err := ValidateResult(result.Output)
	if err != nil {
		return tasks.StatusFailed, "m5_result_invalid", err.Error()
	}
	for _, finding := range validated.Findings {
		if _, err := p.tasks.CreateFinding(ctx, tasks.FindingInput{
			TaskID: task.ID, RunID: run.ID, Severity: finding.Severity, Category: finding.Category,
			Path: finding.Path, LineStart: finding.LineStart, LineEnd: finding.LineEnd,
			Explanation: finding.Explanation, Evidence: finding.Evidence,
			Confidence: finding.Confidence, Remediation: finding.Remediation,
		}); err != nil {
			return tasks.StatusFailed, "m5_finding_persist_failed", err.Error()
		}
	}
	if p.publisher != nil {
		if err := p.publisher.PublishGitHubReview(ctx, task.RepositoryName, payload.InstallationID,
			payload.PullRequest, payload.HeadSHA, result.Output); err != nil {
			return tasks.StatusFailed, "m5_publication_failed", err.Error()
		}
	}
	summaryPayload, _ := json.Marshal(map[string]any{"summary": validated.Summary, "findings": len(validated.Findings)})
	if _, err := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{Kind: "result", Message: "structured review result accepted", Payload: summaryPayload, Terminal: true}); err != nil {
		return tasks.StatusFailed, "m5_event_persist_failed", err.Error()
	}
	return tasks.StatusSucceeded, "", fmt.Sprintf("review completed with %d findings", len(validated.Findings))
}

func reviewPrompt(task tasks.Task, payload taskPayload) string {
	return strings.Join([]string{
		"Review the repository commit below for actionable defects.",
		"Return only JSON matching the supplied schema; do not modify or publish code.",
		"Repository: " + payload.CloneURL,
		"Immutable commit: " + payload.HeadSHA,
		"Pull request: " + fmt.Sprint(payload.PullRequest),
		"Task: " + task.ID,
	}, "\n")
}
