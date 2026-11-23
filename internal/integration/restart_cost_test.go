package integration_test

import (
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

const (
	// restartRowCount is how many engine rows the store holds when orreryd restarts.
	restartRowCount = 10_000
	// restartTurnEvery makes every restartTurnEvery-th row a turn that was running, so 100 rows are working or waiting.
	restartTurnEvery = 100
	// restartWorkingEvery makes every restartWorkingEvery-th row working, and the other turn rows waiting.
	restartWorkingEvery = 200
	// restartTextEvery makes every restartTextEvery-th row hold unfinished text, so about 300 rows do.
	restartTextEvery = 33
)

// restartIDFormat is the printf format of the i-th seeded row's id, in Go and in SQLite alike.
const restartIDFormat = "restart-%05d"

// restartRowID is the id of the i-th row seedRestartRows adds.
func restartRowID(i int) string {
	return fmt.Sprintf(restartIDFormat, i)
}

// seedRestartRows adds count engine rows to the store at path, the i-th with id restartRowID(i). Every
// restartTurnEvery-th row is a turn that was running: working every restartWorkingEvery-th row, waiting with a question
// the rest. The other rows cycle through done, stopped, failed and interrupted by i modulo 4, the failed and
// interrupted ones with their reason. Every restartTextEvery-th row, from row 1, holds one unfinished text block.
// SQLite makes the rows itself, in two statements of one transaction: a statement per row, as ApplyEngine runs, costs
// seconds for this many rows, more than the restart under test.
func seedRestartRows(t *testing.T, path string, count int) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close store connection: %v", closeErr)
		}
	}()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin seed: %v", err)
	}
	if _, err = tx.Exec(`
		WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i + 1 < ?1)
		INSERT INTO sessions (id, server, pane, kind, dir, state, changed, question, reason)
		SELECT printf(?2, i), '', '', ?3, '/example/project',
			CASE
				WHEN i % ?4 = 0 THEN 'working'
				WHEN i % ?5 = 0 THEN 'waiting'
				ELSE CASE i % 4 WHEN 0 THEN 'done' WHEN 1 THEN 'stopped' WHEN 2 THEN 'failed' ELSE 'interrupted' END
			END,
			'1',
			CASE WHEN i % ?5 = 0 AND i % ?4 != 0 THEN 'Which color?' ELSE '' END,
			CASE
				WHEN i % ?5 = 0 THEN ''
				WHEN i % 4 = 2 THEN 'spawn failed'
				WHEN i % 4 = 3 THEN 'orreryd stopped during the turn'
				ELSE ''
			END
		FROM n`, count, restartIDFormat, store.KindEngine, restartWorkingEvery, restartTurnEvery); err != nil {
		t.Fatalf("seed sessions: %v", err)
	}
	if _, err = tx.Exec(`
		WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i + 1 < ?1)
		INSERT INTO engine_text (session_id, message_id, block_index, parent_tool_use_id, kind, text)
		SELECT printf(?2, i), 'm1', 0, '', 'text', 'partial' FROM n WHERE i % ?3 = 1`,
		count, restartIDFormat, restartTextEvery); err != nil {
		t.Fatalf("seed unfinished text: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
}

// waitForAccept polls the unix socket at path until a client connects. It fails the test if the daemon exits first. It
// sets no deadline of its own: how long the restart takes under the machine's load is measured on demand by
// TestMeasureRestartCost (restart_cost_measure_test.go), and the test binary's timeout ends a wait that never ends.
func waitForAccept(t *testing.T, daemon *orreryProcess, path string) {
	t.Helper()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		conn, err := net.Dial("unix", path)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Fatalf("close probe connection: %v", closeErr)
			}
			return
		}
		select {
		case <-daemon.done:
			t.Fatalf("%s exited before %s accepted a connection; stderr %q", daemon.name, path, daemon.stderr.String())
		case <-ticker.C:
			stopIfFailed(t)
		}
	}
}

// startRestartDaemon seeds the fixture's store with restartRowCount engine rows and starts an orreryd with --sessions on
// it, returning once the daemon has logged its store-open line.
func startRestartDaemon(t *testing.T, f *engineFixture) *orreryProcess {
	t.Helper()
	seedRestartRows(t, f.dbPath, restartRowCount)
	daemon := startBinary(t, "orreryd", f.env,
		"-L", f.server, "--socket", f.socket, "--claude", f.fakeClaude,
		"--sessions", "--session-shell", f.shell)
	daemon.waitForStoreOpen(t)
	return daemon
}

// requireRestartSettled fails the test unless the first working row the restart settled reads interrupted.
func requireRestartSettled(t *testing.T, f *engineFixture) {
	t.Helper()
	working := restartRowID(0)
	history, err := f.reader.EngineHistory(t.Context(), working, 0, 1)
	if err != nil {
		t.Fatalf("read working row %s: %v", working, err)
	}
	if history.State != "interrupted" {
		t.Fatalf("working row %s state %q after restart, want interrupted", working, history.State)
	}
}

// TestRestartServesSessionsOverALargeStore proves that an orreryd with --sessions restarting on a store of
// restartRowCount engine rows serves sessions.sock, and that a working row the restart settles reads interrupted. How
// soon it serves is the contract's restart bound (DL11), which TestMeasureRestartCost measures on demand.
func TestRestartServesSessionsOverALargeStore(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	daemon := startRestartDaemon(t, f)
	waitForAccept(t, daemon, f.sessions)
	requireRestartSettled(t, f)
	wantExit(t, daemon.stop(t), 0)
}
