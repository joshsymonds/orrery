package enginehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepGrantFixture is a Steps over a real store and writer, with fake sessions. Its named gates are granted by commands,
// and its machine pool file declares the pools given to newStepGrantFixture.
type stepGrantFixture struct {
	steps    *Steps
	sessions *stepStartSessions
	store    *store.Store
	environ  []string
}

// newStepGrantFixture returns a fixture whose Steps grants named gates through commands. Its environment names a config
// home whose machine pool file is poolsText, so the pools a gate needs are declared or not as a test says.
func newStepGrantFixture(t *testing.T, commands PoolCommands, poolsText string) *stepGrantFixture {
	t.Helper()
	s, _ := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sessions := &stepStartSessions{writer: writer}
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), poolsText)
	environ := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + home}
	steps, err := NewSteps(StepsConfig{
		Sessions:  sessions,
		Store:     s,
		Writer:    writer,
		Pools:     commands,
		Worktrees: t.TempDir(),
		Environ:   environ,
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return &stepGrantFixture{steps: steps, sessions: sessions, store: s, environ: environ}
}

// stepGrantGatesText is a gates file that defines gate unit: sh running script with a limit of 300 seconds. needs is
// the gate's needs line, or empty for a gate with no needs.
func stepGrantGatesText(shell, script, needs string) string {
	return fmt.Sprintf("[gates.unit]\nargv = [%q, \"-c\", %q]\nlimit = 300\n%s", shell, script, needs)
}

// stepGrantArgvJSON returns argv as the JSON array a granted gate's check records it as.
func stepGrantArgvJSON(t *testing.T, argv []string) string {
	t.Helper()
	raw, err := json.Marshal(argv)
	if err != nil {
		t.Fatalf("marshal argv: %v", err)
	}
	return string(raw)
}

func TestStepsStartNamedGateCarriesItsGrant(t *testing.T) {
	t.Parallel()
	commands := &fakeGatePools{}
	f := newStepGrantFixture(t, commands, stepGatePoolsText("gpu", gates.DefaultPool))
	shell := stepGateShell(t)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepGrantGatesText(shell, "exit 0", "needs = { gpu = 1 }\n"))
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))

	id, err := f.steps.Start(t.Context(), stepGateNamedStart(linked, "unit", gatesFile))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	project, defs, err := pools.Load(t.Context(), linked, stepGetenv(f.environ))
	if err != nil {
		t.Fatalf("pools.Load: %v", err)
	}
	step := f.sessions.first(t)
	run, ok := step.Observer.(*stepRun)
	if !ok {
		t.Fatalf("StartStep Observer is %T, want *stepRun", step.Observer)
	}
	want := grants.Request{
		Project:   project,
		Pools:     defs,
		Needs:     []grants.Need{{Pool: "gpu", Count: 1}},
		ClientPID: os.Getpid(),
		Command:   []string{shell, "-c", "exit 0"},
	}
	if !reflect.DeepEqual(run.grant, want) {
		t.Fatalf("run grant = %+v, want %+v", run.grant, want)
	}
	if run.pools != PoolCommands(commands) {
		t.Errorf("run pools = %v, want the commands the Steps was configured with", run.pools)
	}
	if run.gateName != "unit" || run.gateSource != gates.SourceEpic {
		t.Errorf("run gate name %q source %q, want unit from %s", run.gateName, run.gateSource, gates.SourceEpic)
	}
	if got := f.sessions.startCalls(); got != 1 || id == "" {
		t.Fatalf("StartStep calls = %d, id %q", got, id)
	}
}

func TestStepsStartNamedGateWithNoNeedsGrantsTheDefaultPool(t *testing.T) {
	t.Parallel()
	commands := &fakeGatePools{}
	f := newStepGrantFixture(t, commands, stepGatePoolsText(gates.DefaultPool))
	shell := stepGateShell(t)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepGrantGatesText(shell, "exit 0", ""))
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))

	if _, err := f.steps.Start(t.Context(), stepGateNamedStart(linked, "unit", gatesFile)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	run, ok := f.sessions.first(t).Observer.(*stepRun)
	if !ok {
		t.Fatal("StartStep Observer is not a *stepRun")
	}
	if want := []grants.Need{{Pool: gates.DefaultPool, Count: 1}}; !reflect.DeepEqual(run.grant.Needs, want) {
		t.Fatalf("grant needs = %v, want the default pool %v", run.grant.Needs, want)
	}
}

func TestStepsStartNamedGateRefusesUndeclaredPoolAndStoresNothing(t *testing.T) {
	shell := stepGateShell(t)
	cases := []struct {
		name      string
		poolsText string
		needs     string
	}{
		{
			name:      "needed pool undeclared",
			poolsText: stepGatePoolsText(gates.DefaultPool),
			needs:     "needs = { gpu = 1 }\n",
		},
		{name: "default gate pool undeclared", poolsText: stepGatePoolsText("gpu"), needs: ""},
		{name: "pool file malformed", poolsText: "[pools.gpu\n", needs: "needs = { gpu = 1 }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepGrantFixture(t, &fakeGatePools{}, tc.poolsText)
			gatesFile := filepath.Join(t.TempDir(), "gates.toml")
			stepGateWriteFile(t, gatesFile, stepGrantGatesText(shell, "exit 0", tc.needs))
			linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))

			id, err := f.steps.Start(t.Context(), stepGateNamedStart(linked, "unit", gatesFile))
			if !errors.Is(err, ErrBadGate) {
				t.Fatalf("Start = %q, %v; want an error matching ErrBadGate", id, err)
			}
			if id != "" {
				t.Errorf("Start id = %q on refusal, want empty", id)
			}
			if n := f.sessions.startCalls(); n != 0 {
				t.Errorf("StartStep calls = %d, want 0", n)
			}
			rows, err := f.store.OpenSteps(t.Context())
			if err != nil {
				t.Fatalf("OpenSteps: %v", err)
			}
			if len(rows) != 0 {
				t.Errorf("stored %d step rows on refusal, want none", len(rows))
			}
		})
	}
}

func TestStepsNamedGateRunsOnlyOnItsGrantAndRecordsItsCheck(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "ran")
	commands := &fakeGatePools{queue: make(chan struct{})}
	f := newStepGrantFixture(t, commands, stepGatePoolsText("cpu", "gpu", gates.DefaultPool))
	closeQueue := sync.OnceFunc(func() { close(commands.queue) })
	t.Cleanup(closeQueue)
	shell := stepGateShell(t)
	script := fmt.Sprintf("touch %q; echo ran", marker)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepGrantGatesText(shell, script, "needs = { gpu = 1, cpu = 2 }\n"))
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	id, err := f.steps.Start(t.Context(), stepGateNamedStart(linked, "unit", gatesFile))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	commit := stepGitTestCommit(t, linked, map[string]string{stepDoneOwned: "owned\n"})
	f.sessions.first(t).Observer.Return(stepDoneReturn(commit))

	stepStartAwait(t, "the gate's grant request", func() bool { return len(commands.requested()) == 1 })
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gate ran before its grant")
	}
	closeQueue()
	stepStartAwait(t, "the step to end", func() bool {
		row, err := f.steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("gate did not run after its grant: %v", err)
	}
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
	check := stepDoneCheck(t, row.Checks, stepLoopGateName)
	if !check.OK || check.Round != 1 {
		t.Errorf("gate check = %+v, want round 1 passing", check)
	}
	want := fmt.Sprintf(
		"gate unit from %s ran %s holding cpu:cpu-1,cpu:cpu-2,gpu:gpu-1; gate exited 0; output tail: ran",
		gates.SourceEpic, stepGrantArgvJSON(t, []string{shell, "-c", script}),
	)
	if check.Detail != want {
		t.Errorf("gate check detail = %q, want %q", check.Detail, want)
	}
}

func TestStepsNamedGateStopStoresNoCheckAndRerunStoresItsCheck(t *testing.T) {
	t.Parallel()
	first := filepath.Join(t.TempDir(), "first")
	commands := &fakeGatePools{stop: make(chan struct{})}
	f := newStepGrantFixture(t, commands, stepGatePoolsText("gpu", gates.DefaultPool))
	shell := stepGateShell(t)
	script := fmt.Sprintf(
		`if [ -e %[1]q ]; then echo rerun; exit 0; fi; echo 1 > %[1]q; sleep 60`, first,
	)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepGrantGatesText(shell, script, "needs = { gpu = 1 }\n"))
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	id, err := f.steps.Start(t.Context(), stepGateNamedStart(linked, "unit", gatesFile))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	commit := stepGitTestCommit(t, linked, map[string]string{stepDoneOwned: "owned\n"})
	f.sessions.first(t).Observer.Return(stepDoneReturn(commit))

	gateWaitFile(t, first)
	commands.stopGrant()
	stepStartAwait(t, "the step to end", func() bool {
		row, err := f.steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})

	requests := commands.requested()
	if len(requests) != 2 {
		t.Fatalf("Acquire was called %d times, want 2", len(requests))
	}
	if requests[1].GrantID != "grant-1" {
		t.Fatalf("rerun GrantID = %q, want the stopped grant %q", requests[1].GrantID, "grant-1")
	}
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
	var gateChecks []store.StepCheck
	for _, c := range row.Checks {
		if c.Name == stepLoopGateName {
			gateChecks = append(gateChecks, c)
		}
	}
	if len(gateChecks) != 1 {
		t.Fatalf("stored %d gate checks, want only the rerun's: %+v", len(gateChecks), gateChecks)
	}
	want := fmt.Sprintf(
		"gate unit from %s ran %s holding gpu:gpu-1; gate exited 0; output tail: rerun",
		gates.SourceEpic, stepGrantArgvJSON(t, []string{shell, "-c", script}),
	)
	if gateChecks[0].Detail != want {
		t.Errorf("gate check detail = %q, want %q", gateChecks[0].Detail, want)
	}
}
