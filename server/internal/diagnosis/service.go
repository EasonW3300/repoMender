package diagnosis

import (
	"context"
	"errors"

	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// scm resolves provider repository IDs without exposing database stores to
// diagnosis business logic; tasks owns durable idempotency and queue state.

type RepositoryResolver interface {
	FindRepository(context.Context, scm.Provider, string) (scm.Repository, error)
}

type Service struct {
	tasks *tasks.Service
	scm   RepositoryResolver
}

func NewService(taskService *tasks.Service, scmService RepositoryResolver) *Service {
	return &Service{tasks: taskService, scm: scmService}
}

// HandleWebhook turns one verified failed workflow_run delivery into an
// immutable-commit diagnosis task. The database source key remains the final
// idempotency boundary even when GitHub retries a delivery concurrently.
func (s *Service) HandleWebhook(ctx context.Context, event scm.WebhookEvent) (tasks.Task, bool, error) {
	if s == nil || s.tasks == nil {
		return tasks.Task{}, false, errors.New("CI diagnosis service unavailable")
	}
	workflow, err := EventFromWebhook(event)
	if errors.Is(err, ErrUnsupportedEvent) {
		return tasks.Task{}, false, nil
	}
	if err != nil {
		return tasks.Task{}, false, err
	}
	repositoryID := ""
	if s.scm != nil {
		repository, lookupErr := s.scm.FindRepository(ctx, scm.ProviderGitHub, workflow.RepositoryID)
		if lookupErr == nil {
			repositoryID = repository.ID
		} else if !errors.Is(lookupErr, scm.ErrRepositoryNotFound) {
			return tasks.Task{}, false, lookupErr
		}
	}
	input, err := buildTaskInput(workflow, repositoryID)
	if err != nil {
		return tasks.Task{}, false, err
	}
	task, err := s.tasks.Create(ctx, input)
	if err != nil {
		return tasks.Task{}, false, err
	}
	return task, true, nil
}
