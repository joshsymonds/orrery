// Package exectest writes the programs tests run. It is imported only by tests.
package exectest

import (
	"os"
	"syscall"
	"testing"
)

// WriteFile writes data to path with mode perm, as os.WriteFile does, while holding syscall.ForkLock for reading.
//
// A program a test writes and then runs can fail to start with "text file busy" (ETXTBSY) when the test binary runs
// tests in parallel: a fork another test makes while the file is still open for writing copies that open file into
// the child, and the file stays open for writing until the child execs, however soon this test closes its own copy.
// os/exec forks only while it holds syscall.ForkLock for writing, so no fork can happen between this open and close.
func WriteFile(tb testing.TB, path string, data []byte, perm os.FileMode) {
	tb.Helper()
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	if err := os.WriteFile(path, data, perm); err != nil {
		tb.Fatalf("write %s: %v", path, err)
	}
}
