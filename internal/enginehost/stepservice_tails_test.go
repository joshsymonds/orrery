package enginehost

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepMessageCarriesEveryCheckTails checks that stepMessage passes each check's stdout and stderr tails through
// unchanged, for a gate check and for a passing check alike. The tails are redacted where the store gets them.
func TestStepMessageCarriesEveryCheckTails(t *testing.T) {
	t.Parallel()
	row := fullStepRow()
	row.Checks = []store.StepCheck{
		{
			Round: 1, Name: "gate", OK: false, Detail: "exit 1",
			Stdout: "synthetic gate stdout", Stderr: "synthetic gate stderr",
		},
		{
			Round: 2, Name: "build", OK: true,
			Stdout: "synthetic build stdout", Stderr: "synthetic build stderr",
		},
	}

	step, err := stepMessage(row, false)
	if err != nil {
		t.Fatalf("stepMessage: %v", err)
	}
	checks := step.GetChecks()
	if len(checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(checks))
	}
	if got := checks[0]; got.GetStdout() != "synthetic gate stdout" || got.GetStderr() != "synthetic gate stderr" {
		t.Errorf("gate check tails = stdout %q stderr %q, want the gate check's stdout and stderr",
			got.GetStdout(), got.GetStderr())
	}
	if got := checks[1]; got.GetStdout() != "synthetic build stdout" || got.GetStderr() != "synthetic build stderr" {
		t.Errorf("build check tails = stdout %q stderr %q, want the build check's stdout and stderr",
			got.GetStdout(), got.GetStderr())
	}
}
