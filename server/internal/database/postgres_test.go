package database

import (
	"io/fs"
	// sort and strings make migration filename pairing and ordering explicit.
	"sort"
	"strings"
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

func TestMigrationsArePairedAndOrdered(t *testing.T) {
	up, err := fs.Glob(migrationFiles, "migrations/*.up.sql")
	if err != nil {
		t.Fatalf("Glob(up) error = %v", err)
	}
	down, err := fs.Glob(migrationFiles, "migrations/*.down.sql")
	if err != nil {
		t.Fatalf("Glob(down) error = %v", err)
	}
	sort.Strings(up)
	downSet := make(map[string]struct{}, len(down))
	for _, path := range down {
		downSet[path] = struct{}{}
	}
	for index, path := range up {
		base := strings.TrimSuffix(path, ".up.sql")
		if _, ok := downSet[base+".down.sql"]; !ok {
			t.Fatalf("migration %s has no matching down script", path)
		}
		if index > 0 && up[index-1] >= path {
			t.Fatalf("migration order is not strictly increasing: %s then %s", up[index-1], path)
		}
	}
}
