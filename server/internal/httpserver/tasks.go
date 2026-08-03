package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// Task handlers keep authentication and CSRF checks at the HTTP boundary while
// the tasks service owns persistence and transition validation.

type createTaskRequest struct {
	Kind           tasks.Kind      `json:"kind"`
	Title          string          `json:"title"`
	RepositoryID   string          `json:"repositoryId"`
	RepositoryName string          `json:"repositoryName"`
	SourceKey      string          `json:"sourceKey"`
	IdempotencyKey string          `json:"idempotencyKey"`
	Payload        json.RawMessage `json:"payload"`
	Priority       int             `json:"priority"`
	MaxAttempts    int             `json:"maxAttempts"`
}

type taskMutationRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireTaskReader(w, r); !ok {
		return
	}
	status := tasks.Status(strings.TrimSpace(r.URL.Query().Get("status")))
	kind := tasks.Kind(strings.TrimSpace(r.URL.Query().Get("kind")))
	if status != "" && !tasks.ValidStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid_task_status")
		return
	}
	if kind != "" && !tasks.ValidKind(kind) {
		writeError(w, http.StatusBadRequest, "invalid_task_kind")
		return
	}
	limit := queryInt(r, "limit", 100)
	items, err := s.tasks.List(r.Context(), tasks.ListFilter{
		Status: status,
		Kind:   kind,
		Limit:  limit,
	})
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": items})
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireTaskMutator(w, r)
	if !ok {
		return
	}
	var request createTaskRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	sourceKey := strings.TrimSpace(request.SourceKey)
	if sourceKey == "" {
		sourceKey = strings.TrimSpace(request.IdempotencyKey)
	}
	created, err := s.tasks.Create(r.Context(), tasks.CreateInput{
		Kind:           request.Kind,
		Title:          request.Title,
		RepositoryID:   request.RepositoryID,
		RepositoryName: request.RepositoryName,
		SourceKey:      sourceKey,
		Payload:        request.Payload,
		Priority:       request.Priority,
		MaxAttempts:    request.MaxAttempts,
		CreatedBy:      session.User.ID,
		CreatedAt:      time.Now().UTC(),
	})
	if err != nil {
		writeTaskError(w, err)
		return
	}
	s.recordTaskAudit(r, session.User.ID, "task.create", "task", created.ID, "accepted", created.SourceKey)
	writeJSON(w, http.StatusAccepted, map[string]any{"task": created})
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireTaskReader(w, r)
	if !ok {
		return
	}
	task, err := s.tasks.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeTaskError(w, err)
		return
	}
	runs, err := s.tasks.Runs(r.Context(), task.ID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	audit, err := s.tasks.Audit(r.Context(), tasks.AuditFilter{ResourceType: "task", ResourceID: task.ID, Limit: 100})
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task, "runs": runs, "findings": []tasks.Finding{}, "evidence": []tasks.Evidence{}, "audit": audit, "viewer": session.User.ID})
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireTaskMutator(w, r)
	if !ok {
		return
	}
	var request taskMutationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	task, err := s.tasks.Cancel(r.Context(), r.PathValue("id"), request.Reason)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	s.recordTaskAudit(r, session.User.ID, "task.cancel", "task", task.ID, "accepted", request.Reason)
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (s *Server) retryTask(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireTaskMutator(w, r)
	if !ok {
		return
	}
	var request taskMutationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	task, err := s.tasks.Retry(r.Context(), r.PathValue("id"), request.Reason)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	s.recordTaskAudit(r, session.User.ID, "task.retry", "task", task.ID, "accepted", request.Reason)
	writeJSON(w, http.StatusAccepted, map[string]any{"task": task})
}

func (s *Server) listTaskRuns(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireTaskReader(w, r); !ok {
		return
	}
	runs, err := s.tasks.Runs(r.Context(), r.PathValue("id"))
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireTaskReader(w, r); !ok {
		return
	}
	runs, err := s.tasks.AllRuns(r.Context(), queryInt(r, "limit", 100))
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireTaskReader(w, r); !ok {
		return
	}
	run, err := s.tasks.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		writeTaskError(w, err)
		return
	}
	events, err := s.tasks.Events(r.Context(), run.ID, 0)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "events": events})
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireTaskReader(w, r); !ok {
		return
	}
	after := int64(0)
	if value := strings.TrimSpace(r.URL.Query().Get("after")); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid_event_offset")
			return
		}
		after = parsed
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		events, err := s.tasks.Events(r.Context(), r.PathValue("id"), after)
		if err != nil {
			writeSSEError(w, flusher, taskErrorCode(err))
			return
		}
		for _, event := range events {
			payload, err := json.Marshal(event)
			if err != nil {
				writeSSEError(w, flusher, "task_event_encoding_failed")
				return
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Kind, payload)
			flusher.Flush()
			after = event.Sequence
			if event.Terminal {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) auditEvents(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireTaskReader(w, r); !ok {
		return
	}
	events, err := s.tasks.Audit(r.Context(), tasks.AuditFilter{
		ResourceType: strings.TrimSpace(r.URL.Query().Get("resourceType")),
		ResourceID:   strings.TrimSpace(r.URL.Query().Get("resourceId")),
		Limit:        queryInt(r, "limit", 100),
	})
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) requireTaskReader(w http.ResponseWriter, r *http.Request) (auth.Session, string, bool) {
	return s.requireSession(w, r)
}

func (s *Server) requireTaskMutator(w http.ResponseWriter, r *http.Request) (auth.Session, string, bool) {
	session, token, ok := s.requireTaskReader(w, r)
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

func (s *Server) recordTaskAudit(r *http.Request, actorID, action, resourceType, resourceID, outcome, detail string) {
	metadata, _ := json.Marshal(map[string]string{"detail": strings.TrimSpace(detail), "method": r.Method})
	_, _ = s.tasks.RecordAudit(r.Context(), tasks.AuditInput{
		ActorID: actorID, Action: action, ResourceType: resourceType, ResourceID: resourceID,
		Outcome: outcome, Metadata: metadata,
	})
}

func queryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func writeTaskError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, tasks.ErrTaskNotFound) || errors.Is(err, tasks.ErrRunNotFound) {
		status = http.StatusNotFound
	} else if errors.Is(err, tasks.ErrInvalidKind) || errors.Is(err, tasks.ErrInvalidStatus) {
		status = http.StatusBadRequest
	} else if errors.Is(err, tasks.ErrInvalidTransition) || errors.Is(err, tasks.ErrLeaseLost) {
		status = http.StatusConflict
	}
	writeError(w, status, taskErrorCode(err))
}

func taskErrorCode(err error) string {
	switch {
	case errors.Is(err, tasks.ErrTaskNotFound):
		return "task_not_found"
	case errors.Is(err, tasks.ErrRunNotFound):
		return "run_not_found"
	case errors.Is(err, tasks.ErrInvalidKind):
		return "invalid_task_kind"
	case errors.Is(err, tasks.ErrInvalidStatus):
		return "invalid_task_status"
	case errors.Is(err, tasks.ErrInvalidTransition):
		return "invalid_task_transition"
	case errors.Is(err, tasks.ErrLeaseLost):
		return "task_lease_lost"
	default:
		return "task_operation_failed"
	}
}
