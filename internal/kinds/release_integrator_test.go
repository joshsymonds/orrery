package kinds

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// integratorResult is the Result a release prepare records for a merge of origin's commit that conflicts in README.
const integratorResult = `{"error":"","behind":true,"candidate":"","tip":"tip-1","origin":"origin-1",` +
	`"branch":"refs/heads/main","conflicts":["README"]}`

// integratorRows returns the rows of a releasing epic that holds the release slot, with one Done gate and one
// Requirement whose item captures, and the release runs, checks, evidence runs and steps given.
func integratorRows(
	releases []store.ReleaseRun, checks []store.NodeCheck, runs []store.EvidenceRun, steps []store.StepRow,
) Rows {
	epic := releasingEpic(releaseStep("tag", "none"))
	epic.DoneGates = []string{"lint"}
	epic.Requirements = []store.EpicRequirement{{ID: "R1", Item: evidence.Item{Capture: []string{"scripts/check"}}}}
	return Rows{
		Epic: epic, Slot: releaseEpicID, Releases: releases, Checks: checks, Evidence: runs, Steps: steps,
	}
}

// integratorRound returns the release run of round of the integrator at the first attempt and generation, passed at
// head or failed for reason.
func integratorRound(round int64, head, reason string) store.ReleaseRun {
	return store.ReleaseRun{
		Part: store.ReleasePartIntegrator, Position: round, Attempt: 1, Head: head, Passed: reason == "",
		Reason: reason,
	}
}

// roundWorkID returns the id of part of the checks the release walks on the commit of round of the integrator.
func roundWorkID(round int64, part string) string {
	return store.NodeWorkID(fmt.Sprintf("%s-integrator-%d", testRunID, round), 1, part)
}

// TestReleaseIntegratorDecisions checks the release integrator's walk over its rounds: a conflicting merge starts the
// first round while the epic holds the slot and is not paused, records a round whose step ended, stops on a failed
// round, walks a passed round's commit through the Done gate and then the capture, at that commit, sends a capture's
// failure back to the next round, and records the commit as the candidate once both pass.
func TestReleaseIntegratorDecisions(t *testing.T) {
	t.Parallel()
	node := releaseNode(1, "", integratorResult)
	first := attemptWorkID(1, integratorWork(1))
	second := attemptWorkID(1, integratorWork(2))
	passedGate := store.NodeCheck{ID: roundWorkID(1, "done 1"), State: store.NodeEnded}
	failedCapture := store.EvidenceRun{
		ID: roundWorkID(1, "capture R1"), Item: "R1", Phase: evidence.PhaseGreen, State: runEnded,
		NotCaptured: "the capture command is missing",
	}
	passedCapture := failedCapture
	passedCapture.NotCaptured, passedCapture.Verdict, passedCapture.Passed = "", evidence.VerdictHolds, true
	paused := integratorRows(nil, nil, nil, nil)
	paused.Epic.Paused = true
	unheld := integratorRows(nil, nil, nil, nil)
	unheld.Slot = releaseOtherEpicID
	cases := []struct {
		name string
		rows Rows
		want Action
	}{
		{
			name: "first round starts",
			rows: integratorRows(nil, nil, nil, nil),
			want: Action{Kind: ActIntegrator, IDs: []string{first}, Items: []string{"1", ""}},
		},
		{name: "paused epic waits", rows: paused, want: Action{Kind: ActNone}},
		{
			name: "slot not held stops",
			rows: unheld,
			want: Action{
				Kind: ActStop, Output: "the release integrator did not start: this epic does not hold the release slot",
			},
		},
		{
			name: "running round waits",
			rows: integratorRows(nil, nil, nil, []store.StepRow{{ID: first}}),
			want: Action{Kind: ActNone},
		},
		{
			name: "ended round is recorded",
			rows: integratorRows(nil, nil, nil, []store.StepRow{{ID: first, End: "done"}}),
			want: Action{Kind: ActRecord, IDs: []string{first}, Items: []string{store.ReleasePartIntegrator, "1"}},
		},
		{
			name: "failed round stops",
			rows: integratorRows([]store.ReleaseRun{integratorRound(1, "", "blocked: README")}, nil, nil, nil),
			want: Action{Kind: ActStop, Output: "blocked: README"},
		},
		{
			name: "passed round runs the Done gate at its commit",
			rows: integratorRows([]store.ReleaseRun{integratorRound(1, "merge-1", "")}, nil, nil, nil),
			want: Action{
				Kind: ActStart, IDs: []string{roundWorkID(1, "done 1")}, Items: []string{"lint"}, Revision: "merge-1",
			},
		},
		{
			name: "passed gate runs the capture at its commit",
			rows: integratorRows(
				[]store.ReleaseRun{integratorRound(1, "merge-1", "")}, []store.NodeCheck{passedGate}, nil, nil,
			),
			want: Action{
				Kind: ActCapture, IDs: []string{roundWorkID(1, "capture R1")}, Items: []string{"R1"},
				Revision: "merge-1",
			},
		},
		{
			name: "failed capture starts the next round with its failure",
			rows: integratorRows(
				[]store.ReleaseRun{integratorRound(1, "merge-1", "")}, []store.NodeCheck{passedGate},
				[]store.EvidenceRun{failedCapture}, nil,
			),
			want: Action{
				Kind: ActIntegrator, IDs: []string{second}, Items: []string{"2", "merge-1"},
				Output: "The capture of R1 did not run on commit merge-1: the capture command is missing.",
			},
		},
		{
			name: "passed capture records the candidate",
			rows: integratorRows(
				[]store.ReleaseRun{integratorRound(1, "merge-1", "")}, []store.NodeCheck{passedGate},
				[]store.EvidenceRun{passedCapture}, nil,
			),
			want: Action{
				Kind: ActRecord, Items: []string{store.ReleasePartCandidate, "merge-1"}, Revision: "merge-1",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := (Release{}).Decide(node, tc.rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decide = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestReleaseIntegratorLastRoundFailureStops: a capture that fails on the commit of the third round stops the release
// with the capture's cause at that commit, and no fourth round starts.
func TestReleaseIntegratorLastRoundFailureStops(t *testing.T) {
	t.Parallel()
	var releases []store.ReleaseRun
	var checks []store.NodeCheck
	var runs []store.EvidenceRun
	for round := int64(1); round <= integratorRounds; round++ {
		releases = append(releases, integratorRound(round, "merge-1", ""))
		checks = append(checks, store.NodeCheck{ID: roundWorkID(round, "done 1"), State: store.NodeEnded})
		runs = append(runs, store.EvidenceRun{
			ID: roundWorkID(round, "capture R1"), Item: "R1", Phase: evidence.PhaseGreen, State: runEnded,
			NotCaptured: "the capture command is missing",
		})
	}
	got := (Release{}).Decide(releaseNode(1, "", integratorResult), integratorRows(releases, checks, runs, nil))
	want := Action{Kind: ActStop, Output: "R1 capture failed at merge-1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decide = %+v, want %+v", got, want)
	}
}
