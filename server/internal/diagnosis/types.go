package diagnosis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// scm supplies the verified provider event envelope, while tasks supplies the
// durable kind and payload contract shared by webhook handlers and workers.

const (
	MaxLogBytes      = 8 << 20
	DefaultChunkSize = 32 << 10
)

var (
	ErrUnsupportedEvent = errors.New("unsupported workflow run event")
	ErrInvalidEvent     = errors.New("invalid workflow run event")
	ErrInvalidResult    = errors.New("invalid CI diagnosis result")
	ErrBinaryLogs       = errors.New("workflow logs are binary or invalid UTF-8")
	ErrLogsTooLarge     = errors.New("workflow logs exceed the configured limit")
)

// WorkflowRunEvent is the provider-neutral trigger for a failed CI diagnosis.
// It contains the immutable commit and installation identity needed by the
// worker to fetch logs and run a governed diagnosis without trusting mutable
// branch state later.
type WorkflowRunEvent struct {
	DeliveryID     string
	Action         string
	Conclusion     string
	RepositoryID   string
	RepositoryName string
	CloneURL       string
	WebURL         string
	WorkflowRunID  int64
	WorkflowName   string
	HeadSHA        string
	InstallationID string
}

// Result is the only structured diagnosis response accepted from AC. A
// hypothesis is persisted as a finding while its line-based log evidence is
// persisted separately, allowing the UI and provider summary to share one
// validated source of truth.
type Result struct {
	SchemaVersion string       `json:"schemaVersion"`
	Summary       string       `json:"summary"`
	Hypotheses    []Hypothesis `json:"hypotheses"`
}

type Hypothesis struct {
	RootCause         string     `json:"rootCause"`
	Component         string     `json:"component"`
	Confidence        float64    `json:"confidence"`
	Evidence          []Evidence `json:"evidence"`
	Reproduction      string     `json:"reproduction"`
	RecommendedAction string     `json:"recommendedAction"`
}

type Evidence struct {
	Source    string `json:"source"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Excerpt   string `json:"excerpt"`
}

type LogChunk struct {
	Source    string
	StartLine int
	EndLine   int
	Text      string
}

// OutputSchema is strict because AC output is untrusted input and the
// Responses API rejects unconstrained object schemas. Every nested field is
// required so a partial hypothesis cannot be published as a diagnosis.
const OutputSchema = `{
  "type": "object",
  "required": ["schemaVersion", "summary", "hypotheses"],
  "properties": {
    "schemaVersion": {"type": "string", "const": "v1"},
    "summary": {"type": "string", "maxLength": 20000},
    "hypotheses": {
      "type": "array",
      "minItems": 1,
      "maxItems": 20,
      "items": {
        "type": "object",
        "required": ["rootCause", "component", "confidence", "evidence", "reproduction", "recommendedAction"],
        "properties": {
          "rootCause": {"type": "string", "maxLength": 10000},
          "component": {"type": "string", "maxLength": 1000},
          "confidence": {"type": "number", "minimum": 0, "maximum": 1},
          "evidence": {
            "type": "array",
            "maxItems": 20,
            "items": {
              "type": "object",
              "required": ["source", "startLine", "endLine", "excerpt"],
              "properties": {
                "source": {"type": "string", "maxLength": 1000},
                "startLine": {"type": "integer", "minimum": 1},
                "endLine": {"type": "integer", "minimum": 1},
                "excerpt": {"type": "string", "maxLength": 4000}
              },
              "additionalProperties": false
            }
          },
          "reproduction": {"type": "string", "enum": ["confirmed", "not_reproducible", "not_attempted", "blocked"]},
          "recommendedAction": {"type": "string", "maxLength": 10000}
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}`

// EventFromWebhook rejects successful, in-progress, and unrelated webhook
// events before task creation. The head SHA and run ID make the resulting task
// idempotent and ensure diagnosis evidence remains bound to one run.
func EventFromWebhook(event scm.WebhookEvent) (WorkflowRunEvent, error) {
	if event.EventType != "workflow_run" {
		return WorkflowRunEvent{}, ErrUnsupportedEvent
	}
	value := func(key string) string {
		if raw, ok := event.Normalized[key]; ok {
			return strings.TrimSpace(fmt.Sprint(raw))
		}
		return ""
	}
	if value("action") != "completed" {
		return WorkflowRunEvent{}, ErrUnsupportedEvent
	}
	conclusion := value("conclusion")
	switch conclusion {
	case "failure", "timed_out", "action_required":
	default:
		return WorkflowRunEvent{}, ErrUnsupportedEvent
	}
	runID, err := integerValue(event.Normalized["workflowRunId"])
	if err != nil || runID <= 0 {
		return WorkflowRunEvent{}, fmt.Errorf("%w: workflow run id", ErrInvalidEvent)
	}
	sha := value("headSHA")
	if len(sha) != 40 || strings.Trim(sha, "0123456789abcdefABCDEF") != "" {
		return WorkflowRunEvent{}, fmt.Errorf("%w: head commit", ErrInvalidEvent)
	}
	repositoryID, repositoryName, cloneURL := value("repositoryId"), value("repository"), value("cloneURL")
	if repositoryID == "" || repositoryName == "" || cloneURL == "" {
		return WorkflowRunEvent{}, fmt.Errorf("%w: repository", ErrInvalidEvent)
	}
	return WorkflowRunEvent{
		DeliveryID: event.DeliveryID, Action: "completed", Conclusion: conclusion,
		RepositoryID: repositoryID, RepositoryName: repositoryName, CloneURL: cloneURL,
		WebURL: value("webURL"), WorkflowRunID: runID, WorkflowName: value("workflowName"),
		HeadSHA: sha, InstallationID: value("installationId"),
	}, nil
}

// RedactAndChunkLogs validates the log format, removes explicit and common
// credential forms, and splits only at line boundaries so evidence can point
// back to stable source lines. The total input limit is checked before any
// persistence or AC prompt construction.
func RedactAndChunkLogs(logs string, patterns []string, chunkSize int) ([]LogChunk, error) {
	if len(logs) > MaxLogBytes {
		return nil, ErrLogsTooLarge
	}
	if !utf8.ValidString(logs) || bytes.IndexByte([]byte(logs), 0) >= 0 {
		return nil, ErrBinaryLogs
	}
	if chunkSize <= 0 || chunkSize > MaxLogBytes {
		chunkSize = DefaultChunkSize
	}
	redacted := redact(logs, patterns)
	lines := strings.Split(strings.ReplaceAll(redacted, "\r\n", "\n"), "\n")
	chunks := make([]LogChunk, 0, len(lines)/8+1)
	current := strings.Builder{}
	startLine := 1
	lineNumber := 1
	flush := func(endLine int) {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, LogChunk{Source: "workflow.log", StartLine: startLine, EndLine: endLine, Text: current.String()})
		current.Reset()
		startLine = endLine + 1
	}
	for _, line := range lines {
		candidate := line
		if current.Len() > 0 {
			candidate = "\n" + candidate
		}
		if current.Len() > 0 && current.Len()+len(candidate) > chunkSize {
			flush(lineNumber - 1)
			candidate = line
		}
		// A single oversized line is bounded into fixed pieces while retaining
		// the same source line instead of silently dropping diagnostic context.
		for len(candidate) > chunkSize {
			part := candidate[:chunkSize]
			if current.Len() == 0 {
				startLine = lineNumber
			}
			current.WriteString(part)
			flush(lineNumber)
			candidate = candidate[chunkSize:]
		}
		if candidate != "" {
			if current.Len() == 0 {
				startLine = lineNumber
			}
			current.WriteString(candidate)
		}
		lineNumber++
	}
	flush(lineNumber - 1)
	return chunks, nil
}

var (
	assignmentSecret = regexp.MustCompile(`(?i)(token|secret|password|authorization|api[_-]?key)\s*[:=]\s*([^\s,;]+)`)
	knownToken       = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+|sk-[A-Za-z0-9_-]+)\b`)
)

func redact(value string, patterns []string) string {
	for _, pattern := range patterns {
		if strings.TrimSpace(pattern) != "" {
			value = strings.ReplaceAll(value, pattern, "[REDACTED]")
		}
	}
	value = assignmentSecret.ReplaceAllString(value, "$1=[REDACTED]")
	return knownToken.ReplaceAllString(value, "[REDACTED]")
}

// ValidateResult applies both JSON shape validation and semantic bounds before
// results become findings/evidence or a provider comment.
func ValidateResult(raw json.RawMessage) (Result, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return Result{}, ErrInvalidResult
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil || result.SchemaVersion != "v1" || strings.TrimSpace(result.Summary) == "" || len(result.Summary) > 20000 {
		return Result{}, ErrInvalidResult
	}
	if len(result.Hypotheses) == 0 || len(result.Hypotheses) > 20 {
		return Result{}, ErrInvalidResult
	}
	for index := range result.Hypotheses {
		hypothesis := &result.Hypotheses[index]
		if strings.TrimSpace(hypothesis.RootCause) == "" || strings.TrimSpace(hypothesis.Component) == "" || strings.TrimSpace(hypothesis.RecommendedAction) == "" || math.IsNaN(hypothesis.Confidence) || hypothesis.Confidence < 0 || hypothesis.Confidence > 1 {
			return Result{}, fmt.Errorf("%w: hypothesis %d fields", ErrInvalidResult, index)
		}
		switch hypothesis.Reproduction {
		case "confirmed", "not_reproducible", "not_attempted", "blocked":
		default:
			return Result{}, fmt.Errorf("%w: hypothesis %d reproduction", ErrInvalidResult, index)
		}
		if len(hypothesis.Evidence) > 20 {
			return Result{}, fmt.Errorf("%w: hypothesis %d evidence count", ErrInvalidResult, index)
		}
		for evidenceIndex, evidence := range hypothesis.Evidence {
			if evidence.StartLine < 1 || evidence.EndLine < evidence.StartLine || strings.TrimSpace(evidence.Source) == "" || strings.TrimSpace(evidence.Excerpt) == "" || len(evidence.Excerpt) > 4000 {
				return Result{}, fmt.Errorf("%w: hypothesis %d evidence %d", ErrInvalidResult, index, evidenceIndex)
			}
			if strings.ContainsAny(evidence.Source, "\\\x00\r\n") {
				return Result{}, fmt.Errorf("%w: unsafe evidence source", ErrInvalidResult)
			}
		}
	}
	return result, nil
}

func integerValue(value any) (int64, error) {
	switch number := value.(type) {
	case int:
		return int64(number), nil
	case int64:
		return number, nil
	case float64:
		return int64(number), nil
	case json.Number:
		return number.Int64()
	case string:
		var parsed int64
		_, err := fmt.Sscan(number, &parsed)
		return parsed, err
	default:
		return 0, errors.New("not an integer")
	}
}

type taskPayload struct {
	Provider       string `json:"provider"`
	RepositoryID   string `json:"repositoryId"`
	RepositoryName string `json:"repositoryName"`
	CloneURL       string `json:"cloneURL"`
	WebURL         string `json:"webURL"`
	WorkflowRunID  int64  `json:"workflowRunId"`
	WorkflowName   string `json:"workflowName"`
	Conclusion     string `json:"conclusion"`
	HeadSHA        string `json:"headSHA"`
	InstallationID string `json:"installationId"`
}

func buildTaskInput(event WorkflowRunEvent, repositoryID string) (tasks.CreateInput, error) {
	payload, err := json.Marshal(taskPayload{
		Provider: "github", RepositoryID: event.RepositoryID, RepositoryName: event.RepositoryName,
		CloneURL: event.CloneURL, WebURL: event.WebURL, WorkflowRunID: event.WorkflowRunID,
		WorkflowName: event.WorkflowName, Conclusion: event.Conclusion, HeadSHA: event.HeadSHA,
		InstallationID: event.InstallationID,
	})
	if err != nil {
		return tasks.CreateInput{}, err
	}
	return tasks.CreateInput{
		Kind:         tasks.KindCIDiagnosis,
		Title:        fmt.Sprintf("Diagnose %s · run %d at %.12s", event.RepositoryName, event.WorkflowRunID, event.HeadSHA),
		RepositoryID: repositoryID, RepositoryName: event.RepositoryName,
		SourceKey: fmt.Sprintf("github:workflow_run:%s:%d:%s", event.RepositoryID, event.WorkflowRunID, event.HeadSHA),
		Payload:   payload, Priority: 60, MaxAttempts: 3,
	}, nil
}

func decodeTaskPayload(raw json.RawMessage) (taskPayload, error) {
	var payload taskPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return taskPayload{}, err
	}
	if payload.WorkflowRunID <= 0 || payload.CloneURL == "" || len(payload.HeadSHA) != 40 || strings.Trim(payload.HeadSHA, "0123456789abcdefABCDEF") != "" || !validRepositoryName(payload.RepositoryName) {
		return taskPayload{}, fmt.Errorf("%w: task payload is incomplete", ErrInvalidEvent)
	}
	return payload, nil
}

func validRepositoryName(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		if strings.ContainsAny(part, "\\?#%\"' \r\n") || part == "." || part == ".." {
			return false
		}
	}
	return true
}
