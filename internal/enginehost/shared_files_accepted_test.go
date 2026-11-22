package enginehost

import (
	"context"
	"strings"
	"testing"
)

// TestSharedFilesAcceptedOwnedCheckStillFails: a file another task holds is accepted in a task's own files, but the owned
// check reads only the task's own files. A commit that changes the shared file outside the task's files fails the owned
// check and names it; the same commit passes once the task lists the file among its own.
func TestSharedFilesAcceptedOwnedCheckStillFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := stepGitTestRepo(t)
	start := stepGitTestRun(t, dir, "rev-parse", "HEAD")
	commit := stepGitTestCommit(t, dir, map[string]string{
		"mine.go":   "mine\n",
		"shared.go": "held by another task\n",
	})

	outside := stepGitTestCheck(t, commitChecks(ctx, dir, start, commit, []string{"mine.go"}), "owned")
	if outside.OK {
		t.Fatalf("owned passed though shared.go is outside the task's files: %s", outside.Detail)
	}
	if !strings.Contains(outside.Detail, "shared.go") {
		t.Errorf("owned detail %q does not name shared.go", outside.Detail)
	}

	own := stepGitTestCheck(t, commitChecks(ctx, dir, start, commit, []string{"mine.go", "shared.go"}), "owned")
	if !own.OK {
		t.Errorf("owned failed though the task lists shared.go among its own files: %s", own.Detail)
	}
}
