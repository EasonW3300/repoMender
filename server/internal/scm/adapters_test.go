package scm

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// httptest servers act as recorded provider fixtures while real crypto verifies
// GitHub App JWT signing and both webhook authentication schemes.

func TestGitHubAdapterInstallationRepositoriesAndWebhook(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: mustPKCS8(t, privateKey),
	})
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Fatal("missing GitHub authorization")
		}
		switch r.URL.Path {
		case "/app/installations/42":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 42, "account": map[string]any{"id": 9, "login": "acme"},
			})
		case "/app/installations/42/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "installation-token"})
		case "/installation/repositories":
			if r.Header.Get("Authorization") != "Bearer installation-token" {
				t.Fatal("repository request did not use installation token")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []map[string]any{{
				"id": 7, "full_name": "acme/payments", "clone_url": server.URL + "/acme/payments.git",
				"html_url": server.URL + "/acme/payments", "default_branch": "main",
				"visibility": "private", "language": "Go",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter, err := NewGitHubAdapter(GitHubConfig{
		AppID: 1, Slug: "repomender-test", PrivateKeyPEM: privateKeyPEM,
		WebhookSecret: "github-secret", APIBaseURL: server.URL, WebBaseURL: server.URL,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	connectURL, _ := adapter.ConnectURL("state-value", "")
	if !strings.Contains(connectURL, "/apps/repomender-test/installations/new") {
		t.Fatalf("connect URL = %s", connectURL)
	}
	connection, credential, err := adapter.Complete(context.Background(), Callback{InstallationID: "42"}, "")
	if err != nil || connection.Name != "acme" || credential.AccessToken != "" {
		t.Fatalf("Complete() = %+v, %+v, %v", connection, credential, err)
	}
	repositories, persisted, err := adapter.Repositories(context.Background(), connection, credential)
	if err != nil || len(repositories) != 1 || repositories[0].FullName != "acme/payments" || persisted.AccessToken != "" {
		t.Fatalf("Repositories() = %+v, %+v, %v", repositories, persisted, err)
	}

	body := []byte(`{"action":"opened","installation":{"id":42},"repository":{"id":7,"full_name":"acme/payments"}}`)
	mac := hmac.New(sha256.New, []byte("github-secret"))
	_, _ = mac.Write(body)
	headers := http.Header{
		"X-Hub-Signature-256": {"sha256=" + fmt.Sprintf("%x", mac.Sum(nil))},
		"X-Github-Delivery":   {"delivery-1"},
		"X-Github-Event":      {"pull_request"},
	}
	event, err := adapter.VerifyWebhook(headers, body)
	if err != nil || event.DeliveryID != "delivery-1" {
		t.Fatalf("VerifyWebhook() = %+v, %v", event, err)
	}
	headers.Set("X-Hub-Signature-256", "sha256=forged")
	if _, err := adapter.VerifyWebhook(headers, body); err == nil {
		t.Fatal("forged GitHub webhook accepted")
	}
}

func TestGitHubAdapterNormalizesFailedWorkflowRunWebhook(t *testing.T) {
	adapter, err := NewGitHubAdapter(GitHubConfig{WebhookSecret: "github-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"action":"completed","installation":{"id":42},"repository":{"id":7,"full_name":"acme/payments","clone_url":"https://github.com/acme/payments.git"},"workflow_run":{"id":99,"name":"CI","html_url":"https://github.com/acme/payments/actions/runs/99","logs_url":"https://api.github.com/repos/acme/payments/actions/runs/99/logs","head_sha":"0123456789abcdef0123456789abcdef01234567","status":"completed","conclusion":"failure"}}`)
	mac := hmac.New(sha256.New, []byte("github-secret"))
	_, _ = mac.Write(body)
	headers := http.Header{}
	headers.Set("X-Hub-Signature-256", "sha256="+fmt.Sprintf("%x", mac.Sum(nil)))
	headers.Set("X-GitHub-Delivery", "delivery-ci-1")
	headers.Set("X-GitHub-Event", "workflow_run")
	event, err := adapter.VerifyWebhook(headers, body)
	if err != nil {
		t.Fatal(err)
	}
	if event.EventType != "workflow_run" || event.Normalized["workflowRunId"] != int64(99) || event.Normalized["conclusion"] != "failure" {
		t.Fatalf("unexpected normalized event: %+v", event)
	}
}

func TestGitHubAdapterDownloadsBoundedWorkflowLogArchive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/payments/actions/runs/99/logs" || r.Header.Get("Authorization") != "Bearer installation-token" {
			t.Fatalf("unexpected log request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var archive bytes.Buffer
		writer := zip.NewWriter(&archive)
		entry, err := writer.Create("job/1.txt")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte("TOKEN=secret\nfailed\n"))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()
	adapter, err := NewGitHubAdapter(GitHubConfig{APIBaseURL: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	logs, err := adapter.DownloadWorkflowLogs(context.Background(), "installation-token", "acme/payments", 99)
	if err != nil || !strings.Contains(logs, "failed") || !strings.Contains(logs, "job/1.txt") {
		t.Fatalf("DownloadWorkflowLogs() = %q, %v", logs, err)
	}
}

func TestGitHubAdapterControlsWorkflowLogProviderFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "permission denied", status: http.StatusForbidden},
		{name: "expired logs", status: http.StatusGone},
		{name: "rate limited", status: http.StatusTooManyRequests},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			adapter, err := NewGitHubAdapter(GitHubConfig{APIBaseURL: server.URL}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.DownloadWorkflowLogs(context.Background(), "installation-token", "acme/payments", 99); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d", test.status)) {
				t.Fatalf("DownloadWorkflowLogs() error = %v, want provider status %d", err, test.status)
			}
		})
	}
}

func TestGitLabAdapterOAuthRepositoriesAndWebhook(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("code_verifier") != "verifier" {
				t.Fatal("GitLab exchange omitted PKCE verifier")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "gitlab-access", "refresh_token": "gitlab-refresh", "expires_in": 7200,
			})
		case "/api/v4/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 12, "username": "acme", "name": "Acme"})
		case "/api/v4/projects":
			if r.Header.Get("Authorization") != "Bearer gitlab-access" {
				t.Fatal("GitLab project request omitted access token")
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": 8, "path_with_namespace": "acme/ledger",
				"http_url_to_repo": server.URL + "/acme/ledger.git",
				"web_url":          server.URL + "/acme/ledger", "default_branch": "main",
				"visibility": "private",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewGitLabAdapter(GitLabConfig{
		ClientID: "client", ClientSecret: "secret", WebhookSecret: "hook-secret",
		RedirectURL: server.URL + "/callback", APIBaseURL: server.URL + "/api/v4",
		WebBaseURL: server.URL,
	}, server.Client())
	connectURL, _ := adapter.ConnectURL("state", "challenge")
	parsed, _ := url.Parse(connectURL)
	if parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("connect URL = %s", connectURL)
	}
	connection, credential, err := adapter.Complete(context.Background(), Callback{Code: "code"}, "verifier")
	if err != nil || connection.ExternalAccountID != "12" || credential.AccessToken != "gitlab-access" {
		t.Fatalf("Complete() = %+v, %+v, %v", connection, credential, err)
	}
	repositories, _, err := adapter.Repositories(context.Background(), connection, credential)
	if err != nil || len(repositories) != 1 || repositories[0].FullName != "acme/ledger" {
		t.Fatalf("Repositories() = %+v, %v", repositories, err)
	}

	body := []byte(`{"object_kind":"merge_request","project":{"id":8,"path_with_namespace":"acme/ledger"},"object_attributes":{"action":"open"}}`)
	headers := http.Header{
		"X-Gitlab-Token":      {"hook-secret"},
		"X-Gitlab-Event-Uuid": {"delivery-2"},
		"X-Gitlab-Event":      {"Merge Request Hook"},
	}
	event, err := adapter.VerifyWebhook(headers, body)
	if err != nil || event.DeliveryID != "delivery-2" {
		t.Fatalf("VerifyWebhook() = %+v, %v", event, err)
	}
	headers.Set("X-Gitlab-Token", "forged")
	if _, err := adapter.VerifyWebhook(headers, body); err == nil {
		t.Fatal("forged GitLab webhook accepted")
	}
}

func mustPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	value, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
