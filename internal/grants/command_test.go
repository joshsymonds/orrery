package grants

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// commandScript writes a fake command that runs body under /bin/sh into a new directory, and returns its path.
func commandScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write fake command: %v", err)
	}
	return path
}

// commandEnv is the environment a fake command runs in: PATH, so the sleep it may run resolves, then extra.
func commandEnv(extra ...string) []string {
	return append([]string{"PATH=" + os.Getenv("PATH")}, extra...)
}

// commandGroupBody starts a background sleep, writes "$$ $! $pgrp" to $PIDS (the leader's pid, the child's pid,
// and the leader's process group id from /proc), then sleeps. The group id is the field after the ") " that ends
// the comm name in /proc/<pid>/stat.
const commandGroupBody = `sleep 60 & read -r stat < /proc/$$/stat; rest=${stat##*) }; set -- $rest; ` +
	`echo "$$ $! $3" > "$PIDS"; sleep 60`

// commandPids polls until path holds the three pids commandGroupBody writes, and returns them.
func commandPids(t *testing.T, path string) (int, int, int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.HasSuffix(string(data), "\n") {
			fields := strings.Fields(string(data))
			if len(fields) != 3 {
				t.Fatalf("pid file %s = %q, want three pids", path, string(data))
			}
			values := make([]int, 3)
			for i, field := range fields {
				value, convErr := strconv.Atoi(field)
				if convErr != nil {
					t.Fatalf("pid file %s field %q: %v", path, field, convErr)
				}
				values[i] = value
			}
			return values[0], values[1], values[2]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fake command never wrote its pids to %s", path)
	return 0, 0, 0
}

// commandProcessGone reports whether pid has exited: no /proc entry, or a zombie waiting to be reaped.
func commandProcessGone(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	_, after, found := strings.Cut(string(stat), ") ")
	return found && (strings.HasPrefix(after, "Z") || strings.HasPrefix(after, "X"))
}

// commandWaitGone polls until every pid in pids has exited.
func commandWaitGone(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, pid := range pids {
		for !commandProcessGone(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("process %d is still running after the command was killed", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestRunCommandSucceedsOnExitZero(t *testing.T) {
	outcome, err := RunCommand(t.Context(), []string{commandScript(t, "exit 0")}, time.Minute, commandEnv())
	if outcome != Succeeded {
		t.Fatalf("outcome = %d, want Succeeded", outcome)
	}
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
}

func TestRunCommandExitedOnNonzeroExit(t *testing.T) {
	outcome, err := RunCommand(t.Context(), []string{commandScript(t, "exit 3")}, time.Minute, commandEnv())
	if outcome != Exited {
		t.Fatalf("outcome = %d, want Exited", outcome)
	}
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v, want it to name exit status 3", err)
	}
}

func TestRunCommandFailedWhenItDoesNotStart(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	cases := map[string]struct {
		argv  []string
		cause string
	}{
		"no argv":          {argv: []string{}, cause: "argv"},
		"absolute missing": {argv: []string{missing}, cause: missing},
		"bare missing": {
			argv:  []string{"orrery-no-such-command-for-test"},
			cause: "orrery-no-such-command-for-test",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			outcome, err := RunCommand(t.Context(), tc.argv, time.Minute, commandEnv())
			if outcome != Failed {
				t.Fatalf("outcome = %d, want Failed", outcome)
			}
			if err == nil || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("err = %v, want it to name %q", err, tc.cause)
			}
		})
	}
}

func TestRunCommandFindsArgvZeroOnPath(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\nprintf ran > \"$MARKER\"\n"
	if err := os.WriteFile(filepath.Join(dir, "orrery-fake-member"), []byte(script), 0o700); err != nil {
		t.Fatalf("write fake command: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	outcome, err := RunCommand(t.Context(), []string{"orrery-fake-member"}, time.Minute, commandEnv("MARKER="+marker))
	if outcome != Succeeded {
		t.Fatalf("outcome = %d, want Succeeded", outcome)
	}
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("command did not run from PATH: %v", err)
	}
	if string(data) != "ran" {
		t.Fatalf("marker = %q, want ran", data)
	}
}

func TestRunCommandPassesArgumentsWithoutShell(t *testing.T) {
	out := filepath.Join(t.TempDir(), "arg")
	arg := `$(printf injected); touch "$OUT"`
	outcome, err := RunCommand(t.Context(),
		[]string{commandScript(t, `printf '%s' "$1" > "$OUT"`), arg}, time.Minute, commandEnv("OUT="+out))
	if outcome != Succeeded {
		t.Fatalf("outcome = %d, want Succeeded", outcome)
	}
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read argument: %v", err)
	}
	if string(data) != arg {
		t.Fatalf("argument = %q, want it passed literally as %q", data, arg)
	}
}

func TestRunCommandGivesExactlyEnv(t *testing.T) {
	t.Setenv("ORRERY_AMBIENT", "leaked")
	out := filepath.Join(t.TempDir(), "env")
	body := `printf '%s|%s' "$KEPT" "${ORRERY_AMBIENT-unset}" > "$OUT"`
	outcome, err := RunCommand(t.Context(), []string{commandScript(t, body)}, time.Minute,
		commandEnv("KEPT=yes", "OUT="+out))
	if outcome != Succeeded {
		t.Fatalf("outcome = %d, want Succeeded", outcome)
	}
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read environment: %v", err)
	}
	if string(data) != "yes|unset" {
		t.Fatalf("environment = %q, want only the given KEPT=yes", data)
	}
}

// stdinHelperEnv marks the child test binary that TestRunCommandGivesNoStdin starts.
const stdinHelperEnv = "ORRERY_GRANTS_STDIN_HELPER"

// TestRunCommandGivesNoStdin runs TestRunCommandStdinHelper in a child test binary whose stdin holds a line. A
// command that inherited that stdin would read the line and exit 7; a command with no stdin reads end of file.
func TestRunCommandGivesNoStdin(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	if _, err = writer.WriteString("line\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRunCommandStdinHelper$")
	cmd.Env = append(os.Environ(), stdinHelperEnv+"=1")
	cmd.Stdin = reader
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stdin helper: %v\n%s", err, out)
	}
}

// TestRunCommandStdinHelper is the child that TestRunCommandGivesNoStdin starts. It runs only there.
func TestRunCommandStdinHelper(t *testing.T) {
	if os.Getenv(stdinHelperEnv) == "" {
		t.Skip("runs only as the child of TestRunCommandGivesNoStdin")
	}
	argv := []string{commandScript(t, `if read -r line; then exit 7; fi`)}
	outcome, err := RunCommand(t.Context(), argv, time.Minute, commandEnv())
	if outcome != Succeeded {
		t.Fatalf("outcome = %d, want Succeeded: the command read this process's stdin (err %v)", outcome, err)
	}
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
}

func TestRunCommandDiscardsOutput(t *testing.T) {
	// One mebibyte on each of stdout and stderr: a pipe nobody reads would hold the command until its limit.
	body := `i=0; while [ "$i" -lt 16384 ]; do printf '%064d' 0; printf '%064d' 0 >&2; i=$((i+1)); done`
	outcome, err := RunCommand(t.Context(), []string{commandScript(t, body)}, 30*time.Second, commandEnv())
	if outcome != Succeeded {
		t.Fatalf("outcome = %d, want Succeeded", outcome)
	}
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
}

func TestRunCommandKillsItsGroupAtLimit(t *testing.T) {
	pids := filepath.Join(t.TempDir(), "pids")
	started := time.Now()
	outcome, err := RunCommand(t.Context(), []string{commandScript(t, commandGroupBody)}, 2*time.Second,
		commandEnv("PIDS="+pids))
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("RunCommand returned after %v: it waited for the background child instead of "+
			"killing the group", elapsed)
	}
	if outcome != Failed {
		t.Fatalf("outcome = %d, want Failed for a command killed at its limit", outcome)
	}
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want it to name the limit", err)
	}
	leader, child, group := commandPids(t, pids)
	if group != leader {
		t.Fatalf("process group = %d, want the command's own pid %d", group, leader)
	}
	commandWaitGone(t, leader, child)
}

func TestRunCommandKillsItsGroupWhenContextEnds(t *testing.T) {
	pids := filepath.Join(t.TempDir(), "pids")
	argv := []string{commandScript(t, commandGroupBody)}
	env := commandEnv("PIDS=" + pids)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type result struct {
		outcome Outcome
		err     error
	}
	done := make(chan result, 1)
	go func() {
		outcome, err := RunCommand(ctx, argv, time.Minute, env)
		done <- result{outcome, err}
	}()

	leader, child, _ := commandPids(t, pids)
	cancel()
	select {
	case got := <-done:
		if got.outcome != Failed {
			t.Fatalf("outcome = %d, want Failed for a command whose context ended", got.outcome)
		}
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("err = %v, want it to wrap context.Canceled", got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunCommand never returned after its context ended")
	}
	commandWaitGone(t, leader, child)
}
