package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"strings"

	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// PullRequestEvent is the provider-neutral input to the code-review trigger.
// Keeping this shape independent of GitHub lets the deferred GitLab adapter
// reuse the orchestration contract without leaking vendor payloads inward.
type PullRequestEvent struct {
	DeliveryID     string
	Action         string
	RepositoryID   string
	RepositoryName string
	CloneURL       string
	WebURL         string
	PullRequest    int
	BaseBranch     string
	HeadBranch     string
	HeadSHA        string
	Author         string
	InstallationID string
}

// Result is the only result shape accepted from an agent. Unknown fields are
// rejected so a prompt-injected or malformed response cannot silently become
// a publishable review.
type Result struct {
	SchemaVersion string    `json:"schemaVersion"`
	Summary       string    `json:"summary"`
	Findings      []Finding `json:"findings"`
}

type Finding struct {
	Severity    string          `json:"severity"`
	Category    string          `json:"category"`
	Path        string          `json:"path"`
	LineStart   *int            `json:"lineStart,omitempty"`
	LineEnd     *int            `json:"lineEnd,omitempty"`
	Explanation string          `json:"explanation"`
	Evidence    json.RawMessage `json:"evidence"`
	Confidence  *float64        `json:"confidence,omitempty"`
	Remediation string          `json:"remediation,omitempty"`
}

var (
	ErrUnsupportedEvent = errors.New("unsupported pull request event")
	ErrInvalidResult    = errors.New("invalid code review result")
)

const OutputSchema = `{"type":"object","required":["schemaVersion","summary","findings"],"properties":{"schemaVersion":{"type":"string","const":"v1"},"summary":{"type":"string"},"findings":{"type":"array","maxItems":200,"items":{"type":"object","required":["severity","category","path","explanation"],"properties":{"severity":{"type":"string","enum":["critical","high","medium","low","info"]},"category":{"type":"string"},"path":{"type":"string"},"lineStart":{"type":"integer","minimum":1},"lineEnd":{"type":"integer","minimum":1},"explanation":{"type":"string"},"evidence":{"type":"object"},"confidence":{"type":"number","minimum":0,"maximum":1},"remediation":{"type":"string"}},"additionalProperties":false}}},"additionalProperties":false}`

func EventFromWebhook(event scm.WebhookEvent) (PullRequestEvent, error) {
	if event.EventType != "pull_request" {
		return PullRequestEvent{}, ErrUnsupportedEvent
	}
	value := func(key string) string {
		if raw, ok := event.Normalized[key]; ok {
			return strings.TrimSpace(fmt.Sprint(raw))
		}
		return ""
	}
	pr, err := strconvInt(event.Normalized["pullRequestNumber"])
	if err != nil || pr <= 0 {
		return PullRequestEvent{}, fmt.Errorf("%w: pull request number", ErrInvalidResult)
	}
	sha := value("headSHA")
	if len(sha) != 40 || strings.Trim(sha, "0123456789abcdefABCDEF") != "" {
		return PullRequestEvent{}, fmt.Errorf("%w: head commit", ErrInvalidResult)
	}
	action := value("action")
	switch action {
	case "opened", "reopened", "synchronize", "ready_for_review":
	default:
		return PullRequestEvent{}, ErrUnsupportedEvent
	}
	repositoryID := value("repositoryId")
	repositoryName := value("repository")
	if repositoryID == "" || repositoryName == "" {
		return PullRequestEvent{}, fmt.Errorf("%w: repository", ErrInvalidResult)
	}
	return PullRequestEvent{
		DeliveryID: event.DeliveryID, Action: action, RepositoryID: repositoryID,
		RepositoryName: repositoryName, CloneURL: value("cloneURL"), WebURL: value("webURL"),
		PullRequest: pr, BaseBranch: value("baseBranch"), HeadBranch: value("headBranch"),
		HeadSHA: sha, Author: value("author"), InstallationID: value("installationId"),
	}, nil
}

func ValidateResult(raw json.RawMessage) (Result, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return Result{}, ErrInvalidResult
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil || result.SchemaVersion != "v1" || strings.TrimSpace(result.Summary) == "" {
		return Result{}, ErrInvalidResult
	}
	if len(result.Findings) > 200 {
		return Result{}, fmt.Errorf("%w: too many findings", ErrInvalidResult)
	}
	for index := range result.Findings {
		finding := &result.Findings[index]
		switch finding.Severity {
		case "critical", "high", "medium", "low", "info":
		default:
			return Result{}, fmt.Errorf("%w: finding %d severity", ErrInvalidResult, index)
		}
		if finding.Category == "" || finding.Path == "" || path.IsAbs(finding.Path) || hasParentSegment(finding.Path) {
			return Result{}, fmt.Errorf("%w: finding %d location", ErrInvalidResult, index)
		}
		if finding.LineStart != nil && *finding.LineStart < 1 || finding.LineEnd != nil && *finding.LineEnd < 1 {
			return Result{}, fmt.Errorf("%w: finding %d line", ErrInvalidResult, index)
		}
		if finding.LineStart != nil && finding.LineEnd != nil && *finding.LineEnd < *finding.LineStart {
			return Result{}, fmt.Errorf("%w: finding %d line range", ErrInvalidResult, index)
		}
		if finding.Confidence != nil && (math.IsNaN(*finding.Confidence) || *finding.Confidence < 0 || *finding.Confidence > 1) {
			return Result{}, fmt.Errorf("%w: finding %d confidence", ErrInvalidResult, index)
		}
		if len(finding.Evidence) == 0 {
			finding.Evidence = json.RawMessage(`{}`)
		}
	}
	return result, nil
}

func hasParentSegment(value string) bool {
	for _, segment := range strings.Split(strings.ReplaceAll(value, "\\", "/"), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func strconvInt(value any) (int, error) {
	switch number := value.(type) {
	case float64:
		return int(number), nil
	case int:
		return number, nil
	case json.Number:
		var parsed int
		_, err := fmt.Sscan(number.String(), &parsed)
		return parsed, err
	case string:
		var parsed int
		_, err := fmt.Sscan(number, &parsed)
		return parsed, err
	default:
		return 0, errors.New("not an integer")
	}
}

// taskPayload is persisted as an explicit contract so workers can later check
// out exactly the immutable head commit represented by the webhook.
type taskPayload struct {
	Provider       string `json:"provider"`
	RepositoryName string `json:"repositoryName"`
	PullRequest    int    `json:"pullRequestNumber"`
	Action         string `json:"action"`
	HeadSHA        string `json:"headSHA"`
	HeadBranch     string `json:"headBranch"`
	BaseBranch     string `json:"baseBranch"`
	CloneURL       string `json:"cloneURL"`
	WebURL         string `json:"webURL"`
	Author         string `json:"author"`
	InstallationID string `json:"installationId"`
}

func decodeTaskPayload(raw json.RawMessage) (taskPayload, error) {
	var payload taskPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return taskPayload{}, err
	}
	if payload.HeadSHA == "" || payload.CloneURL == "" {
		return taskPayload{}, fmt.Errorf("%w: task payload is incomplete", ErrInvalidResult)
	}
	return payload, nil
}

func buildTaskInput(event PullRequestEvent, repositoryID string) (tasks.CreateInput, error) {
	payload, err := json.Marshal(taskPayload{
		Provider: "github", RepositoryName: event.RepositoryName, PullRequest: event.PullRequest, Action: event.Action,
		HeadSHA: event.HeadSHA, HeadBranch: event.HeadBranch, BaseBranch: event.BaseBranch,
		CloneURL: event.CloneURL, WebURL: event.WebURL, Author: event.Author,
		InstallationID: event.InstallationID,
	})
	if err != nil {
		return tasks.CreateInput{}, err
	}
	return tasks.CreateInput{
		Kind:         tasks.KindCodeReview,
		Title:        fmt.Sprintf("Review %s#%d at %.12s", event.RepositoryName, event.PullRequest, event.HeadSHA),
		RepositoryID: repositoryID, RepositoryName: event.RepositoryName,
		SourceKey: fmt.Sprintf("github:pull_request:%s:%d:%s", event.RepositoryID, event.PullRequest, event.HeadSHA),
		Payload:   payload, Priority: 50, MaxAttempts: 3,
	}, nil
}
