package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
)

// auth supplies the canonical role vocabulary used by session authentication;
// context and time keep every mutation bound to the request and an explicit clock.

type Action string

const (
	ActionRepairPlan     Action = "repair_plan"
	ActionPublishPatch   Action = "publish_patch"
	ActionSandboxNetwork Action = "sandbox_network"
)

type Risk string

const (
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)

type State string

const (
	StatePending   State = "pending"
	StateApproved  State = "approved"
	StateRejected  State = "rejected"
	StateExpired   State = "expired"
	StateCancelled State = "cancelled"
	StateConsumed  State = "consumed"
)

var (
	ErrInvalidAction       = errors.New("invalid approval action")
	ErrInvalidRisk         = errors.New("invalid approval risk")
	ErrInvalidState        = errors.New("invalid approval state")
	ErrInvalidDigest       = errors.New("invalid action digest")
	ErrInvalidExpiry       = errors.New("approval expiry must be in the future")
	ErrInvalidIdempotency  = errors.New("approval idempotency key is required")
	ErrForbidden           = errors.New("approval decision forbidden")
	ErrNotFound            = errors.New("approval request not found")
	ErrAlreadyDecided      = errors.New("approval request already decided")
	ErrExpired             = errors.New("approval request expired")
	ErrRejected            = errors.New("approval request rejected")
	ErrCancelled           = errors.New("approval request cancelled")
	ErrConsumed            = errors.New("approval request already consumed")
	ErrDigestMismatch      = errors.New("approval action digest mismatch")
	ErrIdempotencyConflict = errors.New("approval idempotency key reused with different input")
)

var digestPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type Request struct {
	ID                        string          `json:"id"`
	Action                    Action          `json:"action"`
	State                     State           `json:"state"`
	Risk                      Risk            `json:"risk"`
	ActionDigest              string          `json:"actionDigest"`
	RequesterID               string          `json:"requesterId"`
	EligibleRoles             []auth.Role     `json:"eligibleRoles"`
	RequestedAt               time.Time       `json:"requestedAt"`
	ExpiresAt                 time.Time       `json:"expiresAt"`
	DecisionIdempotencyKey    string          `json:"-"`
	DecisionActorID           string          `json:"decisionActorId,omitempty"`
	DecisionReason            string          `json:"decisionReason,omitempty"`
	DecidedAt                 *time.Time      `json:"decidedAt,omitempty"`
	ConsumptionIdempotencyKey string          `json:"-"`
	ConsumedBy                string          `json:"consumedBy,omitempty"`
	ConsumedAt                *time.Time      `json:"consumedAt,omitempty"`
	CancelledBy               string          `json:"cancelledBy,omitempty"`
	CancelReason              string          `json:"cancelReason,omitempty"`
	CancelledAt               *time.Time      `json:"cancelledAt,omitempty"`
	Metadata                  json.RawMessage `json:"metadata"`
	UpdatedAt                 time.Time       `json:"updatedAt"`
}

type CreateInput struct {
	Action        Action
	Risk          Risk
	ActionDigest  string
	RequesterID   string
	EligibleRoles []auth.Role
	ExpiresAt     time.Time
	Metadata      json.RawMessage
	RequestedAt   time.Time
}

type DecisionInput struct {
	ApprovalID     string
	Actor          auth.User
	Decision       State
	Reason         string
	IdempotencyKey string
	Now            time.Time
}

type ConsumeInput struct {
	ApprovalID     string
	ActorID        string
	ActionDigest   string
	IdempotencyKey string
	Now            time.Time
}

type CancelInput struct {
	ApprovalID string
	Actor      auth.User
	Reason     string
	Now        time.Time
}

type ListFilter struct {
	State State
	Limit int
}

type Store interface {
	Create(context.Context, CreateInput) (Request, error)
	Get(context.Context, string) (Request, error)
	List(context.Context, ListFilter) ([]Request, error)
	Decide(context.Context, DecisionInput) (Request, error)
	Consume(context.Context, ConsumeInput) (Request, error)
	Cancel(context.Context, CancelInput) (Request, error)
}

func ValidAction(action Action) bool {
	switch action {
	case ActionRepairPlan, ActionPublishPatch, ActionSandboxNetwork:
		return true
	default:
		return false
	}
}

func ValidRisk(risk Risk) bool {
	switch risk {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

func ValidState(state State) bool {
	switch state {
	case StatePending, StateApproved, StateRejected, StateExpired, StateCancelled, StateConsumed:
		return true
	default:
		return false
	}
}

// CanTransition is the reusable approval state machine. Idempotent repeats are
// handled by the store's decision/consumption keys, so ordinary state changes
// never silently re-run a protected action.
func CanTransition(from, to State) bool {
	allowed := map[State][]State{
		StatePending:  {StateApproved, StateRejected, StateExpired, StateCancelled},
		StateApproved: {StateConsumed, StateCancelled},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

func ValidateActionDigest(value string) bool {
	return digestPattern.MatchString(strings.TrimSpace(value))
}

func ValidateCreate(input CreateInput, now time.Time) error {
	if !ValidAction(input.Action) {
		return ErrInvalidAction
	}
	if !ValidRisk(input.Risk) {
		return ErrInvalidRisk
	}
	if !ValidateActionDigest(input.ActionDigest) {
		return ErrInvalidDigest
	}
	if strings.TrimSpace(input.RequesterID) == "" {
		return errors.New("approval requester is required")
	}
	if input.ExpiresAt.IsZero() || !input.ExpiresAt.After(now) {
		return ErrInvalidExpiry
	}
	if len(input.EligibleRoles) == 0 {
		return errors.New("approval eligible roles are required")
	}
	for _, role := range input.EligibleRoles {
		if role != auth.RoleAdmin && role != auth.RoleMaintainer {
			return ErrForbidden
		}
	}
	if input.Action == ActionSandboxNetwork {
		if err := validateNetworkMetadata(input.Metadata); err != nil {
			return err
		}
	}
	return nil
}

func validateNetworkMetadata(raw json.RawMessage) error {
	var policy struct {
		Destinations  []string `json:"destinations"`
		NetworkPolicy string   `json:"networkPolicy"`
	}
	if len(raw) == 0 || !json.Valid(raw) || json.Unmarshal(raw, &policy) != nil {
		return errors.New("sandbox network approval metadata is invalid")
	}
	if len(policy.Destinations) == 0 || len(policy.Destinations) > 50 || strings.TrimSpace(policy.NetworkPolicy) == "" {
		return errors.New("sandbox network approval requires destinations and network policy")
	}
	for _, destination := range policy.Destinations {
		destination = strings.TrimSpace(destination)
		if destination == "" || strings.ContainsAny(destination, "\r\n*?") || len(destination) > 253 {
			return errors.New("sandbox network destination is invalid")
		}
	}
	return nil
}

func validateIdempotencyKey(value string) error {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 200 || strings.ContainsAny(value, "\r\n") {
		return ErrInvalidIdempotency
	}
	return nil
}

func (r Request) IsExpired(now time.Time) bool {
	return r.State == StatePending && !r.ExpiresAt.After(now)
}

func (r Request) CanConsume(now time.Time) error {
	if r.IsExpired(now) {
		return ErrExpired
	}
	switch r.State {
	case StateApproved:
		return nil
	case StateRejected:
		return ErrRejected
	case StateCancelled:
		return ErrCancelled
	case StateConsumed:
		return ErrConsumed
	case StateExpired:
		return ErrExpired
	default:
		return fmt.Errorf("%w: %s", ErrInvalidState, r.State)
	}
}
