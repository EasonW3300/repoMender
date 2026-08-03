package httpserver

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/review"
	"github.com/EasonW3300/repoMender/server/internal/scm"
)

// auth sessions and CSRF checks protect connection mutations. scm.Service
// performs provider-neutral authorization, synchronization, and webhook logic.

func (s *Server) scmProviders(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requireSession(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.scm.Providers()})
}

func (s *Server) scmConnections(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	connections, err := s.scm.ListConnections(r.Context(), session.User)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scm_connections_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": connections})
}

func (s *Server) scmConnect(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	location, err := s.scm.BeginConnect(r.Context(), session.User,
		scm.Provider(r.PathValue("provider")), r.URL.Query().Get("returnTo"))
	if errors.Is(err, scm.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if errors.Is(err, scm.ErrProviderUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scm_connect_failed")
		return
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func (s *Server) scmCallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("error") != "" {
		writeError(w, http.StatusBadRequest, "scm_authorization_rejected")
		return
	}
	provider := scm.Provider(r.PathValue("provider"))
	connection, returnTo, err := s.scm.CompleteConnect(r.Context(), provider,
		r.URL.Query().Get("state"), scm.Callback{
			Code: r.URL.Query().Get("code"), InstallationID: r.URL.Query().Get("installation_id"),
		})
	if errors.Is(err, scm.ErrInvalidFlow) {
		writeError(w, http.StatusBadRequest, "scm_flow_invalid")
		return
	}
	if errors.Is(err, scm.ErrProviderUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "scm_callback_failed")
		return
	}
	target, _ := url.Parse(returnTo)
	query := target.Query()
	query.Set("connected", string(connection.Provider))
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) scmSync(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !auth.VerifyCSRF(session, r.Header.Get("X-CSRF-Token")) {
		writeError(w, http.StatusForbidden, "csrf_failed")
		return
	}
	connection, err := s.scm.Sync(r.Context(), session.User, r.PathValue("id"))
	if errors.Is(err, scm.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if errors.Is(err, scm.ErrProviderUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "scm_sync_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": connection})
}

func (s *Server) repositories(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	repositories, err := s.scm.ListRepositories(r.Context(), session.User)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "repositories_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repositories})
}

func (s *Server) repository(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	repository, err := s.scm.GetRepository(r.Context(), session.User, r.PathValue("id"))
	if errors.Is(err, scm.ErrRepositoryNotFound) {
		writeError(w, http.StatusNotFound, "repository_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "repository_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repository": repository})
}

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	s.handleWebhook(w, r, scm.ProviderGitHub)
}

func (s *Server) gitlabWebhook(w http.ResponseWriter, r *http.Request) {
	s.handleWebhook(w, r, scm.ProviderGitLab)
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request, provider scm.Provider) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "webhook_too_large")
		return
	}
	if s.review != nil && provider == scm.ProviderGitHub {
		event, err := s.scm.VerifyWebhook(provider, r.Header, body)
		if errors.Is(err, scm.ErrInvalidWebhook) {
			writeError(w, http.StatusUnauthorized, "webhook_invalid")
			return
		}
		if errors.Is(err, scm.ErrProviderUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "webhook_failed")
			return
		}
		task, actionable, err := s.review.HandleWebhook(r.Context(), event)
		if errors.Is(err, review.ErrUnsupportedEvent) {
			actionable = false
			err = nil
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "review_event_invalid")
			return
		}
		if !actionable {
			created, recordErr := s.scm.RecordWebhook(r.Context(), provider, event, body)
			if recordErr != nil {
				writeError(w, http.StatusInternalServerError, "webhook_failed")
				return
			}
			if !created {
				writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate", "provider": "github"})
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "provider": "github"})
			return
		}
		created, err := s.scm.RecordWebhook(r.Context(), provider, event, body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "webhook_failed")
			return
		}
		if !created {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate", "provider": "github"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "task_created", "provider": "github", "task": task})
		return
	}
	created, err := s.scm.HandleWebhook(r.Context(), provider, r.Header, body)
	if errors.Is(err, scm.ErrInvalidWebhook) {
		writeError(w, http.StatusUnauthorized, "webhook_invalid")
		return
	}
	if errors.Is(err, scm.ErrProviderUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "scm_provider_unavailable")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "webhook_failed")
		return
	}
	status := "accepted"
	if !created {
		status = "duplicate"
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": status, "provider": strings.ToLower(string(provider)),
	})
}
