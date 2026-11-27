package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/engine/session"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepHostEpic is the epic the epic-host test starts its step under. The test stores it on host b.
const stepHostEpic = "synthetic-repo-0a1b2c3/epic-step-host"

// launchRecordingHost is the holderhost.Host of host b in these tests. Its launches run on a Local under its own state
// directory, and each Launch is recorded by its session id, so a test can tell which host a step launched on.
type launchRecordingHost struct {
	holderhost.Host
	mu       sync.Mutex
	sessions []string
}

// Launch records the session spec names, then launches it on the Local.
func (h *launchRecordingHost) Launch(ctx context.Context, spec holderhost.Spec) (*session.Session, error) {
	h.mu.Lock()
	h.sessions = append(h.sessions, spec.Session)
	h.mu.Unlock()
	return h.Host.Launch(ctx, spec)
}

// launched returns the session ids Launch was called with, in order.
func (h *launchRecordingHost) launched() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.sessions)
}

// stepHostNoTimer is the AfterFunc of these tests. It arms no timer, so no session's idle deadline ever fires.
func stepHostNoTimer(time.Duration, func()) func() bool {
	return func() bool { return false }
}

// stepHostRig is a Steps over one store, and the manager its steps run on. The manager is host a, the controller, and
// host b is the other host, whose holders live under stateB while the controller's live under stateA. Both hosts' file
// systems are recorders over this machine.
type stepHostRig struct {
	steps  *Steps
	m      *Manager
	st     *store.Store
	path   string
	repo   string
	stateA string
	stateB string
	b      *launchRecordingHost
	aFiles *hostexectest.Recorder
	bFiles *hostexectest.Recorder
}

// newStepHostRig returns a rig over a repository of its own, with a manager and Steps that name host a and know hosts a
// and b. Its clock and deadline timers are fakes, so no real clock or timer runs.
func newStepHostRig(t *testing.T) stepHostRig {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	claude := fakeClaudePath(t)
	st, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: st})
	startRun(t, writer)
	home := t.TempDir()
	writeStepAgents(t, home)
	environ := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	stateA, stateB := holderRoot(t), holderRoot(t)
	t.Cleanup(func() {
		clearHolderDirs(t, stateA)
		clearHolderDirs(t, stateB)
	})
	b := &launchRecordingHost{Host: holderhost.NewLocal(holderhost.LocalConfig{
		Shell: sh, Claude: claude, Environ: environ, StateDir: stateB, CronHook: true,
	})}
	m := NewManager(Config{
		Shell: sh, Claude: claude, Environ: environ, StateDir: stateA, HostName: "a",
		Hosts: map[string]holderhost.Host{"b": b}, Writer: writer, Now: fixedClock, AfterFunc: stepHostNoTimer,
		Notifier: &fakeSink{}, Store: st,
	})
	t.Cleanup(m.Close)
	aFiles := hostexectest.NewRecorder(hostexec.NewLocal())
	bFiles := hostexectest.NewRecorder(hostexec.NewLocal())
	clock := &watchLoopClock{at: watchEpoch()}
	timers := &stepDeadlineTimers{}
	steps := NewSteps(StepsConfig{
		Sessions: m, Store: st, Writer: writer, Worktrees: t.TempDir(), Environ: environ,
		Logger: slog.New(slog.DiscardHandler), Now: clock.now, NewTimer: timers.newTimer,
		Host: "a", Hosts: hostexec.Map{"a": aFiles, "b": bFiles},
	})
	t.Cleanup(steps.Close)
	return stepHostRig{
		steps: steps, m: m, st: st, path: path, repo: stepGitTestRepo(t), stateA: stateA, stateB: stateB,
		b: b, aFiles: aFiles, bFiles: bFiles,
	}
}

// startOnHost starts a read-only step of the rig's repository whose prompt never ends its reply, so its loop runs until
// the test ends. The call arrives as the host that is given, and the step names the epic that is given. It returns its
// id.
func (f stepHostRig) startOnHost(t *testing.T, host, epic string) string {
	t.Helper()
	start := stepStartReadOnly(f.repo, "")
	start.Prompt = "STALL"
	start.Arrived = host
	start.Epic = epic
	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start of a step on host %q under epic %q: %v", host, epic, err)
	}
	return id
}

// checkHolderOn checks that the holder of step id is under the state directory state, and not under other.
func checkHolderOn(t *testing.T, id, state, other string) {
	t.Helper()
	if _, err := os.Stat(holderDirOf(state, id)); err != nil {
		t.Fatalf("holder of %s under %s: %v", id, state, err)
	}
	if _, err := os.Stat(holderDirOf(other, id)); !os.IsNotExist(err) {
		t.Fatalf("holder of %s under %s: stat = %v, want none", id, other, err)
	}
}

// readsAgents reports whether calls holds a ReadDir of an agents folder, the folder a step's agent is read from.
func readsAgents(calls []hostexectest.Call) bool {
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool {
		return call.Op == "ReadDir" && strings.HasSuffix(call.Path, filepath.Join(".claude", "agents"))
	})
}

// TestStepSessionOnHostEpicStoredOnB covers a step of an epic stored on host b: it launches through b's holders, its row
// stores b, and its agent is read through b's file system.
func TestStepSessionOnHostEpicStoredOnB(t *testing.T) {
	t.Parallel()
	f := newStepHostRig(t)
	created := time.Unix(turnStarted, 0)
	if err := f.st.CreateEpic(t.Context(), store.EpicRow{
		ID: stepHostEpic, Repository: f.repo, Host: "b", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	id := f.startOnHost(t, "", stepHostEpic)
	if !slices.Contains(f.b.launched(), id) {
		t.Errorf("host b launched %v, want step %s", f.b.launched(), id)
	}
	checkHolderOn(t, id, f.stateB, f.stateA)
	if host := historyOf(t, f.path, id).Host; host != "b" {
		t.Errorf("row host of step %s = %q, want b", id, host)
	}
	if !readsAgents(f.bFiles.Calls()) {
		t.Errorf("host b read no agents folder of step %s: %v", id, f.bFiles.Calls())
	}
	if calls := f.aFiles.Calls(); len(calls) != 0 {
		t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
	}
}

// TestStepSessionOnHostNoEpicOnCallerHost covers a step with no epic that its caller arrived as host b: it launches on b
// and its row stores b.
func TestStepSessionOnHostNoEpicOnCallerHost(t *testing.T) {
	t.Parallel()
	f := newStepHostRig(t)
	id := f.startOnHost(t, "b", "")
	if !slices.Contains(f.b.launched(), id) {
		t.Errorf("host b launched %v, want step %s", f.b.launched(), id)
	}
	checkHolderOn(t, id, f.stateB, f.stateA)
	if host := historyOf(t, f.path, id).Host; host != "b" {
		t.Errorf("row host of step %s = %q, want b", id, host)
	}
	if calls := f.aFiles.Calls(); len(calls) != 0 {
		t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
	}
}

// TestStepSessionOnHostWithoutHost covers a step that names no host and no epic: it launches on the controller's Local,
// which keeps its holder under the controller's state directory, and its row stores the controller's name.
func TestStepSessionOnHostWithoutHost(t *testing.T) {
	t.Parallel()
	f := newStepHostRig(t)
	id := f.startOnHost(t, "", "")
	if launches := f.b.launched(); len(launches) != 0 {
		t.Errorf("host b launched %v, want none", launches)
	}
	checkHolderOn(t, id, f.stateA, f.stateB)
	if host := historyOf(t, f.path, id).Host; host != "a" {
		t.Errorf("row host of step %s = %q, want a", id, host)
	}
	if calls := f.bFiles.Calls(); len(calls) != 0 {
		t.Errorf("host b recorded %d calls, want none: %v", len(calls), calls)
	}
}

// TestStepSessionOnHostRefusesUnknownHost covers a step whose host is unknown: the Steps and the manager both refuse it
// with ErrUnknownHost before anything is stored or launched.
func TestStepSessionOnHostRefusesUnknownHost(t *testing.T) {
	t.Parallel()
	f := newStepHostRig(t)
	start := stepStartReadOnly(f.repo, "")
	start.Prompt = "STALL"
	start.Host = "c"
	id, err := f.steps.Start(t.Context(), start)
	if !errors.Is(err, hostexec.ErrUnknownHost) {
		t.Fatalf("Start on c: error = %v, want %v", err, hostexec.ErrUnknownHost)
	}
	if id != "" {
		t.Fatalf("refused Start returned id %q, want none", id)
	}
	err = f.m.StartStep(t.Context(), StepSession{
		ID: stepC, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Host: "c",
	})
	if !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("StartStep on c: error = %v, want %v", err, ErrUnknownHost)
	}
	for _, table := range []string{"sessions", "events"} {
		if n := countRows(t, f.path, table); n != 0 {
			t.Errorf("%s rows = %d, want 0", table, n)
		}
	}
	if launches := f.b.launched(); len(launches) != 0 {
		t.Errorf("host b launched %v, want none", launches)
	}
}

// TestStepSessionOnHostHeader covers the Steps service: the host header of a StartStep call becomes the start's
// Arrived, and the call's host field becomes its Host. A call with no host header leaves Arrived empty, and one with no
// host field leaves Host empty, so the epic, the default or the Steps' own host takes it.
func TestStepSessionOnHostHeader(t *testing.T) {
	t.Parallel()
	fake := &fakeStepCommands{startID: stepA}
	service := &stepService{steps: fake}
	cases := []struct{ field, header string }{{field: "", header: "b"}, {field: "a", header: ""}}
	for _, tc := range cases {
		req := connect.NewRequest(&v1.StartStepRequest{
			Agent: "reviewer", Prompt: "check", Schema: "{}", Workspace: "/work", Host: tc.field,
		})
		req.Header().Set(protocol.HeaderSession, "session-7")
		if tc.header != "" {
			req.Header().Set(protocol.HeaderHost, tc.header)
		}
		if _, err := service.StartStep(t.Context(), req); err != nil {
			t.Fatalf("StartStep with host field %q and header %q: %v", tc.field, tc.header, err)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	arrived := []string{fake.starts[0].Arrived, fake.starts[1].Arrived}
	if want := []string{"b", ""}; !slices.Equal(arrived, want) {
		t.Errorf("StartStep arrived hosts = %q, want %q", arrived, want)
	}
	named := []string{fake.starts[0].Host, fake.starts[1].Host}
	if want := []string{"", "a"}; !slices.Equal(named, want) {
		t.Errorf("StartStep named hosts = %q, want %q", named, want)
	}
}
