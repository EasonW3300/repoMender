package worker

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// TaskQueue is the narrow durable queue contract used by a worker process. The
// database implementation claims with row locks; tests and future providers
// can implement the same contract without coupling to PostgreSQL details.
type TaskQueue interface {
	Claim(context.Context, string, time.Duration) (tasks.Task, tasks.Run, error)
	Heartbeat(context.Context, string, string, time.Duration) error
	Complete(context.Context, string, string, tasks.Status, string, string) error
}

type TaskProcessor interface {
	Process(context.Context, tasks.Task, tasks.Run) (status tasks.Status, code, message string)
}

// RunQueue claims at most one task per poll, processes it, and commits exactly
// one terminal state. A lack of work is normal; all other queue errors stop the
// process so orchestration can restart it without silently dropping work.
func RunQueue(ctx context.Context, poll, lease time.Duration, owner string, queue TaskQueue, processor TaskProcessor) error {
	if poll <= 0 || lease <= 0 || strings.TrimSpace(owner) == "" || queue == nil || processor == nil {
		return errors.New("queue worker requires positive intervals, owner, queue, and processor")
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			task, run, err := queue.Claim(ctx, owner, lease)
			if errors.Is(err, tasks.ErrTaskNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			status, code, message := processor.Process(ctx, task, run)
			if err := queue.Complete(ctx, task.ID, run.ID, status, code, message); err != nil {
				return err
			}
		}
	}
}
