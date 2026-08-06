package scm

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RSA signing creates short-lived GitHub App JWTs without persisting
// installation tokens. The standard HTTP client keeps the adapter SDK-free and
// fixture-friendly for contract tests.

type GitHubConfig struct {
	AppID         int64
	Slug          string
	PrivateKeyPEM []byte
	WebhookSecret string
	APIBaseURL    string
	WebBaseURL    string
}

type GitHubAdapter struct {
	config GitHubConfig
	client *http.Client
	key    *rsa.PrivateKey
	now    func() time.Time
}

func NewGitHubAdapter(config GitHubConfig, client *http.Client) (*GitHubAdapter, error) {
	if config.APIBaseURL == "" {
		config.APIBaseURL = "https://api.github.com"
	}
	if config.WebBaseURL == "" {
		config.WebBaseURL = "https://github.com"
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	var key *rsa.PrivateKey
	if len(config.PrivateKeyPEM) != 0 {
		block, _ := pem.Decode(config.PrivateKeyPEM)
		if block == nil {
			return nil, errors.New("decode GitHub App private key")
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err == nil {
			var ok bool
			key, ok = parsed.(*rsa.PrivateKey)
			if !ok {
				return nil, errors.New("GitHub App key is not RSA")
			}
		} else {
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, errors.New("parse GitHub App private key")
			}
		}
	}
	return &GitHubAdapter{config: config, client: client, key: key, now: time.Now}, nil
}

func (a *GitHubAdapter) Provider() Provider { return ProviderGitHub }

func (a *GitHubAdapter) Available() bool {
	return a.config.AppID > 0 && a.config.Slug != "" && a.key != nil && a.config.WebhookSecret != ""
}

func (a *GitHubAdapter) ConnectURL(state, _ string) (string, error) {
	if !a.Available() {
		return "", ErrProviderUnavailable
	}
	return strings.TrimRight(a.config.WebBaseURL, "/") + "/apps/" + url.PathEscape(a.config.Slug) +
		"/installations/new?state=" + url.QueryEscape(state), nil
}

func (a *GitHubAdapter) Complete(ctx context.Context, callback Callback, _ string) (RemoteConnection, Credential, error) {
	installationID, err := strconv.ParseInt(callback.InstallationID, 10, 64)
	if err != nil || installationID <= 0 {
		return RemoteConnection{}, Credential{}, errors.New("invalid GitHub installation")
	}
	var installation struct {
		ID      int64 `json:"id"`
		Account struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := a.appJSON(ctx, http.MethodGet, fmt.Sprintf("/app/installations/%d", installationID), nil, &installation); err != nil {
		return RemoteConnection{}, Credential{}, err
	}
	if installation.ID != installationID || installation.Account.Login == "" {
		return RemoteConnection{}, Credential{}, errors.New("invalid GitHub installation response")
	}
	return RemoteConnection{
		Provider: ProviderGitHub, Name: installation.Account.Login,
		ExternalAccountID: strconv.FormatInt(installationID, 10),
		BaseURL:           strings.TrimRight(a.config.WebBaseURL, "/"),
	}, Credential{}, nil
}

func (a *GitHubAdapter) Repositories(ctx context.Context, connection RemoteConnection, _ Credential) ([]RemoteRepository, Credential, error) {
	installationID, err := strconv.ParseInt(connection.ExternalAccountID, 10, 64)
	if err != nil {
		return nil, Credential{}, err
	}
	var tokenResponse struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := a.appJSON(ctx, http.MethodPost,
		fmt.Sprintf("/app/installations/%d/access_tokens", installationID),
		[]byte("{}"), &tokenResponse); err != nil {
		return nil, Credential{}, err
	}
	if tokenResponse.Token == "" {
		return nil, Credential{}, errors.New("GitHub installation token missing")
	}

	repositories := make([]RemoteRepository, 0)
	for page := 1; ; page++ {
		var response struct {
			Repositories []struct {
				ID            int64          `json:"id"`
				FullName      string         `json:"full_name"`
				CloneURL      string         `json:"clone_url"`
				HTMLURL       string         `json:"html_url"`
				DefaultBranch string         `json:"default_branch"`
				Visibility    string         `json:"visibility"`
				Private       bool           `json:"private"`
				Archived      bool           `json:"archived"`
				Language      string         `json:"language"`
				Owner         map[string]any `json:"owner"`
			} `json:"repositories"`
		}
		path := fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page)
		if err := a.tokenJSON(ctx, http.MethodGet, path, tokenResponse.Token, nil, &response); err != nil {
			return nil, Credential{}, err
		}
		for _, repository := range response.Repositories {
			visibility := repository.Visibility
			if visibility == "" && repository.Private {
				visibility = "private"
			}
			repositories = append(repositories, RemoteRepository{
				ProviderRepositoryID: strconv.FormatInt(repository.ID, 10),
				FullName:             repository.FullName, CloneURL: repository.CloneURL,
				WebURL: repository.HTMLURL, DefaultBranch: repository.DefaultBranch,
				Visibility: visibility, Archived: repository.Archived,
				Metadata: map[string]any{"language": repository.Language},
			})
		}
		if len(response.Repositories) < 100 {
			break
		}
	}
	// Installation tokens intentionally remain in memory and are never returned
	// to the persistence layer.
	return repositories, Credential{}, nil
}

// InstallationToken creates a short-lived token only for the current
// publication request; it is never returned to or stored by RepoMender.
func (a *GitHubAdapter) InstallationToken(ctx context.Context, installationID string) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(installationID), 10, 64)
	if err != nil || id <= 0 {
		return "", errors.New("invalid GitHub installation")
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := a.appJSON(ctx, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", id), []byte("{}"), &response); err != nil {
		return "", err
	}
	if response.Token == "" {
		return "", errors.New("GitHub installation token missing")
	}
	return response.Token, nil
}

func (a *GitHubAdapter) CreateCheckRun(ctx context.Context, token, repository, sha, summary string, findingCount int) error {
	return a.createCheckRun(ctx, token, repository, sha, "RepoMender Code Review", summary, findingCount > 0)
}

func (a *GitHubAdapter) CreateDiagnosisCheckRun(ctx context.Context, token, repository, sha, summary string) error {
	return a.createCheckRun(ctx, token, repository, sha, "RepoMender CI Diagnosis", summary, true)
}

func (a *GitHubAdapter) createCheckRun(ctx context.Context, token, repository, sha, name, summary string, actionRequired bool) error {
	conclusion := "success"
	if actionRequired {
		conclusion = "action_required"
	}
	body, err := json.Marshal(map[string]any{
		"name": name, "head_sha": sha, "status": "completed",
		"conclusion": conclusion,
		"output":     map[string]any{"title": name, "summary": summary},
	})
	if err != nil {
		return err
	}
	var response map[string]any
	return a.tokenJSON(ctx, http.MethodPost, "/repos/"+repository+"/check-runs", token, body, &response)
}

// DownloadWorkflowLogs downloads the GitHub archive, bounds both compressed
// and uncompressed data, and returns line-oriented text with source headers so
// later evidence can identify the original Actions log file.
func (a *GitHubAdapter) DownloadWorkflowLogs(ctx context.Context, token, repository string, runID int64) (string, error) {
	if runID <= 0 || !validGitHubRepository(repository) {
		return "", errors.New("invalid workflow log request")
	}
	body, err := a.tokenBytes(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/actions/runs/%d/logs", repository, runID), token, 8<<20)
	if err != nil {
		return "", err
	}
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", fmt.Errorf("decode workflow logs: %w", err)
	}
	var output strings.Builder
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return "", err
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, 8<<20+1))
		_ = reader.Close()
		if readErr != nil {
			return "", readErr
		}
		if len(content) > 8<<20 {
			return "", errors.New("workflow log entry exceeds limit")
		}
		if output.Len()+len(content) > 8<<20 {
			return "", errors.New("workflow logs exceed limit")
		}
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteString("== ")
		output.WriteString(strings.ReplaceAll(entry.Name, "\n", "_"))
		output.WriteString(" ==\n")
		output.Write(content)
	}
	if output.Len() == 0 {
		return "", errors.New("workflow logs are empty")
	}
	return output.String(), nil
}

func (a *GitHubAdapter) CreateIssueComment(ctx context.Context, token, repository string, number int, bodyText string) error {
	body, err := json.Marshal(map[string]string{"body": bodyText})
	if err != nil {
		return err
	}
	var response map[string]any
	return a.tokenJSON(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repository, number), token, body, &response)
}

func (a *GitHubAdapter) VerifyWebhook(headers http.Header, body []byte) (WebhookEvent, error) {
	signature := headers.Get("X-Hub-Signature-256")
	deliveryID := headers.Get("X-GitHub-Delivery")
	eventType := headers.Get("X-GitHub-Event")
	if signature == "" || deliveryID == "" || eventType == "" || a.config.WebhookSecret == "" {
		return WebhookEvent{}, ErrInvalidWebhook
	}
	mac := hmac.New(sha256.New, []byte(a.config.WebhookSecret))
	_, _ = mac.Write(body)
	expected := "sha256=" + fmt.Sprintf("%x", mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return WebhookEvent{}, ErrInvalidWebhook
	}
	var payload struct {
		Action       string `json:"action"`
		Ref          string `json:"ref"`
		Number       int    `json:"number"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repository struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
			HTMLURL  string `json:"html_url"`
		} `json:"repository"`
		PullRequest struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
			Head    struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
			User struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"pull_request"`
		WorkflowRun struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			HTMLURL    string `json:"html_url"`
			LogsURL    string `json:"logs_url"`
			HeadSHA    string `json:"head_sha"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"workflow_run"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return WebhookEvent{}, ErrInvalidWebhook
	}
	if eventType == "workflow_run" {
		if payload.WorkflowRun.ID <= 0 || payload.WorkflowRun.HeadSHA == "" {
			return WebhookEvent{}, ErrInvalidWebhook
		}
		// Normalize workflow-run payloads here so M6 business logic stays
		// provider-neutral and never depends on GitHub's nested JSON shape.
		return WebhookEvent{
			DeliveryID: deliveryID,
			EventType:  eventType,
			Normalized: map[string]any{
				"action": payload.Action, "conclusion": payload.WorkflowRun.Conclusion,
				"installationId": payload.Installation.ID, "repositoryId": payload.Repository.ID,
				"repository": payload.Repository.FullName, "cloneURL": payload.Repository.CloneURL,
				"webURL": payload.WorkflowRun.HTMLURL, "workflowRunId": payload.WorkflowRun.ID,
				"workflowName": payload.WorkflowRun.Name, "logsURL": payload.WorkflowRun.LogsURL,
				"headSHA": payload.WorkflowRun.HeadSHA, "status": payload.WorkflowRun.Status,
			},
		}, nil
	}
	prNumber := payload.Number
	if prNumber == 0 {
		prNumber = payload.PullRequest.Number
	}
	normalized := map[string]any{
		"action": payload.Action, "ref": payload.Ref,
		"installationId":    payload.Installation.ID,
		"repositoryId":      payload.Repository.ID,
		"repository":        payload.Repository.FullName,
		"cloneURL":          payload.Repository.CloneURL,
		"webURL":            payload.Repository.HTMLURL,
		"pullRequestNumber": prNumber,
		"baseBranch":        payload.PullRequest.Base.Ref,
		"headBranch":        payload.PullRequest.Head.Ref,
		"headSHA":           payload.PullRequest.Head.SHA,
		"author":            payload.PullRequest.User.Login,
	}
	return WebhookEvent{
		DeliveryID: deliveryID,
		EventType:  eventType,
		Normalized: normalized,
	}, nil
}

func (a *GitHubAdapter) appJSON(ctx context.Context, method, path string, body []byte, output any) error {
	token, err := a.appJWT()
	if err != nil {
		return err
	}
	return a.tokenJSON(ctx, method, path, token, body, output)
}

func (a *GitHubAdapter) tokenJSON(ctx context.Context, method, path, token string, body []byte, output any) error {
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.config.APIBaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return fmt.Errorf("GitHub API returned %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output)
}

func (a *GitHubAdapter) tokenBytes(ctx context.Context, method, path, token string, maxBytes int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.config.APIBaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := a.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, fmt.Errorf("GitHub API returned %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("GitHub response exceeds limit")
	}
	return data, nil
}

func (a *GitHubAdapter) appJWT() (string, error) {
	if a.key == nil {
		return "", ErrProviderUnavailable
	}
	now := a.now().UTC()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.config.AppID,
	})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
