package build

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestBriefClosureEvidenceKind: a closure-kind task's brief shows no red block and no evidence instruction, and says
// the round's closure check settles it.
func TestBriefClosureEvidenceKind(t *testing.T) {
	t.Parallel()
	task := briefTask()
	task.Evidence = evidence.Item{Kind: evidence.KindClosure, Conclusion: "the finding is closed"}
	red := store.EvidenceRun{Exit: 1, Verdict: "fails"}
	got := Brief(briefEpic(), task, briefGate(), red, []byte("red log\n"), []byte("red stderr\n"), "")
	t.Run("its brief shows no red block and no Capture instruction", func(t *testing.T) {
		t.Parallel()
		for _, absent := range []string{
			"Red at the base", "Red log", "Red stderr", "Capture:", "Run the Capture", "Evidence:",
			"an Evidence clause left unproved",
		} {
			if strings.Contains(got, absent) {
				t.Errorf("closure brief contains %q; brief:\n%s", absent, got)
			}
		}
	})
	t.Run("its brief says the round's closure check settles it", func(t *testing.T) {
		t.Parallel()
		want := "Closure: this task runs no red and no green, and no evidence judge reads it. After the gate passes " +
			"and the reviewers judge your commit, the round's closure check settles it: it judges each open finding " +
			"of the round again on the integrated change.\n" +
			"What that check must find: the finding is closed\n"
		if !strings.Contains(got, want) {
			t.Fatalf("closure brief lacks the closure lines %q; brief:\n%s", want, got)
		}
	})
}
