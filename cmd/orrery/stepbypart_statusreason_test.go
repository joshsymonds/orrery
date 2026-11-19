package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestStepByPartStatusCommandCutsReason checks that `orrery step status` prints a reason's first line cut to 200 runes,
// with a limit reported beside it, and leaves out the rest of the reason, the result and the check's output.
func TestStepByPartStatusCommandCutsReason(t *testing.T) {
	t.Parallel()
	row := &v1.Step{
		Id:          "s1",
		Agent:       "claude",
		Workspace:   "/invented/workspace",
		Revision:    "invented-revision",
		Worktree:    "/invented/worktree",
		End:         "checks_failed",
		Status:      "fail",
		Result:      `{"summary":"invented-result"}`,
		Stage:       "checks",
		Doing:       "invented-doing",
		Epic:        "invented-epic",
		Task:        "invented-task",
		Limited:     true,
		LimitKind:   "invented-limit-kind",
		LimitResets: 1700003600,
		Checks: []*v1.StepCheck{{
			Round: 1, Name: "gate", Ok: false,
			Detail: "invented-detail", Stdout: "invented-stdout", Stderr: "invented-stderr",
			Log: "/invented/gate.log",
		}},
		Reason:  strings.Repeat("é", 250) + "\ninvented-reason-tail",
		Started: 1700000000,
		Ended:   1700000600,
	}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, &stepsFake{rows: map[string]*v1.Step{"s1": row}})
	code, stdout, stderr := runStepCapture(t, newHoldClock(), daemon, "status", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	var got struct {
		Reason      string `json:"reason"`
		Limited     bool   `json:"limited"`
		LimitKind   string `json:"limitKind"`
		LimitResets int64  `json:"limitResets"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("orrery step status printed %q, want one JSON object: %v", stdout, err)
	}
	t.Logf("orrery step status printed: %s", strings.TrimSuffix(stdout, "\n"))

	wantReason := strings.Repeat("é", 199) + "…"
	if got.Reason != wantReason {
		t.Errorf("reason = %q, want 199 runes of é followed by …", got.Reason)
	} else if n := utf8.RuneCountInString(got.Reason); n != 200 {
		t.Errorf("reason is %d runes, want 200", n)
	} else {
		t.Logf("orrery step status reason is the 250-rune first line cut to 200 runes (199 + …)")
	}

	if !got.Limited || got.LimitKind != "invented-limit-kind" || got.LimitResets != 1700003600 {
		t.Errorf("limited = %t, limitKind = %q, limitResets = %d; want true, invented-limit-kind, 1700003600",
			got.Limited, got.LimitKind, got.LimitResets)
	} else {
		t.Logf("orrery step status limited = true, limitKind = invented-limit-kind, limitResets = 1700003600")
	}

	for _, leaked := range []string{
		"invented-reason-tail", "invented-result", "invented-detail", "invented-stdout", "invented-stderr",
		"/invented/gate.log",
	} {
		if strings.Contains(stdout, leaked) {
			t.Errorf("orrery step status prints %q, want the reason cut and the state only: %s", leaked, stdout)
		} else {
			t.Logf("orrery step status prints no %q", leaked)
		}
	}
}
