package enginehost

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// TestStepPurposeJudgeStartNamesPhasePlaceAndItem: the judge step of a red run starts with a purpose that names red and
// base, and the judge step of a green run one that names green and head, each with the item the run judges.
func TestStepPurposeJudgeStartNamesPhasePlaceAndItem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		phase string
		want  string
	}{
		{name: "red run at base", phase: evidence.PhaseRed, want: "Judge T3's red evidence at base"},
		{name: "green run at head", phase: evidence.PhaseGreen, want: "Judge T3's green evidence at head"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := judgeTestRun()
			run.Item = "T3"
			run.Phase = tc.phase
			if got := judgeStart(run, "/repo", "judge the run").Purpose; got != tc.want {
				t.Errorf("judge step purpose = %q, want %q", got, tc.want)
			}
		})
	}
}
