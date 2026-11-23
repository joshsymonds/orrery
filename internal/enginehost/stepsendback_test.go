package enginehost

import (
	"context"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepSendBackOwnedLine commits an owned file and two files outside the owned set to a real repository, and returns the
// line the step loop sends back for the failed owned check.
func stepSendBackOwnedLine(t *testing.T) string {
	t.Helper()
	dir := stepGitTestRepo(t)
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
	commit := stepGitTestCommit(t, dir, map[string]string{
		"owned/a.go": "owned\n",
		"other/x.go": "stray\n",
		"other/y.go": "stray\n",
	})
	checks := commitChecks(context.Background(), hostexec.NewLocal(), dir, start, commit, []string{"owned/a.go"})
	owned := stepGitTestCheck(t, checks, stepGitOwnedName)
	if owned.OK {
		t.Fatalf("owned passed though other/x.go and other/y.go are not owned: %s", owned.Detail)
	}
	return stepLoopFailures([]store.StepCheck{stepLoopRecord(1, owned)})[0]
}

func TestStepSendBackOwnedLine(t *testing.T) {
	t.Parallel()
	t.Run("the owned send-back names the outside files", func(t *testing.T) {
		t.Parallel()
		line := stepSendBackOwnedLine(t)
		for _, path := range []string{"other/x.go", "other/y.go"} {
			if !strings.Contains(line, path) {
				t.Errorf("send-back %q does not name %s", line, path)
			}
		}
		if strings.Contains(line, "owned/a.go") {
			t.Errorf("send-back %q names owned/a.go, which is owned", line)
		}
	})
	t.Run("the owned send-back says to take them out of the commit", func(t *testing.T) {
		t.Parallel()
		line := stepSendBackOwnedLine(t)
		if !strings.Contains(line, "take those files out of the commit") {
			t.Errorf("send-back %q does not say to take the files out of the commit", line)
		}
	})
	t.Run("the owned send-back says the work is kept", func(t *testing.T) {
		t.Parallel()
		line := stepSendBackOwnedLine(t)
		if !strings.Contains(line, "the work in the worktree is kept") {
			t.Errorf("send-back %q does not say the work is kept", line)
		}
	})
}
