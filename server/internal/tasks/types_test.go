package tasks

import "testing"

func TestTaskStateMachineRejectsIllegalTransitions(t *testing.T) {
	tests := []struct {
		from Status
		to   Status
		want bool
	}{
		{StatusQueued, StatusRunning, true},
		{StatusRunning, StatusSucceeded, true},
		{StatusFailed, StatusQueued, true},
		{StatusCancelled, StatusQueued, true},
		{StatusSucceeded, StatusRunning, false},
		{StatusSuperseded, StatusQueued, false},
		{StatusQueued, StatusSucceeded, false},
	}
	for _, test := range tests {
		if got := CanTransition(test.from, test.to); got != test.want {
			t.Errorf("CanTransition(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
		}
	}
}

func TestValidKindAndStatus(t *testing.T) {
	if !ValidKind(KindCodeReview) || !ValidKind(KindCIDiagnosis) || !ValidKind(KindIssueRepair) {
		t.Fatal("expected all MVP task kinds to be valid")
	}
	if ValidKind("unknown") || ValidStatus("unknown") {
		t.Fatal("unknown task values must be rejected")
	}
}
