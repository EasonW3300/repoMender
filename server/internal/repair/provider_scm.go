package repair

import (
	"context"

	"github.com/EasonW3300/repoMender/server/internal/scm"
)

// SCMProvider adapts the existing GitHub App service to the M8 provider
// boundary, keeping installation tokens and Git data API details out of the
// repair state machine.
type SCMProvider struct {
	service *scm.Service
}

func NewSCMProvider(service *scm.Service) *SCMProvider { return &SCMProvider{service: service} }

func (p *SCMProvider) ResolveIssue(ctx context.Context, repository, installationID string, number int) (Issue, error) {
	issue, err := p.service.ResolveGitHubIssue(ctx, repository, installationID, number)
	if err != nil {
		return Issue{}, err
	}
	return Issue{Provider: "github", Repository: repository, InstallationID: installationID, Number: issue.Number,
		Title: issue.Title, Body: issue.Body, WebURL: issue.WebURL, State: issue.State, IsPullRequest: issue.IsPullRequest}, nil
}

func (p *SCMProvider) ResolveBaseSHA(ctx context.Context, repository, installationID, branch string) (string, error) {
	return p.service.ResolveGitHubBaseSHA(ctx, repository, installationID, branch)
}

func (p *SCMProvider) PublishDraft(ctx context.Context, input PublishInput) (DraftPullRequest, error) {
	files := make([]scm.DraftFile, 0, len(input.Files))
	for _, file := range input.Files {
		files = append(files, scm.DraftFile{Path: file.Path, Content: file.Content})
	}
	pr, err := p.service.PublishGitHubDraft(ctx, scm.DraftPublication{Repository: input.Repository, InstallationID: input.InstallationID,
		BaseBranch: input.BaseBranch, BaseSHA: input.BaseSHA, BranchName: input.BranchName, Title: input.Title, Body: input.Body, Files: files})
	if err != nil {
		return DraftPullRequest{}, err
	}
	return DraftPullRequest{Number: pr.Number, URL: pr.URL}, nil
}
