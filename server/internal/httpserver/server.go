package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/config"
	"github.com/EasonW3300/repoMender/server/internal/database"
)

// auth supplies session, CSRF, RBAC, and OIDC behavior; database constructs the
// PostgreSQL-backed store while the standard HTTP server remains easy to test.

type HealthChecker interface {
	Ping(context.Context) error
}

type Server struct {
	checker      HealthChecker
	auth         *auth.Service
	oidc         auth.OIDCProvider
	cookieSecure bool
}

func New(checker HealthChecker) *Server {
	return &Server{checker: checker}
}

func NewWithAuth(checker HealthChecker, service *auth.Service, provider auth.OIDCProvider, cookieSecure bool) *Server {
	return &Server{checker: checker, auth: service, oidc: provider, cookieSecure: cookieSecure}
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
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           NewWithAuth(db, service, provider, cfg.CookieSecure).Handler(),
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
