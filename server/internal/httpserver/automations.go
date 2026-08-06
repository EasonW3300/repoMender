package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/automations"
)

// Automation HTTP mutations require all three controls at the edge: an admin
// role, the session-bound CSRF token, and a fresh local credential check.
type automationInputRequest struct {
	automations.Input
	ExpectedRevision int `json:"expectedRevision,omitempty"`
}

type automationEnableRequest struct {
	ExpectedRevision int `json:"expectedRevision"`
}

type automationTriggerRequest struct {
	TriggerKey    string          `json:"triggerKey"`
	Provider      string          `json:"provider"`
	Event         string          `json:"event"`
	Action        string          `json:"action"`
	Branch        string          `json:"branch,omitempty"`
	Repository    string          `json:"repository"`
	RepositoryID  string          `json:"repositoryId,omitempty"`
	Title         string          `json:"title,omitempty"`
	TaskPayload   json.RawMessage `json:"taskPayload"`
	SourcePayload json.RawMessage `json:"sourcePayload,omitempty"`
}

// automationOverview is the read-only administrative surface for the M9
// settings page. It deliberately exposes health and aggregate budget data,
// while detailed audit records remain behind the existing audit lookup API.
func (s *Server) automationOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAutomationAdmin(w, r, false); !ok {
		return
	}
	items, err := s.automations.List(r.Context(), 200)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	agentTemplates := make([]string, 0, len(items))
	seenAgents := make(map[string]struct{}, len(items))
	configuredBudget, activeRuns := 0, 0
	for _, item := range items {
		if item.Enabled {
			configuredBudget += item.ExecutionBudget
		}
		if _, exists := seenAgents[item.AgentTemplate]; !exists {
			seenAgents[item.AgentTemplate] = struct{}{}
			agentTemplates = append(agentTemplates, item.AgentTemplate)
		}
		runs, runsErr := s.automations.Runs(r.Context(), item.ID, 200)
		if runsErr != nil {
			writeAutomationError(w, runsErr)
			return
		}
		for _, run := range runs {
			if run.TaskStatus == "queued" || run.TaskStatus == "running" || run.TaskStatus == "awaiting_approval" {
				activeRuns++
			}
		}
	}

	health := map[string]any{"scm": map[string]any{"status": "unconfigured", "providers": map[string]bool{}},
		"agentCompose": map[string]any{"status": "unconfigured"}}
	if s.scm != nil {
		providers := make(map[string]bool)
		for provider, available := range s.scm.Providers() {
			providers[string(provider)] = available
		}
		health["scm"] = map[string]any{"status": "configured", "providers": providers}
	}
	if s.execution != nil {
		checkContext, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		version, healthErr := s.execution.Health(checkContext)
		cancel()
		status := "ready"
		if healthErr != nil {
			status = "unavailable"
		}
		health["agentCompose"] = map[string]any{"status": status, "version": version.Version, "error": errorString(healthErr)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"health":         health,
		"retention":      map[string]any{"auditDays": 365, "runHistoryDays": 365, "enforcement": "M10 policy boundary"},
		"agentTemplates": agentTemplates,
		"budgetUse":      map[string]int{"configured": configuredBudget, "activeRuns": activeRuns},
		"audit":          map[string]string{"lookupPath": "/api/v1/audit-events?resourceType=automation", "resourceType": "automation"},
		"templates":      map[string]int{"total": len(items), "enabled": countEnabled(items)},
	})
}

func countEnabled(items []automations.Template) int {
	count := 0
	for _, item := range items {
		if item.Enabled {
			count++
		}
	}
	return count
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) listAutomations(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireSession(w, r); !ok {
		return
	}
	items, err := s.automations.List(r.Context(), queryInt(r, "limit", 100))
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"automations": items})
}

func (s *Server) createAutomation(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAutomationAdmin(w, r, true)
	if !ok {
		return
	}
	var request automationInputRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	created, err := s.automations.Create(r.Context(), request.Input, session.User.ID)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"automation": created})
}

func (s *Server) getAutomation(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireSession(w, r); !ok {
		return
	}
	template, err := s.automations.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	versions, err := s.automations.History(r.Context(), template.ID, 100)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	runs, err := s.automations.Runs(r.Context(), template.ID, 100)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"automation": template, "versions": versions, "runs": runs})
}

func (s *Server) updateAutomation(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAutomationAdmin(w, r, true)
	if !ok {
		return
	}
	var request automationInputRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	updated, err := s.automations.UpdateDraft(r.Context(), r.PathValue("id"), request.ExpectedRevision, request.Input, session.User.ID)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"automation": updated})
}

func (s *Server) publishAutomation(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAutomationAdmin(w, r, true)
	if !ok {
		return
	}
	var request automationEnableRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	updated, err := s.automations.Publish(r.Context(), r.PathValue("id"), request.ExpectedRevision, session.User.ID)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"automation": updated})
}

func (s *Server) enableAutomation(w http.ResponseWriter, r *http.Request) {
	s.setAutomationEnabled(w, r, true)
}

func (s *Server) disableAutomation(w http.ResponseWriter, r *http.Request) {
	s.setAutomationEnabled(w, r, false)
}

func (s *Server) setAutomationEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	session, ok := s.requireAutomationAdmin(w, r, true)
	if !ok {
		return
	}
	var request automationEnableRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	updated, err := s.automations.SetEnabled(r.Context(), r.PathValue("id"), request.ExpectedRevision, enabled, session.User.ID)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"automation": updated})
}

func (s *Server) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAutomationAdmin(w, r, true)
	if !ok {
		return
	}
	var request automationEnableRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.automations.Delete(r.Context(), r.PathValue("id"), request.ExpectedRevision, session.User.ID); err != nil {
		writeAutomationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) triggerAutomation(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAutomationAdmin(w, r, true)
	if !ok {
		return
	}
	var request automationTriggerRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	result, err := s.automations.Trigger(r.Context(), automations.TriggerInput{
		TemplateID: r.PathValue("id"), TriggerKey: request.TriggerKey, Provider: request.Provider,
		Event: request.Event, Action: request.Action, Branch: request.Branch, Repository: request.Repository,
		RepositoryID: request.RepositoryID, Title: request.Title, TaskPayload: request.TaskPayload,
		SourcePayload: request.SourcePayload, ActorID: session.User.ID,
	})
	if err != nil && !errors.Is(err, automations.ErrDuplicateTrigger) {
		writeAutomationError(w, err)
		return
	}
	status := http.StatusAccepted
	if result.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"run": result.Run, "task": result.Task, "duplicate": result.Duplicate})
}

func (s *Server) automationVersions(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireSession(w, r); !ok {
		return
	}
	versions, err := s.automations.History(r.Context(), r.PathValue("id"), queryInt(r, "limit", 100))
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (s *Server) automationRuns(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireSession(w, r); !ok {
		return
	}
	runs, err := s.automations.Runs(r.Context(), r.PathValue("id"), queryInt(r, "limit", 100))
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) requireAutomationAdmin(w http.ResponseWriter, r *http.Request, mutation bool) (auth.Session, bool) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return auth.Session{}, false
	}
	if session.User.Role != auth.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return auth.Session{}, false
	}
	if mutation && !auth.VerifyCSRF(session, r.Header.Get("X-CSRF-Token")) {
		writeError(w, http.StatusForbidden, "csrf_failed")
		return auth.Session{}, false
	}
	if mutation {
		if err := s.auth.Reauthenticate(r.Context(), session.User, r.Header.Get("X-Reauth-Password")); err != nil {
			writeError(w, http.StatusUnauthorized, "reauthentication_required")
			return auth.Session{}, false
		}
	}
	return session, true
}

func writeAutomationError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, automations.ErrTemplateNotFound):
		status = http.StatusNotFound
	case errors.Is(err, automations.ErrValidation), errors.Is(err, automations.ErrInvalidTrigger):
		status = http.StatusBadRequest
	case errors.Is(err, automations.ErrDisabled), errors.Is(err, automations.ErrDuplicateTrigger), errors.Is(err, automations.ErrConcurrencyLimit),
		errors.Is(err, automations.ErrConflict), errors.Is(err, automations.ErrPublished):
		status = http.StatusConflict
	}
	writeError(w, status, automationErrorCode(err))
}

func automationErrorCode(err error) string {
	switch {
	case errors.Is(err, automations.ErrTemplateNotFound):
		return "automation_not_found"
	case errors.Is(err, automations.ErrValidation):
		return "automation_invalid"
	case errors.Is(err, automations.ErrInvalidTrigger):
		return "automation_trigger_invalid"
	case errors.Is(err, automations.ErrDisabled):
		return "automation_disabled"
	case errors.Is(err, automations.ErrDuplicateTrigger):
		return "automation_trigger_duplicate"
	case errors.Is(err, automations.ErrConcurrencyLimit):
		return "automation_concurrency_limit"
	case errors.Is(err, automations.ErrConflict):
		return "automation_revision_conflict"
	case errors.Is(err, automations.ErrPublished):
		return "automation_published"
	default:
		return strings.TrimSpace(err.Error())
	}
}
