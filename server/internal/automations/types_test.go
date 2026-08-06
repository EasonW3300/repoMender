package automations

import (
	"errors"
	"testing"

	"github.com/EasonW3300/repoMender/server/internal/tasks"
)

func validInput(kind tasks.Kind) Input {
	event, action := "pull_request", "opened"
	if kind == tasks.KindCIDiagnosis {
		event, action = "workflow_run", "completed"
	}
	if kind == tasks.KindIssueRepair {
		event, action = "issues", "opened"
	}
	return Input{
		Name: "Payments automation", Description: "governed workflow", Kind: kind, Provider: ProviderGitHub,
		RepositoryScope: []string{"acme/payments"}, EventFilters: []EventFilter{{Event: event, Actions: []string{action}}},
		AgentTemplate: "codex-reviewer", ExecutionBudget: 100, TimeoutSeconds: 300, ConcurrencyLimit: 2,
		Risk: "high", ApprovalPolicy: ApprovalRequired,
	}
}

func TestValidateAcceptsAllM9WorkflowKinds(t *testing.T) {
	for _, kind := range []tasks.Kind{tasks.KindCodeReview, tasks.KindCIDiagnosis, tasks.KindIssueRepair} {
		if err := Validate(validInput(kind)); err != nil {
			t.Fatalf("Validate(%s) error = %v", kind, err)
		}
	}
}

func TestValidateRejectsUnsafeConfigurationCombinations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Input)
	}{
		{name: "provider", mutate: func(input *Input) { input.Provider = "gitlab" }},
		{name: "event", mutate: func(input *Input) { input.EventFilters[0].Event = "push" }},
		{name: "budget", mutate: func(input *Input) { input.ExecutionBudget = 0 }},
		{name: "timeout", mutate: func(input *Input) { input.TimeoutSeconds = 10 }},
		{name: "approval bypass", mutate: func(input *Input) { input.Risk, input.ApprovalPolicy = "critical", ApprovalRiskBased }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput(tasks.KindCodeReview)
			test.mutate(&input)
			if err := Validate(input); !errors.Is(err, ErrValidation) {
				t.Fatalf("Validate() error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestTemplateMatchesExactScopeEventAndBranch(t *testing.T) {
	input := validInput(tasks.KindCodeReview)
	template := Template{Name: input.Name, Kind: input.Kind, Provider: input.Provider,
		RepositoryScope: input.RepositoryScope, EventFilters: input.EventFilters}
	template.Enabled = true
	if !template.Matches(ProviderGitHub, "pull_request", "opened", "main", "acme/payments") {
		t.Fatal("expected matching trigger")
	}
	for _, values := range [][]string{
		{"gitlab", "pull_request", "opened", "main", "acme/payments"},
		{"github", "issues", "opened", "main", "acme/payments"},
		{"github", "pull_request", "opened", "main", "acme/other"},
	} {
		if template.Matches(values[0], values[1], values[2], values[3], values[4]) {
			t.Fatalf("unexpected match for %#v", values)
		}
	}
}
