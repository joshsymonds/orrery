package exectest

import (
	"bufio"
	"io"
	"os/exec"
	"testing"
)

// execLine is the line the wait of AwaitExec writes to a process and reads back from it.
const execLine = "exec\n"

// AwaitExec readies cmd for a wait until its process has finished its exec, and returns that wait, to be called once cmd
// has started. os/exec returns from Start once the child's exec has passed its point of no return, which is before the
// kernel has laid out the new program's arguments and environment, so /proc/<pid>/environ of a process just started
// can read empty. A process that runs code of its own has finished its exec: the wait writes a line to the process's
// standard input and returns once the process has written that line back to its standard output. cmd must run a program
// that copies its input to its output, such as cat, and must not set Stdin or Stdout. The wait has no deadline of its
// own, and it fails tb when the line cannot be written, or when the process ends or writes another line instead.
func AwaitExec(tb testing.TB, cmd *exec.Cmd) (wait func()) {
	tb.Helper()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		tb.Fatalf("exectest: pipe to the input of %s: %v", cmd.Path, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		tb.Fatalf("exectest: pipe from the output of %s: %v", cmd.Path, err)
	}
	return func() {
		tb.Helper()
		if _, writeErr := io.WriteString(stdin, execLine); writeErr != nil {
			tb.Fatalf("exectest: write to %s: %v", cmd.Path, writeErr)
		}
		got, readErr := bufio.NewReader(stdout).ReadString('\n')
		if readErr != nil {
			tb.Fatalf("exectest: %s wrote %q and then failed: %v", cmd.Path, got, readErr)
		}
		if got != execLine {
			tb.Fatalf("exectest: %s wrote %q, want %q", cmd.Path, got, execLine)
		}
	}
}
