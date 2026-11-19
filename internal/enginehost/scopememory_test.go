package enginehost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// testScopeMemory is the memory cap the scope tests configure, 1 GiB, so the argv shows it in place of the default.
const testScopeMemory int64 = 1 << 30

// testScopeMemoryArg is the systemd-run property testScopeMemory gives.
const testScopeMemoryArg = "MemoryMax=1073741824"

// requireScopeMemoryArgs fails the test unless argv, a recorded systemd-run argv, caps the scope at want with no swap.
func requireScopeMemoryArgs(t *testing.T, argv []string, want string) {
	t.Helper()
	for _, prop := range []string{want, "MemorySwapMax=0"} {
		i := slices.Index(argv, prop)
		if i < 1 || argv[i-1] != "-p" {
			t.Fatalf("systemd-run argv %q has no -p %s", argv, prop)
		}
	}
}

func TestGateScopeMemoryCapsTheGatesScope(t *testing.T) {
	t.Parallel()
	record := filepath.Join(t.TempDir(), "argv")
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Argv:        []string{"/bin/sh", "-c", "echo ok"},
		Dir:         t.TempDir(),
		Limit:       time.Minute,
		Name:        "capped",
		StateDir:    gateShortStateDir(t),
		Scope:       fakeSystemdRunPath(t),
		ScopeMemory: testScopeMemory,
		Environ:     gateEnv("FAKE_SYSTEMD_RUN_ARGS=" + record),
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	t.Cleanup(func() {
		if clearErr := gateClear(context.WithoutCancel(t.Context()), result.Holder); clearErr != nil {
			t.Errorf("clear the gate's holder: %v", clearErr)
		}
	})
	if result.Exit != 0 || result.MemoryKills != 0 {
		t.Fatalf("result = %+v, want exit 0 with no kills", result)
	}
	requireScopeMemoryArgs(t, recordedArgs(t, record), testScopeMemoryArg)
}

func TestGateExitResultCarriesTheMemoryCapsKills(t *testing.T) {
	t.Parallel()
	line := "orrery holder: the scope's memory cap (memory.max 1073741824) killed 1 process"
	result := gateExitResult(grants.Grant{}, gatePlan{dir: "/h", tail: gateTailDefault}, holder.Exit{
		Reason: holder.ReasonExited, Code: 137, Stderr: "partial\n" + line, MemoryKills: 1,
	})
	if result.MemoryKills != 1 || result.Exit != 137 || !strings.HasSuffix(result.Stderr, line) {
		t.Fatalf("result = %+v, want exit 137 with one kill and the cap's line ending stderr", result)
	}
}

func TestStepLoopGateCheckNamesTheMemoryCapsKills(t *testing.T) {
	t.Parallel()
	check := stepLoopGateCheck(1, gateResult{Exit: 137, MemoryKills: 2}, time.Minute)
	want := "gate exited 137; its scope's memory cap killed 2 of its processes"
	if check.OK || check.Detail != want {
		t.Fatalf("check = %+v, want a failed check with detail %q", check, want)
	}
	plain := stepLoopGateCheck(1, gateResult{Exit: 1}, time.Minute)
	if plain.Detail != "gate exited 1" {
		t.Fatalf("detail with no kills = %q, want gate exited 1", plain.Detail)
	}
}

func TestNodeChecksGateRunsUnderTheConfiguredScopeMemory(t *testing.T) {
	t.Parallel()
	checks := NewNodeChecks(NodeChecksConfig{
		StateDir: t.TempDir(), GateScope: "/run/systemd-run", GateScopeMemory: testScopeMemory,
	})
	run := checks.gate(t.TempDir(), store.NodeCheck{ID: "c", Argv: []string{"/bin/true"}, Limit: 1}, nil)
	if run.Scope != "/run/systemd-run" || run.ScopeMemory != testScopeMemory {
		t.Fatalf("gate scope %q with memory %d, want /run/systemd-run with %d", run.Scope, run.ScopeMemory,
			testScopeMemory)
	}
}

func TestEvidenceCaptureGateRunsUnderTheConfiguredScopeMemory(t *testing.T) {
	t.Parallel()
	ev := NewEvidence(EvidenceConfig{
		StateDir: t.TempDir(), GateScope: "/run/systemd-run", GateScopeMemory: testScopeMemory,
	})
	run := ev.captures.captureGate(t.TempDir(), "r", evidence.Item{Capture: []string{"/bin/true"}, Limit: 1}, nil)
	if run.Scope != "/run/systemd-run" || run.ScopeMemory != testScopeMemory {
		t.Fatalf("capture scope %q with memory %d, want /run/systemd-run with %d", run.Scope, run.ScopeMemory,
			testScopeMemory)
	}
}

func TestStepsGateRunsUnderTheConfiguredScopeMemory(t *testing.T) {
	t.Parallel()
	steps := NewSteps(StepsConfig{GateScope: "/run/systemd-run", GateScopeMemory: testScopeMemory})
	if steps.gateScope != "/run/systemd-run" || steps.gateScopeMemory != testScopeMemory {
		t.Fatalf("steps scope %q with memory %d, want /run/systemd-run with %d", steps.gateScope,
			steps.gateScopeMemory, testScopeMemory)
	}
}

func TestStartWithHolderScopeMemoryCapsTheSessionsScope(t *testing.T) {
	t.Parallel()
	root := holderRoot(t)
	record := filepath.Join(t.TempDir(), "argv")
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:  sh,
		Claude: fakeClaudePath(t),
		Environ: []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "FAKE_SYSTEMD_RUN_ARGS=" + record,
		},
		Cap:               1,
		Writer:            writer,
		Now:               fixedClock,
		Store:             s,
		HolderScope:       fakeSystemdRunPath(t),
		HolderScopeMemory: testScopeMemory,
		StateDir:          root,
	})
	t.Cleanup(m.Close)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, m, root, id) })
	waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	requireScopeMemoryArgs(t, recordedArgs(t, record), testScopeMemoryArg)
}
