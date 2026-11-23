package enginehost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// evidenceCloseScript returns a capture that writes its pid to pidFile and sleeps a minute, so its gate runs until a
// test ends it.
func evidenceCloseScript(pidFile string) string {
	return fmt.Sprintf("echo $$ > %q\nsleep 60\n", pidFile)
}

// evidenceCloseEndHolder registers a cleanup that ends the holder of run id's capture under stateRoot, if one is left.
// A test that detaches the gate then leaves no process or holder directory behind.
func evidenceCloseEndHolder(t *testing.T, stateRoot, id string) {
	t.Helper()
	dir := holderDir(t, stateRoot, "evidence-"+id)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		if _, err := os.Stat(dir); err != nil {
			return
		}
		if err := gateEnd(ctx, dir); err != nil {
			t.Errorf("end gate holder %s: %v", dir, err)
		}
	})
}

// evidenceCloseStart files a brief for a green run of R1 through ev, whose capture writes its pid to pidFile and sleeps. Then
// captureTestStep asks c for that capture of the brief. It returns the brief, the capture as Capture returned it, and the
// gate's pid once the gate runs.
func evidenceCloseStart(
	t *testing.T, st *store.Store, ev *Evidence, c *Captures, pidFile string,
) (store.EvidenceRun, store.Capture, int) {
	t.Helper()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"capture.sh": evidenceCloseScript(pidFile)})
	item := evidenceRunItem()
	evidenceRunSeed(t, st, repo, item, item)
	brief, err := ev.Run(t.Context(), evidenceRunEpic, "R1", head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("Run(R1): %v", err)
	}
	capture, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: head, Argv: item.Capture, Briefs: []string{brief.ID}, Files: item.Artifacts,
	})
	if err != nil {
		t.Fatalf("Capture of brief %s: %v", brief.ID, err)
	}
	waittest.UntilIdle(t, c.workFinished, "the capture's gate to start", func() bool {
		_, ok := stepCtlGatePid(pidFile)
		return ok
	})
	pid, _ := stepCtlGatePid(pidFile)
	return brief, capture, pid
}

// TestCapturesCloseWithGateScopeDetachesARunningCapture closes the captures under a gate scope while a capture's gate runs.
// The gate keeps running under its holder, and the capture stays running with its grant stored and nothing more recorded: its
// brief is not attached, and nothing ends.
func TestCapturesCloseWithGateScopeDetachesARunningCapture(t *testing.T) {
	t.Parallel()
	stateRoot := gateShortStateDir(t)
	st := epicTestStore(t)
	steps := &evidenceRunSteps{}
	ev := evidenceRunNew(t, st, steps)
	rig := captureTestNewRig(t, captureTestStore{Store: st}, fakeSystemdRunPath(t))
	rig.gateRoot = stateRoot
	c := rig.captures(t)
	brief, capture, pid := evidenceCloseStart(t, st, ev, c, filepath.Join(t.TempDir(), "gate.pid"))
	evidenceCloseEndHolder(t, stateRoot, capture.ID)

	c.Close()

	if gateProcessGone(pid) {
		t.Fatalf("gate process %d is gone after Close under a gate scope, want it running under its holder", pid)
	}
	got, err := st.Capture(t.Context(), capture.ID)
	if err != nil {
		t.Fatalf("read capture after Close: %v", err)
	}
	run, err := st.EvidenceRun(t.Context(), brief.ID)
	if err != nil {
		t.Fatalf("read brief after Close: %v", err)
	}
	if got.State != "running" || got.Grant == "" || !got.Ended.IsZero() || got.NotCaptured != "" || got.LogHash != "" ||
		run.State != store.RunWaiting || run.Capture != "" || run.Verdict != "" || steps.startCount() != 0 {
		t.Fatalf(
			"capture after Close = state %q grant %q ended %v not captured %q log %q; brief = state %q capture %q "+
				"verdict %q; starts %d, want the capture running with a grant and the brief waiting with nothing more recorded",
			got.State, got.Grant, got.Ended, got.NotCaptured, got.LogHash, run.State, run.Capture, run.Verdict,
			steps.startCount(),
		)
	}
}

// TestCapturesCloseWithoutGateScopeEndsARunningCapture closes the captures without a gate scope while a capture's gate runs. The
// cancel kills the gate, so its process and its holder directory are gone.
func TestCapturesCloseWithoutGateScopeEndsARunningCapture(t *testing.T) {
	t.Parallel()
	stateRoot := gateShortStateDir(t)
	st := epicTestStore(t)
	ev := evidenceRunNew(t, st, &evidenceRunSteps{})
	rig := captureTestNewRig(t, captureTestStore{Store: st}, "")
	rig.gateRoot = stateRoot
	c := rig.captures(t)
	_, capture, pid := evidenceCloseStart(t, st, ev, c, filepath.Join(t.TempDir(), "gate.pid"))
	evidenceCloseEndHolder(t, stateRoot, capture.ID)

	c.Close()

	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, stateRoot, "evidence-"+capture.ID))
}
