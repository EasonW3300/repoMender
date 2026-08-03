package agentcompose

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientHealthParsesVersionAndSendsBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"err":null,"msg":"OK","data":{"version":"0","timestamp":1,"timezone":"CST","timezone_offset":28800,"os":"linux","arch":"arm64","compiled_drivers":["docker"]}}`))
	}))
	defer server.Close()

	client, err := New(Config{
		BaseURL: server.URL, AuthToken: "secret", RequiredVersion: "0",
		RequiredDriver: "docker", RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	info, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if info.Version != "0" || info.OS != "linux" || info.Arch != "arm64" {
		t.Fatalf("unexpected version info: %+v", info)
	}
}

func TestClientHealthMapsFailures(t *testing.T) {
	tests := []struct {
		name            string
		status          int
		body            string
		requiredVersion string
		requiredDriver  string
		want            error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, want: ErrRejected},
		{name: "server unavailable", status: http.StatusServiceUnavailable, want: ErrUnavailable},
		{name: "malformed envelope", body: `{"data":`, want: ErrMalformedResponse},
		{name: "daemon error", body: `{"err":"broken","msg":"no","data":{}}`, want: ErrRejected},
		{
			name: "incompatible version", body: healthyVersionBody,
			requiredVersion: "1.2.3", want: ErrIncompatible,
		},
		{
			name: "missing runtime driver", body: healthyVersionBody,
			requiredDriver: "boxlite", want: ErrIncompatible,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status := test.status
				if status == 0 {
					status = http.StatusOK
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client, err := New(Config{
				BaseURL: server.URL, RequiredVersion: test.requiredVersion,
				RequiredDriver: test.requiredDriver, RequestTimeout: time.Second,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = client.Health(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("Health() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClientCheckHonorsRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, RequestTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := client.Check(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Check() error = %v, want timeout", err)
	}
}

const healthyVersionBody = `{"err":null,"msg":"OK","data":{"version":"0","timestamp":1,"timezone":"CST","timezone_offset":28800,"os":"linux","arch":"arm64","compiled_drivers":["docker"]}}`
