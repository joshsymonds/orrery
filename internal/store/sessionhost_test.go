package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSessionHostReadsStoredHost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	now := time.Unix(1_700_000_000, 0)

	if err := s.SyncPane(ctx, "-L orrery", Pane{Host: "b", ID: "b-1", Pane: "%1"}, now); err != nil {
		t.Fatalf("SyncPane b: %v", err)
	}
	if err := s.SyncPane(ctx, "-L orrery", Pane{Host: "ctl", ID: "ctl-1", Pane: "%1"}, now); err != nil {
		t.Fatalf("SyncPane ctl: %v", err)
	}

	for id, want := range map[string]string{"b-1": "b", "ctl-1": "ctl"} {
		got, ok, err := s.SessionHost(ctx, id)
		if err != nil {
			t.Fatalf("SessionHost(%q): %v", id, err)
		}
		if !ok || got != want {
			t.Fatalf("SessionHost(%q) = %q, %t, want %q, true", id, got, ok, want)
		}
	}
}

func TestStoreSessionHostUnknownSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)

	got, ok, err := s.SessionHost(ctx, "missing")
	if err != nil {
		t.Fatalf("SessionHost(missing): %v", err)
	}
	if ok || got != "" {
		t.Fatalf("SessionHost(missing) = %q, %t, want \"\", false", got, ok)
	}
}
