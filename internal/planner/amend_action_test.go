package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// amendedItem returns a capture item with conclusion, judged by the judge testResolver resolves.
func amendedItem(conclusion string) evidence.Item {
	return evidence.Item{
		Capture: []string{"/usr/bin/go", "test", "-run", "QuietStepRestart", "./cmd/orreryd/"}, Limit: 600,
		Artifacts: []string{}, Supplied: []string{}, Conclusion: conclusion, Judge: "a judge",
		Kind: evidence.KindCapture,
	}
}

// amendArgs returns the args of an amend_evidence action that names requirement and item.
func amendArgs(t *testing.T, requirement string, item evidence.Item) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"requirement": requirement, "item": item})
	if err != nil {
		t.Fatalf("encode args: %v", err)
	}
	return string(encoded)
}

func TestAmendEvidenceArgsAreReadIntoTheAction(t *testing.T) {
	t.Parallel()
	item := amendedItem("a quiet step survives a restart")
	ret, err := Parse(actionPlan(store.ActionAmendEvidence, amendArgs(t, "R2", item)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := ret.Actions[0]
	if got.Requirement != "R2" || !reflect.DeepEqual(got.Item, item) || got.ArgsError != "" || len(got.Unknown) != 0 {
		t.Errorf("Parse amend_evidence = %+v, want R2 and %+v read with no error", got, item)
	}
	if lines := checkPlan(ret, nil); len(lines) != 0 {
		t.Errorf("Check of a well-formed amend_evidence = %q, want none", lines)
	}
	plan := planOf(ret)
	if action := plan.Actions[0]; action.Requirement != "R2" || !reflect.DeepEqual(action.Item, item) {
		t.Errorf("planOf action = %+v, want the Requirement and item carried to the store", action)
	}
}

func TestAmendEvidenceArgsWithAKeyNoItemHasAreRefused(t *testing.T) {
	t.Parallel()
	args := `{"requirement":"R2","item":{"capture":["/bin/true"],"limit":1,"conclusion":"c","judge":"a judge",` +
		`"kind":"capture","concluson":"typo"}}`
	ret, err := Parse(actionPlan(store.ActionAmendEvidence, args))
	if err != nil {
		t.Fatalf("Parse: %v, want the bad item left to Check", err)
	}
	lines := checkPlan(ret, nil)
	want := `actions[0] "amend_evidence" args: item: json: unknown field "concluson"`
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("Check = %q, want only %q", lines, want)
	}
}

func TestCheckAmendEvidenceAction(t *testing.T) {
	t.Parallel()
	noKind := amendedItem("c")
	noKind.Kind = ""
	unknownJudge := amendedItem("c")
	unknownJudge.Judge = "nobody"
	cases := []struct {
		name   string
		action EngineAction
		want   []string
	}{
		{
			name: "a Requirement the epic does not hold",
			action: EngineAction{
				Kind:        store.ActionAmendEvidence,
				Requirement: "R9",
				Item:        amendedItem("c"),
				Reason:      "r",
			},
			want: []string{`actions[0] "amend_evidence" requirement: not a Requirement of the epic`},
		},
		{
			name:   "an item evidence.Check refuses",
			action: EngineAction{Kind: store.ActionAmendEvidence, Requirement: "R1", Item: noKind, Reason: "r"},
			want:   []string{`actions[0] "amend_evidence" item.kind: none, name test, capture or supplied`},
		},
		{
			name:   "an item whose judge does not resolve",
			action: EngineAction{Kind: store.ActionAmendEvidence, Requirement: "R1", Item: unknownJudge, Reason: "r"},
			want: []string{
				`actions[0] "amend_evidence" item.judge: resolve judge: agent name does not resolve: "nobody"`,
			},
		},
		{
			name: "a field amend_evidence does not use",
			action: EngineAction{
				Kind: store.ActionAmendEvidence, Requirement: "R1", Item: amendedItem("c"), Node: "plan", Reason: "r",
			},
			want: []string{`actions[0] "amend_evidence" node: ` + reasonUnused},
		},
		{
			name:   "an item on another kind",
			action: EngineAction{Kind: store.ActionRetryEpic, Item: amendedItem("c"), Requirement: "R1", Reason: "r"},
			want: []string{
				`actions[0] "retry_epic" requirement: ` + reasonUnused, `actions[0] "retry_epic" item: ` + reasonUnused,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := checkPlan(Return{Actions: []EngineAction{tc.action}}, nil)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Check = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckAmendKeepsRefusesAPlannerConclusionOrKindChange(t *testing.T) {
	t.Parallel()
	requirements := []store.EpicRequirement{
		{ID: "R1", Item: amendedItem("the step restarts")},
		{ID: "R2", Item: amendedItem("the step is quiet")},
	}
	test := amendedItem("the step restarts")
	test.Kind, test.Tests = evidence.KindTest, []string{"cmd/orreryd/quiet_test.go"}
	ret := Return{Actions: []EngineAction{
		{Kind: store.ActionAmendEvidence, Requirement: "R1", Item: amendedItem("the step restarts")},
		{Kind: store.ActionAmendEvidence, Requirement: "R2", Item: amendedItem("the step is loud")},
		{Kind: store.ActionAmendEvidence, Requirement: "R1", Item: test},
		{Kind: store.ActionAmendEvidence, Requirement: "R9", Item: amendedItem("anything")},
		{Kind: store.ActionRetryEpic},
	}}
	want := []string{
		`actions[1] "amend_evidence" item.conclusion: differs from the conclusion of R2; only the owner amends a ` +
			`conclusion`,
		`actions[2] "amend_evidence" item.kind: differs from the kind of R1; only the owner amends a kind`,
	}
	if got := checkAmendKeeps(ret, requirements); !reflect.DeepEqual(got, want) {
		t.Errorf("checkAmendKeeps = %q, want %q", got, want)
	}
}

func TestPromptListsTheEvidenceAmendments(t *testing.T) {
	t.Parallel()
	epic := syntheticEpic()
	epic.Amendments = []store.EvidenceAmendment{
		{
			Seq: 1, Requirement: "R1", Amender: store.AmenderPlanner, Reason: "the test moved",
			Item: amendedItem("c"), State: store.AmendmentRefused, Refusal: "the red holds at the base",
		},
		{
			Seq: 2, Requirement: "R1", Amender: store.AmenderOwner, Reason: "the owner's item",
			Item: amendedItem("c"), State: store.AmendmentChecking,
		},
	}
	item, err := json.Marshal(amendedItem("c"))
	if err != nil {
		t.Fatalf("encode item: %v", err)
	}
	inOrder(t, promptOf(t, epic, 2, syntheticTriggers()),
		"## Tasks",
		"## Evidence amendments",
		"- A1 R1 by planner [refused]: the test moved",
		"  new item: "+string(item),
		"  refused: the red holds at the base",
		"- A2 R1 by owner [checking]: the owner's item",
		"  new item: "+string(item),
		"## Triggers",
	)
	if prompt := promptOf(t, syntheticEpic(), 2, syntheticTriggers()); !strings.Contains(prompt,
		"## Evidence amendments\nNone.\n") {
		t.Errorf("prompt of an epic with no amendment does not say None. under Evidence amendments")
	}
}

// amendRunnerEpic stores the runner epic with a base and R1 naming a capture item, so the store can amend it.
func amendRunnerEpic(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: runnerEpicID, Repository: runnerRepo, Base: "base-1", Title: "Plan the runner", Approved: "approved",
		Intent: "plan", Care: "serious", Approach: "one turn", ReleaseThen: "release it", Checks: "just check",
		Phase: "accepted", Created: claimedAt().Add(-time.Minute), Changed: claimedAt().Add(-time.Minute),
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "a quiet step restarts", Item: evidence.Item{
				Capture: []string{"/usr/bin/go", "test", "./internal/enginehost/"}, Limit: 600,
				Conclusion: "a quiet step survives a restart", Judge: "a judge", Kind: evidence.KindCapture,
			}},
		},
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", runnerEpicID, err)
	}
}

// amendPlan returns a planner return with no task or question whose one action amends R1 to item.
func amendPlan(t *testing.T, item evidence.Item) string {
	t.Helper()
	encoded, err := json.Marshal(amendArgs(t, "R1", item))
	if err != nil {
		t.Fatalf("encode args: %v", err)
	}
	return `{"status":"done","decisions":[],"tasks":[],"outline":"amend R1","questions":[],` +
		`"actions":[{"kind":"amend_evidence","args":` + string(encoded) + `,"reason":"the test moved"}],` +
		`"outcome":"wait"}`
}

// completeAmendTurn claims a turn of the runner epic and completes it with plan.
func completeAmendTurn(t *testing.T, s *store.Store, runner *Runner, plan string) {
	t.Helper()
	ctx := context.Background()
	turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = runner.complete(ctx, runnerEpicID, turn.Seq, plan); err != nil {
		t.Fatalf("complete turn %d: %v", turn.Seq, err)
	}
}

// TestAmendEvidenceActionStartsAnAmendmentThatStaysPending checks that the pass carrying out an amend_evidence action
// starts its amendment checking and leaves the action pending, and that a later pass starts no second amendment.
func TestAmendEvidenceActionStartsAnAmendmentThatStaysPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	amendRunnerEpic(t, s)
	var reads atomic.Int64
	runner := New(runnerConfig(s, newFakeSteps(), agentReturning(runnerAgent, nil, &reads)))
	item := amendedItem("a quiet step survives a restart")
	completeAmendTurn(t, s, runner, amendPlan(t, item))

	runner.PassOnly(ctx, func(string) bool { return false })
	runner.PassOnly(ctx, func(string) bool { return false })

	epic := epicOf(t, s)
	if len(epic.Amendments) != 1 {
		t.Fatalf("amendments after two passes = %d, want 1", len(epic.Amendments))
	}
	amended := epic.Amendments[0]
	if amended.State != store.AmendmentChecking || amended.Amender != store.AmenderPlanner ||
		!reflect.DeepEqual(amended.Item, item) || amended.Base != "base-1" {
		t.Errorf("amendment = %+v, want the planner's, checking %+v at base-1", amended, item)
	}
	pending, err := s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	if len(pending) != 1 || pending[0].Kind != store.ActionAmendEvidence {
		t.Errorf("pending actions while the amendment checks = %+v, want the amend_evidence action", pending)
	}
}

// TestAmendEvidenceActionThatChangesTheConclusionIsRefusedByTheEngine checks that the engine refuses a planner amendment
// that changes a conclusion even when no return check ran: the action is refused with its decision, no amendment is
// stored, and R1 keeps its item.
func TestAmendEvidenceActionThatChangesTheConclusionIsRefusedByTheEngine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	amendRunnerEpic(t, s)
	var reads atomic.Int64
	runner := New(runnerConfig(s, newFakeSteps(), agentReturning(runnerAgent, nil, &reads)))
	completeAmendTurn(t, s, runner, amendPlan(t, amendedItem("a quiet step restarts quietly")))

	runner.PassOnly(ctx, func(string) bool { return false })

	epic := epicOf(t, s)
	if len(epic.Amendments) != 0 {
		t.Errorf("amendments = %+v, want none stored", epic.Amendments)
	}
	if conclusion := epic.Requirements[0].Item.Conclusion; conclusion != "a quiet step survives a restart" {
		t.Errorf("R1 conclusion = %q, want it unchanged", conclusion)
	}
	var refused string
	for _, decision := range epic.Decisions {
		if decision.ID == "action-1-1-refused" {
			refused = decision.Text
		}
	}
	want := "planner amend evidence of R1 refused: the planner may not change the conclusion of R1; only the owner " +
		"amends a conclusion. Its reason: the test moved."
	if !strings.HasPrefix(refused, want) {
		t.Errorf("decision action-1-1-refused = %q, want it to start %q", refused, want)
	}
}
