package repair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EasonW3300/repoMender/server/internal/approvals"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// context, crypto/sha256, and encoding/json bind repair records to immutable
// inputs; path, regexp, and utf8 enforce the patch safety boundary before any
// approval or provider write.

type State string

const (
	StatePlanning              State = "planning"
	StateAwaitingPlanApproval  State = "awaiting_plan_approval"
	StatePlanApproved          State = "plan_approved"
	StatePatching              State = "patching"
	StateAwaitingPatchApproval State = "awaiting_patch_approval"
	StatePatchApproved         State = "patch_approved"
	StatePublished             State = "published"
	StateFailed                State = "failed"
	StateCancelled             State = "cancelled"
)

var (
	ErrInvalidState        = errors.New("invalid issue repair state")
	ErrInvalidTrigger      = errors.New("invalid issue repair trigger")
	ErrNotFound            = errors.New("issue repair not found")
	ErrInvalidPlan         = errors.New("invalid repair plan")
	ErrInvalidPatch        = errors.New("invalid repair patch")
	ErrUnsafePath          = errors.New("repair patch contains an unsafe path")
	ErrProtectedPath       = errors.New("repair patch changes a protected path")
	ErrGeneratedFile       = errors.New("repair patch contains generated files")
	ErrBinaryFile          = errors.New("repair patch contains binary data")
	ErrPatchTooLarge       = errors.New("repair patch exceeds size limit")
	ErrSecretDetected      = errors.New("repair patch contains a detected secret")
	ErrTestsFailed         = errors.New("repair tests failed")
	ErrProviderFailure     = errors.New("repair provider operation failed")
	ErrApprovalPhase       = errors.New("repair approval phase is invalid")
	ErrPublicationConflict = errors.New("repair publication conflict")
)

const (
	MaxPlanSteps   = 50
	MaxPatchFiles  = 100
	MaxPatchBytes  = 512 << 10
	MaxFileBytes   = 256 << 10
	MaxTestResults = 100
)

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

type Trigger string

const (
	TriggerGitHubIssue Trigger = "github_issue"
	TriggerM6Diagnosis Trigger = "m6_diagnosis"
)

type Issue struct {
	Provider       string `json:"provider"`
	RepositoryID   string `json:"repositoryId,omitempty"`
	Repository     string `json:"repository"`
	CloneURL       string `json:"cloneUrl"`
	WebURL         string `json:"webUrl,omitempty"`
	InstallationID string `json:"installationId"`
	Number         int    `json:"number"`
	Title          string `json:"title"`
	Body           string `json:"body,omitempty"`
	State          string `json:"state"`
	IsPullRequest  bool   `json:"isPullRequest"`
	BaseBranch     string `json:"baseBranch"`
	BaseSHA        string `json:"baseSha"`
}

type PlanStep struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Paths       []string `json:"paths"`
}

type Plan struct {
	SchemaVersion string     `json:"schemaVersion"`
	Summary       string     `json:"summary"`
	Steps         []PlanStep `json:"steps"`
	Tests         []string   `json:"tests"`
}

type PatchFile struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Generated bool   `json:"generated"`
	Binary    bool   `json:"binary"`
}

type TestResult struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Passed  bool   `json:"passed"`
	Output  string `json:"output,omitempty"`
}

type Patch struct {
	SchemaVersion string       `json:"schemaVersion"`
	Summary       string       `json:"summary"`
	BaseSHA       string       `json:"baseSha"`
	Diff          string       `json:"diff"`
	Files         []PatchFile  `json:"files"`
	Tests         []TestResult `json:"tests"`
}

type DraftPullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type Repair struct {
	ID              string           `json:"id"`
	TaskID          string           `json:"taskId"`
	SourceKey       string           `json:"sourceKey"`
	Trigger         Trigger          `json:"trigger"`
	RepositoryID    string           `json:"repositoryId,omitempty"`
	Repository      string           `json:"repository"`
	CloneURL        string           `json:"cloneUrl"`
	WebURL          string           `json:"webUrl,omitempty"`
	InstallationID  string           `json:"installationId"`
	IssueNumber     int              `json:"issueNumber,omitempty"`
	IssueTitle      string           `json:"issueTitle"`
	IssueBody       string           `json:"issueBody,omitempty"`
	BaseBranch      string           `json:"baseBranch"`
	BaseSHA         string           `json:"baseSha"`
	State           State            `json:"state"`
	Plan            json.RawMessage  `json:"plan"`
	PlanDigest      string           `json:"planDigest,omitempty"`
	PlanApprovalID  string           `json:"planApprovalId,omitempty"`
	Patch           json.RawMessage  `json:"patch"`
	PatchDigest     string           `json:"patchDigest,omitempty"`
	PatchApprovalID string           `json:"patchApprovalId,omitempty"`
	BranchName      string           `json:"branchName,omitempty"`
	DraftPR         DraftPullRequest `json:"draftPr,omitempty"`
	FailureCode     string           `json:"failureCode,omitempty"`
	FailureMessage  string           `json:"failureMessage,omitempty"`
	CreatedBy       string           `json:"createdBy,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
	UpdatedAt       time.Time        `json:"updatedAt"`
}

type CreateInput struct {
	Trigger   Trigger
	SourceKey string
	TaskID    string
	CreatedBy string
	Issue     Issue
	CreatedAt time.Time
}

type Store interface {
	Create(context.Context, CreateInput) (Repair, error)
	Get(context.Context, string) (Repair, error)
	GetByTask(context.Context, string) (Repair, error)
	List(context.Context, State, int) ([]Repair, error)
	Update(context.Context, Repair) (Repair, error)
}

type TaskService interface {
	Create(context.Context, tasks.CreateInput) (tasks.Task, error)
	Get(context.Context, string) (tasks.Task, error)
	Retry(context.Context, string, string) (tasks.Task, error)
	AppendEvent(context.Context, string, tasks.RunEventInput) (tasks.RunEvent, error)
}

type ApprovalService interface {
	Create(context.Context, approvals.CreateInput) (approvals.Request, error)
	Consume(context.Context, approvals.ConsumeInput) (approvals.Request, error)
}

type Provider interface {
	ResolveIssue(context.Context, string, string, int) (Issue, error)
	ResolveBaseSHA(context.Context, string, string, string) (string, error)
	PublishDraft(context.Context, PublishInput) (DraftPullRequest, error)
}

type PublishInput struct {
	Repository     string
	InstallationID string
	BaseBranch     string
	BaseSHA        string
	BranchName     string
	Title          string
	Body           string
	Files          []PatchFile
}

func ValidState(state State) bool {
	switch state {
	case StatePlanning, StateAwaitingPlanApproval, StatePlanApproved, StatePatching,
		StateAwaitingPatchApproval, StatePatchApproved, StatePublished, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

func CanTransition(from, to State) bool {
	if from == to && (from == StatePlanning || from == StatePatching || from == StateFailed) {
		return true
	}
	allowed := map[State][]State{
		StatePlanning:              {StateAwaitingPlanApproval, StateFailed, StateCancelled},
		StateAwaitingPlanApproval:  {StatePlanApproved, StateFailed, StateCancelled},
		StatePlanApproved:          {StatePatching, StateFailed, StateCancelled},
		StatePatching:              {StateAwaitingPatchApproval, StateFailed, StateCancelled},
		StateAwaitingPatchApproval: {StatePatchApproved, StateFailed, StateCancelled},
		StatePatchApproved:         {StatePublished, StateFailed, StateCancelled},
		StateFailed:                {StatePlanning, StatePlanApproved, StatePatchApproved, StateCancelled},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

func ValidateIssue(issue Issue, now time.Time) error {
	if issue.Provider != "github" || strings.TrimSpace(issue.Repository) == "" || strings.TrimSpace(issue.CloneURL) == "" || strings.TrimSpace(issue.InstallationID) == "" || issue.Number < 0 || strings.TrimSpace(issue.Title) == "" || issue.IsPullRequest {
		return ErrInvalidTrigger
	}
	if issue.Number > 0 && issue.State != "" && issue.State != "open" {
		return ErrInvalidTrigger
	}
	if strings.TrimSpace(issue.BaseBranch) == "" || !shaPattern.MatchString(strings.TrimSpace(issue.BaseSHA)) {
		return ErrInvalidTrigger
	}
	if strings.ContainsAny(issue.BaseBranch, "\r\n") || issue.BaseBranch == "main" || issue.BaseBranch == "master" {
		// The base branch may be protected; only the publication branch is
		// required to be non-protected. This guard only rejects malformed input.
		if strings.ContainsAny(issue.BaseBranch, "\r\n") {
			return ErrInvalidTrigger
		}
	}
	if now.IsZero() {
		return ErrInvalidTrigger
	}
	return nil
}

func ValidatePlan(plan Plan) error {
	if plan.SchemaVersion != "v1" || strings.TrimSpace(plan.Summary) == "" || len(plan.Summary) > 20000 || len(plan.Steps) == 0 || len(plan.Steps) > MaxPlanSteps {
		return ErrInvalidPlan
	}
	for _, step := range plan.Steps {
		if strings.TrimSpace(step.ID) == "" || strings.TrimSpace(step.Description) == "" || len(step.Description) > 10000 {
			return ErrInvalidPlan
		}
		for _, value := range step.Paths {
			if err := validatePath(value); err != nil {
				return err
			}
		}
	}
	for _, test := range plan.Tests {
		if err := validateTestCommand(test); err != nil {
			return err
		}
	}
	return nil
}

func ValidatePatch(patch Patch, expectedBase string) error {
	if patch.SchemaVersion != "v1" || strings.TrimSpace(patch.Summary) == "" || !shaPattern.MatchString(patch.BaseSHA) || patch.BaseSHA != expectedBase || strings.TrimSpace(patch.Diff) == "" || len(patch.Files) == 0 || len(patch.Files) > MaxPatchFiles || len(patch.Diff) > MaxPatchBytes {
		return ErrInvalidPatch
	}
	if !utf8.ValidString(patch.Diff) || strings.ContainsRune(patch.Diff, 0) {
		return ErrBinaryFile
	}
	total := 0
	for _, file := range patch.Files {
		if err := validatePath(file.Path); err != nil {
			return err
		}
		if isProtectedPath(file.Path) {
			return ErrProtectedPath
		}
		if file.Generated {
			return ErrGeneratedFile
		}
		if file.Binary || !utf8.ValidString(file.Content) || strings.ContainsRune(file.Content, 0) {
			return ErrBinaryFile
		}
		if len(file.Content) > MaxFileBytes {
			return ErrPatchTooLarge
		}
		total += len(file.Content)
		if containsSecret(file.Content) || containsSecret(patch.Diff) {
			return ErrSecretDetected
		}
	}
	if total > MaxPatchBytes {
		return ErrPatchTooLarge
	}
	if len(patch.Tests) == 0 || len(patch.Tests) > MaxTestResults {
		return ErrInvalidPatch
	}
	for _, test := range patch.Tests {
		if strings.TrimSpace(test.Name) == "" || strings.TrimSpace(test.Command) == "" {
			return ErrInvalidPatch
		}
		if err := validateTestCommand(test.Command); err != nil {
			return err
		}
		if !test.Passed {
			return ErrTestsFailed
		}
	}
	return nil
}

func Digest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func validatePath(value string) error {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || path.IsAbs(value) || strings.ContainsAny(value, "\r\n") || value == "." || strings.HasPrefix(value, ".git/") {
		return ErrUnsafePath
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." || segment == "" {
			return ErrUnsafePath
		}
	}
	return nil
}

func isProtectedPath(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	return value == "CODEOWNERS" || value == ".github/CODEOWNERS" || strings.HasPrefix(value, ".github/workflows/") || value == "compose.yaml" || strings.HasPrefix(value, "compose.")
}

func validateTestCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" || len(command) > 1000 || strings.ContainsAny(command, "\r\n") {
		return ErrInvalidPatch
	}
	for _, forbidden := range []string{"git push", "git merge", "rm -rf", "curl | sh", "curl|sh"} {
		if strings.Contains(strings.ToLower(command), forbidden) {
			return ErrInvalidPatch
		}
	}
	return nil
}

func containsSecret(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"ghp_", "github_pat_", "akia", "-----begin private key-----", "aws_secret_access_key", "xoxb-", "password=", "token="} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func taskPayload(repair Repair) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{"repairId": repair.ID, "repository": repair.Repository, "cloneUrl": repair.CloneURL, "baseSha": repair.BaseSHA})
	return payload
}

var _ TaskService = (*tasks.Service)(nil)
var _ ApprovalService = (*approvals.Service)(nil)
