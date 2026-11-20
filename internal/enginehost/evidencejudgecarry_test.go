package enginehost

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// carryTestStore is an EvidenceStore whose only read is EvidenceRuns, which returns runs for any epic.
type carryTestStore struct {
	EvidenceStore
	runs []store.EvidenceRun
}

// EvidenceRuns returns the runs the store was given.
func (s carryTestStore) EvidenceRuns(context.Context, string) ([]store.EvidenceRun, error) {
	return s.runs, nil
}

// carryTestRun returns a green run of Requirement R1 of evidenceRunEpic with id, at revision. It has the spec and logs of
// judgeTestRun, and no verdict.
func carryTestRun(id, revision string) store.EvidenceRun {
	run := judgeTestRun()
	run.ID = id
	run.Epic = evidenceRunEpic
	run.Item = "R1"
	run.Revision = revision
	return run
}

// carryTestPrompt starts the judge of run through an Evidence over a store whose runs are runs, and returns the prompt
// of the judge step it starts.
func carryTestPrompt(t *testing.T, run store.EvidenceRun, runs []store.EvidenceRun) string {
	t.Helper()
	steps := &taskVersionRecordingSteps{
		EvidenceSteps: &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}},
	}
	ev := &Evidence{store: carryTestStore{runs: runs}, steps: steps, blobs: evidenceBlobs{dir: judgeTestBlobDir}}
	if _, reason := ev.startJudge(t.Context(), "/repo", run, 1); reason != "" {
		t.Fatalf("startJudge refused: %s", reason)
	}
	starts := steps.recorded()
	if len(starts) != 1 {
		t.Fatalf("%d judge steps started, want one", len(starts))
	}
	return starts[0].Prompt
}

// TestJudgeCarryPromptEarlierRuns judges a green Requirement run at a third revision, after an earlier holds run and an
// earlier fails run at other revisions, each with a reason. The prompt shows each earlier run's revision, verdict and
// reason, the latest holds run's logs, and both the latest earlier revision and the judged revision.
func TestJudgeCarryPromptEarlierRuns(t *testing.T) {
	t.Parallel()
	for _, capture := range []string{"integrate", "sync", "release"} {
		t.Run(capture, func(t *testing.T) {
			t.Parallel()
			rev := func(n int) string { return fmt.Sprintf("%s-%d", capture, n) }
			holds := carryTestRun("run-1", rev(1))
			holds.Verdict, holds.JudgeReason = evidence.VerdictHolds, "the build passes at "+rev(1)
			holds.LogHash, holds.StderrHash = judgeTestHash("1"), judgeTestHash("2")
			fails := carryTestRun("run-2", rev(2))
			fails.Verdict, fails.JudgeReason = evidence.VerdictFails, "the build fails at "+rev(2)
			fails.LogHash, fails.StderrHash = judgeTestHash("3"), judgeTestHash("4")
			judged := carryTestRun("run-3", rev(3))

			prompt := carryTestPrompt(t, judged, []store.EvidenceRun{holds, fails, judged})

			wants := []string{
				"revision " + rev(1) + ": verdict holds, reason: the build passes at " + rev(1),
				"revision " + rev(2) + ": verdict fails, reason: the build fails at " + rev(2),
				"Stdout log of the latest holds run at revision " + rev(1) + ": " +
					filepath.Join(judgeTestBlobDir, judgeTestHash("1")+".log"),
				"Stderr log of the latest holds run at revision " + rev(1) + ": " +
					filepath.Join(judgeTestBlobDir, judgeTestHash("2")+".log"),
				"The latest earlier run is at revision " + rev(2) + "; the revision now judged is " + rev(3) + ".",
				"Decide whether the earlier verdict still applies given what changed between those two revisions.",
				"You may run git diff between them in your worktree",
				"Give your reason in the evidence of your result.",
			}
			for _, want := range wants {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt lacks %q:\n%s", want, prompt)
				}
			}
			failsLog := filepath.Join(judgeTestBlobDir, judgeTestHash("3")+".log")
			if strings.Contains(prompt, failsLog) {
				t.Errorf("prompt names the fails run's log %s, which is not a holds run's:\n%s", failsLog, prompt)
			}
		})
	}
}

// TestJudgeCarryPromptFirstRun judges a green Requirement run that has no earlier judged run of its item: an earlier run
// with no verdict is not judged, so the prompt is judgePrompt's alone.
func TestJudgeCarryPromptFirstRun(t *testing.T) {
	t.Parallel()
	unjudged := carryTestRun("run-1", "rev-1")
	judged := carryTestRun("run-2", "rev-2")

	prompt := carryTestPrompt(t, judged, []store.EvidenceRun{unjudged, judged})
	want, err := judgePrompt(judged, evidenceBlobs{dir: judgeTestBlobDir})
	if err != nil {
		t.Fatalf("judgePrompt: %v", err)
	}
	if prompt != want {
		t.Errorf("prompt = %q, want judgePrompt's %q", prompt, want)
	}
}

// TestJudgeCarryPromptOtherItemIgnored judges a green Requirement run whose earlier runs are of another item, and of its
// own item with another spec. Neither is listed, so the prompt is judgePrompt's alone.
func TestJudgeCarryPromptOtherItemIgnored(t *testing.T) {
	t.Parallel()
	otherItem := carryTestRun("run-1", "rev-1")
	otherItem.Item = "R2"
	otherItem.Verdict, otherItem.JudgeReason = evidence.VerdictHolds, "another item's reason"
	otherSpec := carryTestRun("run-2", "rev-2")
	otherSpec.Spec.Conclusion = "a different conclusion"
	otherSpec.Verdict, otherSpec.JudgeReason = evidence.VerdictFails, "another spec's reason"
	judged := carryTestRun("run-3", "rev-3")

	prompt := carryTestPrompt(t, judged, []store.EvidenceRun{otherItem, otherSpec, judged})
	want, err := judgePrompt(judged, evidenceBlobs{dir: judgeTestBlobDir})
	if err != nil {
		t.Fatalf("judgePrompt: %v", err)
	}
	if prompt != want {
		t.Errorf("prompt = %q, want judgePrompt's %q", prompt, want)
	}
}
