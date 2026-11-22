package enginehost

import (
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
	"github.com/joshsymonds/orrery/internal/store"
)

// hostRoutingEpic is the epic the routing steps run for. It is stored on host b.
const hostRoutingEpic = "synthetic-repo-0a1b2c3/epic-host-routing"

// hostRoutingFixture is a Steps over a real store, whose sessions are a manager that runs the fake claude. Its own host
// is a, and the epic lives on b, so only the epic's stored host can send the steps to b. Hosts a and b are recorders
// over the local host, which perform each call and record it.
type hostRoutingFixture struct {
	steps     *Steps
	repo      string
	worktrees string
	a         *hostexectest.Recorder
	b         *hostexectest.Recorder
}

// newHostRoutingFixture returns a fixture for a repository of its own, with an epic stored on host b. Its clock and
// deadline timers are fakes, so no real timer or clock runs.
func newHostRoutingFixture(t *testing.T) hostRoutingFixture {
	t.Helper()
	rig := newStepsRig(t, nil)
	writer := NewWriter(WriterConfig{Applier: rig.store})
	startRun(t, writer)
	repo := stepGitTestRepo(t)
	created := time.Unix(turnStarted, 0)
	if err := rig.store.CreateEpic(t.Context(), store.EpicRow{
		ID: hostRoutingEpic, Repository: repo, Host: "b", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	f := hostRoutingFixture{
		repo:      repo,
		worktrees: rig.worktrees,
		a:         hostexectest.NewRecorder(hostexec.NewLocal()),
		b:         hostexectest.NewRecorder(hostexec.NewLocal()),
	}
	clock := &watchLoopClock{at: watchEpoch()}
	timers := &stepDeadlineTimers{}
	f.steps = NewSteps(StepsConfig{
		Sessions:     rig.manager,
		Store:        rig.store,
		Writer:       writer,
		Worktrees:    rig.worktrees,
		Environ:      rig.environ,
		Logger:       slog.New(slog.DiscardHandler),
		Now:          clock.now,
		NewTimer:     timers.newTimer,
		GateStateDir: rig.gateRoot,
		Host:         "a",
		Hosts:        hostexec.Map{"a": f.a, "b": f.b},
	})
	t.Cleanup(f.steps.Close)
	return f
}

// hostRoutingGitCall reports whether calls holds a git call in dir whose arguments begin with args.
func hostRoutingGitCall(calls []hostexectest.Call, dir string, args ...string) bool {
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool {
		return call.Op == "Git" && call.Path == dir && len(call.Args) >= len(args) &&
			slices.Equal(call.Args[:len(args)], args)
	})
}

// TestEpicHostRoutingSteps covers the routing of a step's git and worktrees to the host its epic is stored on: a
// writing step's git runs on host b, a read-only step's worktree is added and removed on host b, and host a, which the
// steps do not name, records no call.
func TestEpicHostRoutingSteps(t *testing.T) {
	t.Parallel()
	t.Run("writing step goes to b", func(t *testing.T) {
		t.Parallel()
		f := newHostRoutingFixture(t)
		linked := stepWorktreeTestAddLinked(t, f.repo)
		start := stepStartWriting(linked)
		start.Epic = hostRoutingEpic
		start.Prompt = "STALL"
		if _, err := f.steps.Start(t.Context(), start); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if calls := f.b.Calls(); !hostRoutingGitCall(calls, linked, "rev-parse", "--is-inside-work-tree") {
			t.Errorf("host b recorded no workspace check in %s: %v", linked, calls)
		}
		if calls := f.a.Calls(); len(calls) != 0 {
			t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
		}
	})
	t.Run("read-only step worktree goes to b", func(t *testing.T) {
		t.Parallel()
		f := newHostRoutingFixture(t)
		start := stepStartReadOnly(f.repo, "")
		start.Epic = hostRoutingEpic
		start.Prompt = "STALL"
		id, err := f.steps.Start(t.Context(), start)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		wt := filepath.Join(f.worktrees, id)
		if calls := f.b.Calls(); !hostRoutingGitCall(calls, f.repo, "worktree", "add", "--detach", wt) {
			t.Errorf("host b recorded no worktree add of %s in %s: %v", wt, f.repo, calls)
		}
		if err := f.steps.Stop(t.Context(), id); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if calls := f.b.Calls(); !hostRoutingGitCall(calls, f.repo, "worktree", "remove", "--force", wt) {
			t.Errorf("host b recorded no worktree remove of %s in %s: %v", wt, f.repo, calls)
		}
	})
	t.Run("host a records nothing", func(t *testing.T) {
		t.Parallel()
		f := newHostRoutingFixture(t)
		linked := stepWorktreeTestAddLinked(t, f.repo)
		writing := stepStartWriting(linked)
		writing.Epic = hostRoutingEpic
		writing.Prompt = "STALL"
		if _, err := f.steps.Start(t.Context(), writing); err != nil {
			t.Fatalf("Start writing step: %v", err)
		}
		reading := stepStartReadOnly(f.repo, "")
		reading.Epic = hostRoutingEpic
		reading.Prompt = "STALL"
		id, err := f.steps.Start(t.Context(), reading)
		if err != nil {
			t.Fatalf("Start read-only step: %v", err)
		}
		if err := f.steps.Stop(t.Context(), id); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		// Host b must have seen the steps' calls, so a silent run does not pass for the routing.
		if calls := f.b.Calls(); len(calls) == 0 {
			t.Errorf("host b recorded no calls, so the steps did not run on it")
		}
		if calls := f.a.Calls(); len(calls) != 0 {
			t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
		}
	})
}
