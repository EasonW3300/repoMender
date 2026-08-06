package repair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/approvals"
	"github.com/EasonW3300/repoMender/server/internal/execution"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// Processor runs one phase per queue claim. Approval phases finish as
// awaiting_approval, and the M8 service resumes the same task only after the
// exact approval digest has been consumed.
type Processor struct {
	service   *Service
	tasks     TaskService
	adapter   execution.Adapter
	projectID string
	agentName string
	timeout   time.Duration
}

func NewProcessor(service *Service, taskService TaskService, adapter execution.Adapter, timeout time.Duration) Processor {
	return Processor{service: service, tasks: taskService, adapter: adapter, timeout: timeout}
}

func (p Processor) WithProjectID(value string) Processor {
	p.projectID = strings.TrimSpace(value)
	return p
}
func (p Processor) WithAgentName(value string) Processor {
	p.agentName = strings.TrimSpace(value)
	return p
}

func (p Processor) Process(ctx context.Context, task tasks.Task, run tasks.Run) (tasks.Status, string, string) {
	if p.service == nil || p.service.store == nil || p.tasks == nil {
		return tasks.StatusFailed, "m8_dependency_unavailable", "issue repair dependencies are unavailable"
	}
	repair, err := p.service.store.GetByTask(ctx, task.ID)
	if err != nil {
		return tasks.StatusFailed, "m8_repair_not_found", err.Error()
	}
	if p.adapter == nil && (repair.State == StatePlanning || repair.State == StatePlanApproved || repair.State == StatePatching || repair.State == StateFailed && repair.PatchDigest == "") {
		return p.fail(ctx, repair, "m8_ac_unavailable", errors.New("AC execution is required for issue repair"))
	}
	switch repair.State {
	case StatePlanning:
		return p.generatePlan(ctx, task, run, repair)
	case StatePlanApproved:
		return p.generatePatch(ctx, task, run, repair)
	case StatePatchApproved:
		return p.publish(ctx, task, run, repair)
	case StateFailed:
		if repair.PatchApprovalID != "" && repair.PatchDigest != "" {
			repair.State = StatePatchApproved
			return p.publish(ctx, task, run, repair)
		}
		if repair.PlanApprovalID != "" && repair.PlanDigest != "" {
			repair.State = StatePlanApproved
			return p.generatePatch(ctx, task, run, repair)
		}
		return p.generatePlan(ctx, task, run, repair)
	default:
		return tasks.StatusFailed, "m8_invalid_state", string(repair.State)
	}
}

func (p Processor) generatePlan(ctx context.Context, task tasks.Task, run tasks.Run, repair Repair) (tasks.Status, string, string) {
	repair.State = StatePlanning
	if _, err := p.service.store.Update(ctx, repair); err != nil {
		return tasks.StatusFailed, "m8_state_persist_failed", err.Error()
	}
	result, code, err := p.runAC(ctx, task, run, repair, planPrompt(repair), planOutputSchema)
	if err != nil {
		return p.fail(ctx, repair, code, err)
	}
	var plan Plan
	decoder := json.NewDecoder(strings.NewReader(string(result)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return p.fail(ctx, repair, "m8_plan_invalid", ErrInvalidPlan)
	}
	if err := ValidatePlan(plan); err != nil {
		return p.fail(ctx, repair, "m8_plan_invalid", err)
	}
	digest, err := Digest(plan)
	if err != nil {
		return p.fail(ctx, repair, "m8_plan_digest_failed", err)
	}
	if strings.TrimSpace(repair.CreatedBy) == "" {
		return p.fail(ctx, repair, "m8_requester_missing", errors.New("repair requester is required for approval"))
	}
	request, err := p.service.approvals.Create(ctx, repairApprovalInput(repair, "plan", digest, repair.CreatedBy, p.service.now().UTC()))
	if err != nil {
		return p.fail(ctx, repair, "m8_plan_approval_failed", err)
	}
	repair.Plan, repair.PlanDigest, repair.PlanApprovalID, repair.State = mustJSON(plan), digest, request.ID, StateAwaitingPlanApproval
	if _, err := p.service.store.Update(ctx, repair); err != nil {
		return tasks.StatusFailed, "m8_state_persist_failed", err.Error()
	}
	return tasks.StatusAwaitingApproval, "", "repair plan is awaiting approval"
}

func (p Processor) generatePatch(ctx context.Context, task tasks.Task, run tasks.Run, repair Repair) (tasks.Status, string, string) {
	repair.State = StatePatching
	if _, err := p.service.store.Update(ctx, repair); err != nil {
		return tasks.StatusFailed, "m8_state_persist_failed", err.Error()
	}
	var plan Plan
	if err := json.Unmarshal(repair.Plan, &plan); err != nil {
		return p.fail(ctx, repair, "m8_plan_missing", err)
	}
	planJSON, _ := json.Marshal(plan)
	result, code, err := p.runAC(ctx, task, run, repair, patchPrompt(repair, planJSON), patchOutputSchema)
	if err != nil {
		return p.fail(ctx, repair, code, err)
	}
	var patch Patch
	decoder := json.NewDecoder(strings.NewReader(string(result)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		return p.fail(ctx, repair, "m8_patch_invalid", ErrInvalidPatch)
	}
	if err := ValidatePatch(patch, repair.BaseSHA); err != nil {
		return p.fail(ctx, repair, patchErrorCode(err), err)
	}
	digest, err := Digest(patch)
	if err != nil {
		return p.fail(ctx, repair, "m8_patch_digest_failed", err)
	}
	request, err := p.service.approvals.Create(ctx, repairApprovalInput(repair, "patch", digest, repair.CreatedBy, p.service.now().UTC()))
	if err != nil {
		return p.fail(ctx, repair, "m8_patch_approval_failed", err)
	}
	repair.Patch, repair.PatchDigest, repair.PatchApprovalID, repair.BranchName, repair.State = mustJSON(patch), digest, request.ID, "repomender/repair/"+repair.ID, StateAwaitingPatchApproval
	if _, err := p.service.store.Update(ctx, repair); err != nil {
		return tasks.StatusFailed, "m8_state_persist_failed", err.Error()
	}
	return tasks.StatusAwaitingApproval, "", "repair patch is awaiting approval"
}

func (p Processor) publish(ctx context.Context, task tasks.Task, run tasks.Run, repair Repair) (tasks.Status, string, string) {
	if repair.State != StatePatchApproved {
		repair.State = StatePatchApproved
	}
	var patch Patch
	if err := json.Unmarshal(repair.Patch, &patch); err != nil {
		return p.fail(ctx, repair, "m8_patch_missing", err)
	}
	if err := ValidatePatch(patch, repair.BaseSHA); err != nil {
		return p.fail(ctx, repair, patchErrorCode(err), err)
	}
	if p.service.provider == nil {
		return p.fail(ctx, repair, "m8_provider_unavailable", ErrProviderFailure)
	}
	pr, err := p.service.provider.PublishDraft(ctx, PublishInput{Repository: repair.Repository, InstallationID: repair.InstallationID,
		BaseBranch: repair.BaseBranch, BaseSHA: repair.BaseSHA, BranchName: repair.BranchName,
		Title: "Repair: " + repair.IssueTitle, Body: patch.Summary, Files: patch.Files})
	if err != nil {
		return p.fail(ctx, repair, "m8_publication_failed", fmt.Errorf("%w: %v", ErrProviderFailure, err))
	}
	repair.DraftPR, repair.State, repair.FailureCode, repair.FailureMessage = pr, StatePublished, "", ""
	if _, err := p.service.store.Update(ctx, repair); err != nil {
		return tasks.StatusFailed, "m8_state_persist_failed", err.Error()
	}
	return tasks.StatusSucceeded, "", "repair published as a draft pull request"
}

func (p Processor) runAC(ctx context.Context, task tasks.Task, run tasks.Run, repair Repair, prompt, schema string) (json.RawMessage, string, error) {
	if _, err := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{Kind: "status", Message: "starting M8 AC repair phase at " + repair.BaseSHA}); err != nil {
		return nil, "m8_event_persist_failed", err
	}
	timeout := p.timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	projectID := p.projectID
	if projectID == "" {
		projectID = task.RepositoryID
	}
	agentName := p.agentName
	if agentName == "" {
		agentName = "codex"
	}
	request, err := p.adapter.Start(ctx, execution.Request{CorrelationID: run.CorrelationID, ProjectID: projectID, AgentName: agentName,
		Repository: repair.CloneURL, CommitSHA: repair.BaseSHA, Prompt: prompt, Timeout: timeout, OutputSchemaJSON: schema,
		Policy: execution.ResourcePolicy{Driver: "docker", Cleanup: "remove", NetworkEnabled: false}})
	if err != nil {
		return nil, "m8_ac_start_failed", err
	}
	if _, err := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{Kind: "external_run", Message: "M8 AC repair phase started", Payload: json.RawMessage(fmt.Sprintf(`{"externalRunId":%q}`, request.ID))}); err != nil {
		return nil, "m8_event_persist_failed", err
	}
	events, errs := p.adapter.Events(ctx, request.ID, 0)
	for event := range events {
		payload, _ := json.Marshal(event)
		if _, appendErr := p.tasks.AppendEvent(ctx, run.ID, tasks.RunEventInput{Kind: string(event.Kind), Stream: event.Stream, Message: event.Message, Payload: payload, Terminal: event.Terminal}); appendErr != nil {
			return nil, "m8_event_persist_failed", appendErr
		}
	}
	if streamErr := <-errs; streamErr != nil {
		return nil, "m8_ac_stream_failed", streamErr
	}
	result, err := p.adapter.Result(ctx, request.ID)
	if err != nil {
		return nil, "m8_ac_result_failed", err
	}
	if len(result.Output) == 0 {
		return nil, "m8_ac_result_invalid", ErrInvalidPatch
	}
	return result.Output, "", nil
}

func (p Processor) fail(ctx context.Context, repair Repair, code string, err error) (tasks.Status, string, string) {
	if _, updateErr := p.service.markFailure(ctx, repair, code, err); updateErr != nil {
		return tasks.StatusFailed, "m8_state_persist_failed", updateErr.Error()
	}
	return tasks.StatusFailed, code, err.Error()
}

const planOutputSchema = `{"type":"object","required":["schemaVersion","summary","steps","tests"],"properties":{"schemaVersion":{"type":"string","const":"v1"},"summary":{"type":"string","maxLength":20000},"steps":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object","required":["id","description","paths"],"properties":{"id":{"type":"string"},"description":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}},"tests":{"type":"array","items":{"type":"string","maxLength":1000}}},"additionalProperties":false}`

const patchOutputSchema = `{"type":"object","required":["schemaVersion","summary","baseSha","diff","files","tests"],"properties":{"schemaVersion":{"type":"string","const":"v1"},"summary":{"type":"string","maxLength":20000},"baseSha":{"type":"string","pattern":"^[0-9a-fA-F]{40}$"},"diff":{"type":"string","maxLength":524288},"files":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","required":["path","content","generated","binary"],"properties":{"path":{"type":"string"},"content":{"type":"string"},"generated":{"type":"boolean"},"binary":{"type":"boolean"}},"additionalProperties":false}},"tests":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","required":["name","command","passed","output"],"properties":{"name":{"type":"string"},"command":{"type":"string"},"passed":{"type":"boolean"},"output":{"type":"string"}},"additionalProperties":false}}},"additionalProperties":false}`

func planPrompt(repair Repair) string {
	return strings.Join([]string{"Create a structured repair plan for the GitHub issue below.", "Inspect only the immutable base commit. Do not modify files, publish branches, merge, or push.", "Return only JSON matching the supplied schema.", "Repository: " + repair.CloneURL, "Base commit: " + repair.BaseSHA, "Issue: " + repair.IssueTitle, "Issue body: " + repair.IssueBody}, "\n")
}

func patchPrompt(repair Repair, plan []byte) string {
	return strings.Join([]string{"Implement the approved repair plan at the immutable base commit.", "Return only JSON matching the supplied schema. Generate a unified diff and complete text contents for every changed file.", "Run only the configured tests and report each result. Do not publish or merge.", "Repository: " + repair.CloneURL, "Base commit: " + repair.BaseSHA, "Approved plan: " + string(plan)}, "\n")
}

func mustJSON(value any) json.RawMessage { encoded, _ := json.Marshal(value); return encoded }

func patchErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrTestsFailed):
		return "m8_tests_failed"
	case errors.Is(err, ErrSecretDetected):
		return "m8_secret_detected"
	case errors.Is(err, ErrUnsafePath):
		return "m8_unsafe_path"
	case errors.Is(err, ErrProtectedPath):
		return "m8_protected_path"
	case errors.Is(err, ErrGeneratedFile):
		return "m8_generated_file"
	case errors.Is(err, ErrBinaryFile):
		return "m8_binary_file"
	case errors.Is(err, ErrPatchTooLarge):
		return "m8_patch_too_large"
	default:
		return "m8_patch_invalid"
	}
}

var _ TaskService = (*tasks.Service)(nil)
var _ ApprovalService = (*approvals.Service)(nil)
