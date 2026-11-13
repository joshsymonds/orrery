package daemon_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
)

// tidyStartInterval is the TidyInterval of the TidyStart tests: short enough that several tidies would run
// within a test, and long enough that a tidy finishes between two of them.
const tidyStartInterval = 20 * time.Millisecond

// TestTidyStartHoldsTidyUntilClosed checks that no tidy writes a row while TidyStart is open, however many
// intervals pass, and that the first tidy after it closes writes the pane's row.
func TestTidyStartHoldsTidyUntilClosed(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	tidyStart := make(chan struct{})
	serve(t, daemon.Config{
		Server:       srv.server,
		Mirror:       daemon.NewMirror(st, tidyClock, nil),
		TidyInterval: tidyStartInterval,
		TidyStart:    tidyStart,
	})

	for range 5 {
		time.Sleep(tidyStartInterval)
		if rows := listSessions(t, db); len(rows) != 0 {
			t.Fatalf("rows before TidyStart closed = %+v, want none", rows)
		}
	}

	close(tidyStart)
	id := waitForPaneID(t, srv.client, srv.pane)
	if row := mustSession(t, db, id); row.Pane != srv.pane || row.Server != tidyLocalServer(srv) {
		t.Fatalf("row = server %q pane %q, want server %q pane %q",
			row.Server, row.Pane, tidyLocalServer(srv), srv.pane)
	}
}

// TestTidyStartNeverClosedServeReturnsOnCancel checks that Serve returns nil when ctx is canceled while
// TidyStart stays open, so the tidy loop that waits on it cannot keep Serve from returning.
func TestTidyStartNeverClosedServeReturnsOnCancel(t *testing.T) {
	st, _ := openFrameStore(t)
	ln, err := daemon.Listen(filepath.Join(t.TempDir(), "orreryd.sock"))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- daemon.Serve(ctx, ln, daemon.Config{
			Mirror:       daemon.NewMirror(st, tidyClock, nil),
			TidyInterval: tidyStartInterval,
			TidyStart:    make(chan struct{}),
		})
	}()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v, want nil", err)
		}
	case <-time.After(ioTimeout):
		t.Fatalf("Serve did not return after cancel within %v", ioTimeout)
	}
}
