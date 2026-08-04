package review

import (
	"context"
	"errors"

	"github.com/EasonW3300/repoMender/server/internal/scm"
	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

// RepositoryResolver is deliberately smaller than scm.Store. The review
// trigger only needs to map a provider repository identity to our UUID.
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

// HandleWebhook turns a verified, deduplicated GitHub pull_request event into
// an immutable-commit task. The bool reports whether the event is actionable;
// ignored event actions are acknowledged without creating work.
func (s *Service) HandleWebhook(ctx context.Context, event scm.WebhookEvent) (tasks.Task, bool, error) {
	if s == nil || s.tasks == nil {
		return tasks.Task{}, false, errors.New("code review service unavailable")
	}
	prEvent, err := EventFromWebhook(event)
	if errors.Is(err, ErrUnsupportedEvent) {
		return tasks.Task{}, false, nil
	}
	if err != nil {
		return tasks.Task{}, false, err
	}
	repositoryID := ""
	if s.scm != nil {
		if repository, lookupErr := s.scm.FindRepository(ctx, scm.ProviderGitHub, prEvent.RepositoryID); lookupErr == nil {
			repositoryID = repository.ID
		} else if !errors.Is(lookupErr, scm.ErrRepositoryNotFound) {
			return tasks.Task{}, false, lookupErr
		}
	}
	input, err := buildTaskInput(prEvent, repositoryID)
	if err != nil {
		return tasks.Task{}, false, err
	}
	task, err := s.tasks.Create(ctx, input)
	if err != nil {
		return tasks.Task{}, false, err
	}
	if repositoryID != "" || prEvent.RepositoryID != "" {
		if err := s.tasks.SupersedeCodeReviews(ctx, repositoryID, prEvent.RepositoryID, prEvent.PullRequest, task.ID); err != nil {
			return tasks.Task{}, false, err
		}
	}
	return task, true, nil
}
