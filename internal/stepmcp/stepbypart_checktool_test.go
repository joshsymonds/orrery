package stepmcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// checkSteps is the daemon's steps for the check tool. Its Status answers row, and it keeps its gate logs in logs. Every
// other command is the embedded StepCommands', which the tool never reaches.
type checkSteps struct {
	enginehost.StepCommands
	row  store.StepRow
	logs string
}

// Status answers the fake's row whatever the id.
func (f checkSteps) Status(context.Context, string) (store.StepRow, error) {
	return f.row, nil
}

// The service finds the kept gate logs by a type assertion, so this keeps checkSteps satisfying the method it asserts.
var _ interface{ KeptGateLogs() (string, error) } = checkSteps{}

// KeptGateLogs names the directory the fake keeps its gate logs in.
func (f checkSteps) KeptGateLogs() (string, error) {
	return f.logs, nil
}

// logLine returns line i of a log whose lines start with prefix. Each 10th line also says FAIL.
func logLine(prefix string, i int) string {
	line := fmt.Sprintf("%s %03d", prefix, i)
	if i%10 == 0 {
		line += " FAIL"
	}
	return line
}

// numberedLines returns the lines from through to of a log that logLine numbers with prefix.
func numberedLines(prefix string, from, to int) []string {
	var lines []string
	for i := from; i <= to; i++ {
		lines = append(lines, logLine(prefix, i))
	}
	return lines
}

// writeKeptLog writes lines, each ended by a newline, as the file name in dir and returns its path.
func writeKeptLog(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// expectCheckText fails t unless outcome is a result, not a tool error, whose text is want.
func expectCheckText(t *testing.T, args string, outcome toolOutcome, want string) {
	t.Helper()
	if outcome.isError {
		t.Fatalf("step_check %s reported an error: %s", args, outcome.text)
	}
	if outcome.text != want {
		t.Fatalf("step_check %s = %q, want %q", args, outcome.text, want)
	}
}

// TestStepByPartCheckTool checks step_check over MCP against a daemon whose step failed its gate in two rounds and its
// lint check once. Each gate round kept its log: round 1 holds five lines, round 2 a hundred. A check with no kept log
// reads its stored tails. The header names the round and whether it passed, the detail follows, and the slice is the
// last lines of the kept log, bounded by lines and before, filtered by match. An unknown check and a call without check
// are tool errors.
func TestStepByPartCheckTool(t *testing.T) {
	logs := t.TempDir()
	round1 := writeKeptLog(t, logs, "round-1.log", numberedLines("round one", 1, 5))
	round2 := writeKeptLog(t, logs, "round-2.log", numberedLines("line", 1, 100))
	row := store.StepRow{
		ID:     "s1",
		Agent:  "reviewer",
		Status: "fail",
		End:    "checks_failed",
		Checks: []store.StepCheck{
			{
				Round: 1, Name: "gate", OK: false, Detail: "exit 1 in round one",
				Stdout: "round one stdout\n", Stderr: "round one stderr\n", Log: round1,
			},
			{
				Round: 2, Name: "gate", OK: false, Detail: "exit 1 in round two",
				Stdout: "round two stdout\n", Stderr: "round two stderr\n", Log: round2,
			},
			{
				Round: 2, Name: "lint", OK: false, Detail: "2 issues",
				Stdout: "lint: 2 issues\n", Stderr: "lint: exit 1\n",
			},
		},
	}
	p := newPeer(t, startDaemon(t, checkSteps{row: row, logs: logs}), nil)
	gateRoundTwo := []string{"gate round 2: failed", "exit 1 in round two"}

	t.Run("header, detail and last 40 lines of the kept log", func(t *testing.T) {
		args := `{"id":"s1","check":"gate"}`
		want := strings.Join(slices.Concat(
			gateRoundTwo,
			[]string{"[output lines 61-100 of 100 from the kept log]"},
			numberedLines("line", 61, 100),
			[]string{"[earlier lines: continue with before 61]"},
		), "\n")
		expectCheckText(t, args, p.tool("step_check", args), want)
	})

	t.Run("lines and before page backward to the first line", func(t *testing.T) {
		for _, tc := range []struct {
			args string
			want []string
		}{
			{
				args: `{"id":"s1","check":"gate","lines":10}`,
				want: slices.Concat(
					gateRoundTwo,
					[]string{"[output lines 91-100 of 100 from the kept log]"},
					numberedLines("line", 91, 100),
					[]string{"[earlier lines: continue with before 91]"},
				),
			},
			{
				args: `{"id":"s1","check":"gate","lines":10,"before":91}`,
				want: slices.Concat(
					gateRoundTwo,
					[]string{"[output lines 81-90 of 100 from the kept log]"},
					numberedLines("line", 81, 90),
					[]string{"[earlier lines: continue with before 81]"},
				),
			},
			{
				args: `{"id":"s1","check":"gate","lines":10,"before":11}`,
				want: slices.Concat(
					gateRoundTwo,
					[]string{"[output lines 1-10 of 100 from the kept log]"},
					numberedLines("line", 1, 10),
				),
			},
			{
				args: `{"id":"s1","check":"gate","lines":10,"before":1}`,
				want: slices.Concat(gateRoundTwo, []string{"[no output lines]"}),
			},
		} {
			expectCheckText(t, tc.args, p.tool("step_check", tc.args), strings.Join(tc.want, "\n"))
		}
	})

	t.Run("match keeps only matching lines", func(t *testing.T) {
		args := `{"id":"s1","check":"gate","match":"FAIL"}`
		var failing []string
		for i := 10; i <= 100; i += 10 {
			failing = append(failing, fmt.Sprintf("line %03d FAIL", i))
		}
		want := strings.Join(slices.Concat(
			gateRoundTwo,
			[]string{"[output lines 1-10 of 10 from the kept log]"},
			failing,
		), "\n")
		expectCheckText(t, args, p.tool("step_check", args), want)
	})

	t.Run("a match no line holds reads as no output lines match", func(t *testing.T) {
		args := `{"id":"s1","check":"gate","match":"NOPE"}`
		want := strings.Join(append(slices.Clone(gateRoundTwo), `[no output lines match "NOPE"]`), "\n")
		expectCheckText(t, args, p.tool("step_check", args), want)
	})

	t.Run("round defaults to the last round, and an earlier round is read when named", func(t *testing.T) {
		defaulted := `{"id":"s1","check":"gate","lines":1}`
		wantDefault := strings.Join(slices.Concat(
			gateRoundTwo,
			[]string{"[output lines 100-100 of 100 from the kept log]"},
			numberedLines("line", 100, 100),
			[]string{"[earlier lines: continue with before 100]"},
		), "\n")
		expectCheckText(t, defaulted, p.tool("step_check", defaulted), wantDefault)

		named := `{"id":"s1","check":"gate","round":1}`
		wantNamed := strings.Join(slices.Concat(
			[]string{"gate round 1: failed", "exit 1 in round one"},
			[]string{"[output lines 1-5 of 5 from the kept log]"},
			numberedLines("round one", 1, 5),
		), "\n")
		expectCheckText(t, named, p.tool("step_check", named), wantNamed)
	})

	t.Run("a check with no kept log shows its stored tails", func(t *testing.T) {
		args := `{"id":"s1","check":"lint"}`
		want := strings.Join([]string{
			"lint round 2: failed",
			"2 issues",
			"[output lines 1-4 of 4 from the stored tails]",
			"== stdout ==",
			"lint: 2 issues",
			"== stderr ==",
			"lint: exit 1",
		}, "\n")
		expectCheckText(t, args, p.tool("step_check", args), want)
	})

	t.Run("an unknown check is a tool error naming the checks", func(t *testing.T) {
		args := `{"id":"s1","check":"nope"}`
		outcome := p.tool("step_check", args)
		if !outcome.isError {
			t.Fatalf("step_check %s = %q, want a tool error", args, outcome.text)
		}
		if !strings.Contains(outcome.text, "its checks are gate, lint") {
			t.Fatalf("step_check %s error = %q, want it to name the checks gate and lint", args, outcome.text)
		}
	})

	t.Run("a call without check is refused", func(t *testing.T) {
		args := `{"id":"s1"}`
		outcome := p.tool("step_check", args)
		if !outcome.isError {
			t.Fatalf("step_check %s = %q, want a tool error", args, outcome.text)
		}
		if !strings.Contains(outcome.text, "check is required") {
			t.Fatalf("step_check %s error = %q, want it to contain %q", args, outcome.text, "check is required")
		}
	})
}
