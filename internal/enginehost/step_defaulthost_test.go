package enginehost

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/fleettest"
	"github.com/joshsymonds/orrery/internal/store"
)

// defaultHostEpic is the epic the epic-host test starts its step under. The test stores it on host a, which is not the
// default host b.
const defaultHostEpic = "synthetic-repo-0a1b2c3/epic-default-host"

// defaultHostRig is a Steps over a fleet of hosts a and b. The fleet's controller is the Steps' own host, and defaultHost
// is the default host of its Placement, which places the fleet's clients for reachability. Its sessions are fakes, so
// a start stores its step's row and runs no session, and its clock and deadline timers are fakes too.
type defaultHostRig struct {
	fleet *fleettest.Fleet
	steps *Steps
	st    *store.Store
}

// newDefaultHostRig returns a rig whose Placement names defaultHost as the default host, which empty names none.
func newDefaultHostRig(t *testing.T, defaultHost string) defaultHostRig {
	t.Helper()
	fleet := fleettest.Start(t, "a", "b")
	st, _ := openStore(t)
	writer := NewWriter(WriterConfig{Applier: st})
	startRun(t, writer)
	clock := &watchLoopClock{at: watchEpoch()}
	timers := &stepDeadlineTimers{}
	placement := NewPlacement(PlacementConfig{
		Controller: fleet.Controller,
		Default:    defaultHost,
		Hosts:      map[string]Reacher{"a": fleet.Clients["a"], "b": fleet.Clients["b"]},
	})
	steps := NewSteps(StepsConfig{
		Sessions: &stepStartSessions{writer: writer}, Store: st, Writer: writer, Worktrees: t.TempDir(),
		Logger: slog.New(slog.DiscardHandler), Now: clock.now, NewTimer: timers.newTimer,
		Host: fleet.Controller, Hosts: fleet.Hosts, Placement: placement,
	})
	t.Cleanup(steps.Close)
	return defaultHostRig{fleet: fleet, steps: steps, st: st}
}

// defaultHostWorkspace makes a repository with one commit under the root of the fleet's agent named host, and returns a
// linked worktree of it under that root. The agent serves paths under its root only, so a writing step on host runs in
// that worktree.
func defaultHostWorkspace(t *testing.T, fleet *fleettest.Fleet, host string) string {
	t.Helper()
	repo := filepath.Join(fleet.Roots[host], "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("make the repository of host %s: %v", host, err)
	}
	stepGitTestRun(t, repo, "init", "-q", "-b", "main")
	stepGitTestWrite(t, repo, "README", "base\n")
	stepGitTestRun(t, repo, "add", "README")
	stepGitTestRun(t, repo, "commit", "-q", "-m", "base")
	linked := filepath.Join(fleet.Roots[host], "linked")
	stepGitTestRun(t, repo, "worktree", "add", "--detach", linked, "HEAD")
	return linked
}

// rowHost returns the host the stored row of step id names. It fails the test when the row cannot be read.
func (r defaultHostRig) rowHost(t *testing.T, id string) string {
	t.Helper()
	row, err := r.st.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("read the row of step %s: %v", id, err)
	}
	return row.Host
}

// refused checks that Start refuses start with an error matching want that names host name, and that it returns no id.
func (r defaultHostRig) refused(t *testing.T, start StepStart, want error, name string) {
	t.Helper()
	id, err := r.steps.Start(t.Context(), start)
	if !errors.Is(err, want) {
		t.Fatalf("Start: error = %v, want one matching %v", err, want)
	}
	if !strings.HasSuffix(err.Error(), ": "+name) {
		t.Errorf("Start error %q does not name host %s", err, name)
	}
	if id != "" {
		t.Errorf("refused Start returned id %q, want none", id)
	}
}

// TestStepStartDefaultHostNoNameStoresDefault covers a start that names no host and no epic: it runs on the default
// host, and its row stores that host.
func TestStepStartDefaultHostNoNameStoresDefault(t *testing.T) {
	t.Parallel()
	f := newDefaultHostRig(t, "b")
	id, err := f.steps.Start(t.Context(), stepStartWriting(defaultHostWorkspace(t, f.fleet, "b")))
	if err != nil {
		t.Fatalf("Start with no host and no epic: %v", err)
	}
	if host := f.rowHost(t, id); host != "b" {
		t.Errorf("row host of step %s = %q, want the default b", id, host)
	}
}

// TestStepStartDefaultHostNamedReachable covers a start that names host a, which is reachable, over the default host b:
// it runs on a, and its row stores a.
func TestStepStartDefaultHostNamedReachable(t *testing.T) {
	t.Parallel()
	f := newDefaultHostRig(t, "b")
	start := stepStartWriting(defaultHostWorkspace(t, f.fleet, "a"))
	start.Host = "a"
	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start naming a reachable host a: %v", err)
	}
	if host := f.rowHost(t, id); host != "a" {
		t.Errorf("row host of step %s = %q, want the named a", id, host)
	}
}

// TestStepStartDefaultHostRefusesUnknownAndUnreachable covers a start that names an unknown host, a named host that
// cannot be reached, and a default host that cannot be reached. Each is refused naming its host, and stores nothing.
func TestStepStartDefaultHostRefusesUnknownAndUnreachable(t *testing.T) {
	t.Parallel()
	t.Run("unknown named host", func(t *testing.T) {
		t.Parallel()
		f := newDefaultHostRig(t, "b")
		start := stepStartWriting(defaultHostWorkspace(t, f.fleet, "b"))
		start.Host = "c"
		f.refused(t, start, ErrUnknownHost, "c")
	})
	t.Run("unreachable named host", func(t *testing.T) {
		t.Parallel()
		f := newDefaultHostRig(t, "b")
		f.fleet.StopAgent(t, "a")
		f.fleet.WaitReach(t, "a", false)
		start := stepStartWriting(defaultHostWorkspace(t, f.fleet, "b"))
		start.Host = "a"
		f.refused(t, start, ErrUnreachableHost, "a")
	})
	t.Run("unreachable default host", func(t *testing.T) {
		t.Parallel()
		f := newDefaultHostRig(t, "b")
		f.fleet.StopAgent(t, "b")
		f.fleet.WaitReach(t, "b", false)
		f.refused(t, stepStartWriting(defaultHostWorkspace(t, f.fleet, "a")), ErrUnreachableHost, "b")
	})
}

// TestStepStartDefaultHostNoDefaultKeepsToday covers a Placement with no default: a start that names no host takes the
// host it arrived as, and one that arrived as none takes the Steps' own host, the controller.
func TestStepStartDefaultHostNoDefaultKeepsToday(t *testing.T) {
	t.Parallel()
	t.Run("arrived host", func(t *testing.T) {
		t.Parallel()
		f := newDefaultHostRig(t, "")
		start := stepStartWriting(defaultHostWorkspace(t, f.fleet, "b"))
		start.Arrived = "b"
		id, err := f.steps.Start(t.Context(), start)
		if err != nil {
			t.Fatalf("Start arriving as b: %v", err)
		}
		if host := f.rowHost(t, id); host != "b" {
			t.Errorf("row host of step %s = %q, want the arrived b", id, host)
		}
	})
	t.Run("controller", func(t *testing.T) {
		t.Parallel()
		f := newDefaultHostRig(t, "")
		start := stepStartWriting(stepWorktreeTestAddLinked(t, stepGitTestRepo(t)))
		id, err := f.steps.Start(t.Context(), start)
		if err != nil {
			t.Fatalf("Start arriving as none: %v", err)
		}
		if host := f.rowHost(t, id); host != f.fleet.Controller {
			t.Errorf("row host of step %s = %q, want the controller %q", id, host, f.fleet.Controller)
		}
	})
}

// TestStepStartDefaultHostEpicHostKept covers a start under a stored epic on host a, over the default host b: it runs
// on a, and its row stores a. A start that names another host than the epic's is refused, naming both hosts and the
// epic.
func TestStepStartDefaultHostEpicHostKept(t *testing.T) {
	t.Parallel()
	f := newDefaultHostRig(t, "b")
	workspace := defaultHostWorkspace(t, f.fleet, "a")
	created := time.Unix(turnStarted, 0)
	if err := f.st.CreateEpic(t.Context(), store.EpicRow{
		ID: defaultHostEpic, Repository: workspace, Host: "a", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	start := stepStartWriting(workspace)
	start.Epic = defaultHostEpic
	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start under epic %s stored on a: %v", defaultHostEpic, err)
	}
	if host := f.rowHost(t, id); host != "a" {
		t.Errorf("row host of step %s = %q, want the epic's a", id, host)
	}

	other := stepStartWriting(workspace)
	other.Epic = defaultHostEpic
	other.Host = "b"
	otherID, err := f.steps.Start(t.Context(), other)
	if err == nil {
		t.Fatalf("Start naming b under epic %s stored on a = %q, nil error; want a refusal", defaultHostEpic, otherID)
	}
	for _, want := range []string{"b", "a", defaultHostEpic} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	if otherID != "" {
		t.Errorf("refused Start returned id %q, want none", otherID)
	}
}
