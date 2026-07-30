//go:build integration

package database

import (
	"context"
	"os"
	"testing"
)

func TestMigrationsAreRepeatableAndReversible(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required")
	}

	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if err := MigrateUp(ctx, db); err != nil {
		t.Fatalf("first MigrateUp() error = %v", err)
	}
	if err := MigrateUp(ctx, db); err != nil {
		t.Fatalf("second MigrateUp() error = %v", err)
	}
	if err := MigrateDown(ctx, db); err != nil {
		t.Fatalf("MigrateDown() error = %v", err)
	}
	if err := MigrateUp(ctx, db); err != nil {
		t.Fatalf("final MigrateUp() error = %v", err)
	}
}
