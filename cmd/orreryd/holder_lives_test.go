package main

import (
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
)

// TestHolderLivesWhileItsLockIsHeld checks that a holder counts as running from the moment it holds its session lock.
// A holder takes the lock before it starts its child and writes its pids record only after, so a dir whose lock is held
// and that has no record yet is a running holder, as a gate's holder is when its script has run and orreryd restarts at
// once. Once the lock is free, the holder has ended.
func TestHolderLivesWhileItsLockIsHeld(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if holderLives(dir) {
		t.Fatal("a dir with no lock file counts as a running holder")
	}
	lock, err := holder.Lock(filepath.Join(dir, holder.LockName))
	if err != nil {
		t.Fatalf("take the session lock: %v", err)
	}
	if !holderLives(dir) {
		t.Fatal("a holder that holds its lock and has not written its pids record yet counts as gone")
	}
	if err = lock.Close(); err != nil {
		t.Fatalf("free the session lock: %v", err)
	}
	if holderLives(dir) {
		t.Fatal("a dir whose lock is free counts as a running holder")
	}
}
