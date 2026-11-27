package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// projectReleaseSteps are the four release steps of orrery's own project file, each with its step text, target and undo.
func projectReleaseSteps() []EpicReleaseStep {
	return []EpicReleaseStep{
		{
			Step: "1 Fast-forward origin's main to the epic's release candidate and push it, so main holds the change; do not " +
				"wait for CI. A refused push stops the release, and the step never merges, rebases or force-pushes.",
			Target: "github.com/joshsymonds/orrery, branch main",
			Undo:   "Push a revert of the change to main.",
		},
		{
			Step: "2 Push a signed annotated tag at the candidate, named the next patch version above the newest v* tag, with a " +
				"message naming the change. A newer v* tag on origin that contains the candidate meets the step.",
			Target: "github.com/joshsymonds/orrery, the new tag",
			Undo:   "git push --delete origin <tag> and delete the local tag.",
		},
		{
			Step: "3 Pin ~/nix-config's orrery input to the newest v* tag that contains the candidate, in flake.nix and " +
				"flake.lock, and push the pin to nix-config's origin main; never force-push.",
			Target: "~/nix-config, branch main on its origin",
			Undo:   "Commit flake.nix and flake.lock back to the previous tag.",
		},
		{
			Step: "4 Bring ~/nix-config up to its origin main, run `update`, and check that orreryd and orrery-serve run a " +
				"build that contains the candidate.",
			Target: "vermissian user services and the orrery store",
			Undo: "Undo step 3, then run `update`; when the tag added a store migration, fix forward with a new release " +
				"instead, since an older orreryd refuses the migrated store.",
		},
	}
}

// landedLine returns the line a release stop names a step that landed with: its number, target and undo, in the form the
// release step stop takes.
func landedLine(number int, step EpicReleaseStep) string {
	return fmt.Sprintf("- step %d (target %s), undo: %s", number, step.Target, step.Undo)
}

// longFailure returns the output of a failed check of 20 lines of 90 bytes each, under the stderr header a release stop
// gives it, so the stop text outruns TurnReasonMax.
func longFailure() string {
	lines := []string{"the step exited 1", "stderr:"}
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("stderr line %d: %s", i, strings.Repeat("x", 90)))
	}
	return strings.Join(lines, "\n")
}

// stopAtStepFour stops the release of claimEpic at its step 4, which failed with failure after steps 1 to 3 landed, and
// returns the stop text. The text is the first line naming the step by number and target, then the landed steps, then
// failure, as the release step stop gives it.
func stopAtStepFour(t *testing.T, s *Store, steps []EpicReleaseStep, failure string) string {
	t.Helper()
	ctx := context.Background()
	lines := make([]string, 0, 5)
	lines = append(lines, fmt.Sprintf("release step 4 (target %s) failed:", steps[3].Target), "landed:")
	for i := range 3 {
		lines = append(lines, landedLine(i+1, steps[i]))
	}
	stop := strings.Join(lines, "\n") + "\n" + failure
	if err := s.RecordRelease(ctx, claimEpic, ReleaseRun{
		Part: ReleasePartStep, Position: 4, Attempt: 1, Step: steps[3].Step, Reason: failure, At: claimedAt(),
	}); err != nil {
		t.Fatalf("RecordRelease of the failed step: %v", err)
	}
	if err := s.StopRelease(ctx, claimEpic, "release", 1, stop, claimedAt()); err != nil {
		t.Fatalf("StopRelease at the failed step: %v", err)
	}
	return stop
}

// TestStopSaysParkKeepsLandedUnderCap checks that a release stopped at step 4 of orrery's own project file, after steps 1
// to 3 landed and with a long failed check, holds every landed step's number, target and undo whole in the park's reason
// and in its tell. The stop text is longer than TurnReasonMax, so the cut takes the check's output and not the landed list.
func TestStopSaysParkKeepsLandedUnderCap(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	steps := projectReleaseSteps()
	startRelease(t, s, steps)
	stop := stopAtStepFour(t, s, steps, longFailure())
	if len(stop) <= TurnReasonMax {
		t.Fatalf("stop text is %d bytes, want more than TurnReasonMax %d so the cap applies", len(stop), TurnReasonMax)
	}

	epic := closeRow(t, s, claimEpic)
	tells := attentionTellsOf(t, s)
	if len(tells) != 1 {
		t.Fatalf("tells = %+v, want the one tell of the stop", tells)
	}
	for i := range 3 {
		line := landedLine(i+1, steps[i])
		if !strings.Contains(epic.Parked, line) {
			t.Errorf("park reason = %q, want it to hold %q whole", epic.Parked, line)
		}
		if !strings.Contains(tells[0].Text, line) {
			t.Errorf("tell = %q, want it to hold %q whole", tells[0].Text, line)
		}
	}
}

// TestStopSaysTriggerCarriesFullText checks that the pending trigger a release_step stop adds carries the whole stop text as
// its cause, though the text is longer than TurnReasonMax: the planner's prompt shows a trigger's cause in full.
func TestStopSaysTriggerCarriesFullText(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	steps := projectReleaseSteps()
	startRelease(t, s, steps)
	stop := stopAtStepFour(t, s, steps, longFailure())
	if len(stop) <= TurnReasonMax {
		t.Fatalf("stop text is %d bytes, want more than TurnReasonMax %d so the cap applies", len(stop), TurnReasonMax)
	}

	if !slices.Contains(pendingCauses(t, s), stop) {
		t.Errorf(
			"pending triggers = %v, want one whose cause is the whole stop text of %d bytes",
			pendingCauses(t, s),
			len(stop),
		)
	}
}
