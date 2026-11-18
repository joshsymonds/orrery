package planner

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
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
		Requirements: []store.EpicRequirement{{
			ID: "R1", Text: "a trigger is claimed", Evidence: "store test", Kind: "test",
			Item: evidence.Item{
				Kind: evidence.KindTest, Capture: []string{"go", "test", "./internal/store/"},
				Tests: []string{"internal/store/triggers_test.go"}, Limit: 120,
				Conclusion: "the trigger test passes", Judge: "haiku-high-ro",
			},
		}},
		Gates: []store.EpicGate{
			{
				Name: "check", Argv: []string{"just", "check"}, Limit: 600,
				Needs: []store.EpicGateNeed{{Pool: "gate", Count: 1}},
			},
			{Name: "review", Agent: "reviewer", Must: []string{"tests pass"}, MustNot: []string{"a skipped test"}},
		},
		DoneGates:   []string{"check"},
		ReviewGates: []string{"review"},
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
			Files: []string{"internal/planner/prompt.go"}, Requirements: []string{"R1"}, Contributes: []string{"R2"},
			Gate: "check", Evidence: evidence.Item{
				Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the task is done", Judge: "a judge",
			},
			Status: "open",
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

// promptOf returns the prompt of turn of epic, and fails the test when it cannot be rendered.
func promptOf(t *testing.T, epic store.EpicRow, turn int64, triggers []store.EpicTrigger) string {
	t.Helper()
	prompt, err := Prompt(epic, turn, triggers, nil)
	if err != nil {
		t.Fatalf("Prompt of turn %d: %v", turn, err)
	}
	return prompt
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
	prompt := promptOf(t, syntheticEpic(), 2, syntheticTriggers())
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
		`  evidence item: {"capture":["go","test","./internal/store/"],"limit":120,"artifacts":null,"supplied":null,`+
			`"conclusion":"the trigger test passes","judge":"haiku-high-ro","kind":"test",`+
			`"tests":["internal/store/triggers_test.go"]}`,
		"Gates:",
		`- check: command ["just","check"]`,
		"  limit: 600s",
		"  needs: gate x 1",
		"- review: agent reviewer",
		"  must: tests pass",
		"  must not: a skipped test",
		"Done gates: check",
		"Review gates: review",
		"Premises:",
		"- P1: the step runs in process",
		"  if wrong: use the service",
		"Must Not Ship:",
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
		"## Advice followed",
		"## Tasks",
		"- T1 [open] a task",
		"  deliverable: a deliverable",
		"  files: internal/planner/prompt.go",
		"  requirements: R1",
		"  contributes: R2",
		"  gate: check",
		`  evidence: {"capture":["scripts/check"],"limit":60,"artifacts":null,"supplied":null,`+
			`"conclusion":"the task is done","judge":"a judge"}`,
		"## Triggers",
		"- created: epic created",
		"- answered-1: owner answered Q1",
		"## Last failure",
		"planner return refused",
	)
}

func TestPromptRendersEmptyContributesWhenTaskContributesNothing(t *testing.T) {
	t.Parallel()
	epic := syntheticEpic()
	epic.Tasks[0].Contributes = nil
	prompt := promptOf(t, epic, 2, syntheticTriggers())
	inOrder(t, prompt,
		"  requirements: R1\n  contributes: \n  test_changes: \n  gate: check\n",
	)
}

func TestPromptRendersTestChangesAndTheGuardsLastRefusal(t *testing.T) {
	t.Parallel()
	epic := syntheticEpic()
	epic.Tasks[0].TestChanges = []string{"store_test.go"}
	epic.Tasks[0].Build.Guard = "store_test.go: TestOld: test present at the base removed"
	prompt := promptOf(t, epic, 2, syntheticTriggers())
	inOrder(t, prompt,
		"  contributes: ",
		"  test_changes: store_test.go\n  gate: check\n",
		"  guard refused: store_test.go: TestOld: test present at the base removed\n",
	)
}

func TestPromptFirstTurnHasNoNewNotesOrFailureButListsEveryDecision(t *testing.T) {
	t.Parallel()
	epic := syntheticEpic()
	epic.Turns = []store.EpicTurn{{Seq: 1, State: "running"}}
	prompt := promptOf(t, epic, 1, syntheticTriggers())
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
	prompt := promptOf(t, epic, 3, nil)
	want := "## New notes\n- n2: note two\n- n3: note three\n\n## Open questions\nNone.\n"
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt lacks %q: a free turn must not set the new notes", want)
	}
	if !strings.HasSuffix(prompt, "## Last failure\nfirst failure\n") {
		t.Fatalf("last failure is not the failed turn's reason; prompt ends %q", prompt[len(prompt)-60:])
	}
}

// adviceEpic is syntheticEpic with the questions and decisions that carry advice. Q1 is labeled, with two choices and
// a recommendation. Q2 is a park question with its one choice retry. Q3 is answered with choice short, Q4 is an
// unlabelled open question with no choices, and Q5 is answered with choice long. Decision ans-4 answers Q3 with its
// text, and decision ans-5 answers Q5 with no text.
func adviceEpic() store.EpicRow {
	epic := syntheticEpic()
	epic.Questions = []store.EpicQuestion{
		{
			Seq: 1, Turn: 1, Text: "which store?", State: "open", Label: "which-store",
			Choices: []store.EpicChoice{
				{ID: "sqlite", Does: "Keep the store in SQLite."},
				{ID: "files", Does: "Write the store to files."},
			},
			Recommended: "sqlite", Why: "The tables already hold the epic.", Confidence: "high",
		},
		{
			Seq: 2, Turn: 0, Text: "usage limit reached", State: "open", Label: store.ParkLabel,
			Choices: []store.EpicChoice{
				{ID: "retry", Does: "End the park and start a turn on the waiting triggers."},
			},
			Recommended: "retry", Why: "No model judges a park.", Confidence: "low",
		},
		{
			Seq: 3, Turn: 1, Text: "how long to cache?", State: "answered", Label: "cache-ttl", Choice: "short",
			Choices: []store.EpicChoice{
				{ID: "short", Does: "Cache for a minute."},
				{ID: "long", Does: "Cache for a day."},
			},
			Recommended: "short", Why: "Minutes are enough.", Confidence: "medium",
		},
		{Seq: 4, Turn: 2, Text: "name the epic?", State: "open"},
		{
			Seq: 5, Turn: 2, Text: "keep the logs?", State: "answered", Choice: "long",
			Choices: []store.EpicChoice{
				{ID: "long", Does: "Keep them for a day."},
				{ID: "short", Does: "Drop them at once."},
			},
		},
	}
	epic.Decisions = append(epic.Decisions,
		store.EpicDecision{Seq: 4, ID: "ans-4", Text: "keep it short", Question: 3},
		store.EpicDecision{Seq: 5, ID: "ans-5", Question: 5},
	)
	return epic
}

// TestPromptShowsQuestionAdviceAnswerChoicesAndFollowCounts renders adviceEpic with two follow rows. Each question
// shows its label or park, its declared choices and its recommendation; an answer with a choice names it, and one
// with no text has no `: text`. The follow rows render in the order given, under Advice followed.
func TestPromptShowsQuestionAdviceAnswerChoicesAndFollowCounts(t *testing.T) {
	t.Parallel()
	follows := []store.LabelFollow{
		{Label: "cache-ttl", Answered: 2, Followed: 1},
		{Label: "which-store", Answered: 3, Followed: 2},
	}
	prompt, err := Prompt(adviceEpic(), 2, syntheticTriggers(), follows)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	want := "## Open questions\n" +
		"- Q1 (turn 1, which-store): which store?\n" +
		"  sqlite: Keep the store in SQLite.\n" +
		"  files: Write the store to files.\n" +
		"  recommended: sqlite (high): The tables already hold the epic.\n" +
		"- Q2 (park): usage limit reached\n" +
		"  retry: End the park and start a turn on the waiting triggers.\n" +
		"  recommended: retry (low): No model judges a park.\n" +
		"- Q4 (turn 2): name the epic?\n" +
		"\n## Advice followed\n" +
		"- cache-ttl: 1 of 2 answers took the recommendation\n" +
		"- which-store: 2 of 3 answers took the recommendation\n" +
		"\n## Tasks\n"
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt lacks the Open questions and Advice followed sections %q; prompt is:\n%s", want, prompt)
	}
	inOrder(t, prompt,
		"## Decisions",
		"- ans-4 (answers Q3 with short): keep it short\n",
		"- ans-5 (answers Q5 with long)\n",
		"## New notes",
	)
}

// TestPromptSaysNoneForAdviceFollowedWithoutFollows renders adviceEpic with no follow rows: Advice followed is None.
func TestPromptSaysNoneForAdviceFollowedWithoutFollows(t *testing.T) {
	t.Parallel()
	prompt := promptOf(t, adviceEpic(), 2, syntheticTriggers())
	if !strings.Contains(prompt, "\n## Advice followed\nNone.\n\n## Tasks\n") {
		t.Fatalf("prompt does not say None. under Advice followed; prompt is:\n%s", prompt)
	}
}

// TestRoleSaysToReuseALabelForTheSameKindOfQuestion asserts that the role text tells the planner to reuse a label for
// the same kind of question, so the follow counts of that label add up across epics.
func TestRoleSaysToReuseALabelForTheSameKindOfQuestion(t *testing.T) {
	t.Parallel()
	if !strings.Contains(role, "reuse a label for the same kind of question") {
		t.Errorf("role text does not tell the planner to reuse a label for the same kind of question")
	}
}

// TestPromptSaysNoneForAnEpicWithNoGates: an epic that declares no gate, done gate or review gate says so in its
// contract, so the planner reads that it names its gates from the repository's project file.
func TestPromptSaysNoneForAnEpicWithNoGates(t *testing.T) {
	t.Parallel()
	epic := syntheticEpic()
	epic.Gates, epic.DoneGates, epic.ReviewGates = nil, nil, nil
	inOrder(t, promptOf(t, epic, 2, syntheticTriggers()),
		"Requirements:", "Gates: None.", "Done gates: None.", "Review gates: None.", "Premises:")
}
