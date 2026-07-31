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
	"github.com/EasonW3300/repoMender/server/internal/execution"
	"github.com/EasonW3300/repoMender/server/internal/execution/agentcompose"
)

// encoding/json formats SSE payloads, while auth and execution enforce the
// internal diagnostic boundary without exposing M4 task-management concepts.

type startExecutionRequest struct {
	CorrelationID  string `json:"correlationId"`
	ProjectID      string `json:"projectId"`
	AgentName      string `json:"agentName"`
	Repository     string `json:"repository"`
	CommitSHA      string `json:"commitSha"`
	Prompt         string `json:"prompt"`
	TimeoutSeconds int64  `json:"timeoutSeconds"`
	Driver         string `json:"driver"`
	NetworkEnabled bool   `json:"networkEnabled"`
}

type cancelExecutionRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) startExecution(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireExecutionOperator(w, r, true)
	if !ok {
		return
	}
	var request startExecutionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	run, err := s.execution.Start(r.Context(), execution.Request{
		CorrelationID: request.CorrelationID, ProjectID: request.ProjectID,
		AgentName: request.AgentName, Repository: request.Repository,
		CommitSHA: request.CommitSHA, Prompt: request.Prompt,
		Timeout: time.Duration(request.TimeoutSeconds) * time.Second,
		Policy: execution.ResourcePolicy{
			Driver: request.Driver, NetworkEnabled: request.NetworkEnabled, Cleanup: "remove",
		},
	})
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"run": run, "startedBy": session.User.ID,
	})
}

func (s *Server) executionEvents(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireExecutionOperator(w, r, false); !ok {
		return
	}
	after := uint64(0)
	if value := strings.TrimSpace(r.URL.Query().Get("after")); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
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

	events, errs := s.execution.Events(r.Context(), r.PathValue("id"), after)
	for event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			writeSSEError(w, flusher, "ac_malformed_response")
			return
		}
		_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Kind, payload)
		flusher.Flush()
	}
	if err := <-errs; err != nil {
		writeSSEError(w, flusher, executionErrorCode(err))
	}
}

func (s *Server) executionResult(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireExecutionOperator(w, r, false); !ok {
		return
	}
	result, err := s.execution.Result(r.Context(), r.PathValue("id"))
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) cancelExecution(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireExecutionOperator(w, r, true); !ok {
		return
	}
	var request cancelExecutionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.execution.Cancel(r.Context(), r.PathValue("id"), request.Reason); err != nil {
		writeExecutionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireExecutionOperator permits only administrators and maintainers. CSRF
// is mandatory for start/cancel mutations and deliberately omitted for reads.
func (s *Server) requireExecutionOperator(
	w http.ResponseWriter,
	r *http.Request,
	mutation bool,
) (auth.Session, string, bool) {
	session, token, ok := s.requireSession(w, r)
	if !ok {
		return auth.Session{}, "", false
	}
	if session.User.Role != auth.RoleAdmin && session.User.Role != auth.RoleMaintainer {
		writeError(w, http.StatusForbidden, "forbidden")
		return auth.Session{}, "", false
	}
	if mutation && !auth.VerifyCSRF(session, r.Header.Get("X-CSRF-Token")) {
		writeError(w, http.StatusForbidden, "csrf_failed")
		return auth.Session{}, "", false
	}
	return session, token, true
}

func writeExecutionError(w http.ResponseWriter, err error) {
	code := executionErrorCode(err)
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, agentcompose.ErrRejected):
		status = http.StatusBadRequest
	case errors.Is(err, agentcompose.ErrIncompatible):
		status = http.StatusPreconditionFailed
	case errors.Is(err, agentcompose.ErrTimeout):
		status = http.StatusGatewayTimeout
	case errors.Is(err, agentcompose.ErrCancelled):
		status = http.StatusConflict
	case errors.Is(err, agentcompose.ErrUnavailable):
		status = http.StatusServiceUnavailable
	}
	writeError(w, status, code)
}

func executionErrorCode(err error) string {
	switch {
	case errors.Is(err, agentcompose.ErrUnavailable):
		return "ac_unavailable"
	case errors.Is(err, agentcompose.ErrRejected):
		return "ac_rejected"
	case errors.Is(err, agentcompose.ErrIncompatible):
		return "ac_incompatible"
	case errors.Is(err, agentcompose.ErrTimeout):
		return "ac_timeout"
	case errors.Is(err, agentcompose.ErrCancelled):
		return "ac_cancelled"
	case errors.Is(err, agentcompose.ErrSandboxFailed):
		return "ac_sandbox_failed"
	case errors.Is(err, agentcompose.ErrAgentFailed):
		return "ac_agent_failed"
	case errors.Is(err, agentcompose.ErrMalformedResponse):
		return "ac_malformed_response"
	case errors.Is(err, agentcompose.ErrStreamDropped):
		return "ac_stream_dropped"
	default:
		return "ac_execution_failed"
	}
}

func writeSSEError(w http.ResponseWriter, flusher http.Flusher, code string) {
	payload, _ := json.Marshal(map[string]string{"error": code})
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
	flusher.Flush()
}
