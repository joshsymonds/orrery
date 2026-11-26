package enginehost

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
	"github.com/joshsymonds/orrery/internal/store"
)

// epicHostNoLocalStepID is the id of the step the step check read tests read a check of.
const epicHostNoLocalStepID = "0123456789abcdef0123456789abcdef"

// epicHostNamedSteps is a Steps whose Status answers row. Its other methods are the Steps', so it names hosts and keeps its
// gate logs as a Steps does.
type epicHostNamedSteps struct {
	*Steps
	row store.StepRow
}

// Status answers row, whatever id it is asked for.
func (s epicHostNamedSteps) Status(context.Context, string) (store.StepRow, error) {
	return s.row, nil
}

// epicHostRowsOnly is a StepCommands that answers row from Status and names no host for it: its only methods are Status
// and the nil interface's.
type epicHostRowsOnly struct {
	StepCommands
	row store.StepRow
}

// Status answers row, whatever id it is asked for.
func (s epicHostRowsOnly) Status(context.Context, string) (store.StepRow, error) {
	return s.row, nil
}

// epicHostNeverAfter is the After of a check's drive in these tests. Its channel never fires, so no real timer runs.
func epicHostNeverAfter(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

// epicHostReadsPools reports whether calls hold a read of a pool file, which a grant request reads through its host.
func epicHostReadsPools(calls []hostexectest.Call) bool {
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool {
		return call.Op == "ReadFile" && strings.HasSuffix(call.Path, "pools.toml")
	})
}

// epicHostReadsAgents reports whether calls hold a read of an agent folder, which a step's agent resolves through its
// host.
func epicHostReadsAgents(calls []hostexectest.Call) bool {
	return slices.ContainsFunc(calls, func(call hostexectest.Call) bool {
		return call.Op == "ReadDir" && strings.HasSuffix(call.Path, filepath.Join(".claude", "agents"))
	})
}

// epicHostNoLocalCalls fails t for each call the controller's local host recorded. Under an epic stored on b, none is made
// there.
func epicHostNoLocalCalls(t *testing.T, local *hostexectest.Recorder) {
	t.Helper()
	if calls := local.Calls(); len(calls) != 0 {
		t.Errorf("the controller's local host recorded %d calls, want none: %v", len(calls), calls)
	}
}

// TestEpicHostNoLocalEvidenceCapture runs an evidence capture under an epic stored on b. The capture's worktree is added on
// b, its grant request reads the pool files on b, and the controller's local host records no call.
func TestEpicHostNoLocalEvidenceCapture(t *testing.T) {
	t.Parallel()
	local, b, hosts := hostRoutingGatesHosts()
	repo, head := evidenceCaptureRepo(t)
	st := openTestStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	host := hostRoutingGatesEpic(t, st, repo, hosts)
	c := evidenceCaptureFor(t, &fakeGatePools{}).on(host)
	end, err := c.run(t.Context(), repo, head, "nolocal-capture", evidenceCaptureItem(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if end.NotCaptured != "" || end.Exit != 3 {
		t.Fatalf("capture = %+v, want exit 3 and captured", end)
	}
	calls := b.Calls()
	if !hostRoutingGitCall(calls, repo, "worktree", "add") {
		t.Errorf("host b recorded no worktree add in %s: %v", repo, calls)
	}
	if !epicHostReadsPools(calls) {
		t.Errorf("host b recorded no read of the pool files: %v", calls)
	}
	epicHostNoLocalCalls(t, local)
}

// TestEpicHostNoLocalNodeCheck runs a node check of an epic stored on b. The check's worktree is added on b, its grant
// request reads the pool files on b, the check runs to its end, and the controller's local host records no call.
func TestEpicHostNoLocalNodeCheck(t *testing.T) {
	t.Parallel()
	local, b, hosts := hostRoutingGatesHosts()
	repo, head := evidenceCaptureRepo(t)
	st := openTestStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	hostRoutingGatesEpic(t, st, repo, hosts)
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), stepGatePoolsText("gate"))
	ended := make(chan error, 4)
	checks := NewNodeChecks(NodeChecksConfig{
		Store:        nodeCheckStore{Store: st, ended: ended},
		Hosts:        hosts,
		Pools:        &fakeGatePools{},
		Environ:      gateEnv("XDG_CONFIG_HOME=" + home),
		StateDir:     t.TempDir(),
		GateStateDir: gateShortStateDir(t),
		Now:          fixedClock,
		After:        epicHostNeverAfter,
	})
	t.Cleanup(checks.Close)
	check := store.NodeCheck{
		ID: nodeCheckID, Epic: hostRoutingEpic, Path: "check", Revision: head,
		Argv: []string{"/bin/sh", "-c", "exit 0"}, Limit: 60,
	}
	if _, err := checks.Run(t.Context(), check); err != nil {
		t.Fatalf("Run: %v", err)
	}
	nodeCheckEnded(t, ended)
	if got := nodeCheckStored(t, st); got.NotRun != "" {
		t.Fatalf("node check = %+v, want it run", got)
	}
	calls := b.Calls()
	if !hostRoutingGitCall(calls, repo, "worktree", "add") {
		t.Errorf("host b recorded no worktree add in %s: %v", repo, calls)
	}
	if !epicHostReadsPools(calls) {
		t.Errorf("host b recorded no read of the pool files: %v", calls)
	}
	epicHostNoLocalCalls(t, local)
}

// TestEpicHostNoLocalStepCheckRead reads a kept gate log of a step whose row names b. The log is read on b, and the
// controller's local host records no call. A StepCommands that names no host refuses the read rather than reading the log on
// this machine.
func TestEpicHostNoLocalStepCheckRead(t *testing.T) {
	t.Parallel()
	local, b, hosts := hostRoutingGatesHosts()
	steps := &Steps{gateStateDir: gateShortStateDir(t), hosts: hosts}
	logs, err := steps.KeptGateLogs()
	if err != nil {
		t.Fatalf("KeptGateLogs: %v", err)
	}
	if err = os.MkdirAll(logs, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", logs, err)
	}
	logPath := filepath.Join(logs, "gate.log")
	if err = os.WriteFile(logPath, []byte("== stdout ==\nkept-out\n== stderr ==\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", logPath, err)
	}
	row := store.StepRow{
		ID: epicHostNoLocalStepID, Host: "b", Checks: []store.StepCheck{{Name: "gate", Round: 1, Log: logPath}},
	}
	svc := &stepService{steps: epicHostNamedSteps{Steps: steps, row: row}}
	resp, err := svc.ReadStepCheck(t.Context(), connect.NewRequest(&v1.ReadStepCheckRequest{
		Id: epicHostNoLocalStepID, Check: "gate",
	}))
	if err != nil {
		t.Fatalf("ReadStepCheck: %v", err)
	}
	if want := "== stdout ==\nkept-out\n== stderr =="; !resp.Msg.GetKept() || resp.Msg.GetOutput() != want {
		t.Fatalf("check output = %q (kept %v), want %q", resp.Msg.GetOutput(), resp.Msg.GetKept(), want)
	}
	hostRoutingWantCalls(t, "b", b.Calls(), hostRoutingCallOf{"ReadRooted", logPath})
	epicHostNoLocalCalls(t, local)

	refused := &stepService{steps: epicHostRowsOnly{row: row}}
	if _, err = refused.ReadStepCheck(t.Context(), connect.NewRequest(&v1.ReadStepCheckRequest{
		Id: epicHostNoLocalStepID, Check: "gate",
	})); err == nil {
		t.Errorf("ReadStepCheck of a step whose commands name no host succeeded, want it refused")
	}
}

// TestEpicHostNoLocalStepRun starts a read-only step under an epic stored on b. Its worktree's checkout is read on b, and the
// controller's local host records no call.
func TestEpicHostNoLocalStepRun(t *testing.T) {
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
	calls := f.b.Calls()
	if !hostRoutingGitCall(calls, wt, "--no-optional-locks", "status") {
		t.Errorf("host b recorded no checkout status of %s: %v", wt, calls)
	}
	if !epicHostReadsAgents(calls) {
		t.Errorf("host b recorded no read of the agent folder: %v", calls)
	}
	if err = f.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	epicHostNoLocalCalls(t, f.a)
}

// TestEpicHostNoLocalStepRespawn releases a read-only step under an epic stored on b, then continues it through its
// run. The respawned step reads its agent folder on b, and the controller's local host records no call.
func TestEpicHostNoLocalStepRespawn(t *testing.T) {
	t.Parallel()
	f := newHostRoutingFixture(t)
	start := stepStartReadOnly(f.repo, "")
	start.Epic = hostRoutingEpic
	start.Prompt = "STALL"
	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	m, ok := f.steps.sessions.(*Manager)
	if !ok {
		t.Fatalf("steps sessions are %T, want the Manager", f.steps.sessions)
	}
	if err = m.Release(t.Context(), id); err != nil {
		t.Fatalf("Release: %v", err)
	}
	f.steps.mu.Lock()
	run := f.steps.runs[id]
	f.steps.mu.Unlock()
	if run == nil {
		t.Fatalf("step %s has no run", id)
	}
	before := len(f.b.Calls())
	if err = run.do(t.Context(), func(ctx context.Context) error {
		return run.continueStep(ctx, StepPrompt{Text: "again", Sender: store.PromptSenderOwner})
	}); err != nil {
		t.Fatalf("continue: %v", err)
	}
	if calls := f.b.Calls()[before:]; !epicHostReadsAgents(calls) {
		t.Errorf("host b recorded no read of the agent folder on respawn: %v", calls)
	}
	if err = f.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	epicHostNoLocalCalls(t, f.a)
}
