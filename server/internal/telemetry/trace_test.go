package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTraceparentRoundTrip(t *testing.T) {
	input := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	span := ParseTraceparent(input)
	if span.TraceID == "" || FormatTraceparent(span) != input {
		t.Fatalf("traceparent round trip failed: %+v %q", span, FormatTraceparent(span))
	}
	if ParseTraceparent("invalid").TraceID != "" {
		t.Fatal("invalid traceparent accepted")
	}
}

func TestMiddlewareCreatesRequestCorrelation(t *testing.T) {
	handler := Middleware(Config{ServiceName: "test"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if FromContext(r.Context()).TraceID == "" {
			t.Error("trace context missing")
		}
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("X-Request-ID") == "" || response.Header().Get("traceparent") == "" {
		t.Fatal("correlation headers missing")
	}
}
