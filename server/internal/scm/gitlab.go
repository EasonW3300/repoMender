package scm

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitLab OAuth and REST calls use a bounded standard HTTP client. PKCE protects
// the authorization code while refresh tokens stay inside encrypted storage.

type GitLabConfig struct {
	ClientID      string
	ClientSecret  string
	WebhookSecret string
	RedirectURL   string
	APIBaseURL    string
	WebBaseURL    string
}

type GitLabAdapter struct {
	config GitLabConfig
	client *http.Client
	now    func() time.Time
}

func NewGitLabAdapter(config GitLabConfig, client *http.Client) *GitLabAdapter {
	if config.APIBaseURL == "" {
		config.APIBaseURL = "https://gitlab.com/api/v4"
	}
	if config.WebBaseURL == "" {
		config.WebBaseURL = "https://gitlab.com"
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &GitLabAdapter{config: config, client: client, now: time.Now}
}

func (a *GitLabAdapter) Provider() Provider { return ProviderGitLab }

func (a *GitLabAdapter) Available() bool {
	return a.config.ClientID != "" && a.config.ClientSecret != "" &&
		a.config.WebhookSecret != "" && a.config.RedirectURL != ""
}

func (a *GitLabAdapter) ConnectURL(state, challenge string) (string, error) {
	if !a.Available() {
		return "", ErrProviderUnavailable
	}
	query := url.Values{
		"client_id":             {a.config.ClientID},
		"redirect_uri":          {a.config.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {"api read_user"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return strings.TrimRight(a.config.WebBaseURL, "/") + "/oauth/authorize?" + query.Encode(), nil
}

func (a *GitLabAdapter) Complete(ctx context.Context, callback Callback, verifier string) (RemoteConnection, Credential, error) {
	if callback.Code == "" {
		return RemoteConnection{}, Credential{}, errors.New("GitLab authorization code missing")
	}
	credential, err := a.exchange(ctx, url.Values{
		"client_id":     {a.config.ClientID},
		"client_secret": {a.config.ClientSecret},
		"code":          {callback.Code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {a.config.RedirectURL},
		"code_verifier": {verifier},
	})
	if err != nil {
		return RemoteConnection{}, Credential{}, err
	}
	var user struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if err := a.apiJSON(ctx, "/user", credential.AccessToken, &user); err != nil {
		return RemoteConnection{}, Credential{}, err
	}
	if user.ID <= 0 || user.Username == "" {
		return RemoteConnection{}, Credential{}, errors.New("invalid GitLab user response")
	}
	return RemoteConnection{
		Provider: ProviderGitLab, Name: user.Username,
		ExternalAccountID: strconv.FormatInt(user.ID, 10),
		BaseURL:           strings.TrimRight(a.config.WebBaseURL, "/"),
	}, credential, nil
}

func (a *GitLabAdapter) Repositories(ctx context.Context, _ RemoteConnection, credential Credential) ([]RemoteRepository, Credential, error) {
	if credential.RefreshToken != "" && !credential.ExpiresAt.IsZero() &&
		!credential.ExpiresAt.After(a.now().UTC().Add(time.Minute)) {
		refreshed, err := a.exchange(ctx, url.Values{
			"client_id":     {a.config.ClientID},
			"client_secret": {a.config.ClientSecret},
			"refresh_token": {credential.RefreshToken},
			"grant_type":    {"refresh_token"},
			"redirect_uri":  {a.config.RedirectURL},
		})
		if err != nil {
			return nil, Credential{}, err
		}
		credential = refreshed
	}
	if credential.AccessToken == "" {
		return nil, Credential{}, errors.New("GitLab access token missing")
	}

	repositories := make([]RemoteRepository, 0)
	for page := 1; ; page++ {
		var projects []struct {
			ID                int64          `json:"id"`
			PathWithNamespace string         `json:"path_with_namespace"`
			HTTPURLToRepo     string         `json:"http_url_to_repo"`
			WebURL            string         `json:"web_url"`
			DefaultBranch     string         `json:"default_branch"`
			Visibility        string         `json:"visibility"`
			Archived          bool           `json:"archived"`
			Namespace         map[string]any `json:"namespace"`
		}
		path := fmt.Sprintf("/projects?membership=true&simple=true&per_page=100&page=%d", page)
		if err := a.apiJSON(ctx, path, credential.AccessToken, &projects); err != nil {
			return nil, Credential{}, err
		}
		for _, project := range projects {
			repositories = append(repositories, RemoteRepository{
				ProviderRepositoryID: strconv.FormatInt(project.ID, 10),
				FullName:             project.PathWithNamespace, CloneURL: project.HTTPURLToRepo,
				WebURL: project.WebURL, DefaultBranch: project.DefaultBranch,
				Visibility: project.Visibility, Archived: project.Archived,
				Metadata: map[string]any{"namespace": project.Namespace},
			})
		}
		if len(projects) < 100 {
			break
		}
	}
	return repositories, credential, nil
}

func (a *GitLabAdapter) VerifyWebhook(headers http.Header, body []byte) (WebhookEvent, error) {
	token := headers.Get("X-Gitlab-Token")
	deliveryID := headers.Get("X-Gitlab-Event-UUID")
	eventType := headers.Get("X-Gitlab-Event")
	if token == "" || deliveryID == "" || eventType == "" || a.config.WebhookSecret == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(a.config.WebhookSecret)) != 1 {
		return WebhookEvent{}, ErrInvalidWebhook
	}
	var payload struct {
		ObjectKind string `json:"object_kind"`
		Ref        string `json:"ref"`
		Project    struct {
			ID                int64  `json:"id"`
			PathWithNamespace string `json:"path_with_namespace"`
		} `json:"project"`
		ObjectAttributes struct {
			Action string `json:"action"`
		} `json:"object_attributes"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return WebhookEvent{}, ErrInvalidWebhook
	}
	return WebhookEvent{
		DeliveryID: deliveryID,
		EventType:  eventType,
		Normalized: map[string]any{
			"kind": payload.ObjectKind, "action": payload.ObjectAttributes.Action,
			"ref": payload.Ref, "repositoryId": payload.Project.ID,
			"repository": payload.Project.PathWithNamespace,
		},
	}, nil
}

func (a *GitLabAdapter) exchange(ctx context.Context, values url.Values) (Credential, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(a.config.WebBaseURL, "/")+"/oauth/token",
		strings.NewReader(values.Encode()))
	if err != nil {
		return Credential{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := a.client.Do(request)
	if err != nil {
		return Credential{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return Credential{}, fmt.Errorf("GitLab OAuth returned %d", response.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token); err != nil {
		return Credential{}, err
	}
	if token.AccessToken == "" {
		return Credential{}, errors.New("GitLab access token missing")
	}
	return Credential{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		ExpiresAt: a.now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
	}, nil
}

func (a *GitLabAdapter) apiJSON(ctx context.Context, path, token string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(a.config.APIBaseURL, "/")+path, bytes.NewReader(nil))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return fmt.Errorf("GitLab API returned %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output)
}
