package holder

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// filesTestMode is the mode the contract requires of both the lock file and the exit file: only the holder's user
// may open either.
const filesTestMode os.FileMode = 0o600

// lockForTest takes the session lock at path and closes it when the test ends.
func lockForTest(t *testing.T, path string) {
	t.Helper()
	file, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock(%s): %v", path, err)
	}
	t.Cleanup(func() {
		if cerr := file.Close(); cerr != nil {
			t.Errorf("Close lock: %v", cerr)
		}
	})
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}

// assertDirNames fails unless dir holds exactly the names in want, so a leftover temporary file shows up.
func assertDirNames(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if !slices.Equal(got, want) {
		t.Errorf("directory %s holds %q, want %q", dir, got, want)
	}
}

func TestLockRefusesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.lock")
	lockForTest(t, path)

	second, err := Lock(path)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Lock: err = %v, want ErrLocked", err)
	}
	if second != nil {
		t.Fatalf("second Lock returned a file alongside ErrLocked")
	}
}

func TestLockFreedByClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.lock")
	first, err := Lock(path)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first lock: %v", err)
	}

	lockForTest(t, path)
}

func TestLockFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.lock")
	lockForTest(t, path)

	assertMode(t, path, filesTestMode)
}

func TestWriteExitRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		exit Exit
	}{
		{
			name: "exited with output tail",
			exit: Exit{Reason: ReasonExited, Code: 3, StdoutEnd: 4096, Stderr: "boom: \"quoted\"\nlast line\n"},
		},
		{
			name: "stopped by signal",
			exit: Exit{Reason: ReasonStopped, Code: -1, Signal: "SIGTERM", StdoutEnd: 0, Stderr: ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "exit.json")
			if err := WriteExit(path, tc.exit); err != nil {
				t.Fatalf("WriteExit: %v", err)
			}
			got, err := ReadExit(path)
			if err != nil {
				t.Fatalf("ReadExit: %v", err)
			}
			if got != tc.exit {
				t.Errorf("ReadExit = %+v, want %+v", got, tc.exit)
			}
		})
	}
}

func TestWriteExitMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exit.json")
	if err := WriteExit(path, Exit{Reason: ReasonIdle, Code: 0}); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}

	assertMode(t, path, filesTestMode)
}

func TestWriteExitRefusesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exit.json")
	first := Exit{Reason: ReasonExited, Code: 0, StdoutEnd: 10, Stderr: "first"}
	if err := WriteExit(path, first); err != nil {
		t.Fatalf("first WriteExit: %v", err)
	}

	second := Exit{Reason: ReasonStopped, Code: -1, Signal: "SIGKILL", StdoutEnd: 99, Stderr: "second"}
	if err := WriteExit(path, second); !errors.Is(err, ErrExitExists) {
		t.Fatalf("second WriteExit: err = %v, want ErrExitExists", err)
	}

	got, err := ReadExit(path)
	if err != nil {
		t.Fatalf("ReadExit: %v", err)
	}
	if got != first {
		t.Errorf("ReadExit after refused write = %+v, want first record %+v", got, first)
	}
}

func TestWriteExitLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exit.json")
	if err := WriteExit(path, Exit{Reason: ReasonExited, Code: 1}); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}
	assertDirNames(t, dir, "exit.json")

	if err := WriteExit(path, Exit{Reason: ReasonIdle, Code: 2}); !errors.Is(err, ErrExitExists) {
		t.Fatalf("second WriteExit: err = %v, want ErrExitExists", err)
	}
	assertDirNames(t, dir, "exit.json")
}
