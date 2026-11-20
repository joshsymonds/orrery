package interpret

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

func TestDecideReviewRunActions(t *testing.T) {
	t.Parallel()
	review := kindNode("review", "review")
	run := attempted(running(startOf("review", "review", "review", 0)), 1)
	reviewer := store.NodeWorkID(run.ID, run.Attempt, "reviewer 1")
	verify := store.NodeWorkID(run.ID, run.Attempt, "verify 1 1")
	ids := []string{reviewer, verify}
	units := []kinds.Unit{
		{ID: reviewer, Part: "reviewer 1", Reviewer: "security"},
		{
			ID: verify, Part: "verify 1 1", Reviewer: "security",
			Finding: store.LedgerFinding{ID: "security-1", Reviewer: "security", Finding: "F1", Claim: "the claim"},
		},
	}
	verdicts := []store.ReviewVerdict{{Position: 1, Reviewer: "security", Step: "step-1", Verdict: "fails"}}
	ledger := []store.LedgerFinding{{
		ID: "security-1", Position: 1, Reviewer: "security", Finding: "F1", Claim: "the claim", Step: "step-2",
		Result: "confirmed",
	}}
	decideCases(t, []decideCase{
		{
			name: "start gives work that starts each unit's id with its unit",
			flow: []workflow.Node{review},
			rows: rowsOf(nil, nil, run),
			reg:  map[string]kinds.Kind{"review": scripted{0: {Kind: kinds.ActStart, IDs: ids, Units: units}}},
			want: []Action{{
				Kind: ActWork, Work: kinds.ActStart, Path: "review", NodeKind: "review", Def: review, Run: run,
				IDs: ids, Units: units,
			}},
		},
		{
			name: "stopped review step gives the hold of its step with the stop as its reason",
			flow: []workflow.Node{review},
			rows: rowsOf(nil, nil, run),
			reg: map[string]kinds.Kind{"review": scripted{0: {
				Kind: kinds.ActHold, IDs: []string{reviewer}, Output: "reviewer 1 stopped: owner stopped it",
			}}},
			want: []Action{{
				Kind: ActHold, Path: "review", IDs: []string{reviewer}, Reason: "reviewer 1 stopped: owner stopped it",
			}},
		},
		{
			name: "end gives the review with its verdicts and ledger",
			flow: []workflow.Node{review},
			rows: rowsOf(nil, nil, run),
			reg: map[string]kinds.Kind{"review": scripted{0: {
				Kind: kinds.ActEnd, Outcome: "passed", Output: "security: fails", Verdicts: verdicts, Ledger: ledger,
			}}},
			want: []Action{
				{
					Kind:     ActReview,
					Path:     "review",
					Attempt:  1,
					Output:   "security: fails",
					Verdicts: verdicts,
					Ledger:   ledger,
				},
			},
		},
	})
}

func TestDecideClosureRunActions(t *testing.T) {
	t.Parallel()
	closure := kindNode("closure", "closure")
	refine := loopNode(
		workflow.Loop{Max: 2, Until: workflow.Outcome{Node: "closure", Name: "passed"}, Spent: "park"},
		closure,
	)
	bodyRun := attempted(running(startOf("refine@1/closure", "closure", "closure", 1)), 1)
	topRun := attempted(running(startOf("closure", "closure", "closure", 0)), 1)
	round := store.ReviewRound{Round: 1, Revision: "s1", Tasks: []int64{1, 2}, Outcome: "passed"}
	closures := []store.Closure{{Finding: "security-1", Round: 1, Step: "step-3", Result: "closed", Reason: "fixed"}}
	closeID := store.NodeWorkID(bodyRun.ID, bodyRun.Attempt, "close security-1")
	closeUnit := kinds.Unit{
		ID: closeID, Part: "close security-1", Reviewer: "security",
		Finding: store.LedgerFinding{ID: "security-1", Reviewer: "security", Finding: "F1", Result: "confirmed"},
	}
	decideCases(t, []decideCase{
		{
			name: "passed closure in a loop body gives the closure end and the round's walk stops there",
			flow: []workflow.Node{refine},
			rows: rowsOf(nil, nil, running(loopOf(1, 0)), bodyRun),
			reg: map[string]kinds.Kind{"closure": scripted{1: {
				Kind: kinds.ActEnd, Outcome: "passed", Output: "all closed", Round: round, Closures: closures,
			}}},
			want: []Action{{
				Kind: ActClosure, Path: "refine@1/closure", Attempt: 1, Outcome: "passed", Output: "all closed",
				ReviewRound: round, Closures: closures,
			}},
		},
		{
			name: "failed closure in a loop body gives the closure end and the round's walk stops there",
			flow: []workflow.Node{refine},
			rows: rowsOf(nil, nil, running(loopOf(1, 0)), bodyRun),
			reg: map[string]kinds.Kind{"closure": scripted{1: {
				Kind: kinds.ActEnd, Outcome: "failed", Output: "1 open", Round: round, Closures: closures,
			}}},
			want: []Action{{
				Kind: ActClosure, Path: "refine@1/closure", Attempt: 1, Outcome: "failed", Output: "1 open",
				ReviewRound: round, Closures: closures,
			}},
		},
		{
			name: "passed closure at the top level gives the closure end",
			flow: []workflow.Node{closure},
			rows: rowsOf(nil, nil, topRun),
			reg: map[string]kinds.Kind{"closure": scripted{0: {
				Kind: kinds.ActEnd, Outcome: "passed", Output: "all closed", Round: round, Closures: closures,
			}}},
			want: []Action{{
				Kind: ActClosure, Path: "closure", Attempt: 1, Outcome: "passed", Output: "all closed",
				ReviewRound: round, Closures: closures,
			}},
		},
		{
			name: "failed closure at the top level gives the closure end rather than a park",
			flow: []workflow.Node{closure},
			rows: rowsOf(nil, nil, topRun),
			reg: map[string]kinds.Kind{"closure": scripted{0: {
				Kind: kinds.ActEnd, Outcome: "failed", Output: "1 open", Round: round, Closures: closures,
			}}},
			want: []Action{{
				Kind: ActClosure, Path: "closure", Attempt: 1, Outcome: "failed", Output: "1 open",
				ReviewRound: round, Closures: closures,
			}},
		},
		{
			name: "start of a closure in a loop body gives work that starts each unit's id with its unit",
			flow: []workflow.Node{refine},
			rows: rowsOf(nil, nil, running(loopOf(1, 0)), bodyRun),
			reg: map[string]kinds.Kind{"closure": scripted{1: {
				Kind: kinds.ActStart, IDs: []string{closeID}, Units: []kinds.Unit{closeUnit},
			}}},
			want: []Action{{
				Kind: ActWork, Work: kinds.ActStart, Path: "refine@1/closure", NodeKind: "closure", Def: closure,
				Run: bodyRun, IDs: []string{closeID}, Units: []kinds.Unit{closeUnit},
			}},
		},
	})
}

func TestDecideConcludeRunAction(t *testing.T) {
	t.Parallel()
	conclude := kindNode("conclude", "conclude")
	run := attempted(running(startOf("conclude", "conclude", "conclude", 0)), 1)
	question := store.GapsQuestion{
		Gaps: []string{"reviewer 1: fails"}, Recommended: store.ChoiceRelease,
		Why: "the planner recommends release for every gap",
	}
	decideCases(t, []decideCase{
		{
			name: "clean end gives the conclusion with the clean phase and its body",
			flow: []workflow.Node{conclude},
			rows: rowsOf(nil, nil, run),
			reg: map[string]kinds.Kind{"conclude": scripted{0: {
				Kind: kinds.ActEnd, Outcome: "passed", Phase: "clean", Output: "epic e1 ended clean at s1",
			}}},
			want: []Action{{
				Kind: ActConclude, Path: "conclude", Attempt: 1, Phase: "clean", Output: "epic e1 ended clean at s1",
			}},
		},
		{
			name: "end with gaps gives the conclusion with the ended_with_gaps phase and its body",
			flow: []workflow.Node{conclude},
			rows: rowsOf(nil, nil, run),
			reg: map[string]kinds.Kind{"conclude": scripted{0: {
				Kind: kinds.ActEnd, Outcome: "passed", Phase: "ended_with_gaps",
				Output: "epic e1 ended with gaps: reviewer 1: fails", Question: question,
			}}},
			want: []Action{{
				Kind: ActConclude, Path: "conclude", Attempt: 1, Phase: "ended_with_gaps",
				Output: "epic e1 ended with gaps: reviewer 1: fails", Question: question,
			}},
		},
	})
}
