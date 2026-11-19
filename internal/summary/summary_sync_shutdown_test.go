package summary_test

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// TestSummarizerSummarySyncShutdown checks that a run whose context is cancelled after it writes its pane's options
// still gets its pane synced: OnWrite's context is not cancelled with the run, so the sync reads tmux and writes the
// pane's row, and logs no "mirror sync dropped".
func TestSummarizerSummarySyncShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store Close: %v", err)
		}
	})
	var logs bytes.Buffer
	now := func() time.Time { return time.Unix(1700000000, 0) }
	mirror := daemon.NewMirror(st, now, slog.New(slog.NewTextHandler(&logs, nil)))

	var server tmuxctl.Server
	r := newRigWith(t, summary.Config{OnWrite: func(hook context.Context, pane string) {
		cancel()
		mirror.SyncPane(hook, server, pane)
	}})
	// tmux names a -L server's socket after the name, so the server's name is the base name of its socket.
	socket := strings.TrimSpace(mustRun(t, r.client, "display-message", "-p", "-t", r.pane, "#{socket_path}"))
	server = tmuxctl.Server{Name: filepath.Base(socket)}

	r.place(t, "reply", "summary: fresh summary\nlabel: fresh label\n")
	if err := r.s.Summarize(ctx, r.job(true)); err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	snap, err := store.NewReader(path).Snapshot(t.Context())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var rows []store.Session
	for _, row := range snap.Sessions {
		if row.Pane == r.pane {
			rows = append(rows, row)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("store rows for pane %s = %d, want 1 synced row", r.pane, len(rows))
	}
	if got, want := rows[0].Server, server.Column(); got != want {
		t.Errorf("synced row server = %q, want %q", got, want)
	}
	if got, want := rows[0].Fields.Summary, "fresh summary"; got != want {
		t.Errorf("synced row summary = %q, want %q", got, want)
	}
	if got, want := rows[0].Fields.Label, "fresh label"; got != want {
		t.Errorf("synced row label = %q, want %q", got, want)
	}
	if strings.Contains(logs.String(), "mirror sync dropped") {
		t.Fatalf("mirror logged a dropped sync after the run was cancelled:\n%s", logs.String())
	}
}
