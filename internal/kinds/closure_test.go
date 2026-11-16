package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// The kept returns of closure verifier steps in these tests: each gives the first result of the finding f1.
const (
	closureClosedReturn = `{"status":"done","results":[{"id":"f1","evidence":"the fix holds","result":"closed"}]}`
	closureOpenReturn   = `{"status":"done","results":[{"id":"f1","evidence":"still wrong","result":"open"}]}`
	closureUndetReturn  = `{"status":"done","results":[{"id":"f1","evidence":"cannot tell","result":"undetermined"}]}`
)

// The correction round the closure run of these tests belongs to, the path of that run, and the path and mark of the fix
// plan of the same round, whose mark bounds the round's tasks.
const (
	closureRound   = 2
	closureRunPath = "correct@2/closure"
	closureFixPath = "correct@2/fix"
	closureFixMark = 4
)

// closureNode returns the closure run of the tests at revision, in closureRound.
func closureNode(revision string) Node {
	node := nodeWith(revision, false)
	node.Run.Path, node.Run.Round = closureRunPath, closureRound
	return node
}

// closureRows returns the rows of a closure run over review and steps. The epic has three tasks, of which seqs 2 and 3
// are above closureFixMark, the mark of the fix plan of closureRound.
func closureRows(review store.Review, steps ...store.StepRow) Rows {
	return Rows{
		Epic: store.EpicRow{Tasks: []store.EpicTask{
			{Seq: 1, Turn: 3}, {Seq: 2, Turn: 5}, {Seq: 3, Turn: 6},
		}},
		Runs:   []store.NodeRun{{Path: closureFixPath, Kind: "plan", Round: closureRound, Mark: closureFixMark}},
		Review: review,
		Steps:  steps,
	}
}

// closureFinding returns the finding security raises as number place, as the ledger holds it with result and closures.
func closureFinding(place int, result string, closures ...store.Closure) store.LedgerFinding {
	finding := reviewRaised("security", place)
	finding.Position = int64(place)
	finding.Result = result
	finding.Closures = closures
	return finding
}

// closureUnit returns the unit the close chain of part starts for finding.
func closureUnit(part string, finding store.LedgerFinding) Unit {
	return Unit{ID: workID(part), Part: part, Finding: finding}
}

// closureStarted returns the start of the units, with their ids in order.
func closureStarted(units ...Unit) Action {
	ids := make([]string, 0, len(units))
	for _, unit := range units {
		ids = append(ids, unit.ID)
	}
	return Action{Kind: ActStart, IDs: ids, Units: units}
}

// closureEnd returns the end of a closure run with outcome, the round's tasks and the closures it stored.
func closureEnd(outcome string, tasks []int64, closures ...store.Closure) Action {
	return Action{
		Kind: ActEnd, Outcome: outcome, Closures: closures,
		Round: store.ReviewRound{Round: closureRound, Revision: testRev, Tasks: tasks, Outcome: outcome},
	}
}

func TestClosurePrepare(t *testing.T) {
	t.Parallel()
	unrevised := closureNode("")
	open := store.Review{Ledger: []store.LedgerFinding{closureFinding(1, "confirmed")}}
	checkDecisions(t, Closure{}, []decideCase{
		{"unrevised run prepares", unrevised, Rows{Review: open}, Action{Kind: ActPrepare}},
		{
			"unrevised run on a paused epic gives none",
			unrevised,
			Rows{Epic: store.EpicRow{Paused: true}, Review: open},
			Action{Kind: ActNone},
		},
		{
			"unrevised run on a parked epic gives none",
			unrevised,
			Rows{Epic: store.EpicRow{Parked: "budget"}, Review: open},
			Action{Kind: ActNone},
		},
	})
}

func TestClosureStarts(t *testing.T) {
	t.Parallel()
	node := closureNode(testRev)
	first, second := closureFinding(1, "confirmed"), closureFinding(2, "confirmed")
	both := store.Review{Ledger: []store.LedgerFinding{first, second}}
	checkDecisions(t, Closure{}, []decideCase{
		{
			"every open finding starts one close in one start, in ledger order",
			node,
			closureRows(both),
			closureStarted(closureUnit("close security-1", first), closureUnit("close security-2", second)),
		},
		{
			"a finding with a close step is not started again",
			node,
			closureRows(both, store.StepRow{ID: workID("close security-1"), End: "done", Result: closureClosedReturn}),
			closureStarted(closureUnit("close security-2", second)),
		},
		{
			"only open findings start a close",
			node,
			closureRows(store.Review{Ledger: []store.LedgerFinding{
				closureFinding(1, "confirmed", store.Closure{Finding: "security-1", Round: 1, Result: "closed"}),
				closureFinding(2, "dropped"),
				closureFinding(3, "undetermined"),
				closureFinding(4, "confirmed", store.Closure{Finding: "security-4", Round: 1, Result: "undetermined"}),
				closureFinding(5, "confirmed"),
			}}),
			closureStarted(closureUnit("close security-5", closureFinding(5, "confirmed"))),
		},
		{
			"starts give none on a paused epic",
			node,
			Rows{Epic: store.EpicRow{Paused: true}, Review: both},
			Action{Kind: ActNone},
		},
		{
			"starts give none on a parked epic",
			node,
			Rows{Epic: store.EpicRow{Parked: "budget"}, Review: both},
			Action{Kind: ActNone},
		},
	})
}

func TestClosureWaits(t *testing.T) {
	t.Parallel()
	node := closureNode(testRev)
	both := store.Review{Ledger: []store.LedgerFinding{closureFinding(1, "confirmed"), closureFinding(2, "confirmed")}}
	checkDecisions(t, Closure{}, []decideCase{
		{
			"running close waits",
			node,
			closureRows(store.Review{Ledger: both.Ledger[:1]}, store.StepRow{ID: workID("close security-1")}),
			Action{Kind: ActNone},
		},
		{
			"running close waits beside a settled one",
			node,
			closureRows(both,
				store.StepRow{ID: workID("close security-1"), End: "done", Result: closureClosedReturn},
				store.StepRow{ID: workID("close security-2")},
			),
			Action{Kind: ActNone},
		},
	})
}

func TestClosureHolds(t *testing.T) {
	t.Parallel()
	node := closureNode(testRev)
	first := closureFinding(1, "confirmed")
	one := store.Review{Ledger: []store.LedgerFinding{first}}
	both := store.Review{Ledger: []store.LedgerFinding{first, closureFinding(2, "confirmed")}}
	stopped := store.StepRow{ID: workID("close security-1"), End: "stopped", Reason: "owner stopped it"}
	hold := Action{
		Kind: ActHold, IDs: []string{workID("close security-1")}, Output: "close security-1 stopped: owner stopped it",
	}
	checkDecisions(t, Closure{}, []decideCase{
		{"stopped close step holds the epic once", node, closureRows(one, stopped), hold},
		{
			"a stop already held starts the next try",
			node,
			closureRows(store.Review{Ledger: one.Ledger, Holds: []string{workID("close security-1")}}, stopped),
			closureStarted(Unit{ID: workID("close security-1 try 2"), Part: "close security-1", Finding: first}),
		},
		{"a hold comes before the starts", node, closureRows(both, stopped), hold},
		{
			"a hold is kept on a paused epic",
			node,
			Rows{Epic: store.EpicRow{Paused: true}, Review: one, Steps: []store.StepRow{stopped}},
			hold,
		},
	})
}

func TestClosureRetries(t *testing.T) {
	t.Parallel()
	node := closureNode(testRev)
	first := closureFinding(1, "confirmed")
	one := store.Review{Ledger: []store.LedgerFinding{first}}
	retry := closureStarted(Unit{ID: workID("close security-1 try 2"), Part: "close security-1", Finding: first})
	checkDecisions(t, Closure{}, []decideCase{
		{
			"engine restart gives a free next try",
			node,
			closureRows(one, store.StepRow{ID: workID("close security-1"), End: "engine_restarted"}),
			retry,
		},
		{
			"one costly end gives the next try",
			node,
			closureRows(one, store.StepRow{ID: workID("close security-1"), End: "overtime"}),
			retry,
		},
		{
			"a costly end then a closure verdict closes the finding",
			node,
			closureRows(one,
				store.StepRow{ID: workID("close security-1"), End: "overtime", Reason: "ran long"},
				store.StepRow{ID: workID("close security-1 try 2"), End: "done", Result: closureClosedReturn},
			),
			closureEnd(outcomePassed, []int64{2, 3}, store.Closure{
				Finding: "security-1", Round: closureRound, Step: workID("close security-1 try 2"),
				Result: "closed", Reason: "the fix holds",
			}),
		},
		{
			"two costly ends give an undetermined closure with the gap's reason",
			node,
			closureRows(one,
				store.StepRow{ID: workID("close security-1"), End: "overtime", Reason: "ran long"},
				store.StepRow{ID: workID("close security-1 try 2"), End: "failed", Reason: "exit 2"},
			),
			closureEnd(outcomePassed, []int64{2, 3}, store.Closure{
				Finding: "security-1", Round: closureRound, Step: workID("close security-1 try 2"),
				Result: "undetermined", Reason: "close security-1 gave no verdict twice: failed: exit 2",
			}),
		},
	})
}

func TestClosureVerdicts(t *testing.T) {
	t.Parallel()
	node := closureNode(testRev)
	first, second := closureFinding(1, "confirmed"), closureFinding(2, "confirmed")
	one := store.Review{Ledger: []store.LedgerFinding{first}}
	both := store.Review{Ledger: []store.LedgerFinding{first, second}}
	closed := store.Closure{
		Finding: "security-1", Round: closureRound, Step: workID("close security-1"), Result: "closed",
		Reason: "the fix holds",
	}
	open := store.Closure{
		Finding: "security-1", Round: closureRound, Step: workID("close security-1"), Result: "open",
		Reason: "still wrong",
	}
	checkDecisions(t, Closure{}, []decideCase{
		{
			"closed verdict closes the run passed",
			node,
			closureRows(one, store.StepRow{ID: workID("close security-1"), End: "done", Result: closureClosedReturn}),
			closureEnd(outcomePassed, []int64{2, 3}, closed),
		},
		{
			"open verdict fails the run",
			node,
			closureRows(one, store.StepRow{ID: workID("close security-1"), End: "done", Result: closureOpenReturn}),
			closureEnd(outcomeFailed, []int64{2, 3}, open),
		},
		{
			"undetermined verdict leaves the run passed",
			node,
			closureRows(one, store.StepRow{ID: workID("close security-1"), End: "done", Result: closureUndetReturn}),
			closureEnd(outcomePassed, []int64{2, 3}, store.Closure{
				Finding: "security-1", Round: closureRound, Step: workID("close security-1"),
				Result: "undetermined", Reason: "cannot tell",
			}),
		},
		{
			"result other than the three costs one end",
			node,
			closureRows(one, store.StepRow{ID: workID("close security-1"), End: "done", Result: reviewConfirmedReturn}),
			closureStarted(Unit{ID: workID("close security-1 try 2"), Part: "close security-1", Finding: first}),
		},
		{
			"each finding's closure lands in ledger order and any open one fails the run",
			node,
			closureRows(both,
				store.StepRow{ID: workID("close security-1"), End: "done", Result: closureClosedReturn},
				store.StepRow{ID: workID("close security-2"), End: "done", Result: closureOpenReturn},
			),
			closureEnd(outcomeFailed, []int64{2, 3}, closed, store.Closure{
				Finding: "security-2", Round: closureRound, Step: workID("close security-2"), Result: "open",
				Reason: "still wrong",
			}),
		},
		{
			"no open finding ends passed at once with an empty round",
			node,
			closureRows(store.Review{}),
			closureEnd(outcomePassed, []int64{2, 3}),
		},
	})
}

func TestClosureRound(t *testing.T) {
	t.Parallel()
	node := closureNode(testRev)
	// The earlier round's fix plan has mark 1, so its tasks would be 1, 2 and 3 if it bounded this round.
	withEarlier := closureRows(store.Review{})
	withEarlier.Runs = []store.NodeRun{
		{Path: "correct@1/fix", Kind: "plan", Round: 1, Mark: 1},
		{Path: closureFixPath, Kind: "plan", Round: closureRound, Mark: closureFixMark},
	}
	noPlan := closureRows(store.Review{})
	noPlan.Runs = nil
	checkDecisions(t, Closure{}, []decideCase{
		{
			"round carries the tasks above its own fix plan's mark",
			node,
			withEarlier,
			closureEnd(outcomePassed, []int64{2, 3}),
		},
		{"round with no fix plan carries no tasks", node, noPlan, closureEnd(outcomePassed, nil)},
	})
}

func TestClosureOpenFindings(t *testing.T) {
	t.Parallel()
	first, second := closureFinding(1, "confirmed"), closureFinding(2, "confirmed")
	tests := []struct {
		name   string
		ledger []store.LedgerFinding
		round  int64
		want   []store.LedgerFinding
	}{
		{
			name:   "a confirmed finding with no closure is open",
			ledger: []store.LedgerFinding{first},
			round:  closureRound,
			want:   []store.LedgerFinding{first},
		},
		{
			name:   "findings that are not confirmed are not checked",
			ledger: []store.LedgerFinding{closureFinding(1, "dropped"), closureFinding(2, "undetermined")},
			round:  closureRound,
		},
		{
			name: "a finding closed in an earlier round is not open",
			ledger: []store.LedgerFinding{closureFinding(1, "confirmed",
				store.Closure{Finding: "security-1", Round: 1, Result: "closed"})},
			round: closureRound,
		},
		{
			name: "a finding left undetermined in an earlier round is not open",
			ledger: []store.LedgerFinding{closureFinding(1, "confirmed",
				store.Closure{Finding: "security-1", Round: 1, Result: "undetermined"})},
			round: closureRound,
		},
		{
			name: "a finding left open in an earlier round stays open",
			ledger: []store.LedgerFinding{closureFinding(1, "confirmed",
				store.Closure{Finding: "security-1", Round: 1, Result: "open"})},
			round: closureRound,
			want: []store.LedgerFinding{closureFinding(1, "confirmed",
				store.Closure{Finding: "security-1", Round: 1, Result: "open"})},
		},
		{
			name: "a closure in the round itself does not settle its finding",
			ledger: []store.LedgerFinding{closureFinding(1, "confirmed",
				store.Closure{Finding: "security-1", Round: closureRound, Result: "closed"})},
			round: closureRound,
			want: []store.LedgerFinding{closureFinding(1, "confirmed",
				store.Closure{Finding: "security-1", Round: closureRound, Result: "closed"})},
		},
		{
			name:   "open findings keep ledger order",
			ledger: []store.LedgerFinding{first, closureFinding(2, "dropped"), second},
			round:  closureRound,
			want:   []store.LedgerFinding{first, second},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := OpenFindings(store.Review{Ledger: tc.ledger}, tc.round); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("OpenFindings() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
