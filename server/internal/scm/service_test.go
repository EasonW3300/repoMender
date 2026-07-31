package scm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
)

type memorySCMStore struct {
	mu           sync.Mutex
	flows        map[string]Flow
	connections  map[string]StoredConnection
	repositories map[string]Repository
	deliveries   map[string]bool
	lastCipher   []byte
}

func newMemorySCMStore() *memorySCMStore {
	return &memorySCMStore{
		flows: map[string]Flow{}, connections: map[string]StoredConnection{},
		repositories: map[string]Repository{}, deliveries: map[string]bool{},
	}
}

func (m *memorySCMStore) CreateFlow(_ context.Context, state []byte, flow Flow, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flows[string(state)] = flow
	return nil
}

func (m *memorySCMStore) ConsumeFlow(_ context.Context, state []byte, _ time.Time) (Flow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	flow, ok := m.flows[string(state)]
	delete(m.flows, string(state))
	if !ok {
		return Flow{}, errors.New("not found")
	}
	return flow, nil
}

func (m *memorySCMStore) UpsertConnection(
	_ context.Context, _ string, remote RemoteConnection, ciphertext []byte,
	remotes []RemoteRepository, syncedAt time.Time,
) (Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := "connection-1"
	connection := Connection{
		ID: id, Provider: remote.Provider, Name: remote.Name,
		ExternalAccountID: remote.ExternalAccountID, BaseURL: remote.BaseURL,
		Status: "active", LastSyncedAt: &syncedAt, CreatedAt: syncedAt,
	}
	m.lastCipher = append([]byte(nil), ciphertext...)
	m.connections[id] = StoredConnection{Connection: connection, CredentialCiphertext: m.lastCipher}
	for _, remoteRepository := range remotes {
		repository := Repository{
			ID: "repository-1", ConnectionID: id, Provider: remote.Provider,
			ProviderRepositoryID: remoteRepository.ProviderRepositoryID,
			FullName:             remoteRepository.FullName, CloneURL: remoteRepository.CloneURL,
			WebURL: remoteRepository.WebURL, DefaultBranch: remoteRepository.DefaultBranch,
			Visibility: remoteRepository.Visibility, Enabled: true, LastSyncedAt: syncedAt,
		}
		m.repositories[repository.ID] = repository
	}
	return connection, nil
}

func (m *memorySCMStore) GetConnection(_ context.Context, id string) (StoredConnection, error) {
	connection, ok := m.connections[id]
	if !ok {
		return StoredConnection{}, errors.New("not found")
	}
	return connection, nil
}

func (m *memorySCMStore) ListConnections(context.Context) ([]Connection, error) {
	result := make([]Connection, 0, len(m.connections))
	for _, connection := range m.connections {
		result = append(result, connection.Connection)
	}
	return result, nil
}

func (m *memorySCMStore) ListRepositories(context.Context) ([]Repository, error) {
	result := make([]Repository, 0, len(m.repositories))
	for _, repository := range m.repositories {
		result = append(result, repository)
	}
	return result, nil
}

func (m *memorySCMStore) GetRepository(_ context.Context, id string) (Repository, error) {
	repository, ok := m.repositories[id]
	if !ok {
		return Repository{}, errors.New("not found")
	}
	return repository, nil
}

func (m *memorySCMStore) RecordWebhook(
	_ context.Context, provider Provider, event WebhookEvent, _ []byte, _ time.Time,
) (bool, error) {
	key := string(provider) + ":" + event.DeliveryID
	if m.deliveries[key] {
		return false, nil
	}
	m.deliveries[key] = true
	return true, nil
}

type fakeAdapter struct {
	provider  Provider
	available bool
	lastState string
	challenge string
}

func (f *fakeAdapter) Provider() Provider { return f.provider }
func (f *fakeAdapter) Available() bool    { return f.available }
func (f *fakeAdapter) ConnectURL(state, challenge string) (string, error) {
	f.lastState, f.challenge = state, challenge
	return "https://scm.example/connect?state=" + state, nil
}
func (f *fakeAdapter) Complete(context.Context, Callback, string) (RemoteConnection, Credential, error) {
	return RemoteConnection{
		Provider: f.provider, Name: "Acme", ExternalAccountID: "42",
		BaseURL: "https://scm.example",
	}, Credential{AccessToken: "access-secret", RefreshToken: "refresh-secret"}, nil
}
func (f *fakeAdapter) Repositories(
	context.Context, RemoteConnection, Credential,
) ([]RemoteRepository, Credential, error) {
	return []RemoteRepository{{
		ProviderRepositoryID: "7", FullName: "acme/payments",
		CloneURL: "https://scm.example/acme/payments.git",
		WebURL:   "https://scm.example/acme/payments", DefaultBranch: "main",
		Visibility: "private",
	}}, Credential{AccessToken: "access-secret", RefreshToken: "refresh-secret"}, nil
}
func (f *fakeAdapter) VerifyWebhook(headers http.Header, _ []byte) (WebhookEvent, error) {
	if headers.Get("X-Test-Signature") != "valid" {
		return WebhookEvent{}, ErrInvalidWebhook
	}
	return WebhookEvent{DeliveryID: "delivery-1", EventType: "push", Normalized: map[string]any{"repository": "acme/payments"}}, nil
}

func TestConnectionFlowEncryptsCredentialAndPersistsRepository(t *testing.T) {
	store := newMemorySCMStore()
	box, err := NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{provider: ProviderGitLab, available: true}
	service := NewService(store, box, adapter)
	admin := auth.User{ID: "admin-1", Role: auth.RoleAdmin}

	location, err := service.BeginConnect(context.Background(), admin, ProviderGitLab, "//evil.example")
	if err != nil || !strings.Contains(location, "state=") || adapter.challenge == "" {
		t.Fatalf("BeginConnect() = %q, %v", location, err)
	}
	connection, returnTo, err := service.CompleteConnect(context.Background(), ProviderGitLab, adapter.lastState, Callback{Code: "code"})
	if err != nil || connection.ID == "" || returnTo != "/repositories" {
		t.Fatalf("CompleteConnect() = %+v, %q, %v", connection, returnTo, err)
	}
	if strings.Contains(string(store.lastCipher), "access-secret") || len(store.lastCipher) == 0 {
		t.Fatal("SCM credential was not encrypted before persistence")
	}
	repositories, _ := service.ListRepositories(context.Background(), admin)
	if len(repositories) != 1 || repositories[0].FullName != "acme/payments" {
		t.Fatalf("repositories = %+v", repositories)
	}
	if _, _, err := service.CompleteConnect(context.Background(), ProviderGitLab, adapter.lastState, Callback{}); !errors.Is(err, ErrInvalidFlow) {
		t.Fatalf("reused state error = %v", err)
	}
}

func TestSCMRBACAndWebhookDeduplication(t *testing.T) {
	store := newMemorySCMStore()
	box, _ := NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	adapter := &fakeAdapter{provider: ProviderGitHub, available: true}
	service := NewService(store, box, adapter)
	developer := auth.User{ID: "developer-1", Role: auth.RoleDeveloper}
	if _, err := service.BeginConnect(context.Background(), developer, ProviderGitHub, "/"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("developer BeginConnect() error = %v", err)
	}
	if _, err := service.Sync(context.Background(), developer, "connection-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("developer Sync() error = %v", err)
	}

	headers := http.Header{"X-Test-Signature": {"valid"}}
	created, err := service.HandleWebhook(context.Background(), ProviderGitHub, headers, []byte(`{"ok":true}`))
	if err != nil || !created {
		t.Fatalf("first HandleWebhook() = %v, %v", created, err)
	}
	created, err = service.HandleWebhook(context.Background(), ProviderGitHub, headers, []byte(`{"ok":true}`))
	if err != nil || created {
		t.Fatalf("duplicate HandleWebhook() = %v, %v", created, err)
	}
	headers.Set("X-Test-Signature", "forged")
	if _, err := service.HandleWebhook(context.Background(), ProviderGitHub, headers, nil); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("forged webhook error = %v", err)
	}
}

func TestSecretBoxRejectsWrongKeyAndTampering(t *testing.T) {
	box, _ := NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	other, _ := NewSecretBox([]byte("abcdef0123456789abcdef0123456789"))
	ciphertext, err := box.Seal([]byte("credential"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(ciphertext); err == nil {
		t.Fatal("wrong master key decrypted credential")
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := box.Open(ciphertext); err == nil {
		t.Fatal("tampered credential decrypted")
	}
}
