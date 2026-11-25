package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// hostPanesOf reads the pane rows stored under host through a reader of the store at path.
func hostPanesOf(t *testing.T, path, host string) []Session {
	t.Helper()
	panes, err := NewReader(path).HostPanes(context.Background(), host)
	if err != nil {
		t.Fatalf("HostPanes(%q): %v", host, err)
	}
	return panes
}

func TestStoreSyncHostTidiesOnlyItsHost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	t0 := time.Unix(1_700_000_000, 0)

	// Hosts a and b each run a pane %1 and a pane %2 on the same tmux server, under row ids of their own.
	if err := s.SyncServer(ctx, "a", "-L orrery", []Pane{
		{ID: "a-1", Pane: "%1", Fields: Fields{State: "working"}},
		{ID: "a-2", Pane: "%2"},
	}, t0); err != nil {
		t.Fatalf("SyncServer a: %v", err)
	}
	if err := s.SyncServer(ctx, "b", "-L orrery", []Pane{
		{ID: "b-1", Pane: "%1", Fields: Fields{State: "waiting"}},
		{ID: "b-2", Pane: "%2"},
	}, t0); err != nil {
		t.Fatalf("SyncServer b: %v", err)
	}

	// b's tidy lists only b-1, so it removes b-2 and must leave every row of a, which shares the server.
	if err := s.SyncServer(ctx, "b", "-L orrery", []Pane{
		{ID: "b-1", Pane: "%1", Fields: Fields{State: "waiting"}},
	}, t0.Add(time.Second)); err != nil {
		t.Fatalf("SyncServer b removing b-2: %v", err)
	}

	wantB := []Session{{ID: "b-1", Server: "-L orrery", Pane: "%1", Fields: Fields{State: "waiting"}}}
	if got := hostPanesOf(t, path, "b"); !reflect.DeepEqual(got, wantB) {
		t.Fatalf("HostPanes(b) = %+v, want %+v", got, wantB)
	}
	wantA := []Session{
		{ID: "a-1", Server: "-L orrery", Pane: "%1", Fields: Fields{State: "working"}},
		{ID: "a-2", Server: "-L orrery", Pane: "%2"},
	}
	if got := hostPanesOf(t, path, "a"); !reflect.DeepEqual(got, wantA) {
		t.Fatalf("HostPanes(a) = %+v, want %+v", got, wantA)
	}
}
