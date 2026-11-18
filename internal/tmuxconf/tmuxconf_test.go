package tmuxconf_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/keymap"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/shell"
	"github.com/joshsymonds/orrery/internal/tmuxconf"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

const (
	binDirEnv    = "ORRERY_TMUXCONF_BIN_DIR"
	goldenPath   = "testdata/plugin.golden"
	goldenOrrery = "/synthetic/bin/orrery"
	noConfig     = "/dev/null"
	testCommand  = "sleep 300"
	synthetic    = "synthetic"
	changedAt    = "100"
	seenAfter    = "200"
	pollEvery    = 10 * time.Millisecond
	quietWindow  = 1500 * time.Millisecond
)

// TestMain first stops the tmux servers that earlier test binaries started and outlived (see tmuxtest.Reaper), then
// runs the tests.
func TestMain(m *testing.M) {
	if _, err := tmuxtest.DefaultReaper().Reap(); err != nil {
		fmt.Fprintf(os.Stderr, "reap test tmux servers: %v\n", err)
	}
	os.Exit(runWithOrrery(m))
}

// runWithOrrery builds cmd/orrery into a fresh directory, points the tests at it, and runs them.
func runWithOrrery(m *testing.M) int {
	dir, err := os.MkdirTemp("", "orrery-tmuxconf-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "make binary directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "orrery"), "../../cmd/orrery").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build orrery: %v\n%s", err, out)
		return 1
	}
	if err = os.Setenv(binDirEnv, dir); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", binDirEnv, err)
		return 1
	}
	return m.Run()
}

// builtOrrery returns the path of the orrery binary built by TestMain.
func builtOrrery() string {
	return filepath.Join(os.Getenv(binDirEnv), "orrery")
}

func TestPrintGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := tmuxconf.Print(&buf, goldenOrrery); err != nil {
		t.Fatalf("Print: %v", err)
	}
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if buf.String() != string(golden) {
		t.Fatalf("Print output mismatch\n got: %s\nwant: %s", buf.String(), golden)
	}
}

// TestPrefixBindingsFollowKeymap checks that the bind-key lines Print writes bind the keys of the keymap's tmux
// bindings, in keymap order, each to the command for its action.
func TestPrefixBindingsFollowKeymap(t *testing.T) {
	treeCommand := `run-shell -b "tmux display-popup -c #{q:client_tty} -E \"` + goldenOrrery +
		` tree --client #{q:client_tty}\""`
	shellCommand := `run-shell -b "` + goldenOrrery + ` shell --dir #{q:pane_current_path} --client #{q:client_tty}"`
	var want []string
	for _, b := range keymap.For(keymap.Tmux) {
		var command string
		switch b.Action {
		case keymap.ActTree:
			command = treeCommand
		case keymap.ActShell:
			command = shellCommand
		default:
			t.Fatalf("no expected command for tmux action %q", b.Action)
		}
		for _, key := range b.Keys {
			want = append(want, "bind-key "+key+" "+command)
		}
	}

	var buf bytes.Buffer
	if err := tmuxconf.Print(&buf, goldenOrrery); err != nil {
		t.Fatalf("Print: %v", err)
	}
	var got []string
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.HasPrefix(line, "bind-key ") {
			got = append(got, line)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("bind-key lines = %q, want %q", got, want)
	}
}

func TestPrintRefusesUnsafeOrreryPath(t *testing.T) {
	for _, orrery := range []string{
		"",
		"/synthetic/bin/or rery",
		`/synthetic/bin/or"rery`,
		"/synthetic/bin/or'rery",
		"/synthetic/bin/or#rery",
		"/synthetic/bin/or;rery",
	} {
		t.Run(fmt.Sprintf("%q", orrery), func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmuxconf.Print(&buf, orrery); err == nil {
				t.Fatalf("Print(%q) returned nil error", orrery)
			}
			if buf.Len() != 0 {
				t.Fatalf("Print(%q) wrote %d bytes on error", orrery, buf.Len())
			}
		})
	}
}

// testServer is a private tmux server started under a unique name and killed at cleanup.
type testServer struct {
	name    string
	client  *tmuxctl.Client
	tempDir string
}

// startServer starts a private tmux server with -f /dev/null and one session named work, whose pane runs
// testCommand. Its global environment names a daemon socket under the test's temp dir, so no run-shell job of the
// server, a focus hook's included, can reach the owner's orreryd. The test's temp dirs are made before the server's
// stop is registered, so the cleanups run in the other order: the server and every job it ran, a focus hook's fake
// orrery included, are gone before the temp dirs are removed.
func startServer(t *testing.T) testServer {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-tmuxconf-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(%s): %v", name, err)
	}
	s := testServer{name: name, client: client, tempDir: t.TempDir()}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, tmuxctl.Server{Name: name}); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	s.must(t, "-f", noConfig, "new-session", "-d", "-s", "work", "-c", s.tempDir, testCommand)
	s.must(t, "set-environment", "-g", protocol.EnvSocket, filepath.Join(s.tempDir, "orreryd.sock"))
	return s
}

// must runs tmux with args and fails the test on error.
func (s testServer) must(t *testing.T, args ...string) string {
	t.Helper()
	out, err := s.client.Run(t.Context(), args...)
	if err != nil {
		t.Fatalf("tmux %v: %v", args, err)
	}
	return out
}

// panes returns the pane IDs of the current window of session target, in order.
func (s testServer) panes(t *testing.T, target string) []string {
	t.Helper()
	return strings.Fields(s.must(t, "list-panes", "-t", target, "-F", "#{pane_id}"))
}

// sourcePlugin writes the plugin for the orrery binary at orrery to a file and sources it on the server.
func (s testServer) sourcePlugin(t *testing.T, orrery string) {
	t.Helper()
	var buf bytes.Buffer
	if err := tmuxconf.Print(&buf, orrery); err != nil {
		t.Fatalf("Print: %v", err)
	}
	path := filepath.Join(t.TempDir(), "orrery.conf")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	s.must(t, "source-file", path)
}

// setPane sets the state and @orrery_changed of pane to changedAt, and @orrery_seen to seen unless seen is empty.
func (s testServer) setPane(t *testing.T, pane, state, seen string) {
	t.Helper()
	ctx := t.Context()
	if err := s.client.SetPaneOption(ctx, pane, protocol.OptionState, state); err != nil {
		t.Fatalf("set state on %s: %v", pane, err)
	}
	if err := s.client.SetPaneOption(ctx, pane, protocol.OptionChanged, changedAt); err != nil {
		t.Fatalf("set changed on %s: %v", pane, err)
	}
	if seen != "" {
		if err := s.client.SetPaneOption(ctx, pane, protocol.OptionSeen, seen); err != nil {
			t.Fatalf("set seen on %s: %v", pane, err)
		}
	}
}

// waitFor polls check every pollEvery until it reports true, failing the test on its error. It has no deadline of its
// own.
func waitFor(t *testing.T, what string, check func() (bool, error)) {
	t.Helper()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		ok, err := check()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if ok {
			return
		}
		<-ticker.C
	}
}

// quiet gives background jobs that a hook may have started time to finish before a negative assertion.
func quiet() {
	timer := time.NewTimer(quietWindow)
	defer timer.Stop()
	<-timer.C
}

func TestPluginSourcesCleanly(t *testing.T) {
	s := startServer(t)
	s.sourcePlugin(t, builtOrrery())
	if got := s.must(t, "show-options", "-g", "-v", "pane-border-status"); strings.TrimSpace(got) != "top" {
		t.Fatalf("pane-border-status = %q, want top", got)
	}
	for _, hook := range []string{"window-pane-changed", "session-window-changed", "client-session-changed"} {
		if hooks := s.must(t, "show-hooks", "-g", hook); !strings.Contains(hooks, "seen --pane #{pane_id} --focus") {
			t.Fatalf("%s hook missing the focus seen command: %s", hook, hooks)
		}
	}
}

// TestPluginSourcedAgainReplacesItsHooksAndStatus sources the plugin for one binary and then for another, as a server
// that sources it again after an upgrade does: each hook then runs only the second binary, and status-right holds the
// counts once.
func TestPluginSourcedAgainReplacesItsHooksAndStatus(t *testing.T) {
	s := startServer(t)
	s.sourcePlugin(t, "/old/bin/orrery")
	s.sourcePlugin(t, "/new/bin/orrery")
	for _, hook := range []string{"window-pane-changed", "session-window-changed", "client-session-changed"} {
		hooks := strings.TrimSpace(s.must(t, "show-hooks", "-g", hook))
		if lines := strings.Split(hooks, "\n"); len(lines) != 1 {
			t.Fatalf("%s holds %d hooks after a second source, want 1: %s", hook, len(lines), hooks)
		}
		if strings.Contains(hooks, "/old/bin/orrery") || !strings.Contains(hooks, "/new/bin/orrery seen --pane") {
			t.Fatalf("%s after a second source = %s, want only the new binary's seen command", hook, hooks)
		}
	}
	status := s.must(t, "show-options", "-g", "-v", "status-right")
	if n := strings.Count(status, " orrery "); n != 1 {
		t.Fatalf("status-right holds the orrery counts %d times after a second source, want once: %q", n, status)
	}
}

// TestServerRunShellHasOrreryDaemonSocket checks that run-shell jobs of a test server, the focus hooks' jobs
// included, see ORRERY_SOCKET under the test's temp dir, so an orrery command a test runs cannot reach the owner's
// orreryd.
func TestServerRunShellHasOrreryDaemonSocket(t *testing.T) {
	s := startServer(t)
	record := filepath.Join(s.tempDir, "socket")
	script := fmt.Sprintf(`printf '%%s' "$%s" > '%s.tmp' && mv '%s.tmp' '%s'`,
		protocol.EnvSocket, record, record, record)
	s.must(t, "run-shell", "-b", strings.ReplaceAll(script, "#", "##"))
	waitFor(t, "the run-shell job to record its environment", func() (bool, error) {
		_, err := os.Stat(record)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return err == nil, err
	})
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read recorded socket: %v", err)
	}
	if want := filepath.Join(s.tempDir, "orreryd.sock"); string(got) != want {
		t.Fatalf("run-shell %s = %q, want %q", protocol.EnvSocket, got, want)
	}
}

// heldWriterScript is a run-shell job that writes into the test's temp dir only once its server is gone, as a focus
// hook's fake orrery can still be writing when the test returns. Its %[1]s is a directory that outlives the test, %[2]s
// the test's temp dir and %[3]s the server's name. It ignores the SIGTERM tmux sends a job's shell as the server exits,
// writes started, waits on wait-for, which returns once the server is gone, and then writes late into the temp dir
// and, only when that write landed, wrote.
const heldWriterScript = `trap '' TERM
: > '%[1]s/started'
tmux -L '%[3]s' wait-for orrery-never
: > '%[2]s/late' && : > '%[1]s/wrote'`

// TestServerJobsEndBeforeTempDirRemoval holds a server's job past the test body: the job writes into the test's temp
// dir only after kill-server. The test's cleanup waits for the job, so the write lands before the temp dir is
// removed, and the removal then succeeds.
func TestServerJobsEndBeforeTempDirRemoval(t *testing.T) {
	record := t.TempDir()
	ok := t.Run("held job", func(t *testing.T) {
		s := startServer(t)
		script := fmt.Sprintf(heldWriterScript, record, s.tempDir, s.name)
		s.must(t, "run-shell", "-b", strings.ReplaceAll(script, "#", "##"))
		waitFor(t, "the server's job to start", func() (bool, error) {
			_, err := os.Stat(filepath.Join(record, "started"))
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return err == nil, err
		})
	})
	if !ok {
		return
	}
	if _, err := os.Stat(filepath.Join(record, "wrote")); err != nil {
		t.Fatalf("the server's job had not written into the test's temp dir when its cleanup ended: %v", err)
	}
}

// attachClient starts a client on a second private server, attached through that server's viewer pane to session
// on s, waits until s counts the client, and returns the second server.
func attachClient(t *testing.T, s testServer, session string) testServer {
	t.Helper()
	viewer := startServer(t)
	viewer.must(t, "-f", noConfig, "new-session", "-d", "-s", "viewer", "-c", t.TempDir(),
		fmt.Sprintf("env -u TMUX tmux -L %s attach-session -t %s", s.name, session))
	waitFor(t, "a client attached to "+session, func() (bool, error) {
		out, err := s.client.Run(t.Context(), "display", "-p", "-t", session, "#{session_attached}")
		return strings.TrimSpace(out) == "1", err
	})
	return viewer
}

func TestSelectPaneSetsSeenOnlyWhenAttached(t *testing.T) {
	s := startServer(t)
	s.sourcePlugin(t, builtOrrery())
	s.must(t, "split-window", "-d", "-t", "work", testCommand)
	attached := s.panes(t, "work")

	attachClient(t, s, "work")

	s.must(t, "select-pane", "-t", attached[1])
	waitFor(t, "@orrery_seen set on the pane selected in an attached session", func() (bool, error) {
		_, ok, err := s.client.PaneOption(t.Context(), attached[1], protocol.OptionSeen)
		return ok, err
	})

	s.must(t, "new-session", "-d", "-s", "idle", "-c", t.TempDir(), testCommand)
	s.must(t, "split-window", "-d", "-t", "idle", testCommand)
	idle := s.panes(t, "idle")
	s.must(t, "select-pane", "-t", idle[1])
	quiet()
	if _, ok, err := s.client.PaneOption(t.Context(), idle[1], protocol.OptionSeen); err != nil || ok {
		t.Fatalf("@orrery_seen on the pane selected in a detached session: set=%v err=%v, want unset", ok, err)
	}
}

func TestStatusRightCountsWaitingAndNew(t *testing.T) {
	s := startServer(t)
	s.sourcePlugin(t, builtOrrery())
	s.must(t, "split-window", "-d", "-t", "work", testCommand)
	s.must(t, "split-window", "-d", "-t", "work", testCommand)
	panes := s.panes(t, "work")

	shellSession := protocol.ShellSessionPrefix + synthetic
	s.must(t, "new-session", "-d", "-s", shellSession, "-c", t.TempDir(), testCommand)
	shell := s.panes(t, shellSession)

	s.setPane(t, panes[0], string(protocol.StateWaiting), "")
	s.setPane(t, panes[1], string(protocol.StateDone), "")
	s.setPane(t, panes[2], string(protocol.StateDone), seenAfter)
	s.setPane(t, shell[0], string(protocol.StateWaiting), "")

	out := s.must(t, "display", "-p", "-t", "work", "#{E:status-right}")
	if plain := styleSpan.ReplaceAllString(out, ""); !strings.Contains(plain, "orrery 1 waiting 1 new") {
		t.Fatalf("expanded status-right without styles = %q, want it to contain %q", plain, "orrery 1 waiting 1 new")
	}
	for _, want := range []string{"#[fg=#fab387]1#[default]", "#[fg=#cba6f7]1#[default]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expanded status-right = %q, want it to contain the count style %q", out, want)
		}
	}
}

// styleSpan matches one tmux style span such as #[fg=#fab387] or #[default].
var styleSpan = regexp.MustCompile(`#\[[^\]]*\]`)

// TestPaneBorderAndPopupStyle checks that each state's pane border shows the state word in its state color, the label
// in Text and the summary in Overlay0, and that the popup options are the Mocha lavender border on the base background.
func TestPaneBorderAndPopupStyle(t *testing.T) {
	s := startServer(t)
	s.sourcePlugin(t, builtOrrery())
	s.must(t, "split-window", "-d", "-t", "work", testCommand)
	s.must(t, "split-window", "-d", "-t", "work", testCommand)
	panes := s.panes(t, "work")

	cases := []struct {
		state string
		color string
	}{
		{string(protocol.StateWaiting), "#fab387"},
		{string(protocol.StateWorking), "#f9e2af"},
		{string(protocol.StateDone), "#a6e3a1"},
	}
	for i, c := range cases {
		pane := panes[i]
		s.setPane(t, pane, c.state, "")
		s.must(t, "set-option", "-p", "-t", pane, protocol.OptionLabel, "label "+c.state)
		s.must(t, "set-option", "-p", "-t", pane, protocol.OptionSummary, "summary "+c.state)

		got := strings.TrimSpace(s.must(t, "display", "-p", "-t", pane, "#{E:pane-border-format}"))
		want := "#[fg=" + c.color + "]" + c.state + "#[default] " +
			"#[fg=#cdd6f4]label " + c.state + "#[default] " +
			"#[fg=#6c7086]summary " + c.state + "#[default]"
		if got != want {
			t.Fatalf("expanded pane-border-format for %s pane = %q, want %q", c.state, got, want)
		}
	}

	popup := map[string]string{
		"popup-border-lines": "rounded",
		"popup-border-style": "fg=#b4befe",
		"popup-style":        "bg=#1e1e2e",
	}
	for option, want := range popup {
		if got := strings.TrimSpace(s.must(t, "show-options", "-gv", option)); got != want {
			t.Fatalf("%s = %q, want %q", option, got, want)
		}
	}
}

// fakeOrrery writes a shell script named orrery into a fresh directory and returns its path. Each run writes its
// arguments, one per line, to a file beside it named after its subcommand, so each subcommand has its own record.
func fakeOrrery(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery")
	script := `#!/bin/sh
d=$(dirname "$0")
printf '%s\n' "$@" > "$d/$1.tmp" && mv "$d/$1.tmp" "$d/$1"
`
	exectest.WriteFile(t, path, []byte(script), 0o700)
	return path
}

// awaitArgs waits until the fake orrery at orrery has recorded a run of subcommand, and returns that run's arguments.
func awaitArgs(t *testing.T, orrery, subcommand string) []string {
	t.Helper()
	record := filepath.Join(filepath.Dir(orrery), subcommand)
	var args []string
	waitFor(t, "the fake orrery to record its arguments", func() (bool, error) {
		data, err := os.ReadFile(record)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		args = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		return true, nil
	})
	return args
}

func TestPrefixSpaceOpensTreeOnInvokingClient(t *testing.T) {
	s := startServer(t)
	orrery := fakeOrrery(t)
	s.sourcePlugin(t, orrery)
	viewer := attachClient(t, s, "work")

	tty := strings.TrimSpace(s.must(t, "list-clients", "-F", "#{client_tty}"))
	viewer.must(t, "send-keys", "-t", viewer.panes(t, "viewer")[0], "C-b", "Space")
	want := []string{"tree", "--client", tty}
	if got := awaitArgs(t, orrery, "tree"); !slices.Equal(got, want) {
		t.Fatalf("fake orrery arguments = %q, want %q", got, want)
	}
}

// projectNames returns the pane directory names the prefix s tests use: one with a space, and two whose #( and #{
// must reach the shell unexpanded.
func projectNames() []string {
	return []string{"project one", "project#(x)", "project#{x}"}
}

// projectDir makes a directory named name under t.TempDir and returns its path.
func projectDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("make project directory: %v", err)
	}
	return dir
}

// startInDir starts session project with its pane in dir. tmux expands formats in -c, so every # is doubled.
func (s testServer) startInDir(t *testing.T, dir string) {
	t.Helper()
	s.must(t, "new-session", "-d", "-s", "project", "-c", strings.ReplaceAll(dir, "#", "##"), testCommand)
}

// paneDir returns the current directory of the project session's pane.
func (s testServer) paneDir(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(s.must(t, "display", "-p", "-t", "project", "#{pane_current_path}"))
}

func TestPrefixSRunsShellWithPaneAndClient(t *testing.T) {
	for i, name := range projectNames() {
		t.Run(fmt.Sprintf("dir%d", i), func(t *testing.T) {
			s := startServer(t)
			orrery := fakeOrrery(t)
			s.sourcePlugin(t, orrery)
			s.startInDir(t, projectDir(t, name))
			viewer := attachClient(t, s, "project")

			path := s.paneDir(t)
			tty := strings.TrimSpace(s.must(t, "list-clients", "-F", "#{client_tty}"))
			viewer.must(t, "send-keys", "-t", viewer.panes(t, "viewer")[0], "C-b", "s")
			want := []string{"shell", "--dir", path, "--client", tty}
			if got := awaitArgs(t, orrery, "shell"); !slices.Equal(got, want) {
				t.Fatalf("fake orrery arguments = %q, want %q", got, want)
			}
		})
	}
}

func TestPrefixSOpensProjectShellSession(t *testing.T) {
	for i, name := range projectNames() {
		t.Run(fmt.Sprintf("dir%d", i), func(t *testing.T) {
			s := startServer(t)
			s.sourcePlugin(t, builtOrrery())
			s.startInDir(t, projectDir(t, name))
			viewer := attachClient(t, s, "project")

			path := s.paneDir(t)
			target := "=" + shell.Name(path) + ":"
			viewer.must(t, "send-keys", "-t", viewer.panes(t, "viewer")[0], "C-b", "s")
			waitFor(t, "the project shell session attached by the popup", func() (bool, error) {
				// The session appears once the popup's shell command creates it, so a failed lookup means not yet.
				out, err := s.client.Run(t.Context(), "display", "-p", "-t", target, "#{session_attached}")
				return err == nil && strings.TrimSpace(out) == "1", nil
			})
			got := strings.TrimSpace(s.must(t, "show-options", "-v", "-t", target, protocol.OptionDir))
			if got != path {
				t.Fatalf("%s = %q, want %q", protocol.OptionDir, got, path)
			}
		})
	}
}
