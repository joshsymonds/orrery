package interpret

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// rvEpic is an epic in the repository /repo/epic-x, with two Requirements and two Must Not Ship entries, and the
// reviewers given.
func rvEpic(reviewerList ...reviewers.Reviewer) store.EpicRow {
	return store.EpicRow{
		ID:         "repo/epic-x",
		Title:      "Review the epic",
		Repository: "/repo/epic-x",
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "one"},
			{ID: "R2", Text: "two"},
		},
		Exclusions: []string{"no new dependency", "no network calls"},
		Reviewers:  reviewerList,
	}
}

// rvModel is an enabled model reviewer at the epic stage. It declares a must and a must not, a limit, tools, an effort,
// and the prompt its inline agent runs under.
func rvModel() reviewers.Reviewer {
	return reviewers.Reviewer{
		Name:    "correctness",
		Enabled: true,
		Stage:   []string{reviewers.StageEpic},
		Must:    []string{"the change holds every Requirement"},
		MustNot: []string{"drop a Requirement"},
		Prompt:  "Read the diff hard.",
		Model:   "opus",
		Tools:   []string{"Read", "Grep"},
		Effort:  "high",
		Limit:   900,
		Verify:  true,
	}
}

// rvReviewer is a reviewer named name, enabled or not, whose verify switch is verify, at the stages given.
func rvReviewer(name string, enabled, verify bool, stages ...string) reviewers.Reviewer {
	return reviewers.Reviewer{
		Name:    name,
		Enabled: enabled,
		Stage:   stages,
		Must:    []string{"holds"},
		Agent:   "rv-" + name,
		Limit:   1800,
		Verify:  verify,
	}
}

// rvDeclared heads the gate-needed changes an epic's tasks declared in an epic-stage review prompt.
const rvDeclared = "Gate-needed changes the epic's tasks declared (each is in scope, though the contract does not " +
	"name it, when that task's gate or evidence could not pass without it and it is no larger than that need):"

// rvWantPrompt is the epic review prompt of rvEpic over base111..frozen222: the epic, its Requirements, its Must Not
// Ship entries, the gate-needed changes its tasks declared, none, and the change.
func rvWantPrompt() string {
	return strings.Join([]string{
		"Epic repo/epic-x (Review the epic)",
		"Requirements:",
		"- R1: one",
		"- R2: two",
		"Must Not Ship:",
		"- no new dependency",
		"- no network calls",
		rvDeclared,
		"- none",
		"The change under review is base111..frozen222; this worktree is checked out at frozen222. " +
			"Inspect it with `git diff base111 frozen222`.",
	}, "\n")
}

func TestEpicReviewersEpicKeepsTheEnabledEpicStageReviewersInMergedOrderWithVerify(t *testing.T) {
	t.Parallel()
	epic := rvEpic(
		rvReviewer("correctness", true, true, reviewers.StageEpic),
		rvReviewer("style", true, true, reviewers.StageTask),
		rvReviewer("retired", false, true, reviewers.StageEpic),
		rvReviewer("security", true, false, reviewers.StageEpic),
		rvReviewer("docs", true, true, reviewers.StageTask, reviewers.StageEpic),
	)
	got := NewEpicReviewers().Epic(epic)
	want := []kinds.Reviewer{
		{Name: "correctness", Verify: true},
		{Name: "security", Verify: false},
		{Name: "docs", Verify: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epic = %+v, want %+v", got, want)
	}
}

func TestEpicReviewersEpicIsEmptyWhenNoEnabledEpicStageReviewerHoldsTheEpic(t *testing.T) {
	t.Parallel()
	if got := NewEpicReviewers().Epic(rvEpic()); len(got) != 0 {
		t.Errorf("Epic of an epic with no reviewers = %+v, want none", got)
	}
	taskOnly := rvEpic(rvReviewer("style", true, true, reviewers.StageTask))
	if got := NewEpicReviewers().Epic(taskOnly); len(got) != 0 {
		t.Errorf("Epic of an epic with only a task-stage reviewer = %+v, want none", got)
	}
}

func TestEpicReviewersStartRunsAModelReviewerReadOnlyAtTheRevisionWithItsGateAgentsAndPrompt(t *testing.T) {
	t.Parallel()
	epic := rvEpic(rvModel())
	got, err := NewEpicReviewers().Start(context.Background(), epic, "correctness", "base111", "frozen222", nil, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantGate := gates.Gate{
		Name:  "correctness",
		Agent: "orrery-reviewer-correctness",
		Limit: 900 * time.Second,
		Assertions: []gates.Assertion{
			{Text: "the change holds every Requirement", Kind: gates.KindMust},
			{Text: "drop a Requirement", Kind: gates.KindMustNot},
		},
	}
	if got.AgentGate == nil || !reflect.DeepEqual(*got.AgentGate, wantGate) {
		t.Errorf("AgentGate = %+v, want %+v", got.AgentGate, wantGate)
	}
	wantAgents := `{"orrery-reviewer-correctness":{"description":"orrery reviewer correctness",` +
		`"effort":"high","model":"opus","prompt":"Read the diff hard.","tools":["Read","Grep","StructuredOutput"]}}`
	if got.Agents != wantAgents {
		t.Errorf("Agents = %s, want %s", got.Agents, wantAgents)
	}
	if !got.ReadOnly || got.Workspace != epic.Repository || got.Revision != "frozen222" {
		t.Errorf("ReadOnly, Workspace, Revision = %t, %q, %q; want true, %q, frozen222",
			got.ReadOnly, got.Workspace, got.Revision, epic.Repository)
	}
	if got.Epic != epic.ID || got.Task != "" {
		t.Errorf("Epic, Task = %q, %q; want %q, none", got.Epic, got.Task, epic.ID)
	}
	if got.Prompt != rvWantPrompt() {
		t.Errorf("Prompt\n got:\n%s\nwant:\n%s", got.Prompt, rvWantPrompt())
	}
}

func TestEpicReviewersStartHeadsAnAgentReviewerWithItsPrompt(t *testing.T) {
	t.Parallel()
	agent := reviewers.Reviewer{
		Name:    "security",
		Enabled: true,
		Stage:   []string{reviewers.StageEpic},
		Must:    []string{"no secret is logged"},
		Agent:   "security-review",
		Prompt:  "Read like an attacker.",
		Limit:   1800,
		Verify:  true,
	}
	got, err := NewEpicReviewers().Start(context.Background(), rvEpic(agent), "security", "base111", "frozen222", nil, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if want := "Read like an attacker.\n\n" + rvWantPrompt(); got.Prompt != want {
		t.Errorf("Prompt\n got:\n%s\nwant:\n%s", got.Prompt, want)
	}
	if got.Agents != "" {
		t.Errorf("Agents = %s, want none for an agent reviewer", got.Agents)
	}
	if got.AgentGate == nil || got.AgentGate.Agent != "security-review" {
		t.Errorf("AgentGate = %+v, want the agent security-review", got.AgentGate)
	}
}

func TestEpicReviewersStartRefusesANameThatIsNotAnEnabledEpicStageReviewer(t *testing.T) {
	t.Parallel()
	epic := rvEpic(
		rvModel(),
		rvReviewer("retired", false, true, reviewers.StageEpic),
		rvReviewer("style", true, true, reviewers.StageTask),
	)
	for _, name := range []string{"nope", "retired", "style"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewEpicReviewers().Start(
				context.Background(), epic, name, "base111", "frozen222", nil, nil); err == nil {
				t.Errorf("Start(%q) error = nil, want an error", name)
			}
		})
	}
}

func TestEpicReviewersStartPromptPrintsNoneForEmptyRequirementsAndMustNotShip(t *testing.T) {
	t.Parallel()
	epic := rvEpic(rvModel())
	epic.Requirements = nil
	epic.Exclusions = nil
	got, err := NewEpicReviewers().Start(context.Background(), epic, "correctness", "base111", "frozen222", nil, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := strings.Join([]string{
		"Epic repo/epic-x (Review the epic)",
		"Requirements:",
		"- none",
		"Must Not Ship:",
		"- none",
		rvDeclared,
		"- none",
		"The change under review is base111..frozen222; this worktree is checked out at frozen222. " +
			"Inspect it with `git diff base111 frozen222`.",
	}, "\n")
	if got.Prompt != want {
		t.Errorf("Prompt\n got:\n%s\nwant:\n%s", got.Prompt, want)
	}
}

// TestEpicReviewersStartPromptListsTheGateChangesItsTasksDeclared: the epic's reviewers read the gate-needed changes
// each task that has not failed declared, by task and then in declared order, before the change. A failed task's
// declarations are not listed, since its change is not in the epic.
func TestEpicReviewersStartPromptListsTheGateChangesItsTasksDeclared(t *testing.T) {
	t.Parallel()
	epic := rvEpic(rvModel())
	epic.Tasks = []store.EpicTask{
		{Seq: 18, Status: "failed", GateChanges: []store.GateChange{{Path: "web/src/app.tsx", Reason: "never shown"}}},
		{Seq: 19, Status: "done", GateChanges: []store.GateChange{
			{Path: "web/src/app.test.tsx", Reason: "the epic's integrate failed on this test's History race"},
			{Path: "web/src/testdom.ts", Reason: "the wait the race needs"},
		}},
		{Seq: 20, Status: "open", GateChanges: []store.GateChange{{Path: "a.go", Reason: "the gate's race"}}},
	}
	got, err := NewEpicReviewers().Start(context.Background(), epic, "correctness", "base111", "frozen222", nil, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := strings.Join([]string{
		"- no network calls",
		rvDeclared,
		"- T19 web/src/app.test.tsx: the epic's integrate failed on this test's History race",
		"- T19 web/src/testdom.ts: the wait the race needs",
		"- T20 a.go: the gate's race",
		"The change under review is base111..frozen222; this worktree is checked out at frozen222. " +
			"Inspect it with `git diff base111 frozen222`.",
	}, "\n")
	if !strings.HasSuffix(got.Prompt, want) {
		t.Errorf("Prompt\n got:\n%s\nwant it to end:\n%s", got.Prompt, want)
	}
}

// TestEpicReviewersStartPromptListsTheEvidenceAmendments checks that an epic whose Requirements' evidence was amended
// shows each amendment to its reviewers, applied or refused, with both items, before the change.
func TestEpicReviewersStartPromptListsTheEvidenceAmendments(t *testing.T) {
	t.Parallel()
	epic := rvEpic(rvModel())
	old := evidence.Item{
		Capture:    []string{"./t", "old"},
		Limit:      1,
		Conclusion: "one holds",
		Judge:      "j",
		Kind:       "capture",
	}
	moved := evidence.Item{
		Capture:    []string{"./t", "new"},
		Limit:      1,
		Conclusion: "one holds",
		Judge:      "j",
		Kind:       "capture",
	}
	epic.Amendments = []store.EvidenceAmendment{
		{
			Seq: 1, Requirement: "R1", Amender: store.AmenderPlanner, State: store.AmendmentApplied,
			Reason: "the test moved", Prior: old, Item: moved,
		},
		{
			Seq: 2, Requirement: "R2", Amender: store.AmenderOwner, State: store.AmendmentRefused,
			Reason: "a looser capture", Prior: old, Item: moved, Refusal: "its red holds at the base",
		},
	}
	got, err := NewEpicReviewers().Start(context.Background(), epic, "correctness", "base111", "frozen222", nil, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	oldJSON := `{"capture":["./t","old"],"limit":1,"artifacts":null,"supplied":null,"conclusion":"one holds",` +
		`"judge":"j","kind":"capture"}`
	newJSON := strings.Replace(oldJSON, `"old"`, `"new"`, 1)
	want := strings.Join([]string{
		"Epic repo/epic-x (Review the epic)",
		"Requirements:",
		"- R1: one",
		"- R2: two",
		"Must Not Ship:",
		"- no new dependency",
		"- no network calls",
		"Evidence amendments:",
		"- A1 R1 by planner, applied: the test moved; prior item: " + oldJSON + "; new item: " + newJSON,
		"- A2 R2 by owner, refused: a looser capture; prior item: " + oldJSON + "; new item: " + newJSON +
			"; refused: its red holds at the base",
		rvDeclared,
		"- none",
		"The change under review is base111..frozen222; this worktree is checked out at frozen222. " +
			"Inspect it with `git diff base111 frozen222`.",
	}, "\n")
	if !strings.HasSuffix(got.Prompt, want) {
		t.Errorf("Prompt\n got:\n%s\nwant it to end:\n%s", got.Prompt, want)
	}
}

// TestEpicReviewersStartEndsTestParsimonyWithTheDoneGatesTestCost: the test_parsimony reviewer's prompt ends with the
// Test cost section, holding the budget lines of the Done gate checks' stdout and stderr, or "no budget lines" when
// they hold none.
func TestEpicReviewersStartEndsTestParsimonyWithTheDoneGatesTestCost(t *testing.T) {
	t.Parallel()
	parsimony := rvReviewer(reviewers.TestParsimony, true, true, reviewers.StageEpic)
	check := store.NodeCheck{
		Stdout: "ok  \tgithub.com/x/internal/store\t41.2s\n" +
			"test-budget: internal/store 212 cpu-s > 160 (+33%)\n" +
			"test-budget: 2 packages checked, none over twice its baseline\n",
		Stderr: "tests exceeded 360s budget\ncheck-changed: OK after 97s\n",
	}
	cases := []struct {
		name      string
		doneGates []store.NodeCheck
		want      []string
	}{
		{
			name:      "budget lines",
			doneGates: []store.NodeCheck{check},
			want: []string{
				"- test-budget: internal/store 212 cpu-s > 160 (+33%)",
				"- test-budget: 2 packages checked, none over twice its baseline",
				"- check-changed: OK after 97s",
			},
		},
		{name: "no Done gate check", want: []string{"- no budget lines"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewEpicReviewers().Start(
				context.Background(),
				rvEpic(parsimony), reviewers.TestParsimony, "base111", "frozen222", tc.doneGates, nil)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			want := strings.Join(append([]string{rvWantPrompt(), reviewers.TestCostHeading}, tc.want...), "\n")
			if got.Prompt != want {
				t.Errorf("Prompt\n got:\n%s\nwant:\n%s", got.Prompt, want)
			}
		})
	}
}

// TestEpicReviewersStartGivesNoOtherReviewerTheTestCost: a reviewer other than test_parsimony gets no Test cost section,
// even with Done gate checks that hold budget lines.
func TestEpicReviewersStartGivesNoOtherReviewerTheTestCost(t *testing.T) {
	t.Parallel()
	check := store.NodeCheck{Stdout: "test-budget: internal/store 212 cpu-s > 160 (+33%)\n"}
	got, err := NewEpicReviewers().Start(
		context.Background(), rvEpic(rvModel()), "correctness", "base111", "frozen222", []store.NodeCheck{check}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got.Prompt != rvWantPrompt() {
		t.Errorf("Prompt\n got:\n%s\nwant:\n%s", got.Prompt, rvWantPrompt())
	}
}
