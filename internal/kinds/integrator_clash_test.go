package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// clashRound returns the recorded round of task 2 of the test batch at round that clashed with explanation, whose step ran.
func clashRound(round int64, explanation string) store.IntegrateRound {
	record := recordedRound(round, RoundClash, "", "", "")
	record.Explanation = explanation
	return record
}

// endedRound returns the recorded round of task 2 of the test batch at round whose step ended with explanation.
func endedRound(round int64, explanation string) store.IntegrateRound {
	record := recordedRound(round, RoundEnded, "", "", "")
	record.Explanation = explanation
	return record
}

// ranStep returns the row of the step of round of task 2 of the test batch, which ended done.
func ranStep(round int64) store.StepRow {
	return store.StepRow{ID: stepWork(2, round), End: "done"}
}

// TestIntegratorClashEndingNamesHowTheRoundsEnded: the rounds of a conflicting task end with the explanation of the round
// that clashed or whose step ended without a done return, or with the failure of the third round. A clash after a failed
// round names the clash, the round it ended the rounds at.
func TestIntegratorClashEndingNamesHowTheRoundsEnded(t *testing.T) {
	t.Parallel()
	run := batchNode("cand-1", conflictLeftOut, true).Run
	cases := []struct {
		name   string
		rounds []store.IntegrateRound
		steps  []store.StepRow
		want   string
	}{
		{
			name:   "a clash names its explanation",
			rounds: []store.IntegrateRound{clashRound(1, "both sides decide the title")},
			steps:  []store.StepRow{ranStep(1)},
			want:   "task integrator round 1 clashed: both sides decide the title",
		},
		{
			name:   "an ended step names how it ended",
			rounds: []store.IntegrateRound{endedRound(1, "task integrator ended stopped")},
			steps:  []store.StepRow{ranStep(1)},
			want:   "task integrator round 1 ended: task integrator ended stopped",
		},
		{
			name: "a failed third round names its failure",
			rounds: []store.IntegrateRound{
				recordedRound(1, RoundMerged, "m1", outcomeFailed, "one"),
				recordedRound(2, RoundMerged, "m2", outcomeFailed, "two"),
				recordedRound(3, RoundMerged, "m3", outcomeFailed, "three"),
			},
			steps: []store.StepRow{ranStep(1), ranStep(2), ranStep(3)},
			want:  "task integrator round 3 failed: three",
		},
		{
			name: "a clash after a failed round names the clash",
			rounds: []store.IntegrateRound{
				recordedRound(1, RoundMerged, "m1", outcomeFailed, "one"),
				clashRound(2, "both sides decide the title"),
			},
			steps: []store.StepRow{ranStep(1), ranStep(2)},
			want:  "task integrator round 2 clashed: both sides decide the title",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := TaskEnding(Rows{Rounds: tc.rounds, Steps: tc.steps}, run, 2)
			if got != tc.want {
				t.Errorf("TaskEnding = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIntegratorClashEndingLeavesOutRoundsThatDidNotEnd: no ending is named while the rounds are undecided, when the task
// was composed, or when a round's step never started. A round whose step cannot start leaves its task out as it was
// before, so its reason is not extended.
func TestIntegratorClashEndingLeavesOutRoundsThatDidNotEnd(t *testing.T) {
	t.Parallel()
	run := batchNode("cand-1", conflictLeftOut, true).Run
	cases := []struct {
		name   string
		rounds []store.IntegrateRound
		steps  []store.StepRow
	}{
		{name: "no round is recorded"},
		{
			name: "a failed round is followed by an undecided one",
			rounds: []store.IntegrateRound{
				recordedRound(1, RoundMerged, "m1", outcomeFailed, "one"),
				recordedRound(2, RoundMerged, "m2", "", ""),
			},
			steps: []store.StepRow{ranStep(1), ranStep(2)},
		},
		{
			name:   "a round that composed the task is no ending",
			rounds: []store.IntegrateRound{recordedRound(1, RoundMerged, "m1", "composed", "")},
			steps:  []store.StepRow{ranStep(1)},
		},
		{
			name:   "an ended round whose step never started is no ending",
			rounds: []store.IntegrateRound{endedRound(1, "the task integrator did not start: no state directory")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TaskEnding(Rows{Rounds: tc.rounds, Steps: tc.steps}, run, 2); got != "" {
				t.Errorf("TaskEnding = %q, want none", got)
			}
		})
	}
}
