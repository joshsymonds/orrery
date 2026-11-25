package main

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// Synthetic names only. hostMigrationName is the --host seam's name. hostMigrationFileName is the name a test's hosts
// file gives the controller in its place.
const (
	hostMigrationName     = "synthetic-host"
	hostMigrationFileName = "desktop"
)

// hostMigrationEnv returns the getenv of a host migration test, which names one state directory of the test's own,
// short enough for the holder and session sockets, and the config and tmux directories under it. A non-empty hostsFile
// is written as the hosts file. The directory is removed when the test ends, and is returned too.
func hostMigrationEnv(t *testing.T, hostsFile string) (func(string) string, string) {
	t.Helper()
	root, err := shortStateRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
	config := filepath.Join(root, "config")
	if hostsFile != "" {
		dir := filepath.Join(config, "orrery")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make hosts directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "hosts.toml"), []byte(hostsFile), 0o600); err != nil {
			t.Fatalf("write hosts file: %v", err)
		}
	}
	env := map[string]string{
		"XDG_STATE_HOME":  root,
		"XDG_CONFIG_HOME": config,
		"TMUX_TMPDIR":     root,
	}
	return func(key string) string { return env[key] }, root
}

// seedHostMigrationRows writes the store at path as one from before the host column holds it: a pane row and an engine
// row in a directory of the test's own, both inserted without a host, so each reads the column's default: the empty
// host.
func seedHostMigrationRows(t *testing.T, path string) {
	t.Helper()
	dir := t.TempDir()
	storetest.Seed(t, path)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open store to seed rows: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close seeded store: %v", err)
		}
	}()
	seeds := []struct {
		stmt string
		args []any
	}{
		{`INSERT INTO sessions (id, server, pane) VALUES ('pane-row', '', '%1')`, nil},
		{`INSERT INTO sessions (id, server, pane, kind, dir, state, changed)
			VALUES ('engine-row', '', '', 'engine', ?, 'waiting', '2026-11-22T09:00:00Z')`, []any{dir}},
	}
	for _, seed := range seeds {
		if _, err := db.Exec(seed.stmt, seed.args...); err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}
}

// sessionHostsAt returns the host of every sessions row of the store at path, by row id. The store must be closed.
func sessionHostsAt(t *testing.T, path string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open store to read hosts: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store read: %v", err)
		}
	}()
	rows, err := db.Query(`SELECT id, host FROM sessions`)
	if err != nil {
		t.Fatalf("read session hosts: %v", err)
	}
	defer rows.Close()
	hosts := map[string]string{}
	for rows.Next() {
		var id, host string
		if err := rows.Scan(&id, &host); err != nil {
			t.Fatalf("scan session host: %v", err)
		}
		hosts[id] = host
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate session hosts: %v", err)
	}
	return hosts
}

// startHostMigrationServe runs serve in-process over opts until the test ends. orreryd sends READY=1 to a notify socket
// of the test's own once it has opened and named its store and started its sessions, and the test waits for that
// notification, and fails if serve ends first. The returned stop cancels serve and returns its error; the first call
// waits for serve to return, and later calls return the same error.
func startHostMigrationServe(t *testing.T, getenv func(string) string, opts options) (stop func() error) {
	t.Helper()
	notify := filepath.Join(filepath.Dir(opts.socket), "notify.sock")
	ready, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: notify, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen notify socket: %v", err)
	}
	t.Cleanup(func() {
		if err := ready.Close(); err != nil {
			t.Errorf("close notify socket: %v", err)
		}
	})
	opts.notifySocket = notify

	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, opts, slog.New(slog.DiscardHandler), getenv) }()
	readied := make(chan error, 1)
	go func() {
		_, _, err := ready.ReadFromUnix(make([]byte, 64))
		readied <- err
	}()
	select {
	case err := <-readied:
		if err != nil {
			cancel()
			<-served
			t.Fatalf("read READY=1: %v", err)
		}
	case err := <-served:
		t.Fatalf("serve ended before READY=1: %v", err)
	}

	stop = sync.OnceValue(func() error {
		cancel()
		return <-served
	})
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return stop
}

// hostMigrationSessionArgs returns the flags of a --sessions orreryd on socket, whose sessions run the fake claude, and
// which names hostMigrationName with --host.
func hostMigrationSessionArgs(t *testing.T, socket string) []string {
	t.Helper()
	return []string{
		"--sessions",
		"--session-shell", eventWakeShell(t),
		"--claude", eventWakeClaude(t),
		"--socket", socket,
		"--host", hostMigrationName,
	}
}

// startHostMigrationSession starts a headless session through the Engine service on the sessions socket beside
// daemonSocket, with no host named, and returns its id.
func startHostMigrationSession(t *testing.T, daemonSocket string) string {
	t.Helper()
	client := enginev1connect.NewEngineClient(cronWiringHTTP(t, protocol.SessionSocketPath(daemonSocket)),
		"http://engine.invalid")
	resp, err := client.Start(t.Context(), connect.NewRequest(&v1.StartRequest{Dir: t.TempDir(), Prompt: "hello"}))
	if err != nil {
		t.Fatalf("Start a headless session: %v", err)
	}
	if resp.Msg.GetId() == "" {
		t.Fatalf("Start returned no session id")
	}
	return resp.Msg.GetId()
}

// TestHostMigrationServeNamesPreMigrationRows runs orreryd in-process with --host over a store holding rows from before
// the host column, and reads every session's host once it has shut down: each reads the name the seam gives.
func TestHostMigrationServeNamesPreMigrationRows(t *testing.T) {
	getenv, root := hostMigrationEnv(t, "")
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	seedHostMigrationRows(t, path)
	opts, err := parseFlags([]string{"--host", hostMigrationName, "--socket", filepath.Join(root, "orreryd.sock")},
		getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	stop := startHostMigrationServe(t, getenv, opts)
	if err := stop(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	want := map[string]string{"pane-row": hostMigrationName, "engine-row": hostMigrationName}
	if got := sessionHostsAt(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts of the pre-migration rows = %v, want %v", got, want)
	}
}

// TestHostMigrationHeadlessStartCarriesHostName runs a --sessions orreryd with no hosts file, starts a headless session
// with no host named, and reads the host of every row: the pre-migration rows and the new one all read the name --host
// gives.
func TestHostMigrationHeadlessStartCarriesHostName(t *testing.T) {
	getenv, root := hostMigrationEnv(t, "")
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	seedHostMigrationRows(t, path)
	socket := filepath.Join(root, "orreryd.sock")
	opts, err := parseFlags(hostMigrationSessionArgs(t, socket), getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	stop := startHostMigrationServe(t, getenv, opts)
	id := startHostMigrationSession(t, socket)
	if err := stop(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	want := map[string]string{"pane-row": hostMigrationName, "engine-row": hostMigrationName, id: hostMigrationName}
	if got := sessionHostsAt(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts of the rows = %v, want %v", got, want)
	}
}

// TestHostMigrationHostsFileNameIsTheOrreryName runs a --sessions orreryd whose hosts file sets the top-level name, and
// reads the host of every row: each reads that name, not the --host name.
func TestHostMigrationHostsFileNameIsTheOrreryName(t *testing.T) {
	getenv, root := hostMigrationEnv(t, "name = \""+hostMigrationFileName+"\"\n")
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	seedHostMigrationRows(t, path)
	socket := filepath.Join(root, "orreryd.sock")
	opts, err := parseFlags(hostMigrationSessionArgs(t, socket), getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	stop := startHostMigrationServe(t, getenv, opts)
	id := startHostMigrationSession(t, socket)
	if err := stop(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	want := map[string]string{
		"pane-row": hostMigrationFileName, "engine-row": hostMigrationFileName, id: hostMigrationFileName,
	}
	if got := sessionHostsAt(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts of the rows = %v, want %v", got, want)
	}
}

// TestHostMigrationHostsFileErrorFailsStartup runs orreryd with a hosts file that breaks a rule of hosts.Load. The run
// exits with a failure that reports the file, and before the store is named: the pre-migration rows still have the
// empty host.
func TestHostMigrationHostsFileErrorFailsStartup(t *testing.T) {
	getenv, root := hostMigrationEnv(t, "name = \"Desktop\"\n")
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	seedHostMigrationRows(t, path)
	socket := filepath.Join(root, "orreryd.sock")

	var stderr bytes.Buffer
	code := run([]string{"--host", hostMigrationName, "--socket", socket}, &stderr, getenv)
	if code != exitFailure {
		t.Fatalf("run exit code = %d, want %d; stderr %q", code, exitFailure, stderr.String())
	}
	if !strings.Contains(stderr.String(), "orreryd: load hosts: hosts file") {
		t.Fatalf("stderr %q does not report the hosts file", stderr.String())
	}

	want := map[string]string{"pane-row": "", "engine-row": ""}
	if got := sessionHostsAt(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts after the refused hosts file = %v, want %v", got, want)
	}
}
