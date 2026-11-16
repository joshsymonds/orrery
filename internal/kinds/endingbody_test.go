package kinds

import "testing"

// TestEndingBody asserts that the body of an ending's notice names its outcome and every gap, in the order given.
func TestEndingBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		phase string
		gaps  []string
		want  string
	}{
		{
			name:  "released with no gaps",
			phase: "released",
			want:  "Released. No gaps.",
		},
		{
			name:  "done with no gaps",
			phase: "done",
			want:  "Done. No gaps.",
		},
		{
			name:  "ended with one gap",
			phase: "ended_with_gaps",
			gaps:  []string{"R2: lineage store-x exhausted"},
			want:  "Ended with 1 gap:\n- R2: lineage store-x exhausted",
		},
		{
			name:  "ended with two gaps",
			phase: "ended_with_gaps",
			gaps:  []string{"integration-1 open after two rounds", "security gave no verdict twice"},
			want:  "Ended with 2 gaps:\n- integration-1 open after two rounds\n- security gave no verdict twice",
		},
		{
			name:  "other phase with no gaps",
			phase: "stopped",
			want:  "Ended stopped. No gaps.",
		},
		{
			name:  "other phase with one gap",
			phase: "stopped",
			gaps:  []string{"R4: store-y unreachable"},
			want:  "Ended stopped with 1 gap:\n- R4: store-y unreachable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := EndingBody(tt.phase, tt.gaps); got != tt.want {
				t.Fatalf("EndingBody(%q, %q) = %q, want %q", tt.phase, tt.gaps, got, tt.want)
			}
		})
	}
}
