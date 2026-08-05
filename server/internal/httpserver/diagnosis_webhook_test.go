package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/diagnosis"
	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// httpGitHubWorkflowAdapter keeps this test at the HTTP/provider boundary: the
// SCM service still verifies the delivery before M6 sees the normalized event.
type httpGitHubWorkflowAdapter struct{}

func (*httpGitHubWorkflowAdapter) Provider() scm.Provider { return scm.ProviderGitHub }
func (*httpGitHubWorkflowAdapter) Available() bool        { return true }
func (*httpGitHubWorkflowAdapter) ConnectURL(string, string) (string, error) {
	return "https://github.example/connect", nil
}
func (*httpGitHubWorkflowAdapter) Complete(context.Context, scm.Callback, string) (scm.RemoteConnection, scm.Credential, error) {
	return scm.RemoteConnection{Provider: scm.ProviderGitHub}, scm.Credential{}, nil
}
func (*httpGitHubWorkflowAdapter) Repositories(context.Context, scm.RemoteConnection, scm.Credential) ([]scm.RemoteRepository, scm.Credential, error) {
	return nil, scm.Credential{}, nil
}
func (*httpGitHubWorkflowAdapter) VerifyWebhook(headers http.Header, body []byte) (scm.WebhookEvent, error) {
	if headers.Get("X-Test-Token") != "valid" {
		return scm.WebhookEvent{}, scm.ErrInvalidWebhook
	}
	var payload struct {
		Conclusion string `json:"conclusion"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return scm.WebhookEvent{}, scm.ErrInvalidWebhook
	}
	return scm.WebhookEvent{DeliveryID: headers.Get("X-Test-Delivery"), EventType: "workflow_run", Normalized: map[string]any{
		"action": "completed", "conclusion": payload.Conclusion, "repositoryId": int64(7),
		"repository": "acme/payments", "cloneURL": "https://github.com/acme/payments.git",
		"workflowRunId": int64(99), "workflowName": "CI",
		"headSHA": "0123456789abcdef0123456789abcdef01234567", "installationId": int64(42),
	}}, nil
}

func TestGitHubWorkflowWebhookCreatesAndDeduplicatesDiagnosisTask(t *testing.T) {
	authService := auth.NewService(newAuthMemoryStore(), time.Hour)
	taskStore := newTaskMemoryStore()
	scmStore := newHTTPSCMStore()
	box, err := scm.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	scmService := scm.NewService(scmStore, box, &httpGitHubWorkflowAdapter{})
	taskService := tasks.NewService(taskStore)
	diagnosisService := diagnosis.NewService(taskService, scmService)
	handler := NewWithDiagnosisServices(fakeChecker{}, authService, nil, scmService, taskService, nil, diagnosisService, false).Handler()

	invalid := requestJSONWithHeadersAndBody(t, handler, "/webhooks/github", `{"conclusion":"failure"}`, http.Header{
		"X-Test-Token": {"invalid"}, "X-Test-Delivery": {"delivery-ci-invalid"},
	})
	if invalid.Code != http.StatusUnauthorized || taskStore.created != 0 {
		t.Fatalf("invalid workflow webhook status = %d body=%s created=%d", invalid.Code, invalid.Body.String(), taskStore.created)
	}

	first := requestJSONWithHeadersAndBody(t, handler, "/webhooks/github", `{"conclusion":"failure"}`, http.Header{
		"X-Test-Token": {"valid"}, "X-Test-Delivery": {"delivery-ci-1"},
	})
	if first.Code != http.StatusAccepted || !strings.Contains(first.Body.String(), "task_created") || taskStore.created != 1 {
		t.Fatalf("first workflow webhook status = %d body=%s created=%d", first.Code, first.Body.String(), taskStore.created)
	}
	if !strings.Contains(first.Body.String(), "ci_diagnosis") {
		t.Fatalf("workflow task kind missing: %s", first.Body.String())
	}

	duplicate := requestJSONWithHeadersAndBody(t, handler, "/webhooks/github", `{"conclusion":"failure"}`, http.Header{
		"X-Test-Token": {"valid"}, "X-Test-Delivery": {"delivery-ci-1"},
	})
	if duplicate.Code != http.StatusAccepted || !strings.Contains(duplicate.Body.String(), "duplicate") || taskStore.created != 1 {
		t.Fatalf("duplicate workflow webhook status = %d body=%s created=%d", duplicate.Code, duplicate.Body.String(), taskStore.created)
	}

	ignored := requestJSONWithHeadersAndBody(t, handler, "/webhooks/github", `{"conclusion":"success"}`, http.Header{
		"X-Test-Token": {"valid"}, "X-Test-Delivery": {"delivery-ci-success"},
	})
	if ignored.Code != http.StatusAccepted || !strings.Contains(ignored.Body.String(), "ignored") || taskStore.created != 1 {
		t.Fatalf("successful workflow webhook status = %d body=%s created=%d", ignored.Code, ignored.Body.String(), taskStore.created)
	}
}

func requestJSONWithHeadersAndBody(t *testing.T, handler http.Handler, path, body string, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
