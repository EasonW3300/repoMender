package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/config"
)

// config supplies the same polling contract used by the production worker.

type fakeChecker struct {
	err error
}

func (f fakeChecker) Ping(context.Context) error {
	return f.err
}

func TestWorkerStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(ctx, config.Config{WorkerPoll: time.Millisecond}, fakeChecker{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestWorkerReturnsDatabaseFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := Run(ctx, config.Config{WorkerPoll: time.Millisecond}, fakeChecker{err: errors.New("offline")})
	if err == nil {
		t.Fatal("expected database failure")
	}
}
