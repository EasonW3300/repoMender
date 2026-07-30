package database

import (
	"io/fs"
	"testing"
)

func TestMigrationPairsExist(t *testing.T) {
	up, err := fs.Glob(migrationFiles, "migrations/*.up.sql")
	if err != nil {
		t.Fatalf("Glob(up) error = %v", err)
	}
	down, err := fs.Glob(migrationFiles, "migrations/*.down.sql")
	if err != nil {
		t.Fatalf("Glob(down) error = %v", err)
	}
	if len(up) == 0 || len(up) != len(down) {
		t.Fatalf("migration pairs mismatch: up=%d down=%d", len(up), len(down))
	}
}
