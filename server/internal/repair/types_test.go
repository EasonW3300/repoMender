package repair

import (
	"strings"
	"testing"
	"time"
)

func validIssue() Issue {
	return Issue{Provider: "github", Repository: "EasonW3300/repoMender", CloneURL: "https://github.com/EasonW3300/repoMender.git",
		InstallationID: "42", Number: 7, Title: "repair", State: "open", BaseBranch: "main", BaseSHA: strings.Repeat("a", 40)}
}

func validPlan() Plan {
	return Plan{SchemaVersion: "v1", Summary: "fix retry", Steps: []PlanStep{{ID: "step-1", Description: "update retry", Paths: []string{"app/retry.ts"}}}, Tests: []string{"npm test"}}
}

func validPatch() Patch {
	return Patch{SchemaVersion: "v1", Summary: "fix retry", BaseSHA: strings.Repeat("a", 40), Diff: "diff --git a/app/retry.ts b/app/retry.ts\n", Files: []PatchFile{{Path: "app/retry.ts", Content: "export const retry = true;\n"}}, Tests: []TestResult{{Name: "unit", Command: "npm test", Passed: true}}}
}

func TestValidateIssueRejectsPullRequestsAndMutableBase(t *testing.T) {
	issue := validIssue()
	issue.IsPullRequest = true
	if err := ValidateIssue(issue, time.Now()); err != ErrInvalidTrigger {
		t.Fatalf("pull request validation error = %v", err)
	}
	issue = validIssue()
	issue.BaseSHA = "main"
	if err := ValidateIssue(issue, time.Now()); err != ErrInvalidTrigger {
		t.Fatalf("mutable base validation error = %v", err)
	}
}

func TestValidatePatchControlsPathsSecretsGeneratedBinarySizeAndTests(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Patch)
		want   error
	}{
		{"unsafe path", func(value *Patch) { value.Files[0].Path = "../escape" }, ErrUnsafePath},
		{"protected path", func(value *Patch) { value.Files[0].Path = ".github/workflows/ci.yml" }, ErrProtectedPath},
		{"generated file", func(value *Patch) { value.Files[0].Generated = true }, ErrGeneratedFile},
		{"binary file", func(value *Patch) { value.Files[0].Binary = true }, ErrBinaryFile},
		{"secret", func(value *Patch) { value.Files[0].Content = "token=ghp_secret" }, ErrSecretDetected},
		{"failed test", func(value *Patch) { value.Tests[0].Passed = false }, ErrTestsFailed},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			value := validPatch()
			testCase.mutate(&value)
			if err := ValidatePatch(value, strings.Repeat("a", 40)); err != testCase.want {
				t.Fatalf("ValidatePatch() = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestRepairStateMachineRequiresBothApprovalStages(t *testing.T) {
	if !CanTransition(StateAwaitingPlanApproval, StatePlanApproved) || !CanTransition(StateAwaitingPatchApproval, StatePatchApproved) {
		t.Fatal("approval transitions should be allowed")
	}
	if CanTransition(StateAwaitingPlanApproval, StatePublished) || CanTransition(StatePatchApproved, StatePublished) == false {
		t.Fatal("repair state machine accepted an invalid direct transition")
	}
}
