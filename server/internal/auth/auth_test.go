package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu           sync.Mutex
	users        map[string]User
	passwords    map[string][]byte
	sessions     map[string]Session
	sessionUsers map[string]string
	flows        map[string]storedFlow
}

type storedFlow struct {
	flow      OIDCFlow
	expiresAt time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		users:        map[string]User{},
		passwords:    map[string][]byte{},
		sessions:     map[string]Session{},
		sessionUsers: map[string]string{},
		flows:        map[string]storedFlow{},
	}
}

func (m *memoryStore) BootstrapAdmin(_ context.Context, user User, password []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.users {
		if current.Role == RoleAdmin {
			return ErrBootstrapClosed
		}
	}
	m.users[user.Email] = user
	m.passwords[user.Email] = password
	return nil
}

func (m *memoryStore) FindLocalUser(_ context.Context, email string) (User, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	user, ok := m.users[email]
	if !ok {
		return User{}, nil, errors.New("not found")
	}
	return user, m.passwords[email], nil
}

func (m *memoryStore) UpsertOIDCUser(_ context.Context, user User, _ string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.users[user.Email]; ok {
		return current, nil
	}
	m.users[user.Email] = user
	return user, nil
}

func (m *memoryStore) CreateSession(_ context.Context, token, csrf []byte, userID string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := string(token)
	m.sessions[key] = Session{ExpiresAt: expires, CSRFHash: csrf}
	m.sessionUsers[key] = userID
	return nil
}

func (m *memoryStore) FindSession(_ context.Context, token []byte, now time.Time) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[string(token)]
	if !ok || !session.ExpiresAt.After(now) {
		return Session{}, errors.New("not found")
	}
	userID := m.sessionUsers[string(token)]
	for _, user := range m.users {
		if user.ID == userID {
			session.User = user
			return session, nil
		}
	}
	return Session{}, errors.New("not found")
}

func (m *memoryStore) DeleteSession(_ context.Context, token []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, string(token))
	delete(m.sessionUsers, string(token))
	return nil
}

func (m *memoryStore) CreateOIDCFlow(_ context.Context, state []byte, verifier, returnTo string, expires time.Time) error {
	m.flows[string(state)] = storedFlow{flow: OIDCFlow{Verifier: verifier, ReturnTo: returnTo}, expiresAt: expires}
	return nil
}

func (m *memoryStore) ConsumeOIDCFlow(_ context.Context, state []byte, now time.Time) (OIDCFlow, error) {
	key := string(state)
	flow, ok := m.flows[key]
	delete(m.flows, key)
	if !ok || !flow.expiresAt.After(now) {
		return OIDCFlow{}, errors.New("not found")
	}
	return flow.flow, nil
}

func (m *memoryStore) ListUsers(context.Context) ([]User, error) {
	users := make([]User, 0, len(m.users))
	for _, user := range m.users {
		users = append(users, user)
	}
	return users, nil
}

func TestBootstrapIsOneTimeAndLocalLoginCreatesSession(t *testing.T) {
	store := newMemoryStore()
	service := NewService(store, time.Hour)
	user, err := service.Bootstrap(context.Background(), "Admin@Example.com", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if user.Email != "admin@example.com" || user.Role != RoleAdmin {
		t.Fatalf("unexpected administrator: %+v", user)
	}
	if _, err := service.Bootstrap(context.Background(), "other@example.com", "correct-horse-battery"); !errors.Is(err, ErrBootstrapClosed) {
		t.Fatalf("second Bootstrap() error = %v", err)
	}

	loggedIn, token, csrf, err := service.LoginLocal(context.Background(), "admin@example.com", "correct-horse-battery")
	if err != nil || loggedIn.ID != user.ID || token == "" || csrf == "" {
		t.Fatalf("LoginLocal() = %+v, %q, %q, %v", loggedIn, token, csrf, err)
	}
	session, err := service.Authenticate(context.Background(), token)
	if err != nil || !VerifyCSRF(session, csrf) || VerifyCSRF(session, "incorrect") {
		t.Fatalf("session validation failed: session=%+v error=%v", session, err)
	}
}

func TestOIDCDeveloperDoesNotCloseAdministratorBootstrap(t *testing.T) {
	service := NewService(newMemoryStore(), time.Hour)
	_, _, _, err := service.LoginOIDC(context.Background(), Identity{
		Subject: "developer-subject", Email: "developer@example.com",
		EmailVerified: true, Name: "Developer",
	})
	if err != nil {
		t.Fatalf("LoginOIDC() error = %v", err)
	}
	admin, err := service.Bootstrap(context.Background(), "admin@example.com", "correct-horse-battery")
	if err != nil || admin.Role != RoleAdmin {
		t.Fatalf("Bootstrap() after OIDC developer = %+v, %v", admin, err)
	}
}

func TestOIDCRejectsUnverifiedEmail(t *testing.T) {
	service := NewService(newMemoryStore(), time.Hour)
	_, _, _, err := service.LoginOIDC(context.Background(), Identity{
		Subject: "unverified-subject", Email: "unverified@example.com",
	})
	if !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("LoginOIDC() error = %v, want ErrInvalidCredential", err)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	store := newMemoryStore()
	service := NewService(store, time.Minute)
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	_, _ = service.Bootstrap(context.Background(), "admin@example.com", "correct-horse-battery")
	_, token, _, _ := service.LoginLocal(context.Background(), "admin@example.com", "correct-horse-battery")
	service.now = func() time.Time { return now.Add(2 * time.Minute) }

	if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

func TestRBACRejectsNonAdmin(t *testing.T) {
	service := NewService(newMemoryStore(), time.Hour)
	_, err := service.ListUsers(context.Background(), User{Role: RoleDeveloper})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListUsers() error = %v", err)
	}
}

func TestOIDCFlowUsesPKCEAndIsSingleUse(t *testing.T) {
	service := NewService(newMemoryStore(), time.Hour)
	state, verifier, challenge, err := service.BeginOIDC(context.Background(), "//malicious.example")
	if err != nil || state == "" || verifier == "" || challenge == "" || challenge == verifier {
		t.Fatalf("BeginOIDC() returned invalid values: %q %q %q %v", state, verifier, challenge, err)
	}
	flow, err := service.ConsumeOIDC(context.Background(), state)
	if err != nil || flow.ReturnTo != "/" || flow.Verifier != verifier {
		t.Fatalf("ConsumeOIDC() = %+v, %v", flow, err)
	}
	if _, err := service.ConsumeOIDC(context.Background(), state); !errors.Is(err, ErrInvalidFlow) {
		t.Fatalf("second ConsumeOIDC() error = %v", err)
	}
}
