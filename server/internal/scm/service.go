package scm

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

// FindRepository resolves a provider repository ID from the last synchronized
// snapshot. Webhook handlers use this to attach tasks to our internal UUID
// without trusting a vendor-owned identifier as a database foreign key.
func (s *Service) FindRepository(ctx context.Context, provider Provider, providerRepositoryID string) (Repository, error) {
	repositories, err := s.store.ListRepositories(ctx)
	if err != nil {
		return Repository{}, err
	}
	for _, repository := range repositories {
		if repository.Provider == provider && repository.ProviderRepositoryID == providerRepositoryID {
			return repository, nil
		}
	}
	return Repository{}, ErrRepositoryNotFound
}

func (s *Service) HandleWebhook(ctx context.Context, provider Provider, headers http.Header, body []byte) (bool, error) {
	_, created, err := s.HandleWebhookEvent(ctx, provider, headers, body)
	return created, err
}

func (s *Service) HandleWebhookEvent(ctx context.Context, provider Provider, headers http.Header, body []byte) (WebhookEvent, bool, error) {
	event, err := s.VerifyWebhook(provider, headers, body)
	if err != nil {
		return WebhookEvent{}, false, err
	}
	created, err := s.RecordWebhook(ctx, provider, event, body)
	return event, created, err
}

func (s *Service) VerifyWebhook(provider Provider, headers http.Header, body []byte) (WebhookEvent, error) {
	adapter, ok := s.adapters[provider]
	if !ok || !adapter.Available() {
		return WebhookEvent{}, ErrProviderUnavailable
	}
	return adapter.VerifyWebhook(headers, body)
}

func (s *Service) RecordWebhook(ctx context.Context, provider Provider, event WebhookEvent, body []byte) (bool, error) {
	payloadHash := sha256.Sum256(body)
	created, err := s.store.RecordWebhook(ctx, provider, event, payloadHash[:], s.now().UTC())
	return created, err
}

// PublishGitHubReview writes the review result to GitHub using a fresh
// installation token. The raw result is intentionally bounded and parsed here
// so provider output cannot smuggle arbitrary API fields into the publication.
func (s *Service) PublishGitHubReview(ctx context.Context, repository, installationID string, number int, sha string, result json.RawMessage) error {
	adapter, ok := s.adapters[ProviderGitHub]
	if !ok || !adapter.Available() {
		return ErrProviderUnavailable
	}
	github, ok := adapter.(*GitHubAdapter)
	if !ok {
		return errors.New("GitHub adapter does not support review publication")
	}
	var output struct {
		Summary  string `json:"summary"`
		Findings []struct {
			Severity string `json:"severity"`
			Path     string `json:"path"`
			Line     *int   `json:"lineStart"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(result, &output); err != nil || strings.TrimSpace(output.Summary) == "" {
		return ErrInvalidWebhook
	}
	if !validGitHubRepository(repository) || len(output.Findings) > 200 {
		return ErrInvalidWebhook
	}
	token, err := github.InstallationToken(ctx, installationID)
	if err != nil {
		return err
	}
	if err := github.CreateCheckRun(ctx, token, repository, sha, output.Summary, len(output.Findings)); err != nil {
		return err
	}
	var comment strings.Builder
	comment.WriteString("## RepoMender code review\n\n")
	comment.WriteString(output.Summary)
	comment.WriteString("\n\n")
	if len(output.Findings) == 0 {
		comment.WriteString("No actionable findings.")
	} else {
		for _, finding := range output.Findings {
			comment.WriteString("- **" + finding.Severity + "** `" + finding.Path + "`")
			if finding.Line != nil {
				comment.WriteString(fmt.Sprintf(":%d", *finding.Line))
			}
			comment.WriteString("\n")
		}
	}
	return github.CreateIssueComment(ctx, token, repository, number, comment.String())
}

// FetchGitHubWorkflowLogs obtains a short-lived installation token only for
// the current worker request; the token and provider archive never enter the
// durable task payload or database.
func (s *Service) FetchGitHubWorkflowLogs(ctx context.Context, repository, installationID string, runID int64) (string, error) {
	adapter, ok := s.adapters[ProviderGitHub]
	if !ok || !adapter.Available() {
		return "", ErrProviderUnavailable
	}
	github, ok := adapter.(*GitHubAdapter)
	if !ok {
		return "", errors.New("GitHub adapter does not support workflow logs")
	}
	token, err := github.InstallationToken(ctx, installationID)
	if err != nil {
		return "", err
	}
	return github.DownloadWorkflowLogs(ctx, token, repository, runID)
}

// PublishGitHubDiagnosis publishes one concise, exact-commit check result.
// Full redacted evidence remains in RepoMender rather than being copied into
// an unbounded provider comment.
func (s *Service) PublishGitHubDiagnosis(ctx context.Context, repository, installationID, sha, summary string) error {
	adapter, ok := s.adapters[ProviderGitHub]
	if !ok || !adapter.Available() {
		return ErrProviderUnavailable
	}
	github, ok := adapter.(*GitHubAdapter)
	if !ok {
		return errors.New("GitHub adapter does not support diagnosis publication")
	}
	if !validGitHubRepository(repository) || len(summary) == 0 || len(summary) > 20000 {
		return ErrInvalidWebhook
	}
	token, err := github.InstallationToken(ctx, installationID)
	if err != nil {
		return err
	}
	return github.CreateDiagnosisCheckRun(ctx, token, repository, sha, summary)
}

// ResolveGitHubIssue and ResolveGitHubBaseSHA keep short-lived installation
// credentials inside the SCM service while M8 receives only provider-neutral
// issue and immutable-commit data.
func (s *Service) ResolveGitHubIssue(ctx context.Context, repository, installationID string, number int) (Issue, error) {
	github, token, err := s.githubToken(ctx, installationID)
	if err != nil {
		return Issue{}, err
	}
	return github.GetIssue(ctx, token, repository, number)
}

func (s *Service) ResolveGitHubBaseSHA(ctx context.Context, repository, installationID, branch string) (string, error) {
	github, token, err := s.githubToken(ctx, installationID)
	if err != nil {
		return "", err
	}
	return github.ResolveDefaultBranchSHA(ctx, token, repository, branch)
}

func (s *Service) PublishGitHubDraft(ctx context.Context, input DraftPublication) (DraftPullRequest, error) {
	github, token, err := s.githubToken(ctx, input.InstallationID)
	if err != nil {
		return DraftPullRequest{}, err
	}
	return github.PublishDraft(ctx, token, input)
}

func (s *Service) githubToken(ctx context.Context, installationID string) (*GitHubAdapter, string, error) {
	adapter, ok := s.adapters[ProviderGitHub]
	if !ok || !adapter.Available() {
		return nil, "", ErrProviderUnavailable
	}
	github, ok := adapter.(*GitHubAdapter)
	if !ok {
		return nil, "", ErrProviderUnavailable
	}
	token, err := github.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, "", err
	}
	return github, token, nil
}

func validGitHubRepository(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		if strings.ContainsAny(part, "\\?#%\"' ") || part == "." || part == ".." {
			return false
		}
	}
	return true
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
