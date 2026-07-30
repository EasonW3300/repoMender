package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcrypt protects the emergency local administrator password. Standard crypto
// primitives generate opaque session, CSRF, OIDC state, and PKCE values.

type Role string

const (
	RoleAdmin      Role = "admin"
	RoleMaintainer Role = "maintainer"
	RoleDeveloper  Role = "developer"
	RoleAuditor    Role = "auditor"
)

var (
	ErrBootstrapClosed   = errors.New("bootstrap administrator already exists")
	ErrInvalidCredential = errors.New("invalid credentials")
	ErrUnauthenticated   = errors.New("authentication required")
	ErrForbidden         = errors.New("insufficient permission")
	ErrInvalidFlow       = errors.New("invalid or expired OIDC flow")
)

type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	Role        Role      `json:"role"`
	AuthSource  string    `json:"authSource"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Session struct {
	User      User
	ExpiresAt time.Time
	CSRFHash  []byte
}

type OIDCFlow struct {
	Verifier string
	ReturnTo string
}

type Store interface {
	BootstrapAdmin(context.Context, User, []byte) error
	FindLocalUser(context.Context, string) (User, []byte, error)
	UpsertOIDCUser(context.Context, User, string) (User, error)
	CreateSession(context.Context, []byte, []byte, string, time.Time) error
	FindSession(context.Context, []byte, time.Time) (Session, error)
	DeleteSession(context.Context, []byte) error
	CreateOIDCFlow(context.Context, []byte, string, string, time.Time) error
	ConsumeOIDCFlow(context.Context, []byte, time.Time) (OIDCFlow, error)
	ListUsers(context.Context) ([]User, error)
}

type Service struct {
	store Store
	ttl   time.Duration
	now   func() time.Time
}

func NewService(store Store, ttl time.Duration) *Service {
	return &Service{store: store, ttl: ttl, now: time.Now}
}

func (s *Service) Bootstrap(ctx context.Context, email, password string) (User, error) {
	email, err := normalizeEmail(email)
	if err != nil || len(password) < 12 {
		return User{}, ErrInvalidCredential
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	user := User{
		ID:          newID(),
		Email:       email,
		DisplayName: displayName(email),
		Role:        RoleAdmin,
		AuthSource:  "local",
		Active:      true,
		CreatedAt:   s.now().UTC(),
	}
	if err := s.store.BootstrapAdmin(ctx, user, passwordHash); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Service) LoginLocal(ctx context.Context, email, password string) (User, string, string, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return User{}, "", "", ErrInvalidCredential
	}
	user, passwordHash, err := s.store.FindLocalUser(ctx, normalized)
	if err != nil || !user.Active || bcrypt.CompareHashAndPassword(passwordHash, []byte(password)) != nil {
		return User{}, "", "", ErrInvalidCredential
	}
	return s.createSession(ctx, user)
}

func (s *Service) LoginOIDC(ctx context.Context, identity Identity) (User, string, string, error) {
	email, err := normalizeEmail(identity.Email)
	if err != nil || identity.Subject == "" || !identity.EmailVerified {
		return User{}, "", "", ErrInvalidCredential
	}
	user, err := s.store.UpsertOIDCUser(ctx, User{
		ID:          newID(),
		Email:       email,
		DisplayName: strings.TrimSpace(identity.Name),
		Role:        RoleDeveloper,
		AuthSource:  "oidc",
		Active:      true,
		CreatedAt:   s.now().UTC(),
	}, identity.Subject)
	if err != nil {
		return User{}, "", "", err
	}
	return s.createSession(ctx, user)
}

func (s *Service) Authenticate(ctx context.Context, sessionToken string) (Session, error) {
	if sessionToken == "" {
		return Session{}, ErrUnauthenticated
	}
	session, err := s.store.FindSession(ctx, digest(sessionToken), s.now().UTC())
	if err != nil || !session.User.Active {
		return Session{}, ErrUnauthenticated
	}
	return session, nil
}

func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, digest(sessionToken))
}

func (s *Service) BeginOIDC(ctx context.Context, returnTo string) (state, verifier, challenge string, err error) {
	if !safeReturnTo(returnTo) {
		returnTo = "/"
	}
	state, err = randomToken(32)
	if err != nil {
		return "", "", "", err
	}
	verifier, err = randomToken(48)
	if err != nil {
		return "", "", "", err
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	err = s.store.CreateOIDCFlow(ctx, digest(state), verifier, returnTo, s.now().UTC().Add(10*time.Minute))
	return state, verifier, challenge, err
}

func (s *Service) ConsumeOIDC(ctx context.Context, state string) (OIDCFlow, error) {
	if state == "" {
		return OIDCFlow{}, ErrInvalidFlow
	}
	flow, err := s.store.ConsumeOIDCFlow(ctx, digest(state), s.now().UTC())
	if err != nil {
		return OIDCFlow{}, ErrInvalidFlow
	}
	return flow, nil
}

func (s *Service) ListUsers(ctx context.Context, actor User) ([]User, error) {
	if actor.Role != RoleAdmin {
		return nil, ErrForbidden
	}
	return s.store.ListUsers(ctx)
}

func (s *Service) createSession(ctx context.Context, user User) (User, string, string, error) {
	sessionToken, err := randomToken(32)
	if err != nil {
		return User{}, "", "", err
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		return User{}, "", "", err
	}
	err = s.store.CreateSession(ctx, digest(sessionToken), digest(csrfToken), user.ID, s.now().UTC().Add(s.ttl))
	return user, sessionToken, csrfToken, err
}

func VerifyCSRF(session Session, value string) bool {
	if value == "" {
		return false
	}
	actual := digest(value)
	return len(actual) == len(session.CSRFHash) && subtleCompare(actual, session.CSRFHash)
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return "", ErrInvalidCredential
	}
	return value, nil
}

func displayName(email string) string {
	name, _, _ := strings.Cut(email, "@")
	return name
}

func safeReturnTo(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.Contains(value, "\n")
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

func subtleCompare(left, right []byte) bool {
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

func newID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(fmt.Sprintf("secure random source unavailable: %v", err))
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}
