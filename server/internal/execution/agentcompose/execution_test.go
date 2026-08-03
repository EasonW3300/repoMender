package agentcompose

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/execution"
)

// encoding/binary builds Connect streaming envelopes, while httptest captures
// the exact JSON contract sent by the adapter.

func TestClientStartSendsImmutableExecutionRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != startRunProcedure {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var request struct {
			Run struct {
				ProjectID       string `json:"projectId"`
				AgentName       string `json:"agentName"`
				Prompt          string `json:"prompt"`
				Source          string `json:"source"`
				ClientRequestID string `json:"clientRequestId"`
				Driver          string `json:"driver"`
				PayloadJSON     string `json:"payloadJson"`
			} `json:"run"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Run.Source != "RUN_SOURCE_API" || request.Run.Driver != "docker" {
			t.Fatalf("unexpected AC run request: %+v", request.Run)
		}
		if !strings.Contains(request.Run.PayloadJSON, `"commit":"0123456789012345678901234567890123456789"`) {
			t.Fatalf("payload does not pin commit: %s", request.Run.PayloadJSON)
		}
		_, _ = w.Write([]byte(`{"run":{"runId":"run-1","projectId":"project-1","agentName":"codex","status":"RUN_STATUS_PENDING","startedAt":"2026-07-31T10:00:00Z"},"started":true}`))
	}))
	defer server.Close()

	client := newExecutionTestClient(t, server.URL)
	run, err := client.Start(context.Background(), validExecutionRequest())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.ID != "run-1" || run.CorrelationID != "corr-1" {
		t.Fatalf("unexpected run: %+v", run)
	}
}

func TestClientStartRejectsMutableOrIncompleteInput(t *testing.T) {
	client := newExecutionTestClient(t, "http://ac.invalid")
	tests := []struct {
		name   string
		mutate func(*execution.Request)
	}{
		{name: "repository", mutate: func(r *execution.Request) { r.Repository = "" }},
		{name: "commit", mutate: func(r *execution.Request) { r.CommitSHA = "main" }},
		{name: "prompt", mutate: func(r *execution.Request) { r.Prompt = "" }},
		{name: "timeout", mutate: func(r *execution.Request) { r.Timeout = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validExecutionRequest()
			test.mutate(&request)
			if _, err := client.Start(context.Background(), request); !errors.Is(err, ErrRejected) {
				t.Fatalf("Start() error = %v, want rejected", err)
			}
		})
	}
}

func TestClientEventsDecodesConnectStreamAndRedactsSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != followRunLogsProcedure {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != connectStreamingJSON {
			t.Fatalf("Content-Type = %q", got)
		}
		requestPayload := readConnectRequestFrame(t, r.Body)
		if !strings.Contains(string(requestPayload), `"runId":"run-1"`) ||
			!strings.Contains(string(requestPayload), `"startOffset":"12"`) {
			t.Fatalf("unexpected stream request: %s", requestPayload)
		}
		w.Header().Set("Content-Type", connectStreamingJSON)
		writeConnectFrame(t, w, 0, `{"offset":"18","data":"token=super-secret\n","runStatus":"RUN_STATUS_RUNNING","createdAt":"2026-07-31T10:00:01Z"}`)
		writeConnectFrame(t, w, 0, `{"offset":"18","isFinal":true,"runStatus":"RUN_STATUS_SUCCEEDED","createdAt":"2026-07-31T10:00:02Z","run":{"runId":"run-1","status":"RUN_STATUS_SUCCEEDED"}}`)
		writeConnectFrame(t, w, connectEndStreamFlag, `{}`)
	}))
	defer server.Close()

	client, err := New(Config{
		BaseURL: server.URL, AuthToken: "super-secret", RequestTimeout: time.Second,
		RequiredDriver: "", SensitivePatterns: []string{"token="},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	events, errs := client.Events(context.Background(), "run-1", 12)
	var received []execution.Event
	for event := range events {
		received = append(received, event)
	}
	if err := <-errs; err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if len(received) != 2 || received[0].Kind != execution.EventLog ||
		received[1].Kind != execution.EventCompleted || !received[1].Terminal {
		t.Fatalf("unexpected events: %+v", received)
	}
	if strings.Contains(received[0].Message, "super-secret") || !strings.Contains(received[0].Message, "[REDACTED]") {
		t.Fatalf("secret was not redacted: %q", received[0].Message)
	}
}

func TestClientCancelAndResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case stopRunProcedure:
			_, _ = w.Write([]byte(`{"run":{"summary":{"runId":"run-1","status":"RUN_STATUS_CANCELED"}},"stopRequested":true}`))
		case getRunProcedure:
			_, _ = w.Write([]byte(`{"run":{"summary":{"runId":"run-1","status":"RUN_STATUS_SUCCEEDED","completedAt":"2026-07-31T10:00:03Z"},"resultJson":"{\"schemaVersion\":\"v1\",\"summary\":\"ok\"}"}}`))
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newExecutionTestClient(t, server.URL)
	if err := client.Cancel(context.Background(), "run-1", "operator requested"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	result, err := client.Result(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if result.Status != "succeeded" || result.SchemaVersion != "v1" ||
		!strings.Contains(string(result.Output), `"summary":"ok"`) {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestClientResultPrefersAgentOutputAndStripsProviderPreamble(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != getRunProcedure {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"run":{"summary":{"runId":"run-1","status":"RUN_STATUS_SUCCEEDED"},"output":"Model metadata warning\n{\"schemaVersion\":\"v1\",\"summary\":\"ok\"}","resultJson":"{\"agent\":\"codex\",\"success\":true}"}}`))
	}))
	defer server.Close()

	client := newExecutionTestClient(t, server.URL)
	result, err := client.Result(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if result.SchemaVersion != "v1" || string(result.Output) != `{"schemaVersion":"v1","summary":"ok"}` {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestClientResultMapsFailedRunBeforeSchemaValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"run":{"summary":{"runId":"run-1","status":"RUN_STATUS_FAILED","error":"agent execution failed: upstream disconnected"}}}`))
	}))
	defer server.Close()

	client := newExecutionTestClient(t, server.URL)
	if _, err := client.Result(context.Background(), "run-1"); !errors.Is(err, ErrAgentFailed) {
		t.Fatalf("Result() error = %v, want agent failure", err)
	}
}

func TestClientMapsConnectErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"unavailable","message":"daemon offline"}`))
	}))
	defer server.Close()

	client := newExecutionTestClient(t, server.URL)
	if _, err := client.Start(context.Background(), validExecutionRequest()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Start() error = %v, want unavailable", err)
	}
}

func TestClientEventsRejectsDroppedAndMalformedStreams(t *testing.T) {
	tests := []struct {
		name string
		send func(*testing.T, http.ResponseWriter)
		want error
	}{
		{
			name: "dropped before terminal",
			send: func(t *testing.T, w http.ResponseWriter) {
				writeConnectFrame(t, w, 0, `{"offset":"2","data":"partial"}`)
				writeConnectFrame(t, w, connectEndStreamFlag, `{}`)
			},
			want: ErrStreamDropped,
		},
		{
			name: "malformed event",
			send: func(t *testing.T, w http.ResponseWriter) {
				writeConnectFrame(t, w, 0, `{"offset":`)
			},
			want: ErrMalformedResponse,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", connectStreamingJSON)
				test.send(t, w)
			}))
			defer server.Close()
			client := newExecutionTestClient(t, server.URL)
			events, errs := client.Events(context.Background(), "run-1", 0)
			for range events {
			}
			if err := <-errs; !errors.Is(err, test.want) {
				t.Fatalf("Events() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClientDeadlineStopsAgentComposeRun(t *testing.T) {
	var stopped atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case startRunProcedure:
			_, _ = w.Write([]byte(`{"run":{"runId":"run-timeout","status":"RUN_STATUS_PENDING"},"started":true}`))
		case followRunLogsProcedure:
			w.Header().Set("Content-Type", connectStreamingJSON)
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		case stopRunProcedure:
			stopped.Store(true)
			_, _ = w.Write([]byte(`{"stopRequested":true}`))
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newExecutionTestClient(t, server.URL)
	request := validExecutionRequest()
	request.Timeout = 30 * time.Millisecond
	run, err := client.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	events, errs := client.Events(context.Background(), run.ID, 0)
	for range events {
	}
	if err := <-errs; !errors.Is(err, ErrTimeout) {
		t.Fatalf("Events() error = %v, want timeout", err)
	}
	if !stopped.Load() {
		t.Fatal("deadline did not request AC cancellation")
	}
}

func validExecutionRequest() execution.Request {
	return execution.Request{
		CorrelationID: "corr-1", ProjectID: "project-1", AgentName: "codex",
		Repository: "https://github.com/EasonW3300/repomender-sandbox.git",
		CommitSHA:  "0123456789012345678901234567890123456789",
		Prompt:     "Inspect the repository and return the diagnostic schema.",
		Timeout:    5 * time.Minute,
		Policy: execution.ResourcePolicy{
			Driver: "docker", Cleanup: "remove",
		},
	}
}

func newExecutionTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := New(Config{BaseURL: baseURL, RequestTimeout: time.Second, RequiredDriver: ""})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func readConnectRequestFrame(t *testing.T, reader io.Reader) []byte {
	t.Helper()
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	payload := make([]byte, binary.BigEndian.Uint32(header[1:]))
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	return payload
}

func writeConnectFrame(t *testing.T, writer io.Writer, flag byte, payload string) {
	t.Helper()
	var header [5]byte
	header[0] = flag
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := writer.Write(header[:]); err != nil {
		t.Fatalf("write frame header: %v", err)
	}
	if _, err := io.WriteString(writer, payload); err != nil {
		t.Fatalf("write frame payload: %v", err)
	}
}
