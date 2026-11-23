package build

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
)

// newHostRoutingFixture returns a pass fixture whose epic lives on host b, and the recorders of hosts a and b, which
// the Runner resolves by those names. It drives the Runner until task 1 has its implementer's worktree, so the branch
// and the worktree are both made.
func newHostRoutingFixture(t *testing.T) (*passFixture, *hostexectest.Recorder, *hostexectest.Recorder) {
	t.Helper()
	f := newPassFixture(t)
	if err := f.store.AdoptHost(f.ctx, "b"); err != nil {
		t.Fatalf("AdoptHost: %v", err)
	}
	if got := f.epic().Host; got != "b" {
		t.Fatalf("epic Host = %q, want b", got)
	}
	recA := hostexectest.NewRecorder(hostexec.NewLocal())
	recB := hostexectest.NewRecorder(hostexec.NewLocal())
	f.runner = New(Config{
		Store: f.store, Steps: f.steps, Evidence: f.evidence, Getenv: f.getenv, StateDir: f.stateDir, Now: passClock,
		Hosts: hostexec.Map{"a": recA, "b": recB},
	})
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	return f, recA, recB
}

// hasGitCall reports whether calls holds a git call in dir whose arguments begin with args.
func hasGitCall(calls []hostexectest.Call, dir string, args ...string) bool {
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool {
		return call.Op == "Git" && call.Path == dir && len(call.Args) >= len(args) &&
			slices.Equal(call.Args[:len(args)], args)
	})
}

// TestEpicHostRoutingBuild covers the routing of an epic's repository work to the host the epic names: the branch and
// the task worktree are made on host b, the host the epic lives on, and host a, which the epic does not name, sees no
// call.
func TestEpicHostRoutingBuild(t *testing.T) {
	t.Run("branch and worktree go to b", func(t *testing.T) {
		f, recA, recB := newHostRoutingFixture(t)
		calls := recB.Calls()
		if !hasGitCall(calls, f.repo, "update-ref", BranchRef(passEpic)) {
			t.Errorf("host b recorded no branch create in %s: %v", f.repo, calls)
		}
		if !hasGitCall(calls, f.repo, "worktree", "add") {
			t.Errorf("host b recorded no task worktree add in %s: %v", f.repo, calls)
		}
		if calls := recA.Calls(); len(calls) != 0 {
			t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
		}
	})
}
