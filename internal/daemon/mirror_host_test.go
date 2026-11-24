package daemon_test

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestMirrorHostNamesItsRows(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	ctx := t.Context()
	m := daemon.NewPendingMirror("a", tidyClock, nil)
	m.Attach(st)

	// Host b has a row on the same tmux server, which host a's tidy and syncs must leave alone.
	if err := st.SyncServer(ctx, "b", tidyLocalServer(srv), []store.Pane{{ID: "b-1", Pane: "%900"}},
		tidyClock()); err != nil {
		t.Fatalf("SyncServer b: %v", err)
	}

	// The tidy inserts a row for the first pane, and a sync inserts one for the pane made after it.
	m.Tidy(ctx, srv.server)
	second := newPane(t, srv)
	m.SyncPane(ctx, srv.server, second)

	var aPanes []string
	bRows := 0
	for _, row := range listSessions(t, db) {
		if row.ID == "b-1" {
			bRows++
			if row.Host != "b" || row.Server != tidyLocalServer(srv) || row.Pane != "%900" {
				t.Fatalf("host b's row = host %q server %q pane %q, want host %q server %q pane %q",
					row.Host, row.Server, row.Pane, "b", tidyLocalServer(srv), "%900")
			}
			continue
		}
		if row.Host != "a" || row.Server != tidyLocalServer(srv) {
			t.Fatalf("mirror row %s = host %q server %q, want host %q server %q",
				row.ID, row.Host, row.Server, "a", tidyLocalServer(srv))
		}
		aPanes = append(aPanes, row.Pane)
	}
	if bRows != 1 {
		t.Fatalf("host b's rows = %d, want 1: the tidy and the sync must leave it", bRows)
	}
	wantPanes := []string{srv.pane, second}
	slices.Sort(wantPanes)
	slices.Sort(aPanes)
	if !slices.Equal(aPanes, wantPanes) {
		t.Fatalf("host a's panes = %v, want %v", aPanes, wantPanes)
	}
}
