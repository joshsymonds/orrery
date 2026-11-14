package integration_test

import (
	"database/sql"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
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
	// restartBound is the most time from the store-open line to the first accepted connection on sessions.sock. It is
	// the contract's bound (DL11) and is never tightened.
	restartBound = 5 * time.Second
)

// restartRow is one seeded engine row: its id, state, question and reason, and whether it holds one unfinished text block.
type restartRow struct {
	id, state, question, reason string
	text                        bool
}

// restartRows returns the n rows the restart test seeds. Every restartTurnEvery-th row is a turn that was running:
// working every restartWorkingEvery-th row, waiting the rest. Every restartTextEvery-th row holds unfinished text. The
// other rows cycle through done, stopped, failed and interrupted, with no text.
func restartRows(n int) []restartRow {
	settled := []restartRow{
		{state: "done"},
		{state: "stopped"},
		{state: "failed", reason: "spawn failed"},
		{state: "interrupted", reason: "orreryd stopped during the turn"},
	}
	rows := make([]restartRow, n)
	for i := range rows {
		var row restartRow
		switch {
		case i%restartWorkingEvery == 0:
			row = restartRow{state: "working"}
		case i%restartTurnEvery == 0:
			row = restartRow{state: "waiting", question: "Which color?"}
		default:
			row = settled[i%len(settled)]
		}
		row.id = restartRowID(i)
		row.text = i%restartTextEvery == 1
		rows[i] = row
	}
	return rows
}

// restartRowID is the id of the i-th row restartRows returns.
func restartRowID(i int) string {
	return fmt.Sprintf("restart-%05d", i)
}

// seedRestartRows adds rows to the store at path, each with the state, question and reason it names and its unfinished
// text block when it has one. The rows go in through one transaction of prepared statements: ApplyEngine prepares every
// statement it runs, and seeding this many rows through it costs more than the restart under test.
func seedRestartRows(t *testing.T, path string, rows []restartRow) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
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
	insertSession, err := tx.Prepare(`
		INSERT INTO sessions (id, server, pane, kind, dir, state, changed, question, reason)
		VALUES (?, '', '', ?, '/example/project', ?, '1', ?, ?)`)
	if err != nil {
		t.Fatalf("prepare session insert: %v", err)
	}
	defer func() {
		if closeErr := insertSession.Close(); closeErr != nil {
			t.Errorf("close session insert: %v", closeErr)
		}
	}()
	insertText, err := tx.Prepare(`
		INSERT INTO engine_text (session_id, message_id, block_index, parent_tool_use_id, kind, text)
		VALUES (?, 'm1', 0, '', 'text', 'partial')`)
	if err != nil {
		t.Fatalf("prepare text insert: %v", err)
	}
	defer func() {
		if closeErr := insertText.Close(); closeErr != nil {
			t.Errorf("close text insert: %v", closeErr)
		}
	}()
	for _, row := range rows {
		if _, err = insertSession.Exec(row.id, store.KindEngine, row.state, row.question, row.reason); err != nil {
			t.Fatalf("seed session %s: %v", row.id, err)
		}
		if !row.text {
			continue
		}
		if _, err = insertText.Exec(row.id); err != nil {
			t.Fatalf("seed text of session %s: %v", row.id, err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
}

// storeOpenTime returns the time of the store-open line in stderr. slog's text handler writes it as time=.
func storeOpenTime(t *testing.T, stderr string) time.Time {
	t.Helper()
	for line := range strings.SplitSeq(stderr, "\n") {
		if !strings.Contains(line, storeOpenLog) {
			continue
		}
		_, rest, found := strings.Cut(line, "time=")
		if !found {
			t.Fatalf("store-open line %q has no time", line)
		}
		stamp, _, _ := strings.Cut(rest, " ")
		opened, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			t.Fatalf("store-open time %q: %v", stamp, err)
		}
		return opened
	}
	t.Fatalf("stderr %q holds no %s line", stderr, storeOpenLog)
	return time.Time{}
}

// waitForAccept polls the unix socket at path until a client connects, and returns the time of that connection. It fails
// the test if the daemon exits first, or if no connection is made by deadline.
func waitForAccept(t *testing.T, daemon *orreryProcess, path string, deadline time.Time) time.Time {
	t.Helper()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		conn, err := net.Dial("unix", path)
		at := time.Now()
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Fatalf("close probe connection: %v", closeErr)
			}
			return at
		}
		if at.After(deadline) {
			t.Fatalf("%s accepted no connection by the deadline; last dial error %v; stderr %q",
				path, err, daemon.stderr.String())
		}
		select {
		case <-daemon.done:
			t.Fatalf("%s exited before %s accepted a connection; stderr %q", daemon.name, path, daemon.stderr.String())
		case <-ticker.C:
		}
	}
}

// TestRestartCostServesSessionsWithinBound proves that a restart serves at once however much history the store holds.
// An orreryd with --sessions starts on a store of restartRowCount engine rows, and its sessions.sock accepts a connection
// within restartBound of its store-open line. A working row the restart settles reads interrupted afterwards.
func TestRestartCostServesSessionsWithinBound(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	storetest.Seed(t, f.dbPath)
	seedRestartRows(t, f.dbPath, restartRows(restartRowCount))

	daemon := startBinary(t, "orreryd", f.env,
		"-L", f.server, "--socket", f.socket, "--claude", f.fakeClaude,
		"--sessions", "--session-shell", f.shell)
	daemon.waitForStoreOpen(t)
	opened := storeOpenTime(t, daemon.stderr.String())
	accepted := waitForAccept(t, daemon, f.sessions, opened.Add(restartBound))
	elapsed := accepted.Sub(opened)
	t.Logf("%s accepted a connection %v after the store-open line", f.sessions, elapsed)
	if elapsed > restartBound {
		t.Fatalf("%s accepted a connection %v after the store-open line, want within %v", f.sessions, elapsed,
			restartBound)
	}

	working := restartRowID(0)
	history, err := f.reader.EngineHistory(t.Context(), working, 0, 1)
	if err != nil {
		t.Fatalf("read working row %s: %v", working, err)
	}
	if history.State != "interrupted" {
		t.Fatalf("working row %s state %q after restart, want interrupted", working, history.State)
	}
	wantExit(t, daemon.stop(t), 0)
}
