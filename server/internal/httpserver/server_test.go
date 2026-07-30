package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeChecker struct {
	err error
}

func (f fakeChecker) Ping(context.Context) error {
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
