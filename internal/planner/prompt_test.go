package planner

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// syntheticEpic is an epic with every contract section filled. Its decisions are two seeds and a note, decision 3,
// recorded after turn 1 saw decision 2. Turn 1 failed with a reason and a decision, turn 2 is the running turn,
// and the epic has one open question and one task.
func syntheticEpic() store.EpicRow {
	return store.EpicRow{
		ID:          "epic-1",
		Title:       "Plan the epic",
		Approved:    "approved text",
		Intent:      "decide the next tasks",
		Care:        "serious",
		Approach:    "one planner turn",
		ReleaseThen: "release it",
		Checks:      "just check",
		Failures: []store.EpicFailure{
			{ID: "F1", Text: "a file owned twice", Rating: "serious", Response: "prevent: R4"},
		},
		Additions: []store.EpicAddition{
			{Location: "internal/planner", What: "a prompt", Why: "R3", Cost: "one file"},
		},
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "a trigger is claimed", Evidence: "store test", Kind: "test"},
		},
		Premises: []store.EpicPremise{
			{ID: "P1", Text: "the step runs in process", IfWrong: "use the service"},
		},
		Exclusions: []string{"no routing"},
		Alternatives: []store.EpicAlternative{{
			Alternative: "an MCP tool", WhyNot: "no MCP tools", Reconsider: "a rule needs it",
		}},
		ReleaseSteps: []store.EpicReleaseStep{{Step: "push main", Target: "the repository", Undo: "delete the tag"}},
		Decisions: []store.EpicDecision{
			{Seq: 1, ID: "D1", Text: "seed one"},
			{Seq: 2, ID: "D2", Text: "seed two"},
			{Seq: 3, ID: "answer-3", Text: "a note after turn one"},
		},
		Turns: []store.EpicTurn{
			{
				Seq: 1, State: "failed", Reason: "planner return refused", Seen: 2,
				Decisions: []string{"turn one decision"},
			},
			{Seq: 2, State: "running"},
		},
		Tasks: []store.EpicTask{{
			Seq: 1, Turn: 1, Title: "a task", Deliverable: "a deliverable",
			Files: []string{"internal/planner/prompt.go"}, Requirements: []string{"R1"},
			Check: "go test", Status: "open",
		}},
		Questions: []store.EpicQuestion{{Seq: 1, Turn: 1, Text: "which store?", State: "open"}},
	}
}

// syntheticTriggers are the two triggers the running turn 2 claimed.
func syntheticTriggers() []store.EpicTrigger {
	return []store.EpicTrigger{
		{ID: "created", Cause: "epic created", State: "claimed", Turn: 2},
		{ID: "answered-1", Cause: "owner answered Q1", State: "claimed", Turn: 2},
	}
}

// inOrder asserts that each of wants occurs in got after the one before it.
func inOrder(t *testing.T, got string, wants ...string) {
	t.Helper()
	at := 0
	for _, want := range wants {
		i := strings.Index(got[at:], want)
		if i < 0 {
			t.Fatalf("prompt has no %q after offset %d", want, at)
		}
		at += i + len(want)
	}
}

func TestPromptOpensWithRoleThenEverySectionInOrder(t *testing.T) {
	t.Parallel()
	prompt := Prompt(syntheticEpic(), 2, syntheticTriggers())
	if !strings.HasPrefix(prompt, strings.TrimRight(role, "\n")) {
		t.Fatalf("prompt does not open with the role text")
	}
	inOrder(t, prompt,
		"## Contract",
		"Title: Plan the epic",
		"Approved: approved text",
		"Intent: decide the next tasks",
		"Failures:",
		"- F1 [serious]: a file owned twice",
		"  response: prevent: R4",
		"Care: serious",
		"Additions:",
		"- internal/planner: a prompt",
		"  why: R3",
		"  cost: one file",
		"Requirements:",
		"- R1: a trigger is claimed",
		"  evidence: store test",
		"  kind: test",
		"Premises:",
		"- P1: the step runs in process",
		"  if wrong: use the service",
		"Exclusions:",
		"- no routing",
		"Approach: one planner turn",
		"Alternatives:",
		"- an MCP tool",
		"  why not: no MCP tools",
		"  reconsider when: a rule needs it",
		"Release steps:",
		"- push main",
		"  target: the repository",
		"  undo: delete the tag",
		"Release then: release it",
		"Checks: just check",
		"## Decisions",
		"- D1: seed one",
		"- D2: seed two",
		"- answer-3: a note after turn one",
		"- turn 1: turn one decision",
		"## New notes",
		"- answer-3: a note after turn one",
		"## Open questions",
		"- Q1 (turn 1): which store?",
		"## Tasks",
		"- T1 [open] a task",
		"  deliverable: a deliverable",
		"  files: internal/planner/prompt.go",
		"  requirements: R1",
		"  check: go test",
		"## Triggers",
		"- created: epic created",
		"- answered-1: owner answered Q1",
		"## Last failure",
		"planner return refused",
	)
}

func TestPromptFirstTurnHasNoNewNotesOrFailureButListsEveryDecision(t *testing.T) {
	t.Parallel()
	epic := syntheticEpic()
	epic.Turns = []store.EpicTurn{{Seq: 1, State: "running"}}
	prompt := Prompt(epic, 1, syntheticTriggers())
	inOrder(t, prompt,
		"## Decisions",
		"- D1: seed one",
		"- D2: seed two",
		"- answer-3: a note after turn one",
		"## New notes\nNone.\n",
		"## Open questions",
	)
	if !strings.HasSuffix(prompt, "## Last failure\nNone.\n") {
		t.Fatalf("first turn's last failure is not None.; prompt ends %q", prompt[len(prompt)-60:])
	}
}

func TestPromptSkipsFreeTurnWhenFindingLastTurn(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		ID: "epic-2",
		Decisions: []store.EpicDecision{
			{Seq: 1, ID: "D1", Text: "seed one"},
			{Seq: 2, ID: "n2", Text: "note two"},
			{Seq: 3, ID: "n3", Text: "note three"},
		},
		Turns: []store.EpicTurn{
			{Seq: 1, State: "failed", Reason: "first failure", Seen: 1},
			{Seq: 2, State: "free", Reason: "usage limit", Seen: 3},
			{Seq: 3, State: "running", Seen: 3},
		},
	}
	prompt := Prompt(epic, 3, nil)
	want := "## New notes\n- n2: note two\n- n3: note three\n\n## Open questions\nNone.\n"
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt lacks %q: a free turn must not set the new notes", want)
	}
	if !strings.HasSuffix(prompt, "## Last failure\nfirst failure\n") {
		t.Fatalf("last failure is not the failed turn's reason; prompt ends %q", prompt[len(prompt)-60:])
	}
}
