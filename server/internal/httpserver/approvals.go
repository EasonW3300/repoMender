package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/approvals"
	"github.com/EasonW3300/repoMender/server/internal/auth"
)

// approvals exposes only authenticated, CSRF-protected mutations; the domain
// service remains responsible for role policy, exact-digest checks, and state.

type createApprovalRequest struct {
	Action        approvals.Action `json:"action"`
	Risk          approvals.Risk   `json:"risk"`
	ActionDigest  string           `json:"actionDigest"`
	EligibleRoles []auth.Role      `json:"eligibleRoles"`
	ExpiresAt     time.Time        `json:"expiresAt"`
	Metadata      json.RawMessage  `json:"metadata"`
}

type decisionApprovalRequest struct {
	Decision       approvals.State `json:"decision"`
	Reason         string          `json:"reason"`
	IdempotencyKey string          `json:"idempotencyKey"`
}

type consumeApprovalRequest struct {
	ActionDigest   string `json:"actionDigest"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type cancelApprovalRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireSession(w, r); !ok {
		return
	}
	state := approvals.State(strings.TrimSpace(r.URL.Query().Get("state")))
	items, err := s.approvals.List(r.Context(), approvals.ListFilter{State: state, Limit: queryInt(r, "limit", 100)})
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": items})
}

func (s *Server) createApproval(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireApprovalMutation(w, r)
	if !ok {
		return
	}
	var input createApprovalRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.EligibleRoles) == 0 {
		input.EligibleRoles = []auth.Role{auth.RoleAdmin, auth.RoleMaintainer}
	}
	request, err := s.approvals.Create(r.Context(), approvals.CreateInput{
		Action: input.Action, Risk: input.Risk, ActionDigest: input.ActionDigest,
		RequesterID: session.User.ID, EligibleRoles: input.EligibleRoles,
		ExpiresAt: input.ExpiresAt, Metadata: input.Metadata, RequestedAt: time.Now().UTC(),
	})
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"approval": request})
}

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	request, err := s.approvals.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	canDecide := session.User.Role == auth.RoleAdmin || session.User.Role == auth.RoleMaintainer
	writeJSON(w, http.StatusOK, map[string]any{"approval": request, "canDecide": canDecide})
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireApprovalDecision(w, r)
	if !ok {
		return
	}
	var input decisionApprovalRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	request, err := s.approvals.Decide(r.Context(), approvals.DecisionInput{
		ApprovalID: r.PathValue("id"), Actor: session.User, Decision: input.Decision,
		Reason: input.Reason, IdempotencyKey: input.IdempotencyKey, Now: time.Now().UTC(),
	})
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval": request})
}

func (s *Server) consumeApproval(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireApprovalMutation(w, r)
	if !ok {
		return
	}
	var input consumeApprovalRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	request, err := s.approvals.Consume(r.Context(), approvals.ConsumeInput{
		ApprovalID: r.PathValue("id"), ActorID: session.User.ID,
		ActionDigest: input.ActionDigest, IdempotencyKey: input.IdempotencyKey, Now: time.Now().UTC(),
	})
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval": request})
}

func (s *Server) cancelApproval(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireApprovalMutation(w, r)
	if !ok {
		return
	}
	var input cancelApprovalRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	request, err := s.approvals.Cancel(r.Context(), approvals.CancelInput{
		ApprovalID: r.PathValue("id"), Actor: session.User, Reason: input.Reason, Now: time.Now().UTC(),
	})
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval": request})
}

func (s *Server) requireApprovalMutation(w http.ResponseWriter, r *http.Request) (auth.Session, string, bool) {
	session, token, ok := s.requireSession(w, r)
	if !ok {
		return auth.Session{}, "", false
	}
	if session.User.Role == auth.RoleAuditor {
		writeError(w, http.StatusForbidden, "forbidden")
		return auth.Session{}, "", false
	}
	if !auth.VerifyCSRF(session, r.Header.Get("X-CSRF-Token")) {
		writeError(w, http.StatusForbidden, "csrf_failed")
		return auth.Session{}, "", false
	}
	return session, token, true
}

func (s *Server) requireApprovalDecision(w http.ResponseWriter, r *http.Request) (auth.Session, string, bool) {
	session, token, ok := s.requireSession(w, r)
	if !ok {
		return auth.Session{}, "", false
	}
	if !auth.VerifyCSRF(session, r.Header.Get("X-CSRF-Token")) {
		writeError(w, http.StatusForbidden, "csrf_failed")
		return auth.Session{}, "", false
	}
	return session, token, true
}

func writeApprovalError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, approvals.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, approvals.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, approvals.ErrInvalidAction), errors.Is(err, approvals.ErrInvalidRisk),
		errors.Is(err, approvals.ErrInvalidDigest), errors.Is(err, approvals.ErrInvalidExpiry),
		errors.Is(err, approvals.ErrInvalidIdempotency), errors.Is(err, approvals.ErrInvalidState):
		status = http.StatusBadRequest
	case errors.Is(err, approvals.ErrAlreadyDecided), errors.Is(err, approvals.ErrExpired),
		errors.Is(err, approvals.ErrRejected), errors.Is(err, approvals.ErrCancelled),
		errors.Is(err, approvals.ErrConsumed), errors.Is(err, approvals.ErrDigestMismatch),
		errors.Is(err, approvals.ErrIdempotencyConflict):
		status = http.StatusConflict
	}
	writeError(w, status, approvalErrorCode(err))
}

func approvalErrorCode(err error) string {
	switch {
	case errors.Is(err, approvals.ErrNotFound):
		return "approval_not_found"
	case errors.Is(err, approvals.ErrForbidden):
		return "approval_forbidden"
	case errors.Is(err, approvals.ErrExpired):
		return "approval_expired"
	case errors.Is(err, approvals.ErrRejected):
		return "approval_rejected"
	case errors.Is(err, approvals.ErrCancelled):
		return "approval_cancelled"
	case errors.Is(err, approvals.ErrConsumed):
		return "approval_consumed"
	case errors.Is(err, approvals.ErrDigestMismatch):
		return "approval_digest_mismatch"
	case errors.Is(err, approvals.ErrIdempotencyConflict):
		return "approval_idempotency_conflict"
	case errors.Is(err, approvals.ErrAlreadyDecided):
		return "approval_already_decided"
	case errors.Is(err, approvals.ErrInvalidAction):
		return "invalid_approval_action"
	case errors.Is(err, approvals.ErrInvalidRisk):
		return "invalid_approval_risk"
	case errors.Is(err, approvals.ErrInvalidDigest):
		return "invalid_action_digest"
	case errors.Is(err, approvals.ErrInvalidExpiry):
		return "invalid_approval_expiry"
	case errors.Is(err, approvals.ErrInvalidIdempotency):
		return "invalid_idempotency_key"
	case errors.Is(err, approvals.ErrInvalidState):
		return "invalid_approval_state"
	default:
		return "approval_unavailable"
	}
}
