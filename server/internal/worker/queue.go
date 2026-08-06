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

// MultiplexProcessor keeps the durable queue single-threaded per claim while
// routing each provider-neutral task kind to its enabled business processor.
// The M4 fallback remains available for kinds whose later module is disabled.
type MultiplexProcessor struct {
	defaultProcessor TaskProcessor
	processors       map[tasks.Kind]TaskProcessor
}

func NewMultiplexProcessor(defaultProcessor TaskProcessor) *MultiplexProcessor {
	return &MultiplexProcessor{defaultProcessor: defaultProcessor, processors: make(map[tasks.Kind]TaskProcessor)}
}

func (p *MultiplexProcessor) Register(kind tasks.Kind, processor TaskProcessor) {
	if p != nil && processor != nil {
		p.processors[kind] = processor
	}
}

func (p *MultiplexProcessor) Process(ctx context.Context, task tasks.Task, run tasks.Run) (tasks.Status, string, string) {
	if p == nil {
		return tasks.StatusFailed, "processor_unavailable", "task processor is unavailable"
	}
	if processor := p.processors[task.Kind]; processor != nil {
		return processor.Process(ctx, task, run)
	}
	if p.defaultProcessor == nil {
		return tasks.StatusFailed, "processor_unavailable", "task processor is unavailable"
	}
	return p.defaultProcessor.Process(ctx, task, run)
}

type CoreProcessor struct {
	service *tasks.Service
}

// NewCoreProcessor is the M4-only processor. It persists the claim event and
// pauses the task for a later business module instead of pretending to perform
// code review, CI diagnosis, or issue repair before those modules are enabled.
func NewCoreProcessor(service *tasks.Service) CoreProcessor {
	return CoreProcessor{service: service}
}

func (p CoreProcessor) Process(ctx context.Context, task tasks.Task, run tasks.Run) (tasks.Status, string, string) {
	if p.service == nil {
		return tasks.StatusFailed, "m4_processor_unavailable", "M4 task processor is unavailable"
	}
	if _, err := p.service.AppendEvent(ctx, run.ID, tasks.RunEventInput{
		Kind: "status", Message: "M4 worker claimed task; business processor pending",
	}); err != nil {
		return tasks.StatusFailed, "m4_event_persist_failed", err.Error()
	}
	return tasks.StatusAwaitingApproval, "", "business processor pending"
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
