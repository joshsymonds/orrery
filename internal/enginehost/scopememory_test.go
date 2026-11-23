package enginehost

import (
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

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

func TestNodeChecksGateRunsUnderTheConfiguredScope(t *testing.T) {
	t.Parallel()
	checks := NewNodeChecks(NodeChecksConfig{StateDir: t.TempDir(), GateScope: "/run/systemd-run"})
	run := checks.gate(t.TempDir(), store.NodeCheck{ID: "c", Argv: []string{"/bin/true"}, Limit: 1}, nil)
	if run.Scope != "/run/systemd-run" {
		t.Fatalf("gate scope %q, want /run/systemd-run", run.Scope)
	}
}

func TestEvidenceCaptureGateRunsUnderTheConfiguredScope(t *testing.T) {
	t.Parallel()
	captures := NewCaptures(CapturesConfig{StateDir: t.TempDir(), GateScope: "/run/systemd-run"})
	run := captures.captures.captureGate(t.TempDir(), "r", evidence.Item{Capture: []string{"/bin/true"}, Limit: 1}, nil)
	if run.Scope != "/run/systemd-run" {
		t.Fatalf("capture scope %q, want /run/systemd-run", run.Scope)
	}
}

func TestStepsGateRunsUnderTheConfiguredScope(t *testing.T) {
	t.Parallel()
	steps := NewSteps(StepsConfig{GateScope: "/run/systemd-run"})
	if steps.gateScope != "/run/systemd-run" {
		t.Fatalf("steps scope %q, want /run/systemd-run", steps.gateScope)
	}
}
