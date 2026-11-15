package daemon_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// tidyStartInterval is the TidyInterval of the cancel test, whose tidy loop never starts.
const tidyStartInterval = 20 * time.Millisecond

// TestTidyStartHoldsTidyUntilClosed checks that no tidy writes a row while TidyStart is open, however many ticks are
// offered, and that the first tidy after it closes writes the pane's row. The tidy loop waits for TidyStart before it
// takes any tick, so while TidyStart is open a tick it is offered is never taken.
func TestTidyStartHoldsTidyUntilClosed(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	tidyStart := make(chan struct{})
	ticks := make(chan time.Time)
	serve(t, daemon.Config{
		Server:    srv.server,
		Mirror:    daemon.NewMirror(st, tidyClock, nil),
		TidyTicks: ticks,
		TidyStart: tidyStart,
	})

	for range 5 {
		select {
		case ticks <- time.Time{}:
			t.Fatal("the tidy loop took a tick before TidyStart closed")
		default:
		}
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
	// A tidy loop that kept Serve from returning would hang here, which the test binary's timeout ends.
	if err := <-done; err != nil {
		t.Fatalf("Serve = %v, want nil", err)
	}
}

// TestAfterTidyRunsAfterEachTidyPass checks that AfterTidy runs once per tidy pass, at start and at each tick, and only
// after the pass: the tidy loop waits in AfterTidy until the test takes its signal, and by then the pass has written
// the pane's row.
func TestAfterTidyRunsAfterEachTidyPass(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	ticks := make(chan time.Time)
	passes := make(chan struct{})
	serve(t, daemon.Config{
		Server:    srv.server,
		Mirror:    daemon.NewMirror(st, tidyClock, nil),
		TidyTicks: ticks,
		AfterTidy: func(context.Context) { passes <- struct{}{} },
	})

	<-passes
	// The loop waits in AfterTidy, so no tidy runs while the test reads: the pass before it must have stamped the pane.
	id, ok, err := srv.client.PaneOption(t.Context(), srv.pane, protocol.OptionID)
	if err != nil || !ok {
		t.Fatalf("pane %s id at the first AfterTidy = %q, %v, %v: AfterTidy ran before the pass", srv.pane, id, ok, err)
	}
	if row := mustSession(t, db, id); row.Pane != srv.pane {
		t.Fatalf("row at the first AfterTidy = pane %q, want %q", row.Pane, srv.pane)
	}
	ticks <- time.Time{}
	<-passes
}
