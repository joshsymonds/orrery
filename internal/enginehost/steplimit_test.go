package enginehost

import (
	"testing"
	"time"
)

// TestStepsStartStoresModelLimit covers R1: the StartStep limit on model time is accepted and stored, and zero means
// the 60 minute default. Both writing and read-only starts store the resolved limit on their step row.
func TestStepsStartStoresModelLimit(t *testing.T) {
	cases := []struct {
		name     string
		limit    time.Duration
		want     time.Duration
		readOnly bool
	}{
		{name: "writing zero is the default", limit: 0, want: 60 * time.Minute},
		{name: "writing 90 seconds is stored", limit: 90 * time.Second, want: 90 * time.Second},
		{name: "read-only zero is the default", limit: 0, want: 60 * time.Minute, readOnly: true},
		{name: "read-only 90 seconds is stored", limit: 90 * time.Second, want: 90 * time.Second, readOnly: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepStartFixture(t)
			repo := stepGitTestRepo(t)
			var start StepStart
			if tc.readOnly {
				revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
				start = stepStartReadOnly(repo, revision)
			} else {
				start = stepStartWriting(stepWorktreeTestAddLinked(t, repo))
			}
			start.ModelLimit = tc.limit

			id, err := f.steps.Start(t.Context(), start)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			row, err := f.steps.Status(t.Context(), id)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if row.ModelLimit != tc.want {
				t.Fatalf("ModelLimit = %v, want %v", row.ModelLimit, tc.want)
			}
		})
	}
}
