package server_test

import (
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// prunedLog seeds events seq 1 through 4 at eventAt, then syncs the unchanged pane a retention period and an hour
// later. That sync appends nothing and prunes every event before the cutoff, which would empty the log if pruning
// kept no event. It returns the store and the time of the prune.
func prunedLog(t *testing.T, path string) (*store.Store, time.Time) {
	t.Helper()
	s := openEventStore(t, path)
	seedEvents(t, s, 3)
	later := time.Unix(eventAt, 0).Add(store.EventRetention + time.Hour)
	pane := store.Pane{ID: eventPane, Pane: "%1", Fields: store.Fields{State: "state-3"}}
	if err := s.SyncServer(t.Context(), "-L a", []store.Pane{pane}, later); err != nil {
		t.Fatalf("SyncServer after retention: %v", err)
	}
	return s, later
}

func TestListThenWatchAfterPrunedLogReceivesNextWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s, later := prunedLog(t, path)
	url := startServer(t, path)
	sessions := orreryv1connect.NewSessionsClient(guardedHTTPClient(), url)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), url)

	list, err := sessions.List(t.Context(), connect.NewRequest(&orreryv1.ListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	w := openWatch(t, events, list.Msg.GetSeq())
	pane := store.Pane{ID: eventPane, Pane: "%1", Fields: store.Fields{State: "after-prune"}}
	if err := s.SyncPane(t.Context(), "-L a", pane, later); err != nil {
		t.Fatalf("SyncPane after prune: %v", err)
	}
	if got := w.next(t).GetSeq(); got != 5 {
		t.Fatalf("first message after List on pruned log: seq %d, want 5", got)
	}
}

func TestWatchAfterPrunedLogLastSeqReceivesNextWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s, later := prunedLog(t, path)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path))

	w := openWatch(t, events, 4)
	pane := store.Pane{ID: eventPane, Pane: "%1", Fields: store.Fields{State: "after-prune"}}
	if err := s.SyncPane(t.Context(), "-L a", pane, later); err != nil {
		t.Fatalf("SyncPane after prune: %v", err)
	}
	if got := w.next(t).GetSeq(); got != 5 {
		t.Fatalf("first message after cursor 4 on pruned log: seq %d, want 5", got)
	}
}
