package execution

import (
	"context"
	"encoding/json"
	"time"
)

// context controls deadlines and cancellation, while json.RawMessage preserves
// schema-validated result JSON without base64 conversion at HTTP boundaries.

type EventKind string

const (
	EventStarted       EventKind = "started"
	EventStatus        EventKind = "status"
	EventLog           EventKind = "log"
	EventTest          EventKind = "test"
	EventAgentActivity EventKind = "agent_activity"
	EventCompleted     EventKind = "completed"
)

type Request struct {
	CorrelationID    string         `json:"correlationId"`
	ProjectID        string         `json:"projectId"`
	AgentName        string         `json:"agentName"`
	Repository       string         `json:"repository"`
	CommitSHA        string         `json:"commitSha"`
	Prompt           string         `json:"prompt"`
	OutputSchemaJSON string         `json:"outputSchemaJson,omitempty"`
	Timeout          time.Duration  `json:"timeout"`
	Policy           ResourcePolicy `json:"policy"`
}

type ResourcePolicy struct {
	Driver         string `json:"driver"`
	NetworkEnabled bool   `json:"networkEnabled"`
	Cleanup        string `json:"cleanup"`
}

type Run struct {
	ID            string    `json:"id"`
	CorrelationID string    `json:"correlationId"`
	StartedAt     time.Time `json:"startedAt"`
}

type Event struct {
	Sequence  uint64    `json:"sequence"`
	RunID     string    `json:"runId"`
	Kind      EventKind `json:"kind"`
	Stream    string    `json:"stream,omitempty"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
	Terminal  bool      `json:"terminal"`
}

type Result struct {
	SchemaVersion string          `json:"schemaVersion"`
	RunID         string          `json:"runId"`
	Status        string          `json:"status"`
	Output        json.RawMessage `json:"output"`
	FinishedAt    time.Time       `json:"finishedAt"`
}

type VersionInfo struct {
	Version         string   `json:"version"`
	OS              string   `json:"os"`
	Arch            string   `json:"arch"`
	CompiledDrivers []string `json:"compiledDrivers"`
}

type Adapter interface {
	Health(context.Context) (VersionInfo, error)
	Start(context.Context, Request) (Run, error)
	Events(context.Context, string, uint64) (<-chan Event, <-chan error)
	Cancel(context.Context, string, string) error
	Result(context.Context, string) (Result, error)
}
