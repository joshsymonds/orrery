package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// engineFakeClaude is the scripted claude the engine host tests run in place of the real one.
const engineFakeClaude = "../enginehost/testdata/fakeclaude.sh"

// engineHistoryLimit is the most stored events one history read returns. The sessions these tests run store far fewer.
const engineHistoryLimit = 1000

// engineFixture is one private tmux server and the directories an orreryd with sessions runs in. Each session starts in
// its own directory, because the fake claude writes its pid into the directory it starts in.
type engineFixture struct {
	server     string
	root       string
	socket     string
	sessions   string
	dbPath     string
	fakeClaude string
	shell      string
	env        []string
	reader     *store.Reader
}

// engineBlock is the payload of an engine_block event.
type engineBlock struct {
	Kind string `json:"kind"`
	Role string `json:"role"`
	Text string `json:"text"`
}

// newEngineFixture starts a private tmux server with a pane, so the server exists for its cleanup, and resolves the fake
// claude and sh. Every path the daemon, the CLI and the store use lives under the test's own directory.
func newEngineFixture(t *testing.T) *engineFixture {
	t.Helper()
	client, name := newServer(t)
	startPane(t, client, "sleep 300")

	root := t.TempDir()
	fakeClaude, err := filepath.Abs(engineFakeClaude)
	if err != nil {
		t.Fatalf("resolve fake claude: %v", err)
	}
	if _, err = os.Stat(fakeClaude); err != nil {
		t.Fatalf("fake claude: %v", err)
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	stateHome := filepath.Join(root, "state")
	socket := filepath.Join(root, "orreryd.sock")
	f := &engineFixture{
		server:     name,
		root:       root,
		socket:     socket,
		sessions:   protocol.SessionSocketPath(socket),
		dbPath:     filepath.Join(stateHome, "orrery", "orrery.db"),
		fakeClaude: fakeClaude,
		shell:      shell,
		env: scrubbedEnv(
			"HOME="+filepath.Join(root, "home"),
			"XDG_RUNTIME_DIR="+filepath.Join(root, "runtime"),
			"XDG_STATE_HOME="+stateHome,
			"ORRERY_SOCKET="+socket,
		),
	}
	f.reader = store.NewReader(f.dbPath)
	return f
}

// startOrreryd starts orreryd on the fixture's tmux server with the fake claude. With sessions it also enables the
// session service with the fixture's shell, and it waits for sessions.sock to listen. extra is passed as more flags.
// The test stops the daemon with stop; a test that fails first has it terminated at cleanup, so its sessions stop too.
func (f *engineFixture) startOrreryd(t *testing.T, sessions bool, extra ...string) *orreryProcess {
	t.Helper()
	args := []string{"-L", f.server, "--socket", f.socket, "--claude", f.fakeClaude}
	if sessions {
		args = append(args, "--sessions", "--session-shell", f.shell)
	}
	args = append(args, extra...)
	daemon := startBinary(t, "orreryd", f.env, args...)
	t.Cleanup(func() { terminate(daemon) })
	daemon.waitForListener(t, "unix", f.socket)
	if sessions {
		// sessions.sock listens only after the store open, which has no bound, so the listen's wait limit starts at
		// the store-open line.
		daemon.waitForLog(t, storeOpenLog)
		daemon.waitForListener(t, "unix", f.sessions)
	}
	return daemon
}

// terminate sends SIGTERM to p and waits for it when p is still running. It is the cleanup of a test that ended before
// it stopped its daemon, so the sessions p runs record their stop.
func terminate(p *orreryProcess) {
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(waitLimit):
	}
}

// orrery runs the built orrery CLI with the fixture's environment.
func (f *engineFixture) orrery(t *testing.T, args ...string) outcome {
	t.Helper()
	return runCommand(t, "", "orrery", "", f.env, args...)
}

// sessionDir makes and returns the directory a session named name starts in.
func (f *engineFixture) sessionDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(f.root, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("make session directory: %v", err)
	}
	return dir
}

// start runs `orrery session start` in dir with prompt and returns the id it prints.
func (f *engineFixture) start(t *testing.T, dir, prompt string) string {
	t.Helper()
	out := f.orrery(t, "session", "start", dir, prompt)
	wantExit(t, out, 0)
	id := strings.TrimSpace(out.stdout)
	if id == "" {
		t.Fatalf("session start printed no id; stderr %q", out.stderr)
	}
	return id
}

// waitForHistory waits until ready accepts the stored history of session id. A store that is missing or has no row for
// id yet is read again, and the test fails after waitLimit.
func (f *engineFixture) waitForHistory(t *testing.T, id string, ready func(store.EngineHistory) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		history, err := f.reader.EngineHistory(ctx, id, 0, engineHistoryLimit)
		if err != nil && !errors.Is(err, store.ErrUnavailable) && !errors.Is(err, store.ErrNoEngineSession) {
			t.Fatalf("read history of %s: %v", id, err)
		}
		if err == nil && ready(history) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("history of %s never reached the wanted state", id)
		case <-ticker.C:
		}
	}
}

// waitForEvent waits until the history of session id holds an event of kind whose payload contains want.
func (f *engineFixture) waitForEvent(t *testing.T, id, kind, want string) {
	t.Helper()
	f.waitForHistory(t, id, func(h store.EngineHistory) bool { return hasEvent(h, kind, want) })
}

// waitForBlock waits until the history of session id holds a finished assistant text block with text.
func (f *engineFixture) waitForBlock(t *testing.T, id, text string) {
	t.Helper()
	f.waitForHistory(t, id, func(h store.EngineHistory) bool { return hasBlock(h, text) })
}

// stopSession runs `orrery session stop` for id in dir, waits for the store to record it stopped, and waits for the fake
// claude it started in dir to exit.
func (f *engineFixture) stopSession(t *testing.T, id, dir string) {
	t.Helper()
	wantExit(t, f.orrery(t, "session", "stop", id), 0)
	f.waitForHistory(t, id, func(h store.EngineHistory) bool { return h.State == "stopped" })
	waitGone(t, fakeClaudePID(t, dir))
}

// hasEvent reports whether history holds an event of kind whose payload contains want. The payloads are the store's
// JSON objects, and want is a JSON fragment such as `"requestId":"ask-1"`.
func hasEvent(history store.EngineHistory, kind, want string) bool {
	for _, ev := range history.Events {
		if ev.Kind == kind && strings.Contains(ev.Payload, want) {
			return true
		}
	}
	return false
}

// hasBlock reports whether history holds a finished assistant text block whose text is text.
func hasBlock(history store.EngineHistory, text string) bool {
	for _, ev := range history.Events {
		if ev.Kind != store.EventEngineBlock {
			continue
		}
		var block engineBlock
		if err := json.Unmarshal([]byte(ev.Payload), &block); err != nil {
			continue
		}
		if block.Role == "assistant" && block.Text == text {
			return true
		}
	}
	return false
}

// fakeClaudePID returns the pid the fake claude wrote into dir.
func fakeClaudePID(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "fakeclaude.pid"))
	if err != nil {
		t.Fatalf("read fake claude pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("fake claude pid %q: %v", raw, err)
	}
	return pid
}

// waitGone fails the test unless the process pid has exited within waitLimit. Signal 0 reports a process that no longer
// exists as ESRCH.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for syscall.Kill(pid, 0) != syscall.ESRCH {
		if time.Now().After(deadline) {
			t.Fatalf("fake claude process %d still running", pid)
		}
		time.Sleep(pollEvery)
	}
}

// turn is one message of a conversation rebuilt from stored events.
type turn struct {
	role, text string
}

// TestEngineSessionsOffByDefault proves that without --sessions orreryd creates no session socket, and that
// `orrery session start` names the flag that enables sessions.
func TestEngineSessionsOffByDefault(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, false)

	if _, err := os.Stat(f.sessions); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s = %v, want it absent", f.sessions, err)
	}
	out := f.orrery(t, "session", "start", f.sessionDir(t, "off"), "hello")
	if out.code == 0 {
		t.Fatalf("session start exited 0 with sessions off; stdout %q", out.stdout)
	}
	if !strings.Contains(out.stderr, "--sessions") {
		t.Errorf("session start stderr %q does not name --sessions", out.stderr)
	}
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineSessionsSocketMode proves that the session socket is created beside the daemon socket and is private.
func TestEngineSessionsSocketMode(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)

	info, err := os.Stat(f.sessions)
	if err != nil {
		t.Fatalf("stat %s: %v", f.sessions, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("session socket mode = %o, want 600", got)
	}
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineSessionTurns proves start, say, seen and stop end to end: the started session's reply reaches the store,
// a second prompt gets a second reply, seen is recorded, and stop ends the fake claude.
func TestEngineSessionTurns(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	dir := f.sessionDir(t, "turns")

	id := f.start(t, dir, "hello")
	f.waitForBlock(t, id, "fake reply 1")

	wantExit(t, f.orrery(t, "session", "say", id, "again"), 0)
	f.waitForBlock(t, id, "fake reply 2")

	wantExit(t, f.orrery(t, "session", "seen", id), 0)
	f.waitForEvent(t, id, store.EventEngineChanged, `"seen":`)

	f.stopSession(t, id, dir)
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineSessionAskAnswer proves that a question the fake claude asks is stored as a request and that answer sends
// the chosen option back, which the reply reports.
func TestEngineSessionAskAnswer(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	dir := f.sessionDir(t, "ask")

	id := f.start(t, dir, "ASK")
	f.waitForEvent(t, id, store.EventEngineRequest, `"requestId":"ask-1"`)

	wantExit(t, f.orrery(t, "session", "answer", id, "ask-1", "red"), 0)
	f.waitForBlock(t, id, "fake answered 1")

	f.stopSession(t, id, dir)
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineSessionInterrupt proves that interrupt ends a turn that is still streaming, and the store records the turn
// as ended by the interrupt.
func TestEngineSessionInterrupt(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	dir := f.sessionDir(t, "interrupt")

	id := f.start(t, dir, "HANG")
	f.waitForBlock(t, id, "fake hanging 1")

	wantExit(t, f.orrery(t, "session", "interrupt", id), 0)
	f.waitForEvent(t, id, store.EventEngineTurnEnded, `"terminalReason":"aborted_streaming"`)

	f.stopSession(t, id, dir)
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineSessionCap proves that --session-cap 1 refuses a second live start with an error naming the cap, and that
// the cap frees when the live session stops.
func TestEngineSessionCap(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true, "--session-cap", "1")
	first := f.sessionDir(t, "first")

	id := f.start(t, first, "hello")
	f.waitForBlock(t, id, "fake reply 1")

	refused := f.orrery(t, "session", "start", f.sessionDir(t, "second"), "hello")
	if refused.code == 0 {
		t.Fatalf("second start exited 0 at cap 1; stdout %q", refused.stdout)
	}
	if !strings.Contains(refused.stderr, "cap 1") {
		t.Errorf("second start stderr %q does not name the cap", refused.stderr)
	}

	f.stopSession(t, id, first)
	wantExit(t, f.orrery(t, "session", "start", f.sessionDir(t, "third"), "hello"), 0)
	wantExit(t, daemon.stop(t), 0)
}

// TestEngineSessionReplay proves that the events `orrery session watch` prints for a stopped session rebuild its
// conversation: each prompt is a user turn and each finished assistant text block is an assistant turn.
func TestEngineSessionReplay(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	dir := f.sessionDir(t, "replay")

	id := f.start(t, dir, "hello")
	f.waitForBlock(t, id, "fake reply 1")
	wantExit(t, f.orrery(t, "session", "say", id, "again"), 0)
	f.waitForBlock(t, id, "fake reply 2")
	f.stopSession(t, id, dir)

	watched := f.orrery(t, "session", "watch", id)
	wantExit(t, watched, 0)
	got := replayConversation(t, watched.stdout)
	want := []turn{
		{role: "user", text: "hello"},
		{role: "assistant", text: "fake reply 1"},
		{role: "user", text: "again"},
		{role: "assistant", text: "fake reply 2"},
	}
	if len(got) != len(want) {
		t.Fatalf("replayed %d turns, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("turn %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	wantExit(t, daemon.stop(t), 0)
}

// replayConversation rebuilds the conversation from the JSON lines `orrery session watch` printed.
func replayConversation(t *testing.T, lines string) []turn {
	t.Helper()
	var conversation []turn
	for _, line := range strings.Split(strings.TrimSpace(lines), "\n") {
		var ev struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode watch line %q: %v", line, err)
		}
		switch ev.Kind {
		case store.EventEnginePrompt:
			var prompt struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Payload, &prompt); err != nil {
				t.Fatalf("decode prompt %s: %v", ev.Payload, err)
			}
			conversation = append(conversation, turn{role: "user", text: prompt.Text})
		case store.EventEngineBlock:
			var block engineBlock
			if err := json.Unmarshal(ev.Payload, &block); err != nil {
				t.Fatalf("decode block %s: %v", ev.Payload, err)
			}
			if block.Role == "assistant" && block.Kind == "text" {
				conversation = append(conversation, turn{role: "assistant", text: block.Text})
			}
		}
	}
	return conversation
}

// TestEngineSessionShutdown proves that stopping orreryd stops every live session: its fake claude exits.
func TestEngineSessionShutdown(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	dir := f.sessionDir(t, "shutdown")

	id := f.start(t, dir, "hello")
	f.waitForBlock(t, id, "fake reply 1")
	pid := fakeClaudePID(t, dir)

	wantExit(t, daemon.stop(t), 0)
	waitGone(t, pid)
}
