package interpret

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

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

// rvWantPrompt is the epic review prompt of rvEpic over base111..frozen222: the epic, its Requirements, its Must Not
// Ship entries, and the change.
func rvWantPrompt() string {
	return strings.Join([]string{
		"Epic repo/epic-x (Review the epic)",
		"Requirements:",
		"- R1: one",
		"- R2: two",
		"Must Not Ship:",
		"- no new dependency",
		"- no network calls",
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
	got, err := NewEpicReviewers().Start(context.Background(), epic, "correctness", "base111", "frozen222")
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
	got, err := NewEpicReviewers().Start(context.Background(), rvEpic(agent), "security", "base111", "frozen222")
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
			if _, err := NewEpicReviewers().Start(context.Background(), epic, name, "base111", "frozen222"); err == nil {
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
	got, err := NewEpicReviewers().Start(context.Background(), epic, "correctness", "base111", "frozen222")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := strings.Join([]string{
		"Epic repo/epic-x (Review the epic)",
		"Requirements:",
		"- none",
		"Must Not Ship:",
		"- none",
		"The change under review is base111..frozen222; this worktree is checked out at frozen222. " +
			"Inspect it with `git diff base111 frozen222`.",
	}, "\n")
	if got.Prompt != want {
		t.Errorf("Prompt\n got:\n%s\nwant:\n%s", got.Prompt, want)
	}
}
