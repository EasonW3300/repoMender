package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
	"github.com/EasonW3300/repoMender/server/internal/scm"
)

// Real auth and SCM services are wired to in-memory boundaries so HTTP tests
// exercise session, CSRF, RBAC, callback, and webhook status mappings together.

type httpSCMStore struct {
	flows      map[string]scm.Flow
	connection scm.StoredConnection
	repository scm.Repository
	deliveries map[string]bool
}

func newHTTPSCMStore() *httpSCMStore {
	return &httpSCMStore{flows: map[string]scm.Flow{}, deliveries: map[string]bool{}}
}

func (s *httpSCMStore) CreateFlow(_ context.Context, hash []byte, flow scm.Flow, _ time.Time) error {
	s.flows[string(hash)] = flow
	return nil
}
func (s *httpSCMStore) ConsumeFlow(_ context.Context, hash []byte, _ time.Time) (scm.Flow, error) {
	flow, ok := s.flows[string(hash)]
	delete(s.flows, string(hash))
	if !ok {
		return scm.Flow{}, errors.New("not found")
	}
	return flow, nil
}
func (s *httpSCMStore) UpsertConnection(
	_ context.Context, _ string, remote scm.RemoteConnection, ciphertext []byte,
	repositories []scm.RemoteRepository, syncedAt time.Time,
) (scm.Connection, error) {
	connection := scm.Connection{
		ID: "connection-1", Provider: remote.Provider, Name: remote.Name,
		ExternalAccountID: remote.ExternalAccountID, BaseURL: remote.BaseURL,
		Status: "active", LastSyncedAt: &syncedAt, CreatedAt: syncedAt,
	}
	s.connection = scm.StoredConnection{Connection: connection, CredentialCiphertext: ciphertext}
	if len(repositories) != 0 {
		s.repository = scm.Repository{
			ID: "repository-1", ConnectionID: connection.ID, Provider: remote.Provider,
			ProviderRepositoryID: repositories[0].ProviderRepositoryID,
			FullName:             repositories[0].FullName, WebURL: repositories[0].WebURL,
			CloneURL: repositories[0].CloneURL, DefaultBranch: repositories[0].DefaultBranch,
			Visibility: "private", Enabled: true, LastSyncedAt: syncedAt,
		}
	}
	return connection, nil
}
func (s *httpSCMStore) GetConnection(context.Context, string) (scm.StoredConnection, error) {
	if s.connection.ID == "" {
		return scm.StoredConnection{}, errors.New("not found")
	}
	return s.connection, nil
}
func (s *httpSCMStore) ListConnections(context.Context) ([]scm.Connection, error) {
	if s.connection.ID == "" {
		return []scm.Connection{}, nil
	}
	return []scm.Connection{s.connection.Connection}, nil
}
func (s *httpSCMStore) ListRepositories(context.Context) ([]scm.Repository, error) {
	if s.repository.ID == "" {
		return []scm.Repository{}, nil
	}
	return []scm.Repository{s.repository}, nil
}
func (s *httpSCMStore) GetRepository(context.Context, string) (scm.Repository, error) {
	if s.repository.ID == "" {
		return scm.Repository{}, errors.New("not found")
	}
	return s.repository, nil
}
func (s *httpSCMStore) RecordWebhook(
	_ context.Context, provider scm.Provider, event scm.WebhookEvent, _ []byte, _ time.Time,
) (bool, error) {
	key := string(provider) + event.DeliveryID
	if s.deliveries[key] {
		return false, nil
	}
	s.deliveries[key] = true
	return true, nil
}

type httpSCMAdapter struct {
	state string
}

func (a *httpSCMAdapter) Provider() scm.Provider { return scm.ProviderGitLab }
func (a *httpSCMAdapter) Available() bool        { return true }
func (a *httpSCMAdapter) ConnectURL(state, challenge string) (string, error) {
	a.state = state
	return "https://gitlab.example/oauth?state=" + url.QueryEscape(state) + "&challenge=" + url.QueryEscape(challenge), nil
}
func (a *httpSCMAdapter) Complete(context.Context, scm.Callback, string) (scm.RemoteConnection, scm.Credential, error) {
	return scm.RemoteConnection{
		Provider: scm.ProviderGitLab, Name: "acme", ExternalAccountID: "12",
		BaseURL: "https://gitlab.example",
	}, scm.Credential{AccessToken: "token"}, nil
}
func (a *httpSCMAdapter) Repositories(
	context.Context, scm.RemoteConnection, scm.Credential,
) ([]scm.RemoteRepository, scm.Credential, error) {
	return []scm.RemoteRepository{{
		ProviderRepositoryID: "8", FullName: "acme/ledger",
		WebURL:   "https://gitlab.example/acme/ledger",
		CloneURL: "https://gitlab.example/acme/ledger.git", DefaultBranch: "main",
	}}, scm.Credential{AccessToken: "token"}, nil
}
func (a *httpSCMAdapter) VerifyWebhook(headers http.Header, _ []byte) (scm.WebhookEvent, error) {
	if headers.Get("X-Test-Token") != "valid" {
		return scm.WebhookEvent{}, scm.ErrInvalidWebhook
	}
	return scm.WebhookEvent{DeliveryID: "delivery-1", EventType: "push"}, nil
}

func TestSCMHTTPConnectionRepositorySyncAndWebhookLifecycle(t *testing.T) {
	authService := auth.NewService(newAuthMemoryStore(), time.Hour)
	_, _ = authService.Bootstrap(context.Background(), "admin@example.com", "correct-horse-battery")
	_, sessionToken, csrfToken, err := authService.LoginLocal(context.Background(), "admin@example.com", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	box, _ := scm.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	store := newHTTPSCMStore()
	adapter := &httpSCMAdapter{}
	scmService := scm.NewService(store, box, adapter)
	handler := NewWithServices(fakeChecker{}, authService, nil, scmService, false).Handler()
	cookies := []*http.Cookie{{Name: sessionCookie, Value: sessionToken}}

	start := requestJSON(t, handler, http.MethodGet, "/api/v1/scm/gitlab/connect", nil, cookies, "")
	if start.Code != http.StatusFound || adapter.state == "" {
		t.Fatalf("connect status = %d", start.Code)
	}
	callback := requestJSON(t, handler, http.MethodGet,
		"/api/v1/scm/gitlab/callback?state="+url.QueryEscape(adapter.state)+"&code=test", nil, nil, "")
	if callback.Code != http.StatusFound || !strings.Contains(callback.Header().Get("Location"), "connected=gitlab") {
		t.Fatalf("callback status = %d location=%q body=%s", callback.Code, callback.Header().Get("Location"), callback.Body.String())
	}
	repositories := requestJSON(t, handler, http.MethodGet, "/api/v1/repositories", nil, cookies, "")
	if repositories.Code != http.StatusOK || !strings.Contains(repositories.Body.String(), "acme/ledger") {
		t.Fatalf("repositories status = %d body=%s", repositories.Code, repositories.Body.String())
	}
	rejectedSync := requestJSON(t, handler, http.MethodPost,
		"/api/v1/scm/connections/connection-1/sync", nil, cookies, "")
	if rejectedSync.Code != http.StatusForbidden {
		t.Fatalf("sync without CSRF status = %d", rejectedSync.Code)
	}
	sync := requestJSON(t, handler, http.MethodPost,
		"/api/v1/scm/connections/connection-1/sync", nil, cookies, csrfToken)
	if sync.Code != http.StatusOK {
		t.Fatalf("sync status = %d body=%s", sync.Code, sync.Body.String())
	}

	forged := requestJSON(t, handler, http.MethodPost, "/webhooks/gitlab", map[string]bool{"ok": true}, nil, "")
	if forged.Code != http.StatusUnauthorized {
		t.Fatalf("forged webhook status = %d", forged.Code)
	}
	validRequest := requestJSONWithHeaders(t, handler, "/webhooks/gitlab", http.Header{"X-Test-Token": {"valid"}})
	if validRequest.Code != http.StatusAccepted || !strings.Contains(validRequest.Body.String(), "accepted") {
		t.Fatalf("valid webhook status = %d body=%s", validRequest.Code, validRequest.Body.String())
	}
	duplicate := requestJSONWithHeaders(t, handler, "/webhooks/gitlab", http.Header{"X-Test-Token": {"valid"}})
	if duplicate.Code != http.StatusAccepted || !strings.Contains(duplicate.Body.String(), "duplicate") {
		t.Fatalf("duplicate webhook status = %d body=%s", duplicate.Code, duplicate.Body.String())
	}
}

func requestJSONWithHeaders(t *testing.T, handler http.Handler, path string, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"ok":true}`))
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
