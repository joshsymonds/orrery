package interpret

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
	"github.com/joshsymonds/orrery/internal/kinds"
)

// newHostRoutingSync returns a sync world whose epic lives on host b, and the recorders of hosts a and b, which the
// world's Runner resolves by those names. It drives the sync's prepare on origin's main once, so the git the sync runs
// has been made. origin's main is the epic's stored base, so the sync finds origin's commit held.
func newHostRoutingSync(t *testing.T) (*syncWorld, *hostexectest.Recorder, *hostexectest.Recorder) {
	t.Helper()
	w := newSyncWorld(t)
	if err := w.s.AdoptHost(t.Context(), "b"); err != nil {
		t.Fatalf("AdoptHost: %v", err)
	}
	recA := hostexectest.NewRecorder(hostexec.NewLocal())
	recB := hostexectest.NewRecorder(hostexec.NewLocal())
	cfg := nodeConfig(w.s, w.fakes)
	cfg.Hosts = hostexec.Map{"a": recA, "b": recB}
	w.runner = New(cfg)
	var result kinds.SyncResult
	if err := w.runner.mergeMain(t.Context(), epicOf(t, w.s), Action{}, &result); err != nil {
		t.Fatalf("mergeMain: %v", err)
	}
	if !result.Contained {
		t.Fatalf("mergeMain = %+v, want origin's main held by the stored base", result)
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

// TestEpicHostRoutingInterpret covers the routing of interpret's own git to the host the epic names: a sync of origin's
// main runs its git on host b, the host the epic lives on, and host a, which the epic does not name, sees no call.
func TestEpicHostRoutingInterpret(t *testing.T) {
	t.Run("interpret git goes to b", func(t *testing.T) {
		w, recA, recB := newHostRoutingSync(t)
		calls := recB.Calls()
		for _, args := range [][]string{
			{"remote"}, {"ls-remote"}, {"fetch"}, {"merge-base", "--is-ancestor"},
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
