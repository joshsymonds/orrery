package inotify_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/inotify"
)

// open returns a new instance, closed at cleanup.
func open(t *testing.T) *inotify.Inotify {
	t.Helper()
	ino, err := inotify.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = ino.Close() })
	return ino
}

// add adds a watch for mask on path, failing the test when the kernel refuses it.
func add(t *testing.T, ino *inotify.Inotify, path string, mask uint32) int {
	t.Helper()
	wd, err := ino.Add(path, mask)
	if err != nil {
		t.Fatalf("Add(%s): %v", path, err)
	}
	return wd
}

// next reads events from ino until one of watch wd has a mask in want, and returns it. The kernel delivers an event
// before the write that raised it returns, so the read never waits for one that was raised.
func next(t *testing.T, ino *inotify.Inotify, wd int, want uint32) inotify.Event {
	t.Helper()
	for {
		events, err := ino.Read()
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		for _, event := range events {
			if event.WD == wd && event.Mask&want != 0 {
				return event
			}
		}
	}
}

func TestReadReportsFileWriteAndDirectoryEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	ino := open(t)
	fileWD := add(t, ino, path, unix.IN_MODIFY)
	dirWD := add(t, ino, dir, unix.IN_CREATE)

	if err := os.WriteFile(path, []byte("line\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	next(t, ino, fileWD, unix.IN_MODIFY)

	if err := os.WriteFile(filepath.Join(dir, "entry"), nil, 0o600); err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if event := next(t, ino, dirWD, unix.IN_CREATE); event.Name != "entry" {
		t.Fatalf("create event name = %q, want %q", event.Name, "entry")
	}
}

func TestAddFileWatchesTheOpenedInode(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	ino := open(t)
	wd, err := ino.AddFile(file, unix.IN_CLOSE_NOWRITE)
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if err = file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	next(t, ino, wd, unix.IN_CLOSE_NOWRITE)
}

func TestRemoveDropsTheWatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ino := open(t)
	wd := add(t, ino, dir, unix.IN_CREATE)
	if err := ino.Remove(wd); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	next(t, ino, wd, unix.IN_IGNORED)
	if err := ino.Remove(wd); err == nil {
		t.Fatal("Remove of a dropped watch succeeded, want an error")
	}
}

func TestCloseEndsAWaitingRead(t *testing.T) {
	t.Parallel()
	ino, err := inotify.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	add(t, ino, t.TempDir(), unix.IN_CREATE)
	ended := make(chan error, 1)
	go func() {
		_, readErr := ino.Read()
		ended <- readErr
	}()
	if err = ino.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if readErr := <-ended; readErr == nil {
		t.Fatal("Read after Close succeeded, want an error")
	}
	if _, err = ino.Add(t.TempDir(), unix.IN_CREATE); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Add after Close: %v, want %v", err, os.ErrClosed)
	}
}
