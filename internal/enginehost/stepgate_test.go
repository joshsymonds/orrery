package enginehost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// newStepGateFixture returns a step start fixture whose Environ also sets HOME and XDG_CONFIG_HOME to a temporary
// directory, so gate resolution never reads the real machine pool file.
func newStepGateFixture(t *testing.T) *stepStartFixture {
	t.Helper()
	f := newStepStartFixture(t)
	home := t.TempDir()
	f.steps.environ = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + home}
	return f
}

// stepGateShell returns the path of sh. A gate's argv[0] must contain a slash, so the tests name sh by its path.
func stepGateShell(t *testing.T) string {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("LookPath sh: %v", err)
	}
	return shell
}

// stepGateWriteFile writes contents to path, creating its directory.
func stepGateWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stepGateFileText is a gates file that defines gate unit: sh running script, a limit of limit seconds, and two pools.
func stepGateFileText(shell, script string, limit int) string {
	return fmt.Sprintf("[gates.unit]\nargv = [%q, \"-c\", %q]\nlimit = %d\nneeds = { pool_a = 2, pool_b = 1 }\n",
		shell, script, limit)
}

// stepGateNamedStart returns a valid writing start in workspace that names gate name from gatesFile, with no argv.
func stepGateNamedStart(workspace, name, gatesFile string) StepStart {
	start := stepStartWriting(workspace)
	start.Gate = nil
	start.GateLimit = 0
	start.GateName = name
	start.GatesFile = gatesFile
	return start
}

// stepGateAdd returns the StepAdd the step was started with. The test fails if its first op is not one.
func stepGateAdd(t *testing.T, step StepSession) store.StepAdd {
	t.Helper()
	if len(step.Ops) == 0 {
		t.Fatal("StartStep got no ops")
	}
	add, ok := step.Ops[0].(store.StepAdd)
	if !ok {
		t.Fatalf("StartStep op is %T, want store.StepAdd", step.Ops[0])
	}
	return add
}

func TestStepsStartNamedGateStoresTheResolvedGate(t *testing.T) {
	f := newStepGateFixture(t)
	shell := stepGateShell(t)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepGateFileText(shell, "exit 0", 300))
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	commit := stepGitTestRun(t, linked, "rev-parse", "HEAD")
	start := stepGateNamedStart(linked, "unit", gatesFile)

	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	step := f.sessions.first(t)
	want := store.StepAdd{
		ID:         id,
		Agent:      start.Agent,
		Prompt:     start.Prompt,
		Schema:     start.Schema,
		Workspace:  linked,
		Revision:   commit,
		DoneValue:  stepLoopDone,
		Owned:      []string{stepDoneOwned},
		Gate:       []string{shell, "-c", "exit 0"},
		GateName:   "unit",
		GateSource: gates.SourceEpic,
		GateNeeds:  []store.StepNeed{{Pool: "pool_a", Count: 2}, {Pool: "pool_b", Count: 1}},
		GateLimit:  300 * time.Second,
		ModelLimit: stepDefaultModelLimit,
	}
	if got := stepGateAdd(t, step); !reflect.DeepEqual(got, want) {
		t.Fatalf("StepAdd = %#v, want %#v", got, want)
	}
	run, ok := step.Observer.(*stepRun)
	if !ok {
		t.Fatalf("StartStep Observer is %T, want *stepRun", step.Observer)
	}
	if !reflect.DeepEqual(run.gate, want.Gate) || run.gateLimit != want.GateLimit {
		t.Fatalf("step run gate = %q limit %s, want %q limit %s", run.gate, run.gateLimit, want.Gate, want.GateLimit)
	}
}

// stepGateProjectFile writes the project file of the work tree at linked, which defines gate lint: sh exiting zero and a
// two-minute limit, with no needs.
func stepGateProjectFile(t *testing.T, linked, shell string) {
	t.Helper()
	stepGateWriteFile(t, filepath.Join(linked, ".orrery", "project.toml"),
		fmt.Sprintf("[gates.lint]\nargv = [%q, \"-c\", \"exit 0\"]\nlimit = 120\n", shell))
}

func TestStepsStartNamedGateFromTheProjectFile(t *testing.T) {
	f := newStepGateFixture(t)
	shell := stepGateShell(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	commit := stepGitTestRun(t, linked, "rev-parse", "HEAD")
	stepGateProjectFile(t, linked, shell)
	start := stepGateNamedStart(linked, "lint", "")

	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	want := store.StepAdd{
		ID:         id,
		Agent:      start.Agent,
		Prompt:     start.Prompt,
		Schema:     start.Schema,
		Workspace:  linked,
		Revision:   commit,
		DoneValue:  stepLoopDone,
		Owned:      []string{stepDoneOwned},
		Gate:       []string{shell, "-c", "exit 0"},
		GateName:   "lint",
		GateSource: gates.SourceProject,
		GateNeeds:  []store.StepNeed{{Pool: gates.DefaultPool, Count: 1}},
		GateLimit:  120 * time.Second,
		ModelLimit: stepDefaultModelLimit,
	}
	if got := stepGateAdd(t, f.sessions.first(t)); !reflect.DeepEqual(got, want) {
		t.Fatalf("StepAdd = %#v, want %#v", got, want)
	}
}

func TestStepsStartNamedGateRefusalsStoreNothing(t *testing.T) {
	shell := stepGateShell(t)
	cases := []struct {
		name   string
		mutate func(*StepStart)
	}{
		{name: "name does not resolve", mutate: func(s *StepStart) { s.GateName = "missing" }},
		{name: "gates file missing", mutate: func(s *StepStart) {
			s.GatesFile = filepath.Join(t.TempDir(), "absent.toml")
		}},
		{name: "gates file not absolute", mutate: func(s *StepStart) { s.GatesFile = "gates.toml" }},
		{name: "gates file without a name", mutate: func(s *StepStart) { s.GateName = "" }},
		{name: "name with an argv", mutate: func(s *StepStart) {
			s.Gate = []string{shell, "-c", "exit 0"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepGateFixture(t)
			gatesFile := filepath.Join(t.TempDir(), "gates.toml")
			stepGateWriteFile(t, gatesFile, stepGateFileText(shell, "exit 0", 300))
			linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
			start := stepGateNamedStart(linked, "unit", gatesFile)
			tc.mutate(&start)

			id, err := f.steps.Start(t.Context(), start)
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

func TestStepsStartNamedGateRewriteAppliesToLaterStarts(t *testing.T) {
	f := newStepGateFixture(t)
	shell := stepGateShell(t)
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	stepGateWriteFile(t, gatesFile, stepGateFileText(shell, "exit 0", 300))
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	start := stepGateNamedStart(linked, "unit", gatesFile)

	if _, err := f.steps.Start(t.Context(), start); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	stepGateWriteFile(t, gatesFile, stepGateFileText(shell, "exit 1", 600))
	if _, err := f.steps.Start(t.Context(), start); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	f.sessions.mu.Lock()
	if len(f.sessions.started) != 2 {
		f.sessions.mu.Unlock()
		t.Fatalf("StartStep calls = %d, want 2", len(f.sessions.started))
	}
	firstStep, secondStep := f.sessions.started[0], f.sessions.started[1]
	f.sessions.mu.Unlock()

	if got := stepGateAdd(t, firstStep); !reflect.DeepEqual(got.Gate, []string{shell, "-c", "exit 0"}) ||
		got.GateLimit != 300*time.Second {
		t.Errorf("first step gate = %q limit %s, want the file before the rewrite", got.Gate, got.GateLimit)
	}
	if got := stepGateAdd(t, secondStep); !reflect.DeepEqual(got.Gate, []string{shell, "-c", "exit 1"}) ||
		got.GateLimit != 600*time.Second {
		t.Errorf("second step gate = %q limit %s, want the file after the rewrite", got.Gate, got.GateLimit)
	}
}

func TestStepsStartNamedGateReadsTheLastEnvironEntry(t *testing.T) {
	f := newStepGateFixture(t)
	shell := stepGateShell(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	stepGateProjectFile(t, linked, shell)
	// With no HOME, the machine pool file is located only through XDG_CONFIG_HOME, and the empty earlier entry must not
	// win over the later one. The gate is in the project file, so resolution consults the environment.
	f.steps.environ = []string{"PATH=" + os.Getenv("PATH"), "XDG_CONFIG_HOME=", "XDG_CONFIG_HOME=" + t.TempDir()}

	if _, err := f.steps.Start(t.Context(), stepGateNamedStart(linked, "lint", "")); err != nil {
		t.Fatalf("Start: %v", err)
	}
}
