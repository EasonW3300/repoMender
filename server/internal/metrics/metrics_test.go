package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMiddlewareUsesMuxPatternAndExposesStableMetrics(t *testing.T) {
	registry := New("m10-test")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	handler := registry.Middleware(mux)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/1234567890abcdef", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
	metricsResponse := httptest.NewRecorder()
	registry.Handler("").ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()
	if !strings.Contains(body, `repomender_build_info{version="m10-test"} 1`) ||
		!strings.Contains(body, `route="GET /api/v1/tasks/{id}"`) {
		t.Fatalf("metrics body missing stable labels: %s", body)
	}
}

func TestResponseWriterPreservesFlush(t *testing.T) {
	registry := New("test")
	handler := registry.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("middleware removed http.Flusher")
			return
		}
		flusher.Flush()
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	registry.Record("GET", "/", http.StatusOK, time.Millisecond)
}
