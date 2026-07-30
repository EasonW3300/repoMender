package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/config"
)

// The standard HTTP server keeps the M0 surface small while exposing handlers
// that can be tested without a running network listener.

type HealthChecker interface {
	Ping(context.Context) error
}

type Server struct {
	checker HealthChecker
}

func New(checker HealthChecker) *Server {
	return &Server{checker: checker}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	return securityHeaders(mux)
}

func Run(ctx context.Context, cfg config.Config, checker HealthChecker) error {
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           New(checker).Handler(),
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
