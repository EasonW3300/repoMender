package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/auth"
)

// auth.Service is exercised through HTTP with an in-memory Store, keeping the
// security behavior deterministic while PostgreSQL is covered separately.

type authMemoryStore struct {
	mu        sync.Mutex
	users     map[string]auth.User
	passwords map[string][]byte
	sessions  map[string]auth.Session
	userIDs   map[string]string
	flows     map[string]auth.OIDCFlow
}

func newAuthMemoryStore() *authMemoryStore {
	return &authMemoryStore{
		users: map[string]auth.User{}, passwords: map[string][]byte{},
		sessions: map[string]auth.Session{}, userIDs: map[string]string{}, flows: map[string]auth.OIDCFlow{},
	}
}

func (m *authMemoryStore) BootstrapAdmin(_ context.Context, user auth.User, password []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.users {
		if current.Role == auth.RoleAdmin {
			return auth.ErrBootstrapClosed
		}
	}
	m.users[user.Email], m.passwords[user.Email] = user, password
	return nil
}

func (m *authMemoryStore) FindLocalUser(_ context.Context, email string) (auth.User, []byte, error) {
	user, ok := m.users[email]
	if !ok {
		return auth.User{}, nil, errors.New("not found")
	}
	return user, m.passwords[email], nil
}

func (m *authMemoryStore) UpsertOIDCUser(_ context.Context, user auth.User, _ string) (auth.User, error) {
	m.users[user.Email] = user
	return user, nil
}

func (m *authMemoryStore) CreateSession(_ context.Context, token, csrf []byte, userID string, expires time.Time) error {
	key := string(token)
	m.sessions[key], m.userIDs[key] = auth.Session{CSRFHash: csrf, ExpiresAt: expires}, userID
	return nil
}

func (m *authMemoryStore) FindSession(_ context.Context, token []byte, now time.Time) (auth.Session, error) {
	key := string(token)
	session, ok := m.sessions[key]
	if !ok || !session.ExpiresAt.After(now) {
		return auth.Session{}, errors.New("not found")
	}
	for _, user := range m.users {
		if user.ID == m.userIDs[key] {
			session.User = user
			return session, nil
		}
	}
	return auth.Session{}, errors.New("not found")
}

func (m *authMemoryStore) DeleteSession(_ context.Context, token []byte) error {
	delete(m.sessions, string(token))
	return nil
}

func (m *authMemoryStore) CreateOIDCFlow(_ context.Context, state []byte, verifier, returnTo string, _ time.Time) error {
	m.flows[string(state)] = auth.OIDCFlow{Verifier: verifier, ReturnTo: returnTo}
	return nil
}

func (m *authMemoryStore) ConsumeOIDCFlow(_ context.Context, state []byte, _ time.Time) (auth.OIDCFlow, error) {
	key := string(state)
	flow, ok := m.flows[key]
	delete(m.flows, key)
	if !ok {
		return auth.OIDCFlow{}, errors.New("not found")
	}
	return flow, nil
}

func (m *authMemoryStore) ListUsers(context.Context) ([]auth.User, error) {
	users := make([]auth.User, 0, len(m.users))
	for _, user := range m.users {
		users = append(users, user)
	}
	return users, nil
}

type failingOIDC struct{}

func (f failingOIDC) AuthCodeURL(state, challenge string) string {
	return "https://identity.example/authorize?state=" + url.QueryEscape(state) + "&challenge=" + url.QueryEscape(challenge)
}

func (f failingOIDC) Exchange(context.Context, string, string) (auth.Identity, error) {
	return auth.Identity{}, errors.New("identity provider offline")
}

func TestBootstrapLoginCSRFLogoutLifecycle(t *testing.T) {
	service := auth.NewService(newAuthMemoryStore(), time.Hour)
	handler := NewWithAuth(fakeChecker{}, service, nil, false).Handler()

	bootstrap := requestJSON(t, handler, http.MethodPost, "/api/v1/auth/bootstrap",
		map[string]string{"email": "admin@example.com", "password": "correct-horse-battery"}, nil, "")
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d body=%s", bootstrap.Code, bootstrap.Body.String())
	}

	login := requestJSON(t, handler, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "admin@example.com", "password": "correct-horse-battery"}, nil, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	var loginBody struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}

	me := requestJSON(t, handler, http.MethodGet, "/api/v1/auth/me", nil, cookies, "")
	if me.Code != http.StatusOK {
		t.Fatalf("me status = %d", me.Code)
	}
	rejected := requestJSON(t, handler, http.MethodPost, "/api/v1/auth/logout", nil, cookies, "")
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF status = %d", rejected.Code)
	}
	logout := requestJSON(t, handler, http.MethodPost, "/api/v1/auth/logout", nil, cookies, loginBody.CSRFToken)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d body=%s", logout.Code, logout.Body.String())
	}
	afterLogout := requestJSON(t, handler, http.MethodGet, "/api/v1/auth/me", nil, cookies, "")
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout status = %d", afterLogout.Code)
	}
}

func TestOIDCFailureDoesNotDisableLocalFallback(t *testing.T) {
	service := auth.NewService(newAuthMemoryStore(), time.Hour)
	_, _ = service.Bootstrap(context.Background(), "admin@example.com", "correct-horse-battery")
	handler := NewWithAuth(fakeChecker{}, service, failingOIDC{}, false).Handler()

	start := requestJSON(t, handler, http.MethodGet, "/api/v1/auth/oidc/start?returnTo=/settings", nil, nil, "")
	if start.Code != http.StatusFound {
		t.Fatalf("OIDC start status = %d", start.Code)
	}
	location, _ := url.Parse(start.Header().Get("Location"))
	callback := requestJSON(t, handler, http.MethodGet,
		"/api/v1/auth/oidc/callback?state="+url.QueryEscape(location.Query().Get("state"))+"&code=test", nil, nil, "")
	if callback.Code != http.StatusBadGateway {
		t.Fatalf("OIDC callback status = %d", callback.Code)
	}
	login := requestJSON(t, handler, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "admin@example.com", "password": "correct-horse-battery"}, nil, "")
	if login.Code != http.StatusOK {
		t.Fatalf("fallback login status = %d", login.Code)
	}
}

func TestUsersEndpointRejectsDeveloperRole(t *testing.T) {
	service := auth.NewService(newAuthMemoryStore(), time.Hour)
	_, sessionToken, _, err := service.LoginOIDC(context.Background(), auth.Identity{
		Subject:       "developer-subject",
		Email:         "developer@example.com",
		EmailVerified: true,
		Name:          "Developer",
	})
	if err != nil {
		t.Fatalf("LoginOIDC() error = %v", err)
	}
	handler := NewWithAuth(fakeChecker{}, service, nil, false).Handler()
	response := requestJSON(t, handler, http.MethodGet, "/api/v1/users", nil,
		[]*http.Cookie{{Name: sessionCookie, Value: sessionToken}}, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("developer users status = %d body=%s", response.Code, response.Body.String())
	}
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
