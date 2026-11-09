package shell_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/shell"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	noConfig   = "/dev/null"
	waitLimit  = 10 * time.Second
	pollEvery  = 50 * time.Millisecond
	settleWait = 500 * time.Millisecond
)

// fixture is a private tmux server holding a base session, with two clients attached to it through
// script. invoking is the client Open is called for; other is a larger client that a misplaced popup
// would be drawn on.
type fixture struct {
	t        *testing.T
	client   *tmuxctl.Client
	invoking string
	other    string
}

// clientInfo is one attached client as list-clients reports it.
type clientInfo struct {
	tty     string
	session string
	width   int
	height  int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-shell-test-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := client.Run(context.Background(), "kill-server"); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	f := &fixture{t: t, client: client}
	f.run("-f", noConfig, "new-session", "-d", "-s", "base", "-c", t.TempDir(), "sleep 300")
	f.invoking = f.attach(80, 24)
	f.other = f.attach(200, 60)
	return f
}

// run runs tmux on the fixture server and returns its trimmed stdout.
func (f *fixture) run(args ...string) string {
	f.t.Helper()
	out, err := f.client.Run(f.t.Context(), args...)
	if err != nil {
		f.t.Fatalf("tmux %v: %v", args, err)
	}
	return strings.TrimSpace(out)
}

// attach starts a client of cols by rows on base through script and returns its tty once tmux
// lists it. The client's stdin stays open until cleanup.
func (f *fixture) attach(cols, rows int) string {
	f.t.Helper()
	socket := f.run("display-message", "-p", "#{socket_path}")
	known := map[string]bool{}
	infos, err := f.clients()
	if err != nil {
		f.t.Fatalf("list clients: %v", err)
	}
	for _, info := range infos {
		known[info.tty] = true
	}
	command := fmt.Sprintf("stty cols %d rows %d; tmux -S %s attach -t base", cols, rows, socket)
	cmd := exec.CommandContext(f.t.Context(), "script", "-qfc", command, "/dev/null")
	cmd.Env = withoutEnv(os.Environ(), "TMUX")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		f.t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		f.t.Fatalf("start client: %v", err)
	}
	f.t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	var tty string
	waitFor(f.t, "client attached to base", func() error {
		infos, err := f.clients()
		if err != nil {
			return err
		}
		for _, info := range infos {
			if !known[info.tty] && info.session == "base" {
				tty = info.tty
				return nil
			}
		}
		return fmt.Errorf("no new client on base in %v", infos)
	})
	return tty
}

// withoutEnv returns env without the entries for name, so that a client started from a process
// inside tmux does not refuse to attach.
func withoutEnv(env []string, name string) []string {
	var kept []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			kept = append(kept, kv)
		}
	}
	return kept
}

// clients returns each attached client with the session it shows and its size.
func (f *fixture) clients() ([]clientInfo, error) {
	out, err := f.client.Run(f.t.Context(), "list-clients", "-F",
		"#{client_tty} #{client_session} #{client_width} #{client_height}")
	if err != nil {
		return nil, fmt.Errorf("list-clients: %w", err)
	}
	var infos []clientInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("list-clients line %q: want 4 fields", line)
		}
		width, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("list-clients width in %q: %w", line, err)
		}
		height, err := strconv.Atoi(fields[3])
		if err != nil {
			return nil, fmt.Errorf("list-clients height in %q: %w", line, err)
		}
		infos = append(infos, clientInfo{tty: fields[0], session: fields[1], width: width, height: height})
	}
	return infos, nil
}

// popupErr returns an error unless a client other than the other client shows session name, the
// invoking client still shows base, and the client showing name is smaller than the invoking
// client. The other client is larger, so a popup drawn on it would fail the size check.
func (f *fixture) popupErr(name string) error {
	infos, err := f.clients()
	if err != nil {
		return err
	}
	var invoking, popup *clientInfo
	for i := range infos {
		switch {
		case infos[i].tty == f.invoking:
			invoking = &infos[i]
		case infos[i].session == name:
			popup = &infos[i]
		}
	}
	if invoking == nil {
		return fmt.Errorf("invoking client %s not listed in %v", f.invoking, infos)
	}
	if invoking.session != "base" {
		return fmt.Errorf("invoking client %s moved to session %s", f.invoking, invoking.session)
	}
	if popup == nil {
		return fmt.Errorf("no client attached to %s in %v", name, infos)
	}
	if popup.width >= invoking.width || popup.height >= invoking.height {
		return fmt.Errorf("popup client %s is %dx%d, not smaller than invoking client %dx%d",
			popup.tty, popup.width, popup.height, invoking.width, invoking.height)
	}
	return nil
}

// panePathErr returns an error unless the first pane of session name currently runs in dir.
func (f *fixture) panePathErr(name, dir string) error {
	out, err := f.client.Run(f.t.Context(), "list-panes", "-t", "="+name+":", "-F", "#{pane_current_path}")
	if err != nil {
		return fmt.Errorf("list-panes %s: %w", name, err)
	}
	if got := strings.TrimSpace(out); got != dir {
		return fmt.Errorf("pane path = %q, want %q", got, dir)
	}
	return nil
}

func waitFor(t *testing.T, what string, cond func() error) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for {
		err := cond()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %v", what, err)
		}
		time.Sleep(pollEvery)
	}
}

func TestNameIsPrefixAndHashOfCleanDir(t *testing.T) {
	dir := "/synthetic/project dir #(x) #{y}"
	sum := sha256.Sum256([]byte(dir))
	want := protocol.ShellSessionPrefix + hex.EncodeToString(sum[:])[:12]
	if got := shell.Name(dir); got != want {
		t.Fatalf("Name(%q) = %q, want %q", dir, got, want)
	}
	for _, variant := range []string{dir + "/", dir + "/./"} {
		if got := shell.Name(variant); got != want {
			t.Fatalf("Name(%q) = %q, want %q (the name of the cleaned directory)", variant, got, want)
		}
	}
}

func TestOpenCreatesSessionForAwkwardDirectories(t *testing.T) {
	parent := t.TempDir()
	marker := filepath.Join(parent, "ran")
	// The name is a tmux format job. A directory name holds no slash, so the marker path is spelled
	// with @ and tr rebuilds it inside the job, with the slash written as the octal escape \057.
	// The job runs only if a format expands it.
	injected := `job #(touch "$(echo ` + strings.ReplaceAll(marker, "/", "@") + ` | tr '@' '\057')") end`
	cases := []struct {
		label string
		name  string
	}{
		{"space", "with space"},
		{"format brace", "hash #{brace} dir"},
		{"format job", injected},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			f := newFixture(t)
			dir := filepath.Join(parent, tc.name)
			if err := os.Mkdir(dir, 0o750); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			name := shell.Name(dir)
			if err := shell.Open(t.Context(), f.client, dir, f.invoking); err != nil {
				t.Fatalf("Open(%q): %v", dir, err)
			}
			waitFor(t, "session "+name+" starts in dir", func() error { return f.panePathErr(name, dir) })
			if got := f.run("display-message", "-p", "-t", "="+name+":", "#{@orrery_dir}"); got != dir {
				t.Fatalf("@orrery_dir = %q, want %q", got, dir)
			}
			waitFor(t, "popup attaches "+name+" on invoking client", func() error { return f.popupErr(name) })
			time.Sleep(settleWait)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("command in directory name ran: stat %s = %v", marker, err)
			}
		})
	}
}

func TestOpenTwiceCreatesOneSession(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	name := shell.Name(dir)
	if err := shell.Open(t.Context(), f.client, dir, f.invoking); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	waitFor(t, "session "+name+" exists", func() error {
		_, err := f.client.Run(t.Context(), "has-session", "-t", "="+name)
		return err
	})
	if err := shell.Open(t.Context(), f.client, dir, f.invoking); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	sessions := strings.Fields(f.run("list-sessions", "-F", "#{session_name}"))
	if len(sessions) != 2 || !slices.Contains(sessions, "base") || !slices.Contains(sessions, name) {
		t.Fatalf("sessions after second Open = %v, want base and %s only", sessions, name)
	}
}

func TestOpenRejectsRelativeDirAndEmptyTTY(t *testing.T) {
	f := newFixture(t)
	abs := t.TempDir()
	cases := []struct {
		label string
		dir   string
		tty   string
	}{
		{"relative dir", "relative/dir", f.invoking},
		{"empty tty", abs, ""},
	}
	for _, tc := range cases {
		if err := shell.Open(t.Context(), f.client, tc.dir, tc.tty); err == nil {
			t.Fatalf("%s: Open(%q, %q) = nil, want error", tc.label, tc.dir, tc.tty)
		}
	}
	if got := f.run("list-sessions", "-F", "#{session_name}"); got != "base" {
		t.Fatalf("sessions after rejected Opens = %q, want base only", got)
	}
}
