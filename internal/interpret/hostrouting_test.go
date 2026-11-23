package interpret

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
	"github.com/joshsymonds/orrery/internal/store"
)

// newHostRoutingRelease returns a release world whose epic lives on host b, and the recorders of hosts a and b, which
// the world's Runner resolves by those names. It decides the release prepare of the epic once, so the git the prepare
// runs has been made. origin's main is an ancestor of the epic's tip, so the prepare finds the tip not behind.
func newHostRoutingRelease(t *testing.T) (*releaseWorld, *hostexectest.Recorder, *hostexectest.Recorder) {
	t.Helper()
	w := newMergeWorld(t)
	mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "epic.txt")
	if err := w.s.AdoptHost(t.Context(), "b"); err != nil {
		t.Fatalf("AdoptHost: %v", err)
	}
	recA := hostexectest.NewRecorder(hostexec.NewLocal())
	recB := hostexectest.NewRecorder(hostexec.NewLocal())
	cfg := nodeConfig(w.s, w.fakes)
	cfg.Hosts = hostexec.Map{"a": recA, "b": recB}
	w.runner = New(cfg)
	result, err := w.runner.decideRelease(t.Context(), epicOf(t, w.s), store.NodeRun{})
	if err != nil {
		t.Fatalf("decideRelease: %v", err)
	}
	if result.Behind || result.Candidate != tip {
		t.Fatalf("decideRelease = %+v, want the tip %s, not behind origin's main", result, tip)
	}
	return w, recA, recB
}

// hasGitCall reports whether calls holds a git call in dir whose arguments begin with args.
func hasGitCall(calls []hostexectest.Call, dir string, args ...string) bool {
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool {
		return call.Op == "Git" && call.Path == dir && len(call.Args) >= len(args) &&
			slices.Equal(call.Args[:len(args)], args)
	})
}

// TestEpicHostRoutingInterpret covers the routing of interpret's own git to the host the epic names: a release's
// prepare reads origin's main on host b, the host the epic lives on, and host a, which the epic does not name, sees no
// call.
func TestEpicHostRoutingInterpret(t *testing.T) {
	t.Run("interpret git goes to b", func(t *testing.T) {
		w, recA, recB := newHostRoutingRelease(t)
		calls := recB.Calls()
		for _, args := range [][]string{
			{"ls-remote"}, {"fetch"}, {"merge-base", "--is-ancestor"},
		} {
			if !hasGitCall(calls, w.repo, args...) {
				t.Errorf("host b recorded no %v in %s: %v", args, w.repo, calls)
			}
		}
		if calls := recA.Calls(); len(calls) != 0 {
			t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
		}
	})
}
