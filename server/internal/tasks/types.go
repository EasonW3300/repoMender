package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Task state and kind constants are shared by the API, queue worker, and UI so
// every boundary uses the same vocabulary and transition rules.

type Kind string

const (
	KindCodeReview  Kind = "code_review"
	KindCIDiagnosis Kind = "ci_diagnosis"
	KindIssueRepair Kind = "issue_repair"
)

type Status string

const (
	StatusQueued           Status = "queued"
	StatusRunning          Status = "running"
	StatusAwaitingApproval Status = "awaiting_approval"
	StatusSucceeded        Status = "succeeded"
	StatusFailed           Status = "failed"
	StatusCancelled        Status = "cancelled"
	StatusSuperseded       Status = "superseded"
)

var (
	ErrInvalidKind       = errors.New("invalid task kind")
	ErrInvalidStatus     = errors.New("invalid task status")
	ErrInvalidTransition = errors.New("invalid task state transition")
	ErrTaskNotFound      = errors.New("task not found")
	ErrRunNotFound       = errors.New("run not found")
	ErrIdempotency       = errors.New("idempotency key already used")
	ErrLeaseLost         = errors.New("task lease lost")
)

type Task struct {
	ID             string          `json:"id"`
	Kind           Kind            `json:"kind"`
	Status         Status          `json:"status"`
	Title          string          `json:"title"`
	RepositoryID   string          `json:"repositoryId,omitempty"`
	RepositoryName string          `json:"repositoryName"`
	SourceKey      string          `json:"sourceKey"`
	Payload        json.RawMessage `json:"payload"`
	Priority       int             `json:"priority"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"maxAttempts"`
	AvailableAt    time.Time       `json:"availableAt"`
	LeaseOwner     string          `json:"leaseOwner,omitempty"`
	LeaseUntil     *time.Time      `json:"leaseUntil,omitempty"`
	LastError      string          `json:"lastError,omitempty"`
	SupersededBy   string          `json:"supersededBy,omitempty"`
	CreatedBy      string          `json:"createdBy"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	StartedAt      *time.Time      `json:"startedAt,omitempty"`
	FinishedAt     *time.Time      `json:"finishedAt,omitempty"`
}

type Run struct {
	ID            string     `json:"id"`
	TaskID        string     `json:"taskId"`
	Attempt       int        `json:"attempt"`
	Status        Status     `json:"status"`
	CorrelationID string     `json:"correlationId"`
	ExternalRunID string     `json:"externalRunId,omitempty"`
	ErrorCode     string     `json:"errorCode,omitempty"`
	ErrorMessage  string     `json:"errorMessage,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	StartedAt     *time.Time `json:"startedAt,omitempty"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
}

type RunEvent struct {
	RunID     string          `json:"runId"`
	Sequence  int64           `json:"sequence"`
	Kind      string          `json:"kind"`
	Stream    string          `json:"stream,omitempty"`
	Message   string          `json:"message"`
	Payload   json.RawMessage `json:"payload"`
	Terminal  bool            `json:"terminal"`
	CreatedAt time.Time       `json:"createdAt"`
}

type Finding struct {
	ID          string          `json:"id"`
	TaskID      string          `json:"taskId"`
	RunID       string          `json:"runId,omitempty"`
	Severity    string          `json:"severity"`
	Category    string          `json:"category"`
	Path        string          `json:"path"`
	LineStart   *int            `json:"lineStart,omitempty"`
	LineEnd     *int            `json:"lineEnd,omitempty"`
	Explanation string          `json:"explanation"`
	Evidence    json.RawMessage `json:"evidence"`
	Confidence  *float64        `json:"confidence,omitempty"`
	Remediation string          `json:"remediation,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type Evidence struct {
	ID        string          `json:"id"`
	TaskID    string          `json:"taskId"`
	RunID     string          `json:"runId,omitempty"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Content   json.RawMessage `json:"content"`
	Digest    string          `json:"digest,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

type AuditEvent struct {
	ID            string          `json:"id"`
	ActorID       string          `json:"actorId,omitempty"`
	Action        string          `json:"action"`
	ResourceType  string          `json:"resourceType"`
	ResourceID    string          `json:"resourceId"`
	Outcome       string          `json:"outcome"`
	CorrelationID string          `json:"correlationId,omitempty"`
	Metadata      json.RawMessage `json:"metadata"`
	CreatedAt     time.Time       `json:"createdAt"`
}

type CreateInput struct {
	Kind           Kind
	Title          string
	RepositoryID   string
	RepositoryName string
	SourceKey      string
	Payload        json.RawMessage
	Priority       int
	MaxAttempts    int
	CreatedBy      string
	CreatedAt      time.Time
}

type Store interface {
	CreateTask(context.Context, CreateInput) (Task, error)
	GetTask(context.Context, string) (Task, error)
	ListTasks(context.Context, ListFilter) ([]Task, error)
	CancelTask(context.Context, string, string) (Task, error)
	RetryTask(context.Context, string, string) (Task, error)
	ClaimTask(context.Context, string, time.Duration) (Task, Run, error)
	Heartbeat(context.Context, string, string, time.Duration) error
	CompleteTask(context.Context, string, string, Status, string, string) error
	GetRun(context.Context, string) (Run, error)
	ListRuns(context.Context, string) ([]Run, error)
	ListAllRuns(context.Context, int) ([]Run, error)
	ListRunEvents(context.Context, string, int64) ([]RunEvent, error)
	AppendRunEvent(context.Context, string, RunEventInput) (RunEvent, error)
	ListAuditEvents(context.Context, AuditFilter) ([]AuditEvent, error)
	RecordAudit(context.Context, AuditInput) (AuditEvent, error)
	CreateFinding(context.Context, FindingInput) (Finding, error)
	ListFindings(context.Context, string) ([]Finding, error)
	CreateEvidence(context.Context, EvidenceInput) (Evidence, error)
	ListEvidence(context.Context, string) ([]Evidence, error)
	SupersedeCodeReviews(context.Context, string, int, string) error
}

type FindingInput struct {
	TaskID      string
	RunID       string
	Severity    string
	Category    string
	Path        string
	LineStart   *int
	LineEnd     *int
	Explanation string
	Evidence    json.RawMessage
	Confidence  *float64
	Remediation string
}

type EvidenceInput struct {
	TaskID  string
	RunID   string
	Kind    string
	Title   string
	Content json.RawMessage
	Digest  string
}

type ListFilter struct {
	Status Status
	Kind   Kind
	Limit  int
}

type AuditFilter struct {
	ResourceType string
	ResourceID   string
	Limit        int
}

type RunEventInput struct {
	Kind     string
	Stream   string
	Message  string
	Payload  json.RawMessage
	Terminal bool
}

type AuditInput struct {
	ActorID       string
	Action        string
	ResourceType  string
	ResourceID    string
	Outcome       string
	CorrelationID string
	Metadata      json.RawMessage
}

func ValidKind(kind Kind) bool {
	return kind == KindCodeReview || kind == KindCIDiagnosis || kind == KindIssueRepair
}

func ValidStatus(status Status) bool {
	switch status {
	case StatusQueued, StatusRunning, StatusAwaitingApproval, StatusSucceeded,
		StatusFailed, StatusCancelled, StatusSuperseded:
		return true
	default:
		return false
	}
}

// CanTransition centralizes the state machine so HTTP mutations and workers
// reject illegal transitions before they touch durable state.
func CanTransition(from, to Status) bool {
	if from == to {
		return from == StatusRunning || from == StatusAwaitingApproval
	}
	allowed := map[Status][]Status{
		StatusQueued:           {StatusRunning, StatusCancelled, StatusSuperseded},
		StatusRunning:          {StatusAwaitingApproval, StatusSucceeded, StatusFailed, StatusCancelled, StatusSuperseded},
		StatusAwaitingApproval: {StatusSucceeded, StatusFailed, StatusCancelled, StatusSuperseded},
		StatusFailed:           {StatusQueued, StatusCancelled, StatusSuperseded},
		StatusCancelled:        {StatusQueued, StatusSuperseded},
		StatusSucceeded:        {StatusSuperseded},
		StatusSuperseded:       {},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}
