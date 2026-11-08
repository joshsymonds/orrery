package tmuxconf_test

import (
	"bytes"
	"context"
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

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxconf"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// nonShellName matches each character a shell session name replaces with an underscore.
var nonShellName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

const (
	binDirEnv    = "ORRERY_TMUXCONF_BIN_DIR"
	goldenPath   = "testdata/plugin.golden"
	goldenOrrery = "/synthetic/bin/orrery"
	noConfig     = "/dev/null"
	testCommand  = "sleep 300"
	synthetic    = "synthetic"
	changedAt    = "100"
	seenAfter    = "200"
	waitLimit    = 10 * time.Second
	pollEvery    = 10 * time.Millisecond
	quietWindow  = 1500 * time.Millisecond
)

func TestMain(m *testing.M) {
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
	name   string
	client *tmuxctl.Client
}

// startServer starts a private tmux server with -f /dev/null and one session named work, whose pane runs
// testCommand.
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
	t.Cleanup(func() {
		if _, err := client.Run(context.Background(), "kill-server"); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	s := testServer{name: name, client: client}
	s.must(t, "-f", noConfig, "new-session", "-d", "-s", "work", "-c", t.TempDir(), testCommand)
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

// waitFor polls check every pollEvery until it reports true, and fails the test at waitLimit.
func waitFor(t *testing.T, what string, check func() (bool, error)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
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
		select {
		case <-ctx.Done():
			t.Fatalf("%s: not true after %v", what, waitLimit)
		case <-ticker.C:
		}
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
	if hooks := s.must(t, "show-hooks", "-g", "window-pane-changed"); !strings.Contains(hooks, "seen --pane") {
		t.Fatalf("window-pane-changed hook missing seen command: %s", hooks)
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
	if !strings.Contains(out, "orrery 1 waiting 1 new") {
		t.Fatalf("expanded status-right = %q, want it to contain %q", out, "orrery 1 waiting 1 new")
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
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake orrery: %v", err)
	}
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

func TestPrefixSOpensProjectShellSessionInPanePath(t *testing.T) {
	s := startServer(t)
	s.sourcePlugin(t, builtOrrery())
	dir := filepath.Join(t.TempDir(), "project one")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("make project directory: %v", err)
	}
	s.must(t, "new-session", "-d", "-s", "project", "-c", dir, testCommand)
	viewer := attachClient(t, s, "project")

	path := strings.TrimSpace(s.must(t, "display", "-p", "-t", "project", "#{pane_current_path}"))
	if !strings.HasSuffix(path, "/project one") {
		t.Fatalf("pane_current_path = %q, want the project directory", path)
	}
	target := "=" + protocol.ShellSessionPrefix + nonShellName.ReplaceAllString(path, "_") + ":"
	viewer.must(t, "send-keys", "-t", viewer.panes(t, "viewer")[0], "C-b", "s")
	waitFor(t, "the project shell session attached by the popup", func() (bool, error) {
		// The session appears once the binding's background job creates it, so a failed lookup means not yet.
		out, err := s.client.Run(t.Context(), "display", "-p", "-t", target, "#{session_attached}")
		return err == nil && strings.TrimSpace(out) == "1", nil
	})
	if got := strings.TrimSpace(s.must(t, "show-options", "-v", "-t", target, protocol.OptionDir)); got != path {
		t.Fatalf("%s = %q, want %q", protocol.OptionDir, got, path)
	}
}
