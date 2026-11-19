package main

import (
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestStepByPartStatusCommandIsStateOnly checks that `orrery step status` prints a step's state only: one line holding
// the state-only step object, with the step's result, its check detail, stdout, stderr and log, and the rest of its
// reason left out.
func TestStepByPartStatusCommandIsStateOnly(t *testing.T) {
	t.Parallel()
	row := &v1.Step{
		Id:        "s1",
		Agent:     "claude",
		Workspace: "/invented/workspace",
		Revision:  "invented-revision",
		Worktree:  "/invented/worktree",
		End:       "checks_failed",
		Status:    "fail",
		Result:    `{"summary":"invented-result"}`,
		Stage:     "checks",
		Doing:     "invented-doing",
		Epic:      "invented-epic",
		Task:      "invented-task",
		Checks: []*v1.StepCheck{{
			Round: 1, Name: "gate", Ok: false,
			Detail: "invented-detail", Stdout: "invented-stdout", Stderr: "invented-stderr",
			Log: "/invented/gate.log",
		}},
		Reason:  "gate red\ninvented-reason-tail",
		Started: 1700000000,
		Ended:   1700000600,
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, &stepsFake{rows: map[string]*v1.Step{"s1": row}})
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "status", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	want := `{"id":"s1","agent":"claude","end":"checks_failed","status":"fail","stage":"checks",` +
		`"doing":"invented-doing","epic":"invented-epic","task":"invented-task",` +
		`"started":1700000000,"ended":1700000600,"limited":false,"limitKind":"","limitResets":0,` +
		`"reason":"gate red","checks":[{"round":1,"name":"gate","ok":false}]}` + "\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	} else {
		t.Logf("orrery step status printed: %s", strings.TrimSuffix(stdout, "\n"))
	}
	for _, leaked := range []string{
		"invented-result", "invented-detail", "invented-stdout", "invented-stderr", "/invented/gate.log",
		"invented-reason-tail",
	} {
		if strings.Contains(stdout, leaked) {
			t.Errorf("orrery step status prints %q, want the state only: %s", leaked, stdout)
		} else {
			t.Logf("orrery step status prints no %q", leaked)
		}
	}
}
