package repair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/approvals"
	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// Store owns durable repair records, tasks owns queue/idempotency state, and
// approvals owns the exact-digest protected-action gate used by both stages.
type Service struct {
	store     Store
	tasks     TaskService
	approvals ApprovalService
	provider  Provider
	now       func() time.Time
}

func NewService(store Store, taskService TaskService, approvalService ApprovalService, provider Provider) *Service {
	return &Service{store: store, tasks: taskService, approvals: approvalService, provider: provider, now: time.Now}
}

func (s *Service) CreateFromIssue(ctx context.Context, issue Issue, actorID string) (Repair, error) {
	if s == nil || s.store == nil || s.tasks == nil {
		return Repair{}, errors.New("issue repair service unavailable")
	}
	if issue.Number <= 0 || issue.State == "" {
		if s.provider == nil {
			return Repair{}, ErrInvalidTrigger
		}
		resolved, err := s.provider.ResolveIssue(ctx, issue.Repository, issue.InstallationID, issue.Number)
		if err != nil {
			return Repair{}, fmt.Errorf("%w: resolve issue: %v", ErrProviderFailure, err)
		}
		issue.Title, issue.Body, issue.WebURL, issue.State, issue.IsPullRequest = resolved.Title, resolved.Body, resolved.WebURL, resolved.State, resolved.IsPullRequest
	}
	if strings.TrimSpace(issue.BaseSHA) == "" && s.provider != nil {
		base, err := s.provider.ResolveBaseSHA(ctx, issue.Repository, issue.InstallationID, issue.BaseBranch)
		if err != nil {
			return Repair{}, fmt.Errorf("%w: resolve base commit: %v", ErrProviderFailure, err)
		}
		issue.BaseSHA = base
	}
	if err := ValidateIssue(issue, s.now().UTC()); err != nil {
		return Repair{}, err
	}
	if actorID == "" && !strings.HasPrefix(issue.Repository, "github:") {
		// Webhook-created tasks are allowed to use the source key as the system
		// creator; authenticated API requests always provide a real actor ID.
		actorID = ""
	}
	sourceKey := fmt.Sprintf("github:issue:%s:%d", issue.Repository, issue.Number)
	task, err := s.tasks.Create(ctx, tasks.CreateInput{
		Kind: tasks.KindIssueRepair, Title: "Repair issue #" + fmt.Sprint(issue.Number) + ": " + issue.Title,
		RepositoryID: issue.RepositoryID, RepositoryName: issue.Repository, SourceKey: sourceKey,
		Payload: json.RawMessage(`{"source":"github_issue"}`), MaxAttempts: 5,
		CreatedBy: actorID, CreatedAt: s.now().UTC(),
	})
	if err != nil {
		return Repair{}, err
	}
	repair, err := s.store.Create(ctx, CreateInput{Trigger: TriggerGitHubIssue, SourceKey: sourceKey, TaskID: task.ID, CreatedBy: actorID, Issue: issue, CreatedAt: s.now().UTC()})
	if err != nil {
		if existing, getErr := s.store.GetByTask(ctx, task.ID); getErr == nil {
			return existing, nil
		}
		return Repair{}, err
	}
	return repair, nil
}

// BindAutomationTask attaches the M9-created task to the normal M8 repair
// record without creating a second queue item. The worker can therefore use
// the same two-stage plan/patch approval flow as a direct issue webhook.
func (s *Service) BindAutomationTask(ctx context.Context, task tasks.Task, issue Issue, actorID string) (Repair, error) {
	if s == nil || s.store == nil || task.Kind != tasks.KindIssueRepair || task.ID == "" {
		return Repair{}, ErrInvalidTrigger
	}
	if issue.Number <= 0 || strings.TrimSpace(issue.State) == "" {
		if s.provider == nil {
			return Repair{}, ErrInvalidTrigger
		}
		resolved, err := s.provider.ResolveIssue(ctx, issue.Repository, issue.InstallationID, issue.Number)
		if err != nil {
			return Repair{}, fmt.Errorf("%w: resolve issue: %v", ErrProviderFailure, err)
		}
		issue.Title, issue.Body, issue.WebURL, issue.State, issue.IsPullRequest = resolved.Title, resolved.Body, resolved.WebURL, resolved.State, resolved.IsPullRequest
	}
	if strings.TrimSpace(issue.BaseSHA) == "" && s.provider != nil {
		base, err := s.provider.ResolveBaseSHA(ctx, issue.Repository, issue.InstallationID, issue.BaseBranch)
		if err != nil {
			return Repair{}, fmt.Errorf("%w: resolve base commit: %v", ErrProviderFailure, err)
		}
		issue.BaseSHA = base
	}
	if err := ValidateIssue(issue, s.now().UTC()); err != nil {
		return Repair{}, err
	}
	created, err := s.store.Create(ctx, CreateInput{Trigger: TriggerGitHubIssue, SourceKey: task.SourceKey,
		TaskID: task.ID, CreatedBy: actorID, Issue: issue, CreatedAt: s.now().UTC()})
	if err != nil {
		if existing, getErr := s.store.GetByTask(ctx, task.ID); getErr == nil {
			return existing, nil
		}
		return Repair{}, err
	}
	return created, nil
}

// CreateFromDiagnosis binds an M8 repair to a succeeded M6 task's immutable
// commit. It intentionally does not infer a mutable branch tip or an issue
// number from model output.
func (s *Service) CreateFromDiagnosis(ctx context.Context, diagnosisTask tasks.Task, actorID string) (Repair, error) {
	if diagnosisTask.Kind != tasks.KindCIDiagnosis || diagnosisTask.Status != tasks.StatusSucceeded {
		return Repair{}, ErrInvalidTrigger
	}
	var payload struct {
		RepositoryID   string `json:"repositoryId"`
		RepositoryName string `json:"repositoryName"`
		CloneURL       string `json:"cloneUrl"`
		InstallationID string `json:"installationId"`
		HeadSHA        string `json:"headSHA"`
	}
	if json.Unmarshal(diagnosisTask.Payload, &payload) != nil || payload.RepositoryName == "" || payload.CloneURL == "" || payload.InstallationID == "" || payload.HeadSHA == "" {
		return Repair{}, ErrInvalidTrigger
	}
	issue := Issue{Provider: "github", RepositoryID: payload.RepositoryID, Repository: payload.RepositoryName, CloneURL: payload.CloneURL,
		InstallationID: payload.InstallationID, Title: diagnosisTask.Title, State: "open", BaseBranch: "main", BaseSHA: payload.HeadSHA}
	if err := ValidateIssue(issue, s.now().UTC()); err != nil {
		return Repair{}, err
	}
	sourceKey := "m6:diagnosis:" + diagnosisTask.ID
	task, err := s.tasks.Create(ctx, tasks.CreateInput{Kind: tasks.KindIssueRepair, Title: "Repair diagnosis: " + diagnosisTask.Title,
		RepositoryID: issue.RepositoryID, RepositoryName: issue.Repository, SourceKey: sourceKey,
		Payload: json.RawMessage(`{"source":"m6_diagnosis"}`), MaxAttempts: 5, CreatedBy: actorID, CreatedAt: s.now().UTC()})
	if err != nil {
		return Repair{}, err
	}
	return s.store.Create(ctx, CreateInput{Trigger: TriggerM6Diagnosis, SourceKey: sourceKey, TaskID: task.ID, CreatedBy: actorID, Issue: issue, CreatedAt: s.now().UTC()})
}

func (s *Service) Get(ctx context.Context, id string) (Repair, error) {
	return s.store.Get(ctx, strings.TrimSpace(id))
}

func (s *Service) List(ctx context.Context, state State, limit int) ([]Repair, error) {
	if state != "" && !ValidState(state) {
		return nil, ErrInvalidState
	}
	return s.store.List(ctx, state, limit)
}

// ConsumeApproval is the only path that resumes the queue. Generic M7
// approval decisions remain separate, while this method binds the consumed
// request to the expected repair stage and exact plan/patch digest.
func (s *Service) ConsumeApproval(ctx context.Context, repairID, phase, actionDigest, idempotencyKey, actorID string) (Repair, error) {
	repair, err := s.store.Get(ctx, repairID)
	if err != nil {
		return Repair{}, err
	}
	var approvalID string
	var expected State
	var action approvals.Action
	switch phase {
	case "plan":
		approvalID, expected, action = repair.PlanApprovalID, StateAwaitingPlanApproval, approvals.ActionRepairPlan
	case "patch":
		approvalID, expected, action = repair.PatchApprovalID, StateAwaitingPatchApproval, approvals.ActionPublishPatch
	default:
		return Repair{}, ErrApprovalPhase
	}
	if repair.State == nextApprovedState(phase) {
		return repair, nil
	}
	if repair.State != expected || approvalID == "" {
		return Repair{}, ErrApprovalPhase
	}
	if strings.TrimSpace(actorID) == "" {
		return Repair{}, approvals.ErrForbidden
	}
	request, err := s.approvals.Consume(ctx, approvals.ConsumeInput{ApprovalID: approvalID, ActorID: actorID, ActionDigest: actionDigest, IdempotencyKey: idempotencyKey, Now: s.now().UTC()})
	if err != nil {
		return Repair{}, err
	}
	if request.Action != action {
		return Repair{}, ErrApprovalPhase
	}
	repair.State = nextApprovedState(phase)
	repair.FailureCode, repair.FailureMessage = "", ""
	updated, err := s.store.Update(ctx, repair)
	if err != nil {
		return Repair{}, err
	}
	if _, err := s.tasks.Retry(ctx, repair.TaskID, "M8 "+phase+" approval consumed"); err != nil {
		return Repair{}, err
	}
	return updated, nil
}

func nextApprovedState(phase string) State {
	if phase == "plan" {
		return StatePlanApproved
	}
	return StatePatchApproved
}

func (s *Service) markFailure(ctx context.Context, repair Repair, code string, cause error) (Repair, error) {
	repair.State = StateFailed
	repair.FailureCode = code
	repair.FailureMessage = cause.Error()
	return s.store.Update(ctx, repair)
}

func repairApprovalInput(repair Repair, phase string, digest string, actorID string, now time.Time) approvals.CreateInput {
	action := approvals.ActionRepairPlan
	if phase == "patch" {
		action = approvals.ActionPublishPatch
	}
	metadata, _ := json.Marshal(map[string]string{"repairId": repair.ID, "taskId": repair.TaskID, "phase": phase, "baseSha": repair.BaseSHA})
	return approvals.CreateInput{Action: action, Risk: approvals.RiskHigh, ActionDigest: digest, RequesterID: actorID,
		EligibleRoles: []auth.Role{auth.RoleAdmin, auth.RoleMaintainer}, ExpiresAt: now.Add(24 * time.Hour), Metadata: metadata, RequestedAt: now}
}
