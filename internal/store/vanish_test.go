package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// vanishRead is one of the Reader's reads that a lost store must answer with ErrUnavailable.
type vanishRead struct {
	name string
	read func(context.Context, *Reader) error
}

// vanishReads are the reads TestReadsOfARemovedStoreAnswerUnavailable removes the store under.
func vanishReads() []vanishRead {
	return []vanishRead{
		{"Snapshot", func(ctx context.Context, r *Reader) error {
			_, err := r.Snapshot(ctx)
			return err
		}},
		{"Events", func(ctx context.Context, r *Reader) error {
			_, err := r.Events(ctx, 0, 10)
			return err
		}},
	}
}

// TestReadsOfARemovedStoreAnswerUnavailable removes the store file before a read and at each point of it: once the
// handle exists, once the read transaction has begun, and once its first query has returned. Each read that fails
// must match ErrUnavailable, because a lost store is reported the same way whether the file is gone before the read
// or disappears during it. Removed before the read, the read must fail. The writer keeps the file open throughout, as
// orreryd does while a reader races its removal.
func TestReadsOfARemovedStoreAnswerUnavailable(t *testing.T) {
	t.Parallel()
	for _, read := range vanishReads() {
		for _, point := range []readPoint{"", readOpened, readBegun, readQueried} {
			name := read.name + "/" + string(point)
			if point == "" {
				name = read.name + "/before"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "orrery.db")
				seedMigrated(t, path)
				s, err := Open(ctx, path, noSync())
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				t.Cleanup(func() {
					if closeErr := s.Close(); closeErr != nil {
						t.Errorf("Close writer: %v", closeErr)
					}
				})
				if err = s.SyncPane(
					ctx,
					"-L a",
					Pane{ID: "orrery-1", Pane: "%1"},
					time.Unix(1_700_000_000, 0),
				); err != nil {
					t.Fatalf("SyncPane: %v", err)
				}

				remove := func() {
					if removeErr := os.Remove(path); removeErr != nil {
						t.Errorf("remove store: %v", removeErr)
					}
				}
				r := NewReader(path)
				if point == "" {
					remove()
				} else {
					r.at = func(reached readPoint) {
						if reached == point {
							remove()
						}
					}
				}
				err = read.read(ctx, r)
				if err != nil && !errors.Is(err, ErrUnavailable) {
					t.Fatalf("%s with the store removed: %v, want ErrUnavailable", name, err)
				}
				if point == "" && !errors.Is(err, ErrUnavailable) {
					t.Fatalf("%s of a store removed before it = %v, want ErrUnavailable", name, err)
				}
			})
		}
	}
}
