package shell_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/shell"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

const (
	noConfig = "/dev/null"
	// popupEnv names the file a popup job appends to once it has started. Only TestPopupJob reads it.
	popupEnv = "ORRERY_SHELL_POPUP"
)

// fixture is a private tmux server holding a base session, with two clients attached to it through
// script. invoking is the client Open is called for; other is a larger client that a misplaced popup
// would be drawn on.
type fixture struct {
	t        *testing.T
	client   *tmuxctl.Client
	invoking string
	other    string
	// events is the directory the server's hooks append to on each change that a wait reads.
	events string
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
	// The events directory is made before the server, so that its cleanup runs after the server is killed: a client's
	// detach, which the hooks append for, can happen until then.
	events := t.TempDir()
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, tmuxctl.Server{Name: name}); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	f := &fixture{t: t, client: client, events: events}
	f.run("-f", noConfig, "new-session", "-d", "-s", "base", "-c", t.TempDir(), "sleep 300")
	// Each hook appends to the changes file the waits watch, after the change it names has happened.
	changes := fmt.Sprintf("run-shell -b 'printf . >> %s'", filepath.Join(f.events, "changes"))
	for _, hook := range []string{"after-new-session", "client-attached", "client-detached", "client-resized",
		"client-session-changed"} {
		f.run("set-hook", "-g", hook, changes)
	}
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
	cmd.Env = append(withoutEnv(os.Environ(), "TMUX"), "TERM=xterm-256color")
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
	f.waitFor("client attached to base", func() error {
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

// showPopup shows command in a popup on the client at tty, the way the Space binding shows the tree: a
// shell job runs tmux display-popup. A display-popup -E queued in the server's command list would hold
// that list until the popup closes, so the popup is not started that way.
func (f *fixture) showPopup(tty, command string) {
	f.t.Helper()
	socket := f.run("display-message", "-p", "#{socket_path}")
	f.run("run-shell", "-b", fmt.Sprintf("tmux -S %s display-popup -c %s -E '%s'", socket, tty, command))
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

// waitFor checks cond, then blocks until the server's hooks append to the changes file, and checks again after each
// append. The watch is added before the first check, so a change made after a check is an event the next read returns.
// It has no deadline of its own: the test binary's timeout ends a wait that never ends.
func (f *fixture) waitFor(what string, cond func() error) {
	f.t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		f.t.Fatalf("waiting for %s: inotify: %v", what, err)
	}
	// The descriptor is non-blocking, so the runtime poller serves each read and Close ends a read that is waiting.
	inotify := os.NewFile(uintptr(fd), "wait watch")
	defer func() {
		if err := inotify.Close(); err != nil {
			f.t.Errorf("close wait watch: %v", err)
		}
	}()
	const mask = unix.IN_CREATE | unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO
	if _, err := unix.InotifyAddWatch(fd, f.events, mask); err != nil {
		f.t.Fatalf("waiting for %s: watch %s: %v", what, f.events, err)
	}
	buf := make([]byte, 4096)
	for cond() != nil {
		if _, err := inotify.Read(buf); err != nil {
			f.t.Fatalf("waiting for %s: read watch events: %v", what, err)
		}
	}
}

// popupJob returns the command a popup runs in place of the tree, and the command line popupJobs matches for it. The
// command runs the test binary as TestPopupJob, with tag as its argument, so each job's command line is its own.
// popupEnv names the file the job appends to in the events directory once it has exec'd.
func (f *fixture) popupJob(t *testing.T, tag string) (command, cmdline string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("test binary: %v", err)
	}
	cmdline = fmt.Sprintf("%s -test.run=^TestPopupJob$ %s", exe, tag)
	command = fmt.Sprintf("env %s=%s %s", popupEnv, filepath.Join(f.events, tag), cmdline)
	return command, cmdline
}

// TestPopupJob is the job a popup runs in place of the tree. The popup runs the test binary with only this test selected
// and popupEnv set; without popupEnv the test does nothing.
func TestPopupJob(t *testing.T) {
	announce := os.Getenv(popupEnv)
	if announce == "" {
		t.Skip("runs only as a popup job")
	}
	// The announcement comes after the exec, so the command line a wait matches is the job's own argv.
	if err := announceJob(announce); err != nil {
		t.Fatalf("announce job: %v", err)
	}
	// The job holds the popup open on its stdin and catches no signal, so the SIGHUP tmux sends when the popup closes
	// ends it, as it ended the sleep this stood in for.
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatalf("hold the popup open: %v", err)
	}
}

// announceJob appends to the file at path, which the fixture's waits watch in the events directory.
func announceJob(path string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(".")
	return errors.Join(err, file.Close())
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
			f.waitFor("session "+name+" starts in dir", func() error { return f.panePathErr(name, dir) })
			if got := f.run("display-message", "-p", "-t", "="+name+":", "#{@orrery_dir}"); got != dir {
				t.Fatalf("@orrery_dir = %q, want %q", got, dir)
			}
			// The session's directory is the name as written, and only an expansion of its formats would run the job, so
			// the exact @orrery_dir is the proof; the marker is checked as it stands.
			f.waitFor("popup attaches "+name+" on invoking client", func() error { return f.popupErr(name) })
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
	f.waitFor("session "+name+" exists", func() error {
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

func TestOpenWhilePopupShownOnInvokingClient(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	name := shell.Name(dir)
	command, tree := f.popupJob(t, "invoking") // stands in for the tree, shown in a popup on the invoking client
	f.showPopup(f.invoking, command)
	socket := f.run("display-message", "-p", "#{socket_path}")
	f.waitFor("popup running on invoking client", func() error { return running(socket, tree) })
	if err := shell.Open(t.Context(), f.client, dir, f.invoking); err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	f.waitFor("popup attaches "+name+" on invoking client", func() error { return f.popupErr(name) })
}

func TestOpenLeavesPopupOnOtherClientOpen(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	name := shell.Name(dir)
	command, tree := f.popupJob(t, "other") // stands in for the tree, shown in a popup on the other client
	f.showPopup(f.other, command)
	socket := f.run("display-message", "-p", "#{socket_path}")
	f.waitFor("popup running on other client", func() error { return running(socket, tree) })
	if err := shell.Open(t.Context(), f.client, dir, f.invoking); err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	f.waitFor("popup attaches "+name+" on invoking client", func() error { return f.popupErr(name) })
	// Open's tmux commands have all run by now, and a popup tmux closes has its job signaled as it closes. So the job
	// still runs with no termination signal pending only if its popup was left open.
	if err := runningUnsignaled(socket, tree); err != nil {
		t.Fatalf("popup on other client closed: %v", err)
	}
	infos, err := f.clients()
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	for _, info := range infos {
		if info.tty == f.other && info.session != "base" {
			t.Fatalf("other client moved to session %s, want base", info.session)
		}
	}
}

// popupJobs returns the pids of the processes whose command line is command and that run under the tmux server at
// socket, as a popup's job runs with TMUX naming its server's socket. Popup jobs are matched on their server as well as
// their command: another run of this test, by another test binary on the machine, shows the same command in its own
// popup, and matched on the command alone this test would read that run's job as its own.
func popupJobs(socket, command string) ([]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	var pids []string
	for _, entry := range entries {
		if _, convErr := strconv.Atoi(entry.Name()); convErr != nil {
			continue
		}
		cmdline, readErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if readErr != nil || strings.Join(strings.Split(strings.TrimSuffix(string(cmdline), "\x00"), "\x00"), " ") !=
			command {
			continue
		}
		environ, readErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if readErr != nil || !onServer(string(environ), socket) {
			continue
		}
		pids = append(pids, entry.Name())
	}
	return pids, nil
}

// onServer reports whether environ, a process's NUL-separated environment, sets TMUX to name the server socket.
func onServer(environ, socket string) bool {
	for entry := range strings.SplitSeq(environ, "\x00") {
		if value, ok := strings.CutPrefix(entry, "TMUX="); ok {
			return strings.HasPrefix(value, socket+",")
		}
	}
	return false
}

// running returns an error unless a popup job on the server at socket runs command.
func running(socket, command string) error {
	pids, err := popupJobs(socket, command)
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		return fmt.Errorf("no process running %q on %s", command, socket)
	}
	return nil
}

// runningUnsignaled reports an error unless a popup job on the server at socket runs command, none of them has ended,
// and none holds a pending SIGHUP, SIGTERM or SIGKILL. A signaled process may not have ended yet, so its pending signals
// are read too.
func runningUnsignaled(socket, command string) error {
	pids, err := popupJobs(socket, command)
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		return fmt.Errorf("no process running %q on %s", command, socket)
	}
	const ending = uint64(1)<<(syscall.SIGHUP-1) | uint64(1)<<(syscall.SIGTERM-1) | uint64(1)<<(syscall.SIGKILL-1)
	for _, pid := range pids {
		status, err := os.ReadFile(filepath.Join("/proc", pid, "status"))
		if err != nil {
			return fmt.Errorf("process %s running %q ended: %w", pid, command, err)
		}
		for field := range strings.Lines(string(status)) {
			name, value, ok := strings.Cut(strings.TrimSpace(field), ":\t")
			if !ok {
				continue
			}
			if name == "State" && (strings.HasPrefix(value, "Z") || strings.HasPrefix(value, "X")) {
				return fmt.Errorf("process %s running %q has ended", pid, command)
			}
			if name != "SigPnd" && name != "ShdPnd" {
				continue
			}
			mask, err := strconv.ParseUint(value, 16, 64)
			if err != nil {
				return fmt.Errorf("parse %s of process %s: %w", name, pid, err)
			}
			if mask&ending != 0 {
				return fmt.Errorf("process %s running %q holds a pending termination signal (%s %s)", pid, command,
					name, value)
			}
		}
	}
	return nil
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
