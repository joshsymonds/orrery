package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// integratorResult is the Result a release prepare records for a merge of origin's commit that conflicts in README, and
// cleanResult the one it records for a merge of origin's commit that git makes with no conflict.
const (
	integratorResult = `{"error":"","behind":true,"candidate":"","tip":"tip-1","origin":"origin-1",` +
		`"branch":"refs/heads/main","conflicts":["README"]}`
	cleanResult = `{"error":"","behind":true,"candidate":"","tip":"tip-1","origin":"origin-1",` +
		`"branch":"refs/heads/main"}`
)

// integratorRows returns the rows of a releasing epic, with one Done gate and one Requirement whose item captures, and
// the release runs and steps given.
func integratorRows(releases []store.ReleaseRun, steps []store.StepRow) Rows {
	epic := releasingEpic(releaseStep("tag", "none"))
	epic.DoneGates = []string{"lint"}
	epic.Requirements = []store.EpicRequirement{{ID: "R1", Item: evidence.Item{Capture: []string{"scripts/check"}}}}
	return Rows{Epic: epic, Releases: releases, Steps: steps}
}

// integratorRound returns the release run of the integrator's round at position of the first attempt, passed at head
// or failed for reason.
func integratorRound(position int64, head, reason string) store.ReleaseRun {
	return store.ReleaseRun{
		Part: store.ReleasePartIntegrator, Position: position, Attempt: 1, Head: head, Passed: reason == "",
		Reason: reason,
	}
}

// TestReleaseIntegratorDecisions checks the release integrator's one round, for a merge of origin's commit that
// conflicts and for one git makes cleanly: it starts while the epic is neither paused nor parked, records its step once
// that ended, stops on a failed round, and records a passed round's commit as the candidate at that commit, with no
// Done gate or capture started, though the epic has both.
func TestReleaseIntegratorDecisions(t *testing.T) {
	t.Parallel()
	id := attemptWorkID(1, integratorWork)
	paused := integratorRows(nil, nil)
	paused.Epic.Paused = true
	parked := integratorRows(nil, nil)
	parked.Epic.Parked = "a gate's holder is gone"
	stop := store.ReleaseRun{Part: store.ReleasePartStop, Attempt: 1, Reason: "owner stopped the release"}
	stopped := integratorRows([]store.ReleaseRun{stop}, nil)
	cases := []struct {
		name string
		rows Rows
		want Action
	}{
		{
			name: "the round starts",
			rows: integratorRows(nil, nil),
			want: Action{Kind: ActIntegrator, IDs: []string{id}, Items: []string{"1"}},
		},
		{name: "paused epic waits", rows: paused, want: Action{Kind: ActNone}},
		{name: "parked epic waits", rows: parked, want: Action{Kind: ActNone}},
		{
			name: "owner stop stops before the round starts",
			rows: stopped,
			want: Action{
				Kind: ActStop, Output: "the release integrator did not start: the owner stopped the release",
			},
		},
		{
			name: "running round waits",
			rows: integratorRows(nil, []store.StepRow{{ID: id}}),
			want: Action{Kind: ActNone},
		},
		{
			name: "ended round is recorded",
			rows: integratorRows(nil, []store.StepRow{{ID: id, End: "done"}}),
			want: Action{Kind: ActRecord, IDs: []string{id}, Items: []string{store.ReleasePartIntegrator, "1"}},
		},
		{
			name: "failed round stops",
			rows: integratorRows([]store.ReleaseRun{integratorRound(1, "", "blocked: README")}, nil),
			want: Action{Kind: ActStop, Output: "blocked: README"},
		},
		{
			name: "passed round records its commit as the candidate with no gate or capture",
			rows: integratorRows([]store.ReleaseRun{integratorRound(1, "merge-1", "")}, nil),
			want: Action{
				Kind: ActRecord, Items: []string{store.ReleasePartCandidate, "merge-1"}, Revision: "merge-1",
			},
		},
	}
	merges := []struct{ name, result string }{
		{name: "conflicting merge", result: integratorResult},
		{name: "clean merge", result: cleanResult},
	}
	for _, merge := range merges {
		node := releaseNode(1, "", merge.result)
		for _, tc := range cases {
			t.Run(merge.name+": "+tc.name, func(t *testing.T) {
				t.Parallel()
				if got := (Release{}).Decide(node, tc.rows); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("Decide = %+v, want %+v", got, tc.want)
				}
			})
		}
	}
}

// TestReleaseIntegratorRoundOfEachGeneration checks that the round of a candidate prepared again runs under an id that
// carries its generation and is read at the position the stride gives it, generation*3+1, where a store that ran up to
// three rounds a generation recorded its first. A round recorded for the candidate it replaces, at an earlier position,
// decides nothing, and neither does a later round an earlier orreryd recorded for that candidate.
func TestReleaseIntegratorRoundOfEachGeneration(t *testing.T) {
	t.Parallel()
	replaced := []store.ReleaseRun{
		integratorRound(1, "merge-1", ""), candidateRun(1, "merge-1"), staleRun(1, "merge-1"),
	}
	id := store.NodeWorkID(store.ReleaseGatedID(testRunID, 1), 1, integratorWork)
	node := releaseNode(1, "", cleanResult)
	cases := []decideCase{
		{
			"the round of a candidate prepared again starts under its generation at position 4",
			node,
			integratorRows(replaced, nil),
			Action{Kind: ActIntegrator, IDs: []string{id}, Items: []string{"4"}},
		},
		{
			"a later round of the replaced generation is not the new candidate's round",
			node,
			integratorRows(append(replaced, integratorRound(2, "merge-2", "")), nil),
			Action{Kind: ActIntegrator, IDs: []string{id}, Items: []string{"4"}},
		},
		{
			"the passed round of the new generation records its commit as the candidate",
			node,
			integratorRows(append(replaced, integratorRound(4, "merge-4", "")), nil),
			Action{Kind: ActRecord, Items: []string{store.ReleasePartCandidate, "merge-4"}, Revision: "merge-4"},
		},
	}
	checkDecisions(t, Release{}, cases)
}
