package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/approvals"
	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/config"
	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/EasonW3300/repoMender/server/internal/diagnosis"
	"github.com/EasonW3300/repoMender/server/internal/execution"
	"github.com/EasonW3300/repoMender/server/internal/execution/agentcompose"
	"github.com/EasonW3300/repoMender/server/internal/repair"
	"github.com/EasonW3300/repoMender/server/internal/review"
	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// auth supplies identity controls, database constructs PostgreSQL stores, and
// execution/agentcompose supplies the external runtime readiness dependency,
// while scm exposes provider-neutral repository and webhook orchestration.

type HealthChecker interface {
	Ping(context.Context) error
}

type ReadinessDependency interface {
	Check(context.Context) error
}

type Server struct {
	checker      HealthChecker
	readiness    []ReadinessDependency
	auth         *auth.Service
	oidc         auth.OIDCProvider
	scm          *scm.Service
	execution    execution.Adapter
	tasks        *tasks.Service
	review       *review.Service
	diagnosis    *diagnosis.Service
	approvals    *approvals.Service
	repair       *repair.Service
	cookieSecure bool
}

func New(checker HealthChecker) *Server {
	return &Server{checker: checker}
}

func NewWithReadiness(checker HealthChecker, dependencies ...ReadinessDependency) *Server {
	return &Server{checker: checker, readiness: dependencies}
}

func NewWithAuth(checker HealthChecker, service *auth.Service, provider auth.OIDCProvider, cookieSecure bool) *Server {
	return &Server{checker: checker, auth: service, oidc: provider, cookieSecure: cookieSecure}
}

func NewWithServices(
	checker HealthChecker,
	service *auth.Service,
	provider auth.OIDCProvider,
	scmService *scm.Service,
	cookieSecure bool,
	readiness ...ReadinessDependency,
) *Server {
	server := &Server{
		checker: checker, auth: service, oidc: provider, scm: scmService,
		cookieSecure: cookieSecure, readiness: readiness,
	}
	for _, dependency := range readiness {
		if adapter, ok := dependency.(execution.Adapter); ok {
			server.execution = adapter
			break
		}
	}
	return server
}

// NewWithTaskServices keeps the M0-M3 constructor stable while adding the M4
// task store only when its feature flag is enabled.
func NewWithTaskServices(
	checker HealthChecker,
	service *auth.Service,
	provider auth.OIDCProvider,
	scmService *scm.Service,
	taskService *tasks.Service,
	cookieSecure bool,
	readiness ...ReadinessDependency,
) *Server {
	server := NewWithServices(checker, service, provider, scmService, cookieSecure, readiness...)
	server.tasks = taskService
	return server
}

// NewWithReviewServices extends the M4 constructor without changing existing
// test and embedding call sites that intentionally keep M5 disabled.
func NewWithReviewServices(
	checker HealthChecker,
	service *auth.Service,
	provider auth.OIDCProvider,
	scmService *scm.Service,
	taskService *tasks.Service,
	reviewService *review.Service,
	cookieSecure bool,
	readiness ...ReadinessDependency,
) *Server {
	server := NewWithTaskServices(checker, service, provider, scmService, taskService, cookieSecure, readiness...)
	server.review = reviewService
	return server
}

// NewWithDiagnosisServices adds M6 without changing the constructor used by
// M0-M5 tests and deployments where the later feature flag remains disabled.
func NewWithDiagnosisServices(
	checker HealthChecker,
	service *auth.Service,
	provider auth.OIDCProvider,
	scmService *scm.Service,
	taskService *tasks.Service,
	reviewService *review.Service,
	diagnosisService *diagnosis.Service,
	cookieSecure bool,
	readiness ...ReadinessDependency,
) *Server {
	server := NewWithReviewServices(checker, service, provider, scmService, taskService, reviewService, cookieSecure, readiness...)
	server.diagnosis = diagnosisService
	return server
}

// NewWithApprovalServices adds M7 without changing constructors used by M0-M6
// tests and deployments where the approval feature flag remains disabled.
func NewWithApprovalServices(
	checker HealthChecker,
	service *auth.Service,
	provider auth.OIDCProvider,
	scmService *scm.Service,
	taskService *tasks.Service,
	reviewService *review.Service,
	diagnosisService *diagnosis.Service,
	approvalService *approvals.Service,
	cookieSecure bool,
	readiness ...ReadinessDependency,
) *Server {
	server := NewWithDiagnosisServices(checker, service, provider, scmService, taskService, reviewService, diagnosisService, cookieSecure, readiness...)
	server.approvals = approvalService
	return server
}

// NewWithRepairServices adds M8 while preserving all earlier constructor
// signatures used by M0-M7 tests and deployments.
func NewWithRepairServices(
	checker HealthChecker,
	service *auth.Service,
	provider auth.OIDCProvider,
	scmService *scm.Service,
	taskService *tasks.Service,
	reviewService *review.Service,
	diagnosisService *diagnosis.Service,
	approvalService *approvals.Service,
	repairService *repair.Service,
	cookieSecure bool,
	readiness ...ReadinessDependency,
) *Server {
	server := NewWithApprovalServices(checker, service, provider, scmService, taskService, reviewService, diagnosisService, approvalService, cookieSecure, readiness...)
	server.repair = repairService
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	if s.auth != nil {
		mux.HandleFunc("POST /api/v1/auth/bootstrap", s.bootstrap)
		mux.HandleFunc("POST /api/v1/auth/login", s.login)
		mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
		mux.HandleFunc("GET /api/v1/auth/me", s.me)
		mux.HandleFunc("GET /api/v1/auth/oidc/start", s.oidcStart)
		mux.HandleFunc("GET /api/v1/auth/oidc/callback", s.oidcCallback)
		mux.HandleFunc("GET /api/v1/users", s.users)
	}
	if s.auth != nil && s.scm != nil {
		mux.HandleFunc("GET /api/v1/scm/providers", s.scmProviders)
		mux.HandleFunc("GET /api/v1/scm/connections", s.scmConnections)
		mux.HandleFunc("GET /api/v1/scm/{provider}/connect", s.scmConnect)
		mux.HandleFunc("GET /api/v1/scm/{provider}/callback", s.scmCallback)
		mux.HandleFunc("POST /api/v1/scm/connections/{id}/sync", s.scmSync)
		mux.HandleFunc("GET /api/v1/repositories", s.repositories)
		mux.HandleFunc("GET /api/v1/repositories/{id}", s.repository)
		mux.HandleFunc("POST /webhooks/github", s.githubWebhook)
		mux.HandleFunc("POST /webhooks/gitlab", s.gitlabWebhook)
	}
	if s.auth != nil && s.execution != nil {
		mux.HandleFunc("POST /api/v1/internal/executions", s.startExecution)
		mux.HandleFunc("GET /api/v1/internal/executions/{id}/events", s.executionEvents)
		mux.HandleFunc("GET /api/v1/internal/executions/{id}/result", s.executionResult)
		mux.HandleFunc("POST /api/v1/internal/executions/{id}/cancel", s.cancelExecution)
	}
	if s.auth != nil && s.tasks != nil {
		mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
		mux.HandleFunc("POST /api/v1/tasks", s.createTask)
		mux.HandleFunc("GET /api/v1/tasks/{id}", s.getTask)
		mux.HandleFunc("POST /api/v1/tasks/{id}/cancel", s.cancelTask)
		mux.HandleFunc("POST /api/v1/tasks/{id}/retry", s.retryTask)
		mux.HandleFunc("GET /api/v1/tasks/{id}/runs", s.listTaskRuns)
		mux.HandleFunc("GET /api/v1/runs", s.listRuns)
		mux.HandleFunc("GET /api/v1/runs/{id}", s.getRun)
		mux.HandleFunc("GET /api/v1/runs/{id}/events", s.runEvents)
		mux.HandleFunc("GET /api/v1/audit-events", s.auditEvents)
	}
	if s.auth != nil && s.approvals != nil {
		mux.HandleFunc("GET /api/v1/approvals", s.listApprovals)
		mux.HandleFunc("POST /api/v1/approvals", s.createApproval)
		mux.HandleFunc("GET /api/v1/approvals/{id}", s.getApproval)
		mux.HandleFunc("POST /api/v1/approvals/{id}/decision", s.decideApproval)
		mux.HandleFunc("POST /api/v1/approvals/{id}/consume", s.consumeApproval)
		mux.HandleFunc("POST /api/v1/approvals/{id}/cancel", s.cancelApproval)
	}
	if s.auth != nil && s.repair != nil {
		mux.HandleFunc("GET /api/v1/issue-repairs", s.listRepairs)
		mux.HandleFunc("POST /api/v1/issue-repairs", s.createRepair)
		mux.HandleFunc("POST /api/v1/issue-repairs/from-diagnosis", s.createRepairFromDiagnosis)
		mux.HandleFunc("GET /api/v1/issue-repairs/{id}", s.getRepair)
		mux.HandleFunc("POST /api/v1/issue-repairs/{id}/plan/consume", s.consumeRepairPlan)
		mux.HandleFunc("POST /api/v1/issue-repairs/{id}/patch/consume", s.consumeRepairPatch)
	}
	return securityHeaders(mux)
}

func Run(ctx context.Context, cfg config.Config, db *database.DB) error {
	service := auth.NewService(auth.NewPostgreSQLStore(db), cfg.SessionTTL)
	var provider auth.OIDCProvider
	if cfg.OIDCIssuer != "" {
		discovered, err := auth.NewProvider(ctx, cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret,
			strings.TrimRight(cfg.PublicURL, "/")+"/api/v1/auth/oidc/callback")
		if err != nil {
			// Local administrator login stays available when enterprise identity
			// discovery is temporarily unavailable.
			slog.Error("OIDC provider unavailable; local fallback remains active", "error", err)
		} else {
			provider = discovered
		}
	}
	var scmService *scm.Service
	if cfg.FeatureM2SCM {
		box, err := scm.NewSecretBox(cfg.SCMMasterKey)
		if err != nil {
			return err
		}
		github, err := scm.NewGitHubAdapter(scm.GitHubConfig{
			AppID: cfg.GitHubAppID, Slug: cfg.GitHubAppSlug,
			PrivateKeyPEM: cfg.GitHubPrivateKey, WebhookSecret: cfg.GitHubWebhookSecret,
			APIBaseURL: cfg.GitHubAPIBaseURL, WebBaseURL: cfg.GitHubWebBaseURL,
		}, nil)
		if err != nil {
			return err
		}
		adapters := []scm.Adapter{github}
		if cfg.FeatureGitLab {
			// GitLab remains compiled and contract-tested, but only joins the
			// runtime adapter registry when its dedicated deferred flag is enabled.
			gitlab := scm.NewGitLabAdapter(scm.GitLabConfig{
				ClientID: cfg.GitLabClientID, ClientSecret: cfg.GitLabClientSecret,
				WebhookSecret: cfg.GitLabWebhookSecret,
				RedirectURL:   strings.TrimRight(cfg.PublicURL, "/") + "/api/v1/scm/gitlab/callback",
				APIBaseURL:    cfg.GitLabAPIBaseURL, WebBaseURL: cfg.GitLabWebBaseURL,
			}, nil)
			adapters = append(adapters, gitlab)
		}
		scmService = scm.NewService(scm.NewPostgreSQLStore(db), box, adapters...)
	}
	var readiness []ReadinessDependency
	if cfg.FeatureM3ACExecution {
		acClient, err := agentcompose.New(agentcompose.Config{
			BaseURL: cfg.ACBaseURL, AuthToken: cfg.ACAuthToken,
			RequiredVersion: cfg.ACRequiredVersion, RequiredDriver: cfg.ACRequiredDriver,
			RequestTimeout: cfg.ACRequestTimeout, SensitivePatterns: cfg.ACSensitivePatterns,
		})
		if err != nil {
			return err
		}
		readiness = append(readiness, acClient)
	}
	var taskService *tasks.Service
	if cfg.FeatureM4Tasks {
		taskService = tasks.NewService(tasks.NewPostgreSQLStore(db))
	}
	var reviewService *review.Service
	if cfg.FeatureM5CodeReview && cfg.FeatureM2SCM && cfg.FeatureM4Tasks && scmService != nil && taskService != nil {
		reviewService = review.NewService(taskService, scmService)
	}
	var diagnosisService *diagnosis.Service
	if cfg.FeatureM6CIDiagnosis && cfg.FeatureM2SCM && cfg.FeatureM4Tasks && scmService != nil && taskService != nil {
		diagnosisService = diagnosis.NewService(taskService, scmService)
	}
	var approvalService *approvals.Service
	if cfg.FeatureM7Approvals && cfg.FeatureM4Tasks && taskService != nil {
		approvalService = approvals.NewService(approvals.NewPostgreSQLStore(db), taskService)
	}
	var repairService *repair.Service
	if cfg.FeatureM8IssueRepair && cfg.FeatureM7Approvals && cfg.FeatureM4Tasks && taskService != nil && approvalService != nil && scmService != nil {
		repairService = repair.NewService(repair.NewPostgreSQLStore(db), taskService, approvalService, repair.NewSCMProvider(scmService))
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           NewWithRepairServices(db, service, provider, scmService, taskService, reviewService, diagnosisService, approvalService, repairService, cfg.CookieSecure, readiness...).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("RepoMender API listening", "address", cfg.HTTPAddress)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

const (
	sessionCookie = "repomender_session"
	csrfCookie    = "repomender_csrf"
)

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	var request credentials
	if !decodeJSON(w, r, &request) {
		return
	}
	user, err := s.auth.Bootstrap(r.Context(), request.Email, request.Password)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, auth.ErrBootstrapClosed) {
			status = http.StatusConflict
		}
		writeError(w, status, "bootstrap_unavailable")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var request credentials
	if !decodeJSON(w, r, &request) {
		return
	}
	user, sessionToken, csrfToken, err := s.auth.LoginLocal(r.Context(), request.Email, request.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	s.setSessionCookies(w, sessionToken, csrfToken)
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "csrfToken": csrfToken})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, sessionToken, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !auth.VerifyCSRF(session, r.Header.Get("X-CSRF-Token")) {
		writeError(w, http.StatusForbidden, "csrf_failed")
		return
	}
	if err := s.auth.Logout(r.Context(), sessionToken); err != nil {
		writeError(w, http.StatusInternalServerError, "logout_failed")
		return
	}
	s.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": session.User, "expiresAt": session.ExpiresAt})
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	users, err := s.auth.ListUsers(r.Context(), session.User)
	if errors.Is(err, auth.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "users_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeError(w, http.StatusServiceUnavailable, "oidc_unavailable")
		return
	}
	state, _, challenge, err := s.auth.BeginOIDC(r.Context(), r.URL.Query().Get("returnTo"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "oidc_start_failed")
		return
	}
	http.Redirect(w, r, s.oidc.AuthCodeURL(state, challenge), http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeError(w, http.StatusServiceUnavailable, "oidc_unavailable")
		return
	}
	flow, err := s.auth.ConsumeOIDC(r.Context(), r.URL.Query().Get("state"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "oidc_flow_invalid")
		return
	}
	identity, err := s.oidc.Exchange(r.Context(), r.URL.Query().Get("code"), flow.Verifier)
	if err != nil {
		writeError(w, http.StatusBadGateway, "oidc_exchange_failed")
		return
	}
	_, sessionToken, csrfToken, err := s.auth.LoginOIDC(r.Context(), identity)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "oidc_identity_rejected")
		return
	}
	s.setSessionCookies(w, sessionToken, csrfToken)
	http.Redirect(w, r, flow.ReturnTo, http.StatusFound)
}

func (s *Server) requireSession(w http.ResponseWriter, r *http.Request) (auth.Session, string, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication_required")
		return auth.Session{}, "", false
	}
	session, err := s.auth.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication_required")
		return auth.Session{}, "", false
	}
	return session, cookie.Value, true
}

func (s *Server) setSessionCookies(w http.ResponseWriter, sessionToken, csrfToken string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: sessionToken, Path: "/", HttpOnly: true,
		Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookie, Value: csrfToken, Path: "/",
		Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: name == sessionCookie,
			Secure: s.cookieSecure, SameSite: http.SameSiteLaxMode,
		})
	}
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service": "repomender-api",
		"status":  "ok",
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.checker.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"service": "repomender-api",
			"status":  "not_ready",
		})
		return
	}
	for _, dependency := range s.readiness {
		if err := dependency.Check(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"service": "repomender-api",
				"status":  "not_ready",
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"service": "repomender-api",
		"status":  "ready",
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}
