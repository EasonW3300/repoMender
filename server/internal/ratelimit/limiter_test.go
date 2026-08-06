package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EasonW3300/repoMender/server/internal/metrics"
)

func TestLimiterRejectsWithinWindowAndExemptsHealth(t *testing.T) {
	registry := metrics.New("test")
	limiter := New(1, registry)
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", nil)
	request.RemoteAddr = "127.0.0.1:1000"
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, request)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, request)
	if first.Code != http.StatusNoContent || second.Code != http.StatusTooManyRequests {
		t.Fatalf("statuses = %d, %d", first.Code, second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("rate-limited response missing Retry-After")
	}
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if health.Code != http.StatusNoContent {
		t.Fatalf("health status = %d", health.Code)
	}
}
