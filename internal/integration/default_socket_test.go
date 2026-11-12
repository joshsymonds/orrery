package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// defaultRowLimit bounds how long a pane on the default socket may take to gain its row after orreryd listens.
const defaultRowLimit = 5 * time.Second

// defaultSocketEnv returns the environment for tmux and orreryd run without -L or -S. It is the test's environment
// without TMUX, TMUX_PANE and TMUX_TMPDIR, then TMUX_TMPDIR set to tmuxTmp and extra. Without the removal, the
// process would reach a developer's live tmux server through its own TMUX or TMUX_TMPDIR.
func defaultSocketEnv(tmuxTmp string, extra ...string) []string {
	var env []string
	for _, entry := range scrubbedEnv() {
		if strings.HasPrefix(entry, "TMUX=") || strings.HasPrefix(entry, "TMUX_PANE=") ||
			strings.HasPrefix(entry, "TMUX_TMPDIR=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "TMUX_TMPDIR="+tmuxTmp)
	return append(env, extra...)
}

// defaultTmux runs tmux with env and args and returns its stdout. It fails the test on error.
func defaultTmux(t *testing.T, env []string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "tmux", args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, stderr.String())
	}
	return stdout.String()
}

// killDefaultServer kills the default-socket server that env's TMUX_TMPDIR names. It runs without -L or -S under env,
// so it reaches only that server.
func killDefaultServer(t *testing.T, env []string) {
	t.Helper()
	cmd := exec.Command("tmux", "kill-server")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("kill-server: %v: %s", err, out)
	}
}

// defaultFixture is a tmux server on the default socket under a symlinked TMUX_TMPDIR, with one pane. Its daemon
// runs with no -L or -S and keeps its store under stateHome.
type defaultFixture struct {
	client       *tmuxctl.Client
	socket       string // tmux's #{socket_path}, the -S value a frame's $TMUX names
	pane         string
	tmuxTmp      string
	dir          string // holds the daemon socket, the fake claude, the project and the transcript
	daemonSocket string
	project      string
	transcript   string
	stateHome    string
	dbPath       string
}

// newDefaultFixture starts a tmux server on the default socket with one pane running sleep. The server is killed at
// cleanup, before the test's directories are removed.
func newDefaultFixture(t *testing.T) *defaultFixture {
	t.Helper()
	realTmp := t.TempDir()
	tmuxTmp := filepath.Join(t.TempDir(), "tmpdir")
	if err := os.Symlink(realTmp, tmuxTmp); err != nil {
		t.Fatalf("symlink TMUX_TMPDIR: %v", err)
	}
	dir := t.TempDir()
	stateHome := t.TempDir()
	f := &defaultFixture{
		tmuxTmp:      tmuxTmp,
		dir:          dir,
		daemonSocket: filepath.Join(dir, "orreryd.sock"),
		project:      filepath.Join(dir, exampleProjectName),
		transcript:   filepath.Join(dir, "transcript.jsonl"),
		stateHome:    stateHome,
		dbPath:       filepath.Join(stateHome, "orrery", "orrery.db"),
	}
	env := defaultSocketEnv(tmuxTmp)
	defaultTmux(t, env, "-f", noConfig, "new-session", "-d", "-s", "s1", "-c", realTmp, "sleep 300")
	t.Cleanup(func() { killDefaultServer(t, env) })
	f.socket = strings.TrimSpace(defaultTmux(t, env, "display-message", "-p", "#{socket_path}"))
	f.pane = strings.TrimSpace(defaultTmux(t, env, "list-panes", "-t", "s1", "-F", "#{pane_id}"))
	client, err := tmuxctl.New(tmuxctl.Server{Socket: f.socket})
	if err != nil {
		t.Fatalf("New(Socket=%q): %v", f.socket, err)
	}
	f.client = client

	if err = os.Mkdir(f.project, 0o700); err != nil {
		t.Fatalf("make project: %v", err)
	}
	if err = os.WriteFile(f.transcript, []byte(exampleTranscript), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return f
}

// startDaemon starts orreryd with no -L or -S, on the default socket, and waits for its socket to listen.
func (f *defaultFixture) startDaemon(t *testing.T) {
	t.Helper()
	claude := filepath.Join(f.dir, "fake-claude.sh")
	writeFile(t, claude, fakeClaudeScript, 0o700)
	env := defaultSocketEnv(f.tmuxTmp, "XDG_STATE_HOME="+f.stateHome)
	daemon := startBinary(t, "orreryd", env,
		"--socket", f.daemonSocket, "--claude", claude, "--summary-model", exampleContextName+"="+exampleModel)
	daemon.waitForListener(t, "unix", f.daemonSocket)
}

// hook runs orrery hook for a UserPromptSubmit in the fixture's pane, with $TMUX naming the default socket as a
// Claude Code hook in that pane would.
func (f *defaultFixture) hook(t *testing.T) {
	t.Helper()
	pid := strings.TrimSpace(defaultTmux(t, defaultSocketEnv(f.tmuxTmp), "display-message", "-p", "#{pid}"))
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      exampleSessionID,
		"transcript_path": f.transcript,
	})
	if err != nil {
		t.Fatalf("encode hook: %v", err)
	}
	env := defaultSocketEnv(f.tmuxTmp,
		"TMUX_PANE="+f.pane, "TMUX="+f.socket+","+pid+",0", "ANTHROPIC_BASE_URL="+examplePatchbayURL)
	wantExit(t, runCommand(t, f.project, "orrery", string(payload), env, "--socket", f.daemonSocket, "hook"), 0)
}

// waitForStartupRow polls until the fixture's pane has an @orrery_id and the store row that ID names is on the
// default socket, failing the test after defaultRowLimit. It returns the store, the ID and the row. The store is
// opened once the ID exists: the daemon opens it before the tidy that mints the ID.
func (f *defaultFixture) waitForStartupRow(t *testing.T) (*sql.DB, string, sqlcgen.Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), defaultRowLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	var db *sql.DB
	var last string
	for {
		if id := readOption(t, f.client, f.pane, protocol.OptionID); id == "" {
			last = "no " + protocol.OptionID
		} else {
			if db == nil {
				db = openStoreForInspection(t, f.dbPath)
			}
			row, err := sqlcgen.New(db).GetSession(ctx, id)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				last = "no row for " + id
			case err != nil:
				t.Fatalf("read row %s: %v", id, err)
			case row.Server == "-S "+f.socket:
				return db, id, row
			default:
				last = fmt.Sprintf("row server %q", row.Server)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane %s had no row on -S %s within %v: %s", f.pane, f.socket, defaultRowLimit, last)
		case <-ticker.C:
		}
	}
}

// waitForState polls the row id until its state is want, failing the test after waitLimit.
func waitForState(t *testing.T, db *sql.DB, id string, want protocol.State) sqlcgen.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		row, err := sqlcgen.New(db).GetSession(ctx, id)
		if err != nil {
			t.Fatalf("read row %s: %v", id, err)
		}
		if row.State == string(want) {
			return row
		}
		select {
		case <-ctx.Done():
			t.Fatalf("row %s state = %q, want %q", id, row.State, want)
		case <-ticker.C:
		}
	}
}

func TestDefaultSocketStartupRow(t *testing.T) {
	f := newDefaultFixture(t)
	f.startDaemon(t)

	_, _, row := f.waitForStartupRow(t)
	if row.Pane != f.pane {
		t.Fatalf("row pane = %q, want %q", row.Pane, f.pane)
	}
}

func TestDefaultSocketOneRow(t *testing.T) {
	f := newDefaultFixture(t)
	f.startDaemon(t)
	db, id, _ := f.waitForStartupRow(t)

	f.hook(t)
	waitForState(t, db, id, protocol.StateWorking)
	// The summary run that working starts writes into f.dir; it must finish before the test's directories are removed.
	waitForFinishedRuns(t, f.dir, 1)

	sessions, err := sqlcgen.New(db).ListSessions(t.Context())
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	var rows []sqlcgen.Session
	for _, s := range sessions {
		if s.Pane == f.pane {
			rows = append(rows, s)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("store has %d rows for pane %s, want exactly one: %+v", len(rows), f.pane, rows)
	}
	if rows[0].ID != id || rows[0].Server != "-S "+f.socket {
		t.Fatalf("row %s on %q, want %s on %q", rows[0].ID, rows[0].Server, id, "-S "+f.socket)
	}
}
