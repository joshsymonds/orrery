package store

import "testing"

// TestIntegratedReadsTheClaimingRunOfTheTask proves a task is integrated only when the integrate run its batch names
// ended passed and composed it.
func TestIntegratedReadsTheClaimingRunOfTheTask(t *testing.T) {
	t.Parallel()
	composed := `{"composed":[{"task":5}]}`
	runs := []NodeRun{
		{Path: "passed", Kind: integrateKind, State: NodeEnded, Outcome: nodePassedOutcome, Result: composed},
		{Path: "failed", Kind: integrateKind, State: NodeEnded, Outcome: "failed", Result: composed},
		{Path: "running", Kind: integrateKind, State: NodeRunning, Result: composed},
		{
			Path:    "left-out",
			Kind:    integrateKind,
			State:   NodeEnded,
			Outcome: nodePassedOutcome,
			Result:  `{"composed":[]}`,
		},
		{Path: "not-integrate", Kind: "gate", State: NodeEnded, Outcome: nodePassedOutcome, Result: composed},
	}
	cases := []struct {
		batch string
		want  bool
	}{
		{"", false},
		{"passed", true},
		{"failed", false},
		{"running", false},
		{"left-out", false},
		{"not-integrate", false},
		{"absent", false},
	}
	for _, tc := range cases {
		if got := Integrated(runs, EpicTask{Seq: 5, Batch: tc.batch}); got != tc.want {
			t.Errorf("Integrated with batch %q = %v, want %v", tc.batch, got, tc.want)
		}
	}
}
