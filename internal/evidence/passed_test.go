package evidence

import "testing"

func TestPassed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		phase       string
		notCaptured string
		exit        int
		verdict     string
		want        bool
	}{
		{
			name:    "red with fails passes",
			phase:   PhaseRed,
			verdict: VerdictFails,
			want:    true,
		},
		{
			name:    "red with fails and exit 1 passes, since red does not read the exit",
			phase:   PhaseRed,
			exit:    1,
			verdict: VerdictFails,
			want:    true,
		},
		{
			name:    "red with holds fails",
			phase:   PhaseRed,
			verdict: VerdictHolds,
			want:    false,
		},
		{
			name:    "green with holds and exit 0 passes",
			phase:   PhaseGreen,
			verdict: VerdictHolds,
			want:    true,
		},
		{
			name:    "green with holds and exit 1 fails",
			phase:   PhaseGreen,
			exit:    1,
			verdict: VerdictHolds,
			want:    false,
		},
		{
			name:    "green with fails fails",
			phase:   PhaseGreen,
			verdict: VerdictFails,
			want:    false,
		},
		{
			name:    "red with an empty verdict fails",
			phase:   PhaseRed,
			verdict: "",
			want:    false,
		},
		{
			name:    "green with an empty verdict fails",
			phase:   PhaseGreen,
			verdict: "",
			want:    false,
		},
		{
			name:    "red with an unknown verdict fails",
			phase:   PhaseRed,
			verdict: "maybe",
			want:    false,
		},
		{
			name:    "green with an unknown verdict fails",
			phase:   PhaseGreen,
			verdict: "maybe",
			want:    false,
		},
		{
			name:        "red not captured fails whatever the verdict",
			phase:       PhaseRed,
			notCaptured: "could not start",
			verdict:     VerdictFails,
			want:        false,
		},
		{
			name:        "green not captured fails whatever the verdict",
			phase:       PhaseGreen,
			notCaptured: "killed at its limit",
			verdict:     VerdictHolds,
			want:        false,
		},
		{
			name:    "unknown phase fails",
			phase:   "blue",
			verdict: VerdictFails,
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Passed(tc.phase, tc.notCaptured, tc.exit, tc.verdict); got != tc.want {
				t.Errorf("Passed(%q, %q, %d, %q) = %t, want %t",
					tc.phase, tc.notCaptured, tc.exit, tc.verdict, got, tc.want)
			}
		})
	}
}
