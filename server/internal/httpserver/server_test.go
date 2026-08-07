package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	// strings supplies deterministic request bodies for JSON boundary tests.
	"strings"
	"testing"
)

type fakeChecker struct {
	err error
}

func (f fakeChecker) Ping(context.Context) error {
	return f.err
}

type fakeReadinessDependency struct {
	err error
}

func (f fakeReadinessDependency) Check(context.Context) error {
	return f.err
}

func TestLiveDoesNotDependOnDatabase(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()
	New(fakeChecker{err: errors.New("offline")}).Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers missing")
	}
}

func TestReadyReflectsDatabaseState(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "ready", want: http.StatusOK},
		{name: "database unavailable", err: errors.New("offline"), want: http.StatusServiceUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
			response := httptest.NewRecorder()
			New(fakeChecker{err: test.err}).Handler().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func TestReadyReflectsAgentComposeWithoutChangingLiveness(t *testing.T) {
	server := NewWithReadiness(fakeChecker{}, fakeReadinessDependency{err: errors.New("AC offline")})

	readyRequest := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	readyResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(readyResponse, readyRequest)
	if readyResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want %d", readyResponse.Code, http.StatusServiceUnavailable)
	}

	liveRequest := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	liveResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(liveResponse, liveRequest)
	if liveResponse.Code != http.StatusOK {
		t.Fatalf("live status = %d, want %d", liveResponse.Code, http.StatusOK)
	}
}

func TestM10HardeningExposesProtectedMetricsAndVersionedSecurityHeaders(t *testing.T) {
	server := New(fakeChecker{}).WithM10Hardening(M10Options{
		ServiceVersion: "m10-test", MetricsToken: "metrics-secret", RateLimitPerMinute: 10,
	})
	handler := server.Handler()
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("metrics without token status = %d", unauthorized.Code)
	}
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer metrics-secret")
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK || authorized.Header().Get("Content-Type") == "" {
		t.Fatalf("metrics status = %d", authorized.Code)
	}
	version := httptest.NewRecorder()
	handler.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/health/version", nil))
	if version.Code != http.StatusOK || version.Header().Get("X-Request-ID") == "" {
		t.Fatalf("version response missing M10 correlation: status=%d", version.Code)
	}
	if version.Header().Get("Referrer-Policy") != "no-referrer" || version.Header().Get("Permissions-Policy") == "" {
		t.Fatal("M10 security headers missing")
	}
}

func TestM10SecureTransportAddsHSTS(t *testing.T) {
	handler := NewWithAuth(fakeChecker{}, nil, nil, true).Handler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if response.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("secure transport missing HSTS")
	}
}

func TestDecodeJSONRejectsTrailingDocument(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(`{"ok":true}{"unexpected":true}`))
	response := httptest.NewRecorder()

	if decodeJSON(response, request, &struct {
		OK bool `json:"ok"`
	}{}) {
		t.Fatal("decodeJSON accepted multiple JSON documents")
	}
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestDecodeJSONAcceptsTrailingWhitespace(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader("{\"ok\":true}\n  \t"))
	response := httptest.NewRecorder()
	var value struct {
		OK bool `json:"ok"`
	}

	if !decodeJSON(response, request, &value) {
		t.Fatal("decodeJSON rejected trailing whitespace")
	}
	if !value.OK {
		t.Fatal("decoded value lost the JSON field")
	}
}
