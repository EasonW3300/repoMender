package auth

import (
	"context"
	"errors"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
	"github.com/jackc/pgx/v5"
)

// database.DB exposes the shared PostgreSQL pool; pgx transactions make
// bootstrap and one-time OIDC flow consumption atomic.

type PostgreSQLStore struct {
	db *database.DB
}

func NewPostgreSQLStore(db *database.DB) *PostgreSQLStore {
	return &PostgreSQLStore{db: db}
}

func (s *PostgreSQLStore) BootstrapAdmin(ctx context.Context, user User, passwordHash []byte) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// The advisory lock makes the one-time administrator invariant hold even
	// when two bootstrap requests arrive at the same instant.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(82473302)); err != nil {
		return err
	}
	var adminExists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE role = 'admin')").Scan(&adminExists); err != nil {
		return err
	}
	if adminExists {
		return ErrBootstrapClosed
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO users(id, email, display_name, role, auth_source, password_hash, active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'local', $5, true, $6, $6)`,
		user.ID, user.Email, user.DisplayName, user.Role, passwordHash, user.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgreSQLStore) FindLocalUser(ctx context.Context, email string) (User, []byte, error) {
	var user User
	var passwordHash []byte
	err := s.db.QueryRow(ctx, `
		SELECT id, email, display_name, role, auth_source, active, created_at, password_hash
		FROM users WHERE email = $1 AND auth_source = 'local'`, email).
		Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role, &user.AuthSource, &user.Active, &user.CreatedAt, &passwordHash)
	return user, passwordHash, err
}

func (s *PostgreSQLStore) UpsertOIDCUser(ctx context.Context, user User, subject string) (User, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO users(id, email, display_name, role, auth_source, subject, active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'oidc', $5, true, $6, $6)
		ON CONFLICT (auth_source, subject) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			updated_at = now()
		RETURNING id, email, display_name, role, auth_source, active, created_at`,
		user.ID, user.Email, user.DisplayName, user.Role, subject, user.CreatedAt).
		Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role, &user.AuthSource, &user.Active, &user.CreatedAt)
	return user, err
}

func (s *PostgreSQLStore) CreateSession(ctx context.Context, tokenHash, csrfHash []byte, userID string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO sessions(token_hash, csrf_hash, user_id, expires_at)
		VALUES ($1, $2, $3, $4)`, tokenHash, csrfHash, userID, expiresAt)
	return err
}

func (s *PostgreSQLStore) FindSession(ctx context.Context, tokenHash []byte, now time.Time) (Session, error) {
	var session Session
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name, u.role, u.auth_source, u.active, u.created_at,
		       s.expires_at, s.csrf_hash
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2`, tokenHash, now).
		Scan(&session.User.ID, &session.User.Email, &session.User.DisplayName, &session.User.Role,
			&session.User.AuthSource, &session.User.Active, &session.User.CreatedAt,
			&session.ExpiresAt, &session.CSRFHash)
	return session, err
}

func (s *PostgreSQLStore) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.Exec(ctx, "DELETE FROM sessions WHERE token_hash = $1", tokenHash)
	return err
}

func (s *PostgreSQLStore) CreateOIDCFlow(ctx context.Context, stateHash []byte, verifier, returnTo string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO oidc_flows(state_hash, verifier, return_to, expires_at)
		VALUES ($1, $2, $3, $4)`, stateHash, verifier, returnTo, expiresAt)
	return err
}

func (s *PostgreSQLStore) ConsumeOIDCFlow(ctx context.Context, stateHash []byte, now time.Time) (OIDCFlow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return OIDCFlow{}, err
	}
	defer tx.Rollback(ctx)

	var flow OIDCFlow
	err = tx.QueryRow(ctx, `
		DELETE FROM oidc_flows
		WHERE state_hash = $1 AND expires_at > $2
		RETURNING verifier, return_to`, stateHash, now).
		Scan(&flow.Verifier, &flow.ReturnTo)
	if err != nil {
		return OIDCFlow{}, err
	}
	return flow, tx.Commit(ctx)
}

func (s *PostgreSQLStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, email, display_name, role, auth_source, active, created_at
		FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role, &user.AuthSource, &user.Active, &user.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return users, nil
}
