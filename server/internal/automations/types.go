package automations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// Kind, status, and approval policy values are kept in this package so the
// configuration API can reject invalid combinations before a task reaches a
// business worker.
type Kind = tasks.Kind

const (
	ProviderGitHub    = "github"
	ApprovalRequired  = "required"
	ApprovalRiskBased = "risk_based"
)

type EventFilter struct {
	Event    string   `json:"event"`
	Actions  []string `json:"actions,omitempty"`
	Branches []string `json:"branches,omitempty"`
}

type Input struct {
	Name             string
	Description      string
	Kind             Kind
	Provider         string
	RepositoryScope  []string
	EventFilters     []EventFilter
	AgentTemplate    string
	ExecutionBudget  int
	TimeoutSeconds   int
	ConcurrencyLimit int
	Risk             string
	ApprovalPolicy   string
}

type Template struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Description      string        `json:"description,omitempty"`
	Kind             Kind          `json:"kind"`
	Provider         string        `json:"provider"`
	Enabled          bool          `json:"enabled"`
	RepositoryScope  []string      `json:"repositoryScope"`
	EventFilters     []EventFilter `json:"eventFilters"`
	AgentTemplate    string        `json:"agentTemplate"`
	ExecutionBudget  int           `json:"executionBudget"`
	TimeoutSeconds   int           `json:"timeoutSeconds"`
	ConcurrencyLimit int           `json:"concurrencyLimit"`
	Risk             string        `json:"risk"`
	ApprovalPolicy   string        `json:"approvalPolicy"`
	Version          int           `json:"version"`
	Revision         int           `json:"revision"`
	CreatedBy        string        `json:"createdBy,omitempty"`
	UpdatedBy        string        `json:"updatedBy,omitempty"`
	PublishedBy      string        `json:"publishedBy,omitempty"`
	CreatedAt        time.Time     `json:"createdAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
	PublishedAt      *time.Time    `json:"publishedAt,omitempty"`
}

type Version struct {
	TemplateID string          `json:"templateId"`
	Version    int             `json:"version"`
	Enabled    bool            `json:"enabled"`
	Config     json.RawMessage `json:"config"`
	ChangedBy  string          `json:"changedBy,omitempty"`
	ChangedAt  time.Time       `json:"changedAt"`
}

type TriggerInput struct {
	TemplateID    string
	TriggerKey    string
	Provider      string
	Event         string
	Action        string
	Branch        string
	Repository    string
	RepositoryID  string
	Title         string
	TaskPayload   json.RawMessage
	SourcePayload json.RawMessage
	ActorID       string
	CreatedAt     time.Time
}

type TriggerResult struct {
	Run       Run
	Task      tasks.Task
	Duplicate bool
}

type Run struct {
	ID              string          `json:"id"`
	TemplateID      string          `json:"templateId"`
	TemplateVersion int             `json:"templateVersion"`
	TriggerKey      string          `json:"triggerKey"`
	Provider        string          `json:"provider"`
	Event           string          `json:"event"`
	Action          string          `json:"action,omitempty"`
	Repository      string          `json:"repository"`
	TaskID          string          `json:"taskId"`
	TaskStatus      tasks.Status    `json:"taskStatus"`
	Status          string          `json:"status"`
	SourcePayload   json.RawMessage `json:"sourcePayload"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type Store interface {
	Create(context.Context, Input, string) (Template, error)
	Get(context.Context, string) (Template, error)
	List(context.Context, int) ([]Template, error)
	UpdateDraft(context.Context, string, int, Input, string) (Template, error)
	Publish(context.Context, string, int, string) (Template, error)
	SetEnabled(context.Context, string, int, bool, string) (Template, error)
	Delete(context.Context, string, int, string) error
	History(context.Context, string, int) ([]Version, error)
	Trigger(context.Context, TriggerInput) (TriggerResult, error)
	Runs(context.Context, string, int) ([]Run, error)
}

type AuditRecorder interface {
	RecordAudit(context.Context, tasks.AuditInput) (tasks.AuditEvent, error)
}

var (
	ErrTemplateNotFound = errors.New("automation template not found")
	ErrRunNotFound      = errors.New("automation run not found")
	ErrValidation       = errors.New("invalid automation configuration")
	ErrConflict         = errors.New("automation configuration changed concurrently")
	ErrDisabled         = errors.New("automation is disabled")
	ErrDuplicateTrigger = errors.New("automation trigger already scheduled")
	ErrConcurrencyLimit = errors.New("automation concurrency limit reached")
	ErrInvalidTrigger   = errors.New("invalid automation trigger")
	ErrPublished        = errors.New("published automation cannot be edited directly")
)

func Validate(input Input) error {
	if length := len(strings.TrimSpace(input.Name)); length < 1 || length > 120 {
		return fmt.Errorf("%w: name must be 1-120 characters", ErrValidation)
	}
	if len(strings.TrimSpace(input.Description)) > 2000 {
		return fmt.Errorf("%w: description is too long", ErrValidation)
	}
	if input.Provider != ProviderGitHub {
		return fmt.Errorf("%w: provider %q is not enabled", ErrValidation, input.Provider)
	}
	if !tasks.ValidKind(input.Kind) {
		return fmt.Errorf("%w: unsupported task kind", ErrValidation)
	}
	if len(input.RepositoryScope) == 0 || len(input.RepositoryScope) > 100 {
		return fmt.Errorf("%w: repository scope must contain 1-100 repositories", ErrValidation)
	}
	seenRepositories := make(map[string]struct{}, len(input.RepositoryScope))
	for _, repository := range input.RepositoryScope {
		repository = strings.TrimSpace(repository)
		if repository == "" || len(repository) > 200 || strings.ContainsAny(repository, "\r\n\t") || repository == "*" {
			return fmt.Errorf("%w: repository scope contains an invalid entry", ErrValidation)
		}
		if _, exists := seenRepositories[repository]; exists {
			return fmt.Errorf("%w: repository scope contains duplicates", ErrValidation)
		}
		seenRepositories[repository] = struct{}{}
	}
	if len(input.EventFilters) == 0 || len(input.EventFilters) > 20 {
		return fmt.Errorf("%w: at least one event filter is required", ErrValidation)
	}
	for _, filter := range input.EventFilters {
		if err := validateEventFilter(input.Kind, filter); err != nil {
			return err
		}
	}
	if value := strings.TrimSpace(input.AgentTemplate); value == "" || len(value) > 200 {
		return fmt.Errorf("%w: agent template must be 1-200 characters", ErrValidation)
	}
	if input.ExecutionBudget < 1 || input.ExecutionBudget > 100000 {
		return fmt.Errorf("%w: execution budget must be between 1 and 100000", ErrValidation)
	}
	if input.TimeoutSeconds < 30 || input.TimeoutSeconds > 3600 {
		return fmt.Errorf("%w: timeout must be between 30 and 3600 seconds", ErrValidation)
	}
	if input.ConcurrencyLimit < 1 || input.ConcurrencyLimit > 20 {
		return fmt.Errorf("%w: concurrency must be between 1 and 20", ErrValidation)
	}
	if !validRisk(input.Risk) {
		return fmt.Errorf("%w: invalid risk", ErrValidation)
	}
	if input.ApprovalPolicy != ApprovalRequired && input.ApprovalPolicy != ApprovalRiskBased {
		return fmt.Errorf("%w: invalid approval policy", ErrValidation)
	}
	if input.Risk == "critical" && input.ApprovalPolicy != ApprovalRequired {
		return fmt.Errorf("%w: critical automation requires approval", ErrValidation)
	}
	return nil
}

func validateEventFilter(kind Kind, filter EventFilter) error {
	allowedEvent := map[Kind]string{
		tasks.KindCodeReview:  "pull_request",
		tasks.KindCIDiagnosis: "workflow_run",
		tasks.KindIssueRepair: "issues",
	}
	if filter.Event != allowedEvent[kind] {
		return fmt.Errorf("%w: event %q is invalid for %s", ErrValidation, filter.Event, kind)
	}
	allowedActions := map[string]map[string]struct{}{
		"pull_request": {"opened": {}, "reopened": {}, "synchronize": {}, "ready_for_review": {}},
		"workflow_run": {"completed": {}},
		"issues":       {"opened": {}, "reopened": {}},
	}
	if len(filter.Actions) == 0 || len(filter.Actions) > 20 {
		return fmt.Errorf("%w: event actions are required", ErrValidation)
	}
	seen := make(map[string]struct{}, len(filter.Actions))
	for _, action := range filter.Actions {
		action = strings.TrimSpace(action)
		if _, ok := allowedActions[filter.Event][action]; !ok {
			return fmt.Errorf("%w: event action %q is not supported", ErrValidation, action)
		}
		if _, ok := seen[action]; ok {
			return fmt.Errorf("%w: event actions contain duplicates", ErrValidation)
		}
		seen[action] = struct{}{}
	}
	for _, branch := range filter.Branches {
		if branch = strings.TrimSpace(branch); branch == "" || len(branch) > 200 || strings.ContainsAny(branch, "\r\n\t") {
			return fmt.Errorf("%w: branch filter contains an invalid entry", ErrValidation)
		}
	}
	return nil
}

func validRisk(value string) bool {
	switch value {
	case "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

func (t Template) Input() Input {
	return Input{Name: t.Name, Description: t.Description, Kind: t.Kind, Provider: t.Provider,
		RepositoryScope: append([]string(nil), t.RepositoryScope...), EventFilters: append([]EventFilter(nil), t.EventFilters...),
		AgentTemplate: t.AgentTemplate, ExecutionBudget: t.ExecutionBudget, TimeoutSeconds: t.TimeoutSeconds,
		ConcurrencyLimit: t.ConcurrencyLimit, Risk: t.Risk, ApprovalPolicy: t.ApprovalPolicy}
}

func (t Template) Matches(provider, event, action, branch, repository string) bool {
	if !t.Enabled || provider != t.Provider || !contains(t.RepositoryScope, repository) {
		return false
	}
	for _, filter := range t.EventFilters {
		if filter.Event != event || !contains(filter.Actions, action) {
			continue
		}
		if len(filter.Branches) > 0 && !contains(filter.Branches, branch) {
			continue
		}
		return true
	}
	return false
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(wanted) {
			return true
		}
	}
	return false
}
