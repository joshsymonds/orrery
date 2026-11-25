package enginehost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// TestCaptureGoneFilingAddsNoWorktreeOrCapture files a captured item by each entry point that files a brief: Run, RunItem,
// RunTask and RunAmendment. The brief starts nothing: no worktree is added under the capture worktrees, and no capture is
// recorded at the commit, so no gate starts and no grant is taken. A gate runs only in a worktree of a capture the store
// holds.
func TestCaptureGoneFilingAddsNoWorktreeOrCapture(t *testing.T) {
	t.Parallel()
	amended := evidenceRunItem()
	amended.Conclusion = "the amended build passes"
	for _, filing := range evidenceBriefFilings(amended) {
		t.Run(filing.name, func(t *testing.T) {
			t.Parallel()
			repo, head := evidenceRunRepo(t, "exit 0\n")
			st := epicTestStore(t)
			evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
			ev, stateDir := evidenceBriefNew(t, st, &evidenceRunSteps{}, &evidenceBriefGatherers{})

			if _, err := filing.file(t.Context(), ev, head, evidence.PhaseGreen); err != nil {
				t.Fatalf("%s: %v", filing.name, err)
			}
			evidenceBriefWantNoWorktree(t, stateDir)
			captures, err := st.CapturesAt(t.Context(), repo, head)
			if err != nil {
				t.Fatalf("CapturesAt: %v", err)
			}
			if len(captures) != 0 {
				t.Fatalf("%s recorded %d captures at %s, want none, so no grant is taken",
					filing.name, len(captures), head)
			}
		})
	}
}

// TestCaptureGoneCaptureThroughCapturesAddsItsWorktreeAndTakesItsGrant asks for a capture of a captured brief through
// Captures, and holds its gate until the test releases it. While the gate runs, the capture's worktree is under the capture
// worktrees and its grant is stored, so a capture started through Captures is the path that adds a worktree and takes a
// grant.
func TestCaptureGoneCaptureThroughCapturesAddsItsWorktreeAndTakesItsGrant(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60)
	rig := captureTestNewRig(t, st, "")
	c := rig.captures(t)
	release := filepath.Join(t.TempDir(), "release")

	capture, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: head, Argv: []string{"/bin/sh", "-c", "while [ ! -e " + release + " ]; do sleep 0.05; done; echo done"},
		Briefs: briefs,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	waittest.UntilIdle(t, c.workFinished, "the grant of the capture is stored", func() bool {
		return captureTestStored(t, st, capture.ID).Grant != ""
	})
	if _, err = os.Stat(filepath.Join(rig.stateDir, "evidence-worktrees", capture.ID)); err != nil {
		t.Fatalf("worktree of capture %s while its gate runs: %v, want it added", capture.ID, err)
	}
	if n := len(rig.pools.requested()); n != 1 {
		t.Fatalf("%d grant requests for the capture, want its one", n)
	}
	if err = os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	captureTestDelivered(t, c, rig.deliveries, 1)
}
