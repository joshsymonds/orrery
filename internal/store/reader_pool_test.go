package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// closeReader closes r when the test ends, and fails the test when the close fails.
func closeReader(t *testing.T, r *Reader) {
	t.Helper()
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("Close reader: %v", err)
		}
	})
}

// TestReaderPoolOpensOneConnection makes several calls of each kind of read, each kind on a pooled reader of its
// own. The first call opens the pool's one connection, and each call after it reuses that connection, so the
// connector's gate runs once. A read that opened its connection outside the pool would leave the gate at zero.
func TestReaderPoolOpensOneConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)

	reads := []struct {
		name string
		read func(*Reader) error
	}{
		{"Snapshot", func(r *Reader) error { _, err := r.Snapshot(ctx); return err }},
		{"Events", func(r *Reader) error { _, err := r.Events(ctx, 0, 10); return err }},
		{"Dirs", func(r *Reader) error { _, err := r.Dirs(ctx); return err }},
		{"LastTurnEnds", func(r *Reader) error { _, err := r.LastTurnEnds(ctx); return err }},
		{"StepFacts", func(r *Reader) error { _, err := r.StepFacts(ctx, []string{"step-1"}); return err }},
	}
	for _, read := range reads {
		r := NewPooledReader(path)
		closeReader(t, r)
		var opens atomic.Int32
		r.connector.gate = func(context.Context) { opens.Add(1) }
		for call := 1; call <= 3; call++ {
			if err := read.read(r); err != nil {
				t.Fatalf("%s call %d: %v", read.name, call, err)
			}
			if got := opens.Load(); got != 1 {
				t.Fatalf("%s call %d: %d connections opened, want 1", read.name, call, got)
			}
		}
	}
}

// TestReaderPoolReadsWhileWriteHoldsWriteLock reads through a pooled reader while a write of a Store on the same file
// holds BEGIN IMMEDIATE. writeDB runs the write's function after BEGIN IMMEDIATE and before COMMIT, and the function
// blocks until the test closes release. The read returns while the lock is held, because a WAL reader never waits for
// the write lock.
func TestReaderPoolReadsWhileWriteHoldsWriteLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	r := NewPooledReader(path)
	closeReader(t, r)

	held := make(chan struct{})
	release := make(chan struct{})
	written := make(chan error, 1)
	go func() {
		written <- s.writeDB(ctx, func(sqlcgen.DBTX) error {
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-written:
		t.Fatalf("write returned before it held the write lock: %v", err)
	}

	snap, readErr := r.Snapshot(ctx)
	close(release)
	if err := <-written; err != nil {
		t.Fatalf("write: %v", err)
	}
	if readErr != nil {
		t.Fatalf("Snapshot while the write holds the write lock: %v", readErr)
	}
	assertSnapshot(t, snap, nil, 0)
}

// TestReaderPoolNewReaderKeepsReplacementReads runs the two tests that replace the store under a reader as subtests.
// Both build their readers with NewReader, so the reader that holds no connection still reopens a replaced store.
func TestReaderPoolNewReaderKeepsReplacementReads(t *testing.T) {
	t.Parallel()
	t.Run("TestSnapshotReopensReplacedStore", TestSnapshotReopensReplacedStore)
	t.Run("TestSnapshotReplacedWhileReaderHasReadAndWriterOpen", TestSnapshotReplacedWhileReaderHasReadAndWriterOpen)
}

// restoreOrderStatements are the statements the restore order is made of. A file states the order when one of its
// sentences states each of them, in any case.
var restoreOrderStatements = []string{
	"serve's reader holds connections to the store",
	"serve is stopped before the store file is replaced",
	"orreryd closes last",
	"both are started after the store file is replaced",
}

// TestReaderPoolRestoreOrderIsStated fails when reader.go or docs/architecture.md lacks a sentence that states the
// restore order, and logs the sentences that state it.
func TestReaderPoolRestoreOrderIsStated(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"reader.go", filepath.Join("..", "..", "docs", "architecture.md")} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		covered := map[string]bool{}
		for _, sentence := range sentencesOf(string(raw)) {
			lower := strings.ToLower(sentence)
			matched := false
			for _, statement := range restoreOrderStatements {
				if strings.Contains(lower, statement) {
					covered[statement] = true
					matched = true
				}
			}
			if matched {
				t.Logf("%s: %s", file, sentence)
			}
		}
		for _, statement := range restoreOrderStatements {
			if !covered[statement] {
				t.Errorf("%s states no sentence with %q", file, statement)
			}
		}
	}
}

// sentencesOf returns the sentences of text. Each line's comment marker is removed and the whitespace is collapsed, so
// a sentence that a Go comment wraps across lines reads as one.
func sentencesOf(text string) []string {
	var prose []string
	for line := range strings.SplitSeq(text, "\n") {
		prose = append(prose, strings.TrimPrefix(strings.TrimSpace(line), "//"))
	}
	flat := strings.Join(strings.Fields(strings.Join(prose, " ")), " ")
	return strings.Split(flat, ". ")
}
