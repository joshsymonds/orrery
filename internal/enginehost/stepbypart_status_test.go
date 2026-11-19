package enginehost

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepByPartStatusRPCKeepsFullStep checks that the StepStatus RPC still answers the whole step: the stored result,
// and each check's detail, stdout, stderr and log. The state-only object is made by the tool, not by this RPC.
func TestStepByPartStatusRPCKeepsFullStep(t *testing.T) {
	t.Parallel()
	row := fullStepRow()
	row.Checks = []store.StepCheck{{
		Round: 1, Name: "gate", OK: false,
		Detail: "invented-detail", Stdout: "invented-stdout", Stderr: "invented-stderr",
		Log: "/invented/gate.log",
	}}
	fake := &fakeStepCommands{row: row}
	path, stop := startServerWithSteps(t, nil, fake)
	client := newStepClient(t, path)

	status, err := client.StepStatus(context.Background(), connect.NewRequest(&v1.StepStatusRequest{Id: "s1"}))
	if err != nil {
		t.Fatalf("StepStatus: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	step := status.Msg.GetStep()
	if got := step.GetResult(); got != row.Result {
		t.Errorf("StepStatus result = %q, want the stored result %q", got, row.Result)
	}
	checks := step.GetChecks()
	if len(checks) != 1 {
		t.Fatalf("StepStatus checks = %d, want 1", len(checks))
	}
	check := checks[0]
	if got := check.GetDetail(); got != "invented-detail" {
		t.Errorf("check detail = %q, want invented-detail", got)
	}
	if got := check.GetStdout(); got != "invented-stdout" {
		t.Errorf("check stdout = %q, want invented-stdout", got)
	}
	if got := check.GetStderr(); got != "invented-stderr" {
		t.Errorf("check stderr = %q, want invented-stderr", got)
	}
	if got := check.GetLog(); got != "/invented/gate.log" {
		t.Errorf("check log = %q, want /invented/gate.log", got)
	}
}
