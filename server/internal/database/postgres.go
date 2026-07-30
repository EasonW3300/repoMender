package database

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgx supplies the typed no-row sentinel, while pgxpool provides concurrency-safe
// PostgreSQL connections for health checks and transactional migrations.

//go:embed migrations/*.sql
var migrationFiles embed.FS

type DB struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	db := &DB{pool: pool}
	if err := db.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() {
	db.pool.Close()
}

func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

func MigrateUp(ctx context.Context, db *DB) error {
	return migrate(ctx, db, true)
}

func MigrateDown(ctx context.Context, db *DB) error {
	return migrate(ctx, db, false)
}

func migrate(ctx context.Context, db *DB, up bool) error {
	// A transaction-scoped advisory lock prevents API and worker startup from
	// applying the same migration concurrently in Docker Compose deployments.
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(82473301)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return err
	}

	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	if up {
		for _, path := range files {
			if !strings.HasSuffix(path, ".up.sql") {
				continue
			}
			version := strings.TrimSuffix(strings.TrimPrefix(path, "migrations/"), ".up.sql")
			var exists bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			sql, err := migrationFiles.ReadFile(path)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("apply migration %s: %w", version, err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES ($1)", version); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}

	var latest string
	err = tx.QueryRow(ctx, "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&latest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tx.Commit(ctx)
		}
		return err
	}
	path := "migrations/" + latest + ".down.sql"
	sql, err := migrationFiles.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read rollback migration %s: %w", latest, err)
	}
	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("rollback migration %s: %w", latest, err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version = $1", latest); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
