package scm

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
)

// auth.User supplies the authenticated actor and role. The service owns provider
// orchestration while Store and Adapter hide persistence and vendor protocols.

var (
	ErrProviderUnavailable = errors.New("SCM provider unavailable")
	ErrInvalidFlow         = errors.New("invalid or expired SCM connection flow")
	ErrForbidden           = errors.New("SCM action forbidden")
	ErrRepositoryNotFound  = errors.New("repository not found")
	ErrInvalidWebhook      = errors.New("invalid SCM webhook")
)

type Store interface {
	CreateFlow(context.Context, []byte, Flow, time.Time) error
	ConsumeFlow(context.Context, []byte, time.Time) (Flow, error)
	UpsertConnection(context.Context, string, RemoteConnection, []byte, []RemoteRepository, time.Time) (Connection, error)
	GetConnection(context.Context, string) (StoredConnection, error)
	ListConnections(context.Context) ([]Connection, error)
	ListRepositories(context.Context) ([]Repository, error)
	GetRepository(context.Context, string) (Repository, error)
	RecordWebhook(context.Context, Provider, WebhookEvent, []byte, time.Time) (bool, error)
}

type Service struct {
	store    Store
	box      *SecretBox
	adapters map[Provider]Adapter
	now      func() time.Time
}

func NewService(store Store, box *SecretBox, adapters ...Adapter) *Service {
	registry := make(map[Provider]Adapter, len(adapters))
	for _, adapter := range adapters {
		if adapter != nil {
			registry[adapter.Provider()] = adapter
		}
	}
	return &Service{store: store, box: box, adapters: registry, now: time.Now}
}

func (s *Service) Providers() map[Provider]bool {
	result := map[Provider]bool{ProviderGitHub: false, ProviderGitLab: false}
	for provider, adapter := range s.adapters {
		result[provider] = adapter.Available()
	}
	return result
}

func (s *Service) BeginConnect(ctx context.Context, actor auth.User, provider Provider, returnTo string) (string, error) {
	if actor.Role != auth.RoleAdmin {
		return "", ErrForbidden
	}
	adapter, ok := s.adapters[provider]
	if !ok || !adapter.Available() {
		return "", ErrProviderUnavailable
	}
	if !safeReturnTo(returnTo) {
		returnTo = "/repositories"
	}
	state, err := randomToken(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomToken(48)
	if err != nil {
		return "", err
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	if err := s.store.CreateFlow(ctx, digest(state), Flow{
		Provider: provider, UserID: actor.ID, Verifier: verifier, ReturnTo: returnTo,
	}, s.now().UTC().Add(10*time.Minute)); err != nil {
		return "", err
	}
	return adapter.ConnectURL(state, challenge)
}

func (s *Service) CompleteConnect(ctx context.Context, provider Provider, state string, callback Callback) (Connection, string, error) {
	if state == "" {
		return Connection{}, "", ErrInvalidFlow
	}
	flow, err := s.store.ConsumeFlow(ctx, digest(state), s.now().UTC())
	if err != nil || flow.Provider != provider {
		return Connection{}, "", ErrInvalidFlow
	}
	adapter, ok := s.adapters[provider]
	if !ok || !adapter.Available() {
		return Connection{}, "", ErrProviderUnavailable
	}

	// Complete the provider handshake before persistence, then obtain a full
	// repository snapshot so a connection never appears active but empty due
	// to a partially completed callback.
	remote, credential, err := adapter.Complete(ctx, callback, flow.Verifier)
	if err != nil {
		return Connection{}, "", err
	}
	repositories, credential, err := adapter.Repositories(ctx, remote, credential)
	if err != nil {
		return Connection{}, "", err
	}
	ciphertext, err := s.sealCredential(credential)
	if err != nil {
		return Connection{}, "", err
	}
	connection, err := s.store.UpsertConnection(ctx, flow.UserID, remote, ciphertext, repositories, s.now().UTC())
	return connection, flow.ReturnTo, err
}

func (s *Service) Sync(ctx context.Context, actor auth.User, connectionID string) (Connection, error) {
	if actor.Role != auth.RoleAdmin && actor.Role != auth.RoleMaintainer {
		return Connection{}, ErrForbidden
	}
	stored, err := s.store.GetConnection(ctx, connectionID)
	if err != nil {
		return Connection{}, err
	}
	adapter, ok := s.adapters[stored.Provider]
	if !ok || !adapter.Available() {
		return Connection{}, ErrProviderUnavailable
	}
	credential, err := s.openCredential(stored.CredentialCiphertext)
	if err != nil {
		return Connection{}, err
	}
	remote := RemoteConnection{
		Provider: stored.Provider, Name: stored.Name,
		ExternalAccountID: stored.ExternalAccountID, BaseURL: stored.BaseURL,
	}
	repositories, credential, err := adapter.Repositories(ctx, remote, credential)
	if err != nil {
		return Connection{}, err
	}
	ciphertext, err := s.sealCredential(credential)
	if err != nil {
		return Connection{}, err
	}
	return s.store.UpsertConnection(ctx, actor.ID, remote, ciphertext, repositories, s.now().UTC())
}

func (s *Service) ListConnections(ctx context.Context, _ auth.User) ([]Connection, error) {
	return s.store.ListConnections(ctx)
}

func (s *Service) ListRepositories(ctx context.Context, _ auth.User) ([]Repository, error) {
	return s.store.ListRepositories(ctx)
}

func (s *Service) GetRepository(ctx context.Context, _ auth.User, id string) (Repository, error) {
	repository, err := s.store.GetRepository(ctx, id)
	if err != nil {
		return Repository{}, ErrRepositoryNotFound
	}
	return repository, nil
}

func (s *Service) HandleWebhook(ctx context.Context, provider Provider, headers http.Header, body []byte) (bool, error) {
	adapter, ok := s.adapters[provider]
	if !ok || !adapter.Available() {
		return false, ErrProviderUnavailable
	}
	event, err := adapter.VerifyWebhook(headers, body)
	if err != nil {
		return false, err
	}
	payloadHash := sha256.Sum256(body)
	return s.store.RecordWebhook(ctx, provider, event, payloadHash[:], s.now().UTC())
}

func (s *Service) sealCredential(credential Credential) ([]byte, error) {
	if credential.AccessToken == "" && credential.RefreshToken == "" {
		return nil, nil
	}
	plaintext, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	return s.box.Seal(plaintext)
}

func (s *Service) openCredential(ciphertext []byte) (Credential, error) {
	if len(ciphertext) == 0 {
		return Credential{}, nil
	}
	plaintext, err := s.box.Open(ciphertext)
	if err != nil {
		return Credential{}, err
	}
	var credential Credential
	err = json.Unmarshal(plaintext, &credential)
	return credential, err
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func digest(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func safeReturnTo(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "\r\n")
}
