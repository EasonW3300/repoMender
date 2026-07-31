package scm

import (
	"context"
	"net/http"
	"time"
)

// Adapter is the provider boundary used by business logic. GitHub and GitLab
// HTTP details stay behind this contract so later providers do not leak into
// repository, webhook, or persistence code.

type Provider string

const (
	ProviderGitHub Provider = "github"
	ProviderGitLab Provider = "gitlab"
)

type Connection struct {
	ID                string     `json:"id"`
	Provider          Provider   `json:"provider"`
	Name              string     `json:"name"`
	ExternalAccountID string     `json:"externalAccountId"`
	BaseURL           string     `json:"baseUrl"`
	Status            string     `json:"status"`
	LastSyncedAt      *time.Time `json:"lastSyncedAt"`
	CreatedAt         time.Time  `json:"createdAt"`
}

type StoredConnection struct {
	Connection
	CredentialCiphertext []byte
}

type Repository struct {
	ID                   string         `json:"id"`
	ConnectionID         string         `json:"connectionId"`
	Provider             Provider       `json:"provider"`
	ProviderRepositoryID string         `json:"providerRepositoryId"`
	FullName             string         `json:"fullName"`
	CloneURL             string         `json:"cloneUrl"`
	WebURL               string         `json:"webUrl"`
	DefaultBranch        string         `json:"defaultBranch"`
	Visibility           string         `json:"visibility"`
	Archived             bool           `json:"archived"`
	Enabled              bool           `json:"enabled"`
	Metadata             map[string]any `json:"metadata"`
	LastSyncedAt         time.Time      `json:"lastSyncedAt"`
}

type RemoteConnection struct {
	Provider          Provider
	Name              string
	ExternalAccountID string
	BaseURL           string
}

type RemoteRepository struct {
	ProviderRepositoryID string
	FullName             string
	CloneURL             string
	WebURL               string
	DefaultBranch        string
	Visibility           string
	Archived             bool
	Metadata             map[string]any
}

type Credential struct {
	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
}

type Callback struct {
	Code           string
	InstallationID string
}

type Flow struct {
	Provider Provider
	UserID   string
	Verifier string
	ReturnTo string
}

type WebhookEvent struct {
	DeliveryID string
	EventType  string
	Normalized map[string]any
}

type Adapter interface {
	Provider() Provider
	Available() bool
	ConnectURL(state, challenge string) (string, error)
	Complete(context.Context, Callback, string) (RemoteConnection, Credential, error)
	Repositories(context.Context, RemoteConnection, Credential) ([]RemoteRepository, Credential, error)
	VerifyWebhook(http.Header, []byte) (WebhookEvent, error)
}
