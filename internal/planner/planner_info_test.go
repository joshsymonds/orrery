package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// claimPlan returns a planner return of outcome whose one task touches internal/a.go and claims requirements.
func claimPlan(outcome string, requirements ...string) string {
	claims, _ := json.Marshal(requirements)
	return fmt.Sprintf(`{"status":"done","decisions":[],"tasks":[{"title":"Touch a","deliverable":"d",`+
		`"files":["internal/a.go"],"requirements":%s,"gate":"check","evidence":{"capture":["scripts/check"],`+
		`"limit":60,"artifacts":[],"supplied":[],"conclusion":"c","judge":"a judge","kind":"capture"}}],`+
		`"outline":"o","questions":[],"outcome":%q}`, claims, outcome)
}

func TestPlannerInfoAcceptsAReturnThatLeavesARequirementUnclaimed(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{Requirements: []store.EpicRequirement{{ID: "R1"}, {ID: "R2"}}}
	check := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, nil)
	for _, outcome := range []string{"work", "complete"} {
		if got := check(json.RawMessage(claimPlan(outcome, "R1"))); got != nil {
			t.Errorf("returnCheck of a %s return leaving R2 unclaimed = %q, want none", outcome, got)
		}
	}
}

func TestPlannerInfoPromptListsEachUnclaimedRequirement(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		Requirements: []store.EpicRequirement{{ID: "R1"}, {ID: "R2"}, {ID: "R3"}},
		Tasks:        []store.EpicTask{{Seq: 1, Title: "stored", Status: "open", Requirements: []string{"R1"}}},
	}
	prompt, err := Prompt(epic, 2, nil, nil, nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	const want = "## Unclaimed requirements\n- R2 is claimed by no live task\n- R3 is claimed by no live task\n"
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt lacks %q; prompt:\n%s", want, prompt)
	}
}

// TestPlannerInfoStillRefusesAnUnknownRequirementWithAnOpenTasksFile checks that an unknown Requirement is still refused
// when the task names a file an open task owns, which the check now accepts.
func TestPlannerInfoStillRefusesAnUnknownRequirementWithAnOpenTasksFile(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		Requirements: []store.EpicRequirement{{ID: "R1"}},
		Tasks:        []store.EpicTask{{Seq: 1, Title: "open", Status: "open", Files: []string{"internal/a.go"}}},
	}
	check := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, nil)
	want := []string{`tasks[0] "Touch a" requirements[0]: not a Requirement of the epic`}
	if got := check(json.RawMessage(claimPlan("work", "R9"))); !reflect.DeepEqual(got, want) {
		t.Errorf("returnCheck of an unknown Requirement and an open task's file = %q, want %q", got, want)
	}
}

// TestPlannerInfoStillRefusesAnUnknownContribution checks that an id check stays a refusal: a task contributing to a
// Requirement the epic does not hold is refused, as one claiming it is.
func TestPlannerInfoStillRefusesAnUnknownContribution(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{Requirements: []store.EpicRequirement{{ID: "R1"}}}
	check := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, nil)
	plan := strings.Replace(
		claimPlan("work", "R1"),
		`"requirements":["R1"]`,
		`"requirements":["R1"],"contributes":["R9"]`,
		1,
	)
	want := []string{`tasks[0] "Touch a" contributes[0]: not a Requirement of the epic`}
	if got := check(json.RawMessage(plan)); !reflect.DeepEqual(got, want) {
		t.Errorf("returnCheck of a contribution to R9 = %q, want %q", got, want)
	}
}

func TestPlannerInfoRoleAsksForOneSentenceConclusionsAndSaysRedIsANote(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"write the conclusion as one sentence saying what the evidence proves",
		"Red is a note, not a gate",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}

func TestPlannerInfoPromptReadsTheFailuresOfTheRunBeforeIt(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{Turns: []store.EpicTurn{
		{Seq: 1, State: "failed", Reason: "first failure"},
		{Seq: 2, State: "succeeded"},
		{Seq: 3, State: "failed", Reason: "second failure"},
		{Seq: 4, State: "free", Reason: "usage limit"},
		{Seq: 5, State: "failed", Reason: "third failure"},
		{Seq: 6, State: "running"},
	}}
	prompt, err := Prompt(epic, 6, nil, nil, nil)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	const want = "\n## Failed turns\n- turn 3: second failure\n- turn 5: third failure\n"
	if !strings.HasSuffix(prompt, want) {
		t.Errorf("prompt does not end with %q: a succeeded turn ends the run, a free turn is skipped; prompt ends %q",
			want, prompt[max(0, len(prompt)-120):])
	}
}
