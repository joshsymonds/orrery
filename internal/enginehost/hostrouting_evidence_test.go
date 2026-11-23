package enginehost

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
	"github.com/joshsymonds/orrery/internal/store"
)

// hostRoutingEvidenceScript is the capture the routing test runs. It writes out.txt, which the run keeps as an artifact.
const hostRoutingEvidenceScript = "printf kept > out.txt\n"

// hostRoutingEvidenceFixture is an Evidence and a NodeChecks over one store, in which hostRoutingEpic is stored on host b.
// Hosts a and b are recorders over the local host. The judge of an evidence run sends on judging as it starts, which is
// after the run's capture is recorded, and each node check's end is reported on ended.
type hostRoutingEvidenceFixture struct {
	repo, head string
	stateDir   string
	a, b       *hostexectest.Recorder
	ev         *Evidence
	checks     *NodeChecks
	judging    chan struct{}
	ended      chan error
}

// newHostRoutingEvidenceFixture returns a fixture over a scratch repository whose HEAD commits the capture script. Its
// epic is stored on host b, and its clocks are fixed, so no real clock or timer runs.
func newHostRoutingEvidenceFixture(t *testing.T) hostRoutingEvidenceFixture {
	t.Helper()
	repo, head := evidenceRunRepo(t, hostRoutingEvidenceScript)
	st := openTestStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	created := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: hostRoutingEpic, Repository: repo, Host: "b", Title: "Synthetic routing epic.", Intent: "Synthetic intent.",
		Care: "serious", Phase: "accepted", Created: created, Changed: created,
		Requirements: []store.EpicRequirement{{
			ID: "R1", Text: "Synthetic requirement.", Evidence: "Synthetic evidence.", Kind: "test",
			Item: evidenceRunItem("out.txt"),
		}},
	}); err != nil {
		t.Fatalf("store CreateEpic: %v", err)
	}
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), stepGatePoolsText("gate"))
	f := hostRoutingEvidenceFixture{
		repo: repo, head: head, stateDir: t.TempDir(),
		a:       hostexectest.NewRecorder(hostexec.NewLocal()),
		b:       hostexectest.NewRecorder(hostexec.NewLocal()),
		judging: make(chan struct{}, 1),
		ended:   make(chan error, 4),
	}
	hosts := hostexec.Map{"a": f.a, "b": f.b}
	judge := evidenceRunHolds()
	judge.beforeEnd = func() { f.judging <- struct{}{} }
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{judge}}
	now := func() time.Time { return created }
	f.ev = NewEvidence(EvidenceConfig{
		Store:        st,
		Steps:        steps,
		Hosts:        hosts,
		Pools:        &fakeGatePools{},
		Environ:      gateEnv("XDG_CONFIG_HOME=" + home),
		StateDir:     f.stateDir,
		GateStateDir: gateShortStateDir(t),
		Now:          now,
	})
	t.Cleanup(f.ev.Close)
	f.checks = NewNodeChecks(NodeChecksConfig{
		Store:        nodeCheckStore{Store: st, ended: f.ended},
		Hosts:        hosts,
		Pools:        &fakeGatePools{},
		Environ:      gateEnv("XDG_CONFIG_HOME=" + home),
		StateDir:     f.stateDir,
		GateStateDir: gateShortStateDir(t),
		Now:          now,
	})
	t.Cleanup(f.checks.Close)
	return f
}

// hostRoutingRunEvidence starts a red run of Requirement R1 at HEAD through f's Evidence, and waits until its judge starts,
// by which time its capture is recorded. It returns the run Run stored.
func hostRoutingRunEvidence(t *testing.T, f *hostRoutingEvidenceFixture) store.EvidenceRun {
	t.Helper()
	run, err := f.ev.Run(t.Context(), hostRoutingEpic, "R1", "HEAD", evidence.PhaseRed)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-f.judging
	return run
}

// hostRoutingRunCheck runs a check of hostRoutingEpic at HEAD through f's NodeChecks, and waits until its end is recorded.
func hostRoutingRunCheck(t *testing.T, f *hostRoutingEvidenceFixture) {
	t.Helper()
	check := store.NodeCheck{
		ID: nodeCheckID, Epic: hostRoutingEpic, Path: "check", Revision: f.head, Argv: []string{"/bin/true"}, Limit: 60,
	}
	if _, err := f.checks.Run(t.Context(), check); err != nil {
		t.Fatalf("Run node check: %v", err)
	}
	nodeCheckEnded(t, f.ended)
}

// hostRoutingCallAt reports whether calls holds a call of op on path.
func hostRoutingCallAt(calls []hostexectest.Call, op, path string) bool {
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool {
		return call.Op == op && call.Path == path
	})
}

// TestEpicHostRoutingEvidence covers the routing of evidence runs and node checks to the host their epic is stored on: an
// evidence run adds its worktree and reads its artifacts through host b, a node check adds its worktree through host b, and
// host a, which no epic is stored on, records no call.
func TestEpicHostRoutingEvidence(t *testing.T) {
	t.Parallel()
	t.Run("evidence run goes to b", func(t *testing.T) {
		t.Parallel()
		f := newHostRoutingEvidenceFixture(t)
		run := hostRoutingRunEvidence(t, &f)
		wt := filepath.Join(f.stateDir, "evidence-worktrees", run.ID)
		calls := f.b.Calls()
		if !hostRoutingGitCall(calls, f.repo, "worktree", "add", "--detach", wt) {
			t.Errorf("host b recorded no worktree add of %s in %s: %v", wt, f.repo, calls)
		}
		if artifact := filepath.Join(wt, "out.txt"); !hostRoutingCallAt(calls, "ReadRooted", artifact) {
			t.Errorf("host b recorded no read of artifact %s: %v", artifact, calls)
		}
		if calls := f.a.Calls(); len(calls) != 0 {
			t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
		}
	})
	t.Run("node check goes to b", func(t *testing.T) {
		t.Parallel()
		f := newHostRoutingEvidenceFixture(t)
		hostRoutingRunCheck(t, &f)
		wt := filepath.Join(f.checks.worktrees, nodeCheckID)
		if calls := f.b.Calls(); !hostRoutingGitCall(calls, f.repo, "worktree", "add", "--detach", wt) {
			t.Errorf("host b recorded no worktree add of %s in %s: %v", wt, f.repo, calls)
		}
		if calls := f.a.Calls(); len(calls) != 0 {
			t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
		}
	})
}
