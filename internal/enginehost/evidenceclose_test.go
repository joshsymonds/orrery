package enginehost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceCloseScript returns a capture that writes its pid to pidFile and sleeps a minute, so its gate runs until a
// test ends it.
func evidenceCloseScript(pidFile string) string {
	return fmt.Sprintf("echo $$ > %q\nsleep 60\n", pidFile)
}

// evidenceCloseNew returns an Evidence over st and steps whose captures hold their gates in stateRoot, under the
// systemd-run scope when scope is not "". Close runs at cleanup, after any holder the test ends.
func evidenceCloseNew(t *testing.T, st *store.Store, steps EvidenceSteps, stateRoot, scope string) *Evidence {
	t.Helper()
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), stepGatePoolsText("gate"))
	ev := NewEvidence(EvidenceConfig{
		Store: st, Steps: steps, Pools: &fakeGatePools{}, Environ: gateEnv("XDG_CONFIG_HOME=" + home),
		StateDir: t.TempDir(), GateStateDir: stateRoot, GateScope: scope,
	})
	t.Cleanup(ev.Close)
	return ev
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

// evidenceCloseStart stores an epic whose R1 is a captured item of a capture that writes its pid to pidFile, and starts
// a green run of R1 through ev. It returns the run Run stored and the gate's pid once the gate runs.
func evidenceCloseStart(t *testing.T, st *store.Store, ev *Evidence, pidFile string) (store.EvidenceRun, int) {
	t.Helper()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"capture.sh": evidenceCloseScript(pidFile)})
	item := evidenceRunItem()
	evidenceRunSeed(t, st, repo, item, item)
	started, err := ev.Run(t.Context(), evidenceRunEpic, "R1", head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return started, stepCtlAwaitGatePid(t, pidFile)
}

// TestEvidenceCloseWithGateScopeDetachesARunningCapture closes an Evidence under a gate scope while its capture's gate
// runs. The gate keeps running under its holder, and the run stays capturing with its grant stored and nothing more
// recorded.
func TestEvidenceCloseWithGateScopeDetachesARunningCapture(t *testing.T) {
	t.Parallel()
	stateRoot := gateShortStateDir(t)
	st := epicTestStore(t)
	steps := &evidenceRunSteps{}
	ev := evidenceCloseNew(t, st, steps, stateRoot, fakeSystemdRunPath(t))
	started, pid := evidenceCloseStart(t, st, ev, filepath.Join(t.TempDir(), "gate.pid"))
	evidenceCloseEndHolder(t, stateRoot, started.ID)

	ev.Close()

	if gateProcessGone(pid) {
		t.Fatalf("gate process %d is gone after Close under a gate scope, want it running under its holder", pid)
	}
	got, err := st.EvidenceRun(t.Context(), started.ID)
	if err != nil {
		t.Fatalf("read evidence run after Close: %v", err)
	}
	if got.State != "capturing" || got.Grant == "" || got.NotCaptured != "" || got.LogHash != "" ||
		got.JudgeStep != "" || got.Judges != 0 || got.Verdict != "" || steps.startCount() != 0 {
		t.Fatalf(
			"run after Close = state %q grant %q not captured %q log %q judge %q judges %d verdict %q starts %d, "+
				"want capturing with a grant and nothing more recorded",
			got.State,
			got.Grant,
			got.NotCaptured,
			got.LogHash,
			got.JudgeStep,
			got.Judges,
			got.Verdict,
			steps.startCount(),
		)
	}
}

// TestEvidenceCloseWithoutGateScopeEndsARunningCapture closes an Evidence without a gate scope while its capture's gate
// runs. The cancel kills the gate, so its process and its holder directory are gone.
func TestEvidenceCloseWithoutGateScopeEndsARunningCapture(t *testing.T) {
	t.Parallel()
	stateRoot := gateShortStateDir(t)
	st := epicTestStore(t)
	ev := evidenceCloseNew(t, st, &evidenceRunSteps{}, stateRoot, "")
	started, pid := evidenceCloseStart(t, st, ev, filepath.Join(t.TempDir(), "gate.pid"))
	evidenceCloseEndHolder(t, stateRoot, started.ID)

	ev.Close()

	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, stateRoot, "evidence-"+started.ID))
}
