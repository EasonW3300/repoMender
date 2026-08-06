package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/EasonW3300/repoMender/server/internal/approvals"
	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/repair"
)

type createRepairRequest struct {
	RepositoryID   string `json:"repositoryId"`
	Repository     string `json:"repository"`
	CloneURL       string `json:"cloneUrl"`
	WebURL         string `json:"webUrl"`
	InstallationID string `json:"installationId"`
	IssueNumber    int    `json:"issueNumber"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	State          string `json:"state"`
	BaseBranch     string `json:"baseBranch"`
	BaseSHA        string `json:"baseSha"`
}

type consumeRepairRequest struct {
	ActionDigest   string `json:"actionDigest"`
	IdempotencyKey string `json:"idempotencyKey"`
}

func (s *Server) listRepairs(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireSession(w, r); !ok {
		return
	}
	state := repair.State(strings.TrimSpace(r.URL.Query().Get("state")))
	if state != "" && !repair.ValidState(state) {
		writeError(w, http.StatusBadRequest, "invalid_repair_state")
		return
	}
	items, err := s.repair.List(r.Context(), state, queryInt(r, "limit", 100))
	if err != nil {
		writeRepairError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repairs": items})
}

func (s *Server) createRepair(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireRepairMutator(w, r)
	if !ok {
		return
	}
	var input createRepairRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	created, err := s.repair.CreateFromIssue(r.Context(), repair.Issue{Provider: "github", RepositoryID: input.RepositoryID,
		Repository: input.Repository, CloneURL: input.CloneURL, WebURL: input.WebURL, InstallationID: input.InstallationID,
		Number: input.IssueNumber, Title: input.Title, Body: input.Body, State: input.State, BaseBranch: input.BaseBranch, BaseSHA: input.BaseSHA}, session.User.ID)
	if err != nil {
		writeRepairError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"repair": created})
}

func (s *Server) createRepairFromDiagnosis(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireRepairMutator(w, r)
	if !ok {
		return
	}
	var input struct {
		TaskID string `json:"taskId"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	diagnosisTask, err := s.tasks.Get(r.Context(), strings.TrimSpace(input.TaskID))
	if err != nil {
		writeTaskError(w, err)
		return
	}
	created, err := s.repair.CreateFromDiagnosis(r.Context(), diagnosisTask, session.User.ID)
	if err != nil {
		writeRepairError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"repair": created})
}

func (s *Server) getRepair(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireSession(w, r); !ok {
		return
	}
	item, err := s.repair.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRepairError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repair": item})
}

func (s *Server) consumeRepairPlan(w http.ResponseWriter, r *http.Request) {
	s.consumeRepairApproval(w, r, "plan")
}

func (s *Server) consumeRepairPatch(w http.ResponseWriter, r *http.Request) {
	s.consumeRepairApproval(w, r, "patch")
}

func (s *Server) consumeRepairApproval(w http.ResponseWriter, r *http.Request, phase string) {
	session, _, ok := s.requireRepairMutator(w, r)
	if !ok {
		return
	}
	var input consumeRepairRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repair.ConsumeApproval(r.Context(), r.PathValue("id"), phase, input.ActionDigest, input.IdempotencyKey, session.User.ID)
	if err != nil {
		writeRepairError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repair": item})
}

func (s *Server) requireRepairMutator(w http.ResponseWriter, r *http.Request) (auth.Session, string, bool) {
	session, token, ok := s.requireSession(w, r)
	if !ok {
		return auth.Session{}, "", false
	}
	if session.User.Role != auth.RoleAdmin && session.User.Role != auth.RoleMaintainer {
		writeError(w, http.StatusForbidden, "forbidden")
		return auth.Session{}, "", false
	}
	if !auth.VerifyCSRF(session, r.Header.Get("X-CSRF-Token")) {
		writeError(w, http.StatusForbidden, "csrf_failed")
		return auth.Session{}, "", false
	}
	return session, token, true
}

func writeRepairError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, repair.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, repair.ErrInvalidTrigger), errors.Is(err, repair.ErrInvalidPlan), errors.Is(err, repair.ErrInvalidPatch),
		errors.Is(err, repair.ErrUnsafePath), errors.Is(err, repair.ErrProtectedPath), errors.Is(err, repair.ErrGeneratedFile),
		errors.Is(err, repair.ErrBinaryFile), errors.Is(err, repair.ErrPatchTooLarge), errors.Is(err, repair.ErrSecretDetected):
		status = http.StatusBadRequest
	case errors.Is(err, repair.ErrApprovalPhase), errors.Is(err, repair.ErrTestsFailed), errors.Is(err, repair.ErrPublicationConflict):
		status = http.StatusConflict
	case errors.Is(err, approvals.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, approvals.ErrExpired), errors.Is(err, approvals.ErrRejected), errors.Is(err, approvals.ErrCancelled),
		errors.Is(err, approvals.ErrConsumed), errors.Is(err, approvals.ErrDigestMismatch), errors.Is(err, approvals.ErrAlreadyDecided),
		errors.Is(err, approvals.ErrIdempotencyConflict):
		status = http.StatusConflict
	case errors.Is(err, approvals.ErrInvalidDigest), errors.Is(err, approvals.ErrInvalidIdempotency):
		status = http.StatusBadRequest
	case errors.Is(err, repair.ErrProviderFailure):
		status = http.StatusBadGateway
	case errors.Is(err, auth.ErrForbidden):
		status = http.StatusForbidden
	}
	writeError(w, status, repairErrorCode(err))
}

func repairErrorCode(err error) string {
	switch {
	case errors.Is(err, repair.ErrNotFound):
		return "repair_not_found"
	case errors.Is(err, repair.ErrInvalidTrigger):
		return "invalid_repair_trigger"
	case errors.Is(err, repair.ErrUnsafePath):
		return "repair_unsafe_path"
	case errors.Is(err, repair.ErrProtectedPath):
		return "repair_protected_path"
	case errors.Is(err, repair.ErrGeneratedFile):
		return "repair_generated_file"
	case errors.Is(err, repair.ErrBinaryFile):
		return "repair_binary_file"
	case errors.Is(err, repair.ErrPatchTooLarge):
		return "repair_patch_too_large"
	case errors.Is(err, repair.ErrSecretDetected):
		return "repair_secret_detected"
	case errors.Is(err, repair.ErrTestsFailed):
		return "repair_tests_failed"
	case errors.Is(err, repair.ErrApprovalPhase):
		return "repair_approval_phase"
	case errors.Is(err, repair.ErrProviderFailure):
		return "repair_provider_failure"
	case errors.Is(err, approvals.ErrExpired):
		return "repair_approval_expired"
	case errors.Is(err, approvals.ErrRejected):
		return "repair_approval_rejected"
	case errors.Is(err, approvals.ErrCancelled):
		return "repair_approval_cancelled"
	case errors.Is(err, approvals.ErrConsumed):
		return "repair_approval_consumed"
	case errors.Is(err, approvals.ErrDigestMismatch):
		return "repair_approval_digest_mismatch"
	case errors.Is(err, approvals.ErrIdempotencyConflict):
		return "repair_approval_idempotency_conflict"
	case errors.Is(err, auth.ErrForbidden):
		return "repair_forbidden"
	default:
		return "repair_unavailable"
	}
}
