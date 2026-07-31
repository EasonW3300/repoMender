package execution

import (
	"context"
	"time"
)

// context controls deadlines and cancellation. These provider-neutral types
// prevent task orchestration from depending on Agent Compose wire structures.

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
	CorrelationID string
	ProjectID     string
	AgentName     string
	Repository    string
	CommitSHA     string
	Prompt        string
	Timeout       time.Duration
	Policy        ResourcePolicy
}

type ResourcePolicy struct {
	Driver         string
	NetworkEnabled bool
	Cleanup        string
}

type Run struct {
	ID            string
	CorrelationID string
	StartedAt     time.Time
}

type Event struct {
	Sequence  uint64
	RunID     string
	Kind      EventKind
	Stream    string
	Message   string
	CreatedAt time.Time
	Terminal  bool
}

type Result struct {
	SchemaVersion string
	RunID         string
	Status        string
	Output        []byte
	FinishedAt    time.Time
}

type VersionInfo struct {
	Version         string
	OS              string
	Arch            string
	CompiledDrivers []string
}

type Adapter interface {
	Health(context.Context) (VersionInfo, error)
	Start(context.Context, Request) (Run, error)
	Events(context.Context, string, uint64) (<-chan Event, <-chan error)
	Cancel(context.Context, string, string) error
	Result(context.Context, string) (Result, error)
}
