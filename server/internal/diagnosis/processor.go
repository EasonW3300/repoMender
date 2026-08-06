package diagnosis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/execution"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// execution runs the immutable commit in AC, while WorkflowProvider fetches
// and publishes through the SCM boundary. Keeping both interfaces narrow
// makes redaction, error mapping, and provider fixtures independently testable.

type WorkflowProvider interface {
	FetchGitHubWorkflowLogs(context.Context, string, string, int64) (string, error)
	PublishGitHubDiagnosis(context.Context, string, string, string, string) error
}

type Processor struct {
	tasks     *tasks.Service
	adapter   execution.Adapter
	provider  WorkflowProvider
	projectID string
	agentName string
	timeout   time.Duration
	patterns  []string
	chunkSize int
}

func NewProcessor(taskService *tasks.Service, adapter execution.Adapter, provider WorkflowProvider, timeout time.Duration, patterns []string) Processor {
	return Processor{
		tasks: taskService, adapter: adapter, provider: provider,
		timeout: timeout, patterns: append([]string(nil), patterns...), chunkSize: DefaultChunkSize,
	}
}

func (p Processor) WithProjectID(projectID string) Processor {
	p.projectID = strings.TrimSpace(projectID)
	return p
}

func (p Processor) WithAgentName(agentName string) Processor {
	p.agentName = strings.TrimSpace(agentName)
	return p
}

// Process fetches and redacts logs before persistence or prompting, then
// executes the immutable workflow commit and converts validated hypotheses to
// durable findings/evidence. Every external failure becomes a stable M6 code
// so queue retry and operator UI behavior remain deterministic.
func (p Processor) Process(ctx context.Context, task tasks.Task, run tasks.Run) (tasks.Status, string, string) {
	if p.tasks == nil || p.adapter == nil || p.provider == nil {
		return tasks.StatusFailed, "m6_dependency_unavailable", "CI diagnosis dependencies are unavailable"
	}
	payload, err := decodeTaskPayload(task.Payload)
	if err != nil {
		return tasks.StatusFailed, "m6_payload_invalid", err.Error()
	}
	if _, err := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{
		Kind: "status", Message: fmt.Sprintf("fetching CI logs for workflow run %d", payload.WorkflowRunID),
	}); err != nil {
		return tasks.StatusFailed, "m6_event_persist_failed", err.Error()
	}
	logs, err := p.provider.FetchGitHubWorkflowLogs(ctx, payload.RepositoryName, payload.InstallationID, payload.WorkflowRunID)
	if err != nil {
		return tasks.StatusFailed, "m6_logs_fetch_failed", err.Error()
	}
	chunks, err := RedactAndChunkLogs(logs, p.patterns, p.chunkSize)
	if err != nil {
		code := "m6_logs_invalid"
		if err == ErrLogsTooLarge {
			code = "m6_logs_too_large"
		} else if err == ErrBinaryLogs {
			code = "m6_logs_binary"
		}
		return tasks.StatusFailed, code, err.Error()
	}
	for _, chunk := range chunks {
		content, _ := json.Marshal(map[string]any{
			"source": chunk.Source, "startLine": chunk.StartLine, "endLine": chunk.EndLine, "text": chunk.Text,
		})
		digest := sha256.Sum256(content)
		if _, err := p.tasks.CreateEvidence(ctx, tasks.EvidenceInput{
			TaskID: task.ID, RunID: run.ID, Kind: "ci_log", Title: chunk.Source,
			Content: content, Digest: hex.EncodeToString(digest[:]),
		}); err != nil {
			return tasks.StatusFailed, "m6_log_persist_failed", err.Error()
		}
	}
	requestTimeout := p.timeout
	if requestTimeout <= 0 {
		requestTimeout = 15 * time.Minute
	}
	projectID := p.projectID
	if projectID == "" {
		projectID = task.RepositoryID
	}
	if projectID == "" {
		projectID = payload.RepositoryID
	}
	agentName := p.agentName
	if agentName == "" {
		agentName = "codex"
	}
	request, err := p.adapter.Start(ctx, execution.Request{
		CorrelationID: run.CorrelationID, ProjectID: projectID, AgentName: agentName,
		Repository: payload.CloneURL, CommitSHA: payload.HeadSHA,
		Prompt: diagnosisPrompt(task, payload, chunks), Timeout: requestTimeout,
		OutputSchemaJSON: OutputSchema,
		Policy:           execution.ResourcePolicy{Driver: "docker", Cleanup: "remove", NetworkEnabled: false},
	})
	if err != nil {
		return tasks.StatusFailed, "m6_ac_start_failed", err.Error()
	}
	if _, err := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{
		Kind: "external_run", Message: "AC CI diagnosis started",
		Payload: json.RawMessage(fmt.Sprintf(`{"externalRunId":%q}`, request.ID)),
	}); err != nil {
		return tasks.StatusFailed, "m6_event_persist_failed", err.Error()
	}
	events, errs := p.adapter.Events(ctx, request.ID, 0)
	for event := range events {
		payload, _ := json.Marshal(event)
		if _, appendErr := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{
			Kind: string(event.Kind), Stream: safeEventText(event.Stream), Message: safeEventText(event.Message),
			Payload: payload, Terminal: event.Terminal,
		}); appendErr != nil {
			return tasks.StatusFailed, "m6_event_persist_failed", appendErr.Error()
		}
	}
	if streamErr := <-errs; streamErr != nil {
		return tasks.StatusFailed, "m6_ac_stream_failed", streamErr.Error()
	}
	result, err := p.adapter.Result(ctx, request.ID)
	if err != nil {
		return tasks.StatusFailed, "m6_ac_result_failed", err.Error()
	}
	validated, err := ValidateResult(result.Output)
	if err != nil {
		return tasks.StatusFailed, "m6_result_invalid", err.Error()
	}
	// AC output is also untrusted: redact it again before findings, evidence, or
	// provider publication in case the model echoes a credential from context.
	validated = redactResult(validated, p.patterns)
	for _, hypothesis := range validated.Hypotheses {
		evidence, _ := json.Marshal(map[string]any{
			"reproduction": hypothesis.Reproduction, "evidence": hypothesis.Evidence,
		})
		if _, err := p.tasks.CreateFinding(ctx, tasks.FindingInput{
			TaskID: task.ID, RunID: run.ID, Severity: diagnosisSeverity(hypothesis.Confidence),
			Category: "ci_root_cause", Path: safeComponent(hypothesis.Component),
			Explanation: hypothesis.RootCause, Evidence: evidence,
			Confidence: &hypothesis.Confidence, Remediation: hypothesis.RecommendedAction,
		}); err != nil {
			return tasks.StatusFailed, "m6_finding_persist_failed", err.Error()
		}
	}
	if err := p.provider.PublishGitHubDiagnosis(ctx, payload.RepositoryName, payload.InstallationID, payload.HeadSHA, diagnosisSummary(validated)); err != nil {
		return tasks.StatusFailed, "m6_publication_failed", err.Error()
	}
	summaryPayload, _ := json.Marshal(map[string]any{"summary": validated.Summary, "hypotheses": len(validated.Hypotheses)})
	if _, err := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{
		Kind: "result", Message: "structured CI diagnosis accepted", Payload: summaryPayload, Terminal: true,
	}); err != nil {
		return tasks.StatusFailed, "m6_event_persist_failed", err.Error()
	}
	return tasks.StatusSucceeded, "", fmt.Sprintf("CI diagnosis completed with %d hypotheses", len(validated.Hypotheses))
}

func diagnosisPrompt(task tasks.Task, payload taskPayload, chunks []LogChunk) string {
	var builder strings.Builder
	builder.WriteString("Analyze the failed GitHub Actions run below. Return only JSON matching the supplied schema; do not modify or publish code.\n")
	builder.WriteString("The sandbox provides a short-lived read-only GitHub token in GH_TOKEN and GITHUB_TOKEN. Use a temporary credential helper or authenticated API to fetch a private repository; never print, persist, or include the token in a URL.\n")
	builder.WriteString("Repository: ")
	builder.WriteString(payload.CloneURL)
	builder.WriteString("\nImmutable commit: ")
	builder.WriteString(payload.HeadSHA)
	builder.WriteString("\nWorkflow: ")
	builder.WriteString(payload.WorkflowName)
	builder.WriteString(fmt.Sprintf("\nWorkflow run: %d\nTask: %s\n", payload.WorkflowRunID, task.ID))
	builder.WriteString("Logs are redacted and each block preserves source line references. Reproduce only when permitted by the sandbox policy.\n")
	for _, chunk := range chunks {
		builder.WriteString(fmt.Sprintf("\n[%s lines %d-%d]\n%s\n", chunk.Source, chunk.StartLine, chunk.EndLine, chunk.Text))
	}
	return builder.String()
}

func diagnosisSummary(result Result) string {
	var builder strings.Builder
	builder.WriteString("## RepoMender CI diagnosis\n\n")
	builder.WriteString(result.Summary)
	for _, hypothesis := range result.Hypotheses {
		builder.WriteString("\n\n- ")
		builder.WriteString(hypothesis.RootCause)
		builder.WriteString(" (confidence ")
		builder.WriteString(fmt.Sprintf("%.0f%%", hypothesis.Confidence*100))
		builder.WriteString(")")
	}
	value := builder.String()
	if len(value) > 18000 {
		return value[:18000] + "\n\n[truncated]"
	}
	return value
}

func redactResult(result Result, patterns []string) Result {
	result.Summary = redact(result.Summary, patterns)
	for hypothesisIndex := range result.Hypotheses {
		hypothesis := &result.Hypotheses[hypothesisIndex]
		hypothesis.RootCause = redact(hypothesis.RootCause, patterns)
		hypothesis.Component = redact(hypothesis.Component, patterns)
		hypothesis.RecommendedAction = redact(hypothesis.RecommendedAction, patterns)
		for evidenceIndex := range hypothesis.Evidence {
			hypothesis.Evidence[evidenceIndex].Source = redact(hypothesis.Evidence[evidenceIndex].Source, patterns)
			hypothesis.Evidence[evidenceIndex].Excerpt = redact(hypothesis.Evidence[evidenceIndex].Excerpt, patterns)
		}
	}
	return result
}

func diagnosisSeverity(confidence float64) string {
	if confidence >= 0.9 {
		return "high"
	}
	if confidence >= 0.7 {
		return "medium"
	}
	return "low"
}

func safeComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "workflow"
	}
	return strings.Trim(strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(value), ".")
}

// safeEventText keeps untrusted AC stream text valid for PostgreSQL TEXT and
// SSE consumers. JSON payloads can represent escaped control bytes, but the
// denormalized stream/message columns must never receive NUL or invalid UTF-8.
func safeEventText(value string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(value, "\uFFFD"), "\x00", "")
}
