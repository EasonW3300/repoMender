package scm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/database"
)

// database.DB supplies transaction-safe PostgreSQL access for single-use
// connection flows, repository snapshots, and webhook deduplication.

type PostgreSQLStore struct {
	db *database.DB
}

func NewPostgreSQLStore(db *database.DB) *PostgreSQLStore {
	return &PostgreSQLStore{db: db}
}

func (s *PostgreSQLStore) CreateFlow(ctx context.Context, stateHash []byte, flow Flow, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO scm_oauth_flows(state_hash, provider, user_id, verifier, return_to, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		stateHash, flow.Provider, flow.UserID, flow.Verifier, flow.ReturnTo, expiresAt)
	return err
}

func (s *PostgreSQLStore) ConsumeFlow(ctx context.Context, stateHash []byte, now time.Time) (Flow, error) {
	var flow Flow
	err := s.db.QueryRow(ctx, `
		DELETE FROM scm_oauth_flows
		WHERE state_hash = $1 AND expires_at > $2
		RETURNING provider, user_id, verifier, return_to`, stateHash, now).
		Scan(&flow.Provider, &flow.UserID, &flow.Verifier, &flow.ReturnTo)
	return flow, err
}

func (s *PostgreSQLStore) UpsertConnection(
	ctx context.Context,
	actorID string,
	remote RemoteConnection,
	credentialCiphertext []byte,
	repositories []RemoteRepository,
	syncedAt time.Time,
) (Connection, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback(ctx)

	connectionID := newID()
	var connection Connection
	err = tx.QueryRow(ctx, `
		INSERT INTO scm_connections(
			id, provider, name, external_account_id, base_url, status,
			credential_ciphertext, created_by, last_synced_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, 'active', $6, $7, $8, $8, $8)
		ON CONFLICT (provider, external_account_id) DO UPDATE SET
			name = EXCLUDED.name,
			base_url = EXCLUDED.base_url,
			status = 'active',
			credential_ciphertext = COALESCE(EXCLUDED.credential_ciphertext, scm_connections.credential_ciphertext),
			last_synced_at = EXCLUDED.last_synced_at,
			updated_at = EXCLUDED.updated_at
		RETURNING id, provider, name, external_account_id, base_url, status, last_synced_at, created_at`,
		connectionID, remote.Provider, remote.Name, remote.ExternalAccountID, remote.BaseURL,
		credentialCiphertext, actorID, syncedAt).
		Scan(&connection.ID, &connection.Provider, &connection.Name, &connection.ExternalAccountID,
			&connection.BaseURL, &connection.Status, &connection.LastSyncedAt, &connection.CreatedAt)
	if err != nil {
		return Connection{}, err
	}

	// Disable the previous snapshot first. Every repository returned by the
	// provider is re-enabled below, so removed access is reflected atomically.
	if _, err := tx.Exec(ctx, "UPDATE repositories SET enabled = false, updated_at = $2 WHERE connection_id = $1", connection.ID, syncedAt); err != nil {
		return Connection{}, err
	}
	for _, repository := range repositories {
		metadata, err := json.Marshal(repository.Metadata)
		if err != nil {
			return Connection{}, err
		}
		visibility := repository.Visibility
		if visibility != "public" && visibility != "internal" {
			visibility = "private"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO repositories(
				id, connection_id, provider_repository_id, full_name, clone_url, web_url,
				default_branch, visibility, archived, enabled, metadata, last_synced_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, $10, $11, $11, $11)
			ON CONFLICT (connection_id, provider_repository_id) DO UPDATE SET
				full_name = EXCLUDED.full_name,
				clone_url = EXCLUDED.clone_url,
				web_url = EXCLUDED.web_url,
				default_branch = EXCLUDED.default_branch,
				visibility = EXCLUDED.visibility,
				archived = EXCLUDED.archived,
				enabled = true,
				metadata = EXCLUDED.metadata,
				last_synced_at = EXCLUDED.last_synced_at,
				updated_at = EXCLUDED.updated_at`,
			newID(), connection.ID, repository.ProviderRepositoryID, repository.FullName,
			repository.CloneURL, repository.WebURL, repository.DefaultBranch, visibility,
			repository.Archived, metadata, syncedAt); err != nil {
			return Connection{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Connection{}, err
	}
	return connection, nil
}

func (s *PostgreSQLStore) GetConnection(ctx context.Context, id string) (StoredConnection, error) {
	var connection StoredConnection
	err := s.db.QueryRow(ctx, `
		SELECT id, provider, name, external_account_id, base_url, status,
		       last_synced_at, created_at, credential_ciphertext
		FROM scm_connections WHERE id = $1`, id).
		Scan(&connection.ID, &connection.Provider, &connection.Name, &connection.ExternalAccountID,
			&connection.BaseURL, &connection.Status, &connection.LastSyncedAt,
			&connection.CreatedAt, &connection.CredentialCiphertext)
	return connection, err
}

func (s *PostgreSQLStore) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, provider, name, external_account_id, base_url, status, last_synced_at, created_at
		FROM scm_connections ORDER BY provider, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := make([]Connection, 0)
	for rows.Next() {
		var connection Connection
		if err := rows.Scan(&connection.ID, &connection.Provider, &connection.Name,
			&connection.ExternalAccountID, &connection.BaseURL, &connection.Status,
			&connection.LastSyncedAt, &connection.CreatedAt); err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

func (s *PostgreSQLStore) ListRepositories(ctx context.Context) ([]Repository, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.connection_id, c.provider, r.provider_repository_id, r.full_name,
		       r.clone_url, r.web_url, r.default_branch, r.visibility, r.archived,
		       r.enabled, r.metadata, r.last_synced_at
		FROM repositories r
		JOIN scm_connections c ON c.id = r.connection_id
		ORDER BY r.enabled DESC, r.full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	repositories := make([]Repository, 0)
	for rows.Next() {
		repository, err := scanRepository(rows)
		if err != nil {
			return nil, err
		}
		repositories = append(repositories, repository)
	}
	return repositories, rows.Err()
}

func (s *PostgreSQLStore) GetRepository(ctx context.Context, id string) (Repository, error) {
	row := s.db.QueryRow(ctx, `
		SELECT r.id, r.connection_id, c.provider, r.provider_repository_id, r.full_name,
		       r.clone_url, r.web_url, r.default_branch, r.visibility, r.archived,
		       r.enabled, r.metadata, r.last_synced_at
		FROM repositories r
		JOIN scm_connections c ON c.id = r.connection_id
		WHERE r.id = $1`, id)
	return scanRepository(row)
}

type rowScanner interface {
	Scan(...any) error
}

func scanRepository(row rowScanner) (Repository, error) {
	var repository Repository
	var metadata []byte
	err := row.Scan(&repository.ID, &repository.ConnectionID, &repository.Provider,
		&repository.ProviderRepositoryID, &repository.FullName, &repository.CloneURL,
		&repository.WebURL, &repository.DefaultBranch, &repository.Visibility,
		&repository.Archived, &repository.Enabled, &metadata, &repository.LastSyncedAt)
	if err != nil {
		return Repository{}, err
	}
	if err := json.Unmarshal(metadata, &repository.Metadata); err != nil {
		return Repository{}, err
	}
	return repository, nil
}

func (s *PostgreSQLStore) RecordWebhook(
	ctx context.Context,
	provider Provider,
	event WebhookEvent,
	payloadHash []byte,
	receivedAt time.Time,
) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	normalized, err := json.Marshal(event.Normalized)
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO webhook_deliveries(id, provider, delivery_id, event_type, payload_sha256, normalized_event, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (provider, delivery_id) DO NOTHING`,
		newID(), provider, event.DeliveryID, event.EventType, payloadHash, normalized, receivedAt)
	if err != nil {
		return false, err
	}
	if result.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	outboxPayload, err := json.Marshal(map[string]any{
		"provider": provider, "deliveryId": event.DeliveryID,
		"eventType": event.EventType, "event": event.Normalized,
	})
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events(id, topic, payload, state, available_at, created_at, updated_at)
		VALUES ($1, $2, $3, 'pending', $4, $4, $4)`,
		newID(), "scm.webhook."+string(provider), outboxPayload, receivedAt); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
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
