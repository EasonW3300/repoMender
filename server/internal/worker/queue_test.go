package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

type fakeQueue struct {
	mu         sync.Mutex
	claimed    bool
	complete   int
	completeCh chan struct{}
	claimErr   error
}

func (q *fakeQueue) Claim(context.Context, string, time.Duration) (tasks.Task, tasks.Run, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.claimErr != nil {
		return tasks.Task{}, tasks.Run{}, q.claimErr
	}
	if q.claimed {
		return tasks.Task{}, tasks.Run{}, tasks.ErrTaskNotFound
	}
	q.claimed = true
	return tasks.Task{ID: "task-1", Status: tasks.StatusRunning}, tasks.Run{ID: "run-1", TaskID: "task-1"}, nil
}

func (q *fakeQueue) Heartbeat(context.Context, string, string, time.Duration) error { return nil }

func (q *fakeQueue) Complete(context.Context, string, string, tasks.Status, string, string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.complete++
	if q.completeCh != nil {
		close(q.completeCh)
		q.completeCh = nil
	}
	return nil
}

type fakeProcessor struct{}

func (fakeProcessor) Process(context.Context, tasks.Task, tasks.Run) (tasks.Status, string, string) {
	return tasks.StatusSucceeded, "", "ok"
}

func TestRunQueueClaimsAndCompletesOnce(t *testing.T) {
	queue := &fakeQueue{completeCh: make(chan struct{})}
	complete := queue.completeCh
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunQueue(ctx, time.Millisecond, time.Second, "worker-1", queue, fakeProcessor{})
	}()
	select {
	case <-complete:
	case <-time.After(time.Second):
		t.Fatal("queue did not complete the claimed task")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunQueue() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunQueue() did not stop after cancellation")
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.complete != 1 {
		t.Fatalf("completed = %d, want 1", queue.complete)
	}
}

func TestRunQueueStopsOnQueueFailure(t *testing.T) {
	queue := &fakeQueue{claimErr: errors.New("database offline")}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := RunQueue(ctx, time.Millisecond, time.Second, "worker-1", queue, fakeProcessor{})
	if err == nil || err.Error() != "database offline" {
		t.Fatalf("RunQueue() error = %v", err)
	}
}
