package holder

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// clearLockHeldFor is how long a test holds the session lock before it releases it, so Clear waits on the lock.
const clearLockHeldFor = 50 * time.Millisecond

// requireDirGone fails when dir still exists.
func requireDirGone(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dir %s: err %v, want it removed", dir, err)
	}
}

func TestClearMissingDirIsNil(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "missing")
	if err := Clear(t.Context(), dir); err != nil {
		t.Fatalf("Clear(missing dir) = %v, want nil", err)
	}
}

func TestClearRemovesLeftoverFilesWhenNoHolderServesDir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	exit := Exit{Reason: ReasonExited, Code: 3, StdoutEnd: int64(len("out\n")), Stderr: "err"}
	if err := WriteExit(filepath.Join(cfg.Dir, ExitName), exit); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dir, segmentName(0)), []byte("out\n"), filesTestMode); err != nil {
		t.Fatalf("WriteFile segment: %v", err)
	}
	// A socket file with no listener behind it is what a holder that died left behind.
	if err := os.WriteFile(socketOf(t, cfg.Dir), nil, socketTestMode); err != nil {
		t.Fatalf("WriteFile stale socket: %v", err)
	}

	if err := Clear(t.Context(), cfg.Dir); err != nil {
		t.Fatalf("Clear = %v, want nil", err)
	}
	requireDirGone(t, cfg.Dir)
}

func TestClearStopsRunningHolderThenRemovesDir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	dialHolder(t, run, sock).hello(WireVersion)
	pid := awaitChildPid(t, cfg.Child.Dir)

	if err := Clear(t.Context(), cfg.Dir); err != nil {
		t.Fatalf("Clear = %v, want nil", err)
	}
	if err := run.wait(t); err != nil {
		t.Fatalf("holder Run = %v, want nil", err)
	}
	requireChildGroupGone(t, pid)
	requireDirGone(t, cfg.Dir)
}

func TestClearWaitsForHeldLockThenRemovesDir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	held, err := Lock(filepath.Join(cfg.Dir, LockName))
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	release := time.AfterFunc(clearLockHeldFor, func() {
		if cerr := held.Close(); cerr != nil {
			t.Errorf("close held lock: %v", cerr)
		}
	})
	defer release.Stop()

	if err := Clear(t.Context(), cfg.Dir); err != nil {
		t.Fatalf("Clear = %v, want nil once the lock is released", err)
	}
	requireDirGone(t, cfg.Dir)
}

func TestClearWithLockHeldAndCancelledContextKeepsDir(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	lockForTest(t, filepath.Join(cfg.Dir, LockName))
	if err := WriteExit(filepath.Join(cfg.Dir, ExitName), Exit{Reason: ReasonExited, Code: 0}); err != nil {
		t.Fatalf("WriteExit: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := Clear(ctx, cfg.Dir)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Clear = %v, want an error matching ErrLocked", err)
	}
	assertDirNames(t, cfg.Dir, ExitName, LockName)
}
