package panewatch_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/conversation"
	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/panewatch"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Invented session facts. The registry entry binds the pane of a tmux server the test starts, and the transcripts live
// under a home directory of the test's own.
const (
	registryPID  = 1001
	entryName    = "1001.json"
	session      = "synthetic-session"
	otherSession = "synthetic-session-2"
	cwd          = "/synthetic/project"
	noConfig     = "/dev/null"
	idleCommand  = "sleep 300"
)

// The times of the invented transcript lines, in unix seconds. Each line carries the RFC 3339 stamp of its time.
const (
	promptSec = 1
	toolSec   = 2
	resultSec = 3
	sideSec   = 4
	laterSec  = 5
	finalSec  = 6
)

// Invented usage of the usage lines. The first sums to firstTokens and the last to lastTokens, both out of window; the
// output tokens are not counted.
const (
	firstUsage  = `{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":999}`
	lastUsage   = `{"input_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":200,"output_tokens":999}`
	sideUsage   = `{"input_tokens":900000,"cache_creation_input_tokens":0,"cache_read_input_tokens":90000,"output_tokens":9}`
	firstTokens = 60
	lastTokens  = 300
	window      = 1_000_000
)

// fakeProc is a process table of invented processes: a pid is alive when env names it, with that environment.
type fakeProc struct {
	env map[int]map[string]string
}

// Alive reports whether env names pid.
func (p fakeProc) Alive(pid int) bool {
	_, ok := p.env[pid]
	return ok
}

// Environ returns the environment of pid, which fails for a pid env does not name.
func (p fakeProc) Environ(pid int) (map[string]string, error) {
	env, ok := p.env[pid]
	if !ok {
		return nil, fmt.Errorf("no process %d", pid)
	}
	return env, nil
}

// paneServer is a private tmux server with one session, whose pane runs idleCommand.
type paneServer struct {
	client *tmuxctl.Client
	server tmuxctl.Server
	socket string
	pane   string
}

// startPaneServer starts a private tmux server under a unique name, kills it at cleanup, and returns it.
func startPaneServer(t *testing.T) paneServer {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	server := tmuxctl.Server{Name: fmt.Sprintf("orrery-panewatch-test-%d-%x", os.Getpid(), suffix)}
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, server); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1",
		"-c", t.TempDir(), idleCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	pane, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	socket, err := client.Run(t.Context(), "display", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("display socket_path: %v", err)
	}
	return paneServer{client: client, server: server, socket: strings.TrimSpace(socket), pane: strings.TrimSpace(pane)}
}

// rig is one pane under test: its tmux server, a session registry and a home directory of its own, Run over them, and a
// daemon.Serve that takes Run's updates. A test changes the registry and the transcripts, and takes Run's updates with
// await.
type rig struct {
	srv          paneServer
	registry     string
	home         string
	runUpdates   chan daemon.PaneUpdate
	serveUpdates chan daemon.PaneUpdate
	runEnded     chan struct{}
	runErr       error
}

// newRig starts a tmux server with a pane, writes a busy registry entry for session, and starts Run and a daemon.Serve
// over them. The transcript of session holds lines when there are any, and does not exist otherwise.
func newRig(t *testing.T, lines ...string) *rig {
	t.Helper()
	rg := &rig{
		srv:          startPaneServer(t),
		registry:     t.TempDir(),
		home:         t.TempDir(),
		runUpdates:   make(chan daemon.PaneUpdate),
		serveUpdates: make(chan daemon.PaneUpdate),
		runEnded:     make(chan struct{}),
	}
	if err := os.MkdirAll(filepath.Dir(rg.transcriptOf(session)), 0o700); err != nil {
		t.Fatalf("create transcript directory: %v", err)
	}
	if len(lines) > 0 {
		rg.addLines(t, session, lines...)
	}
	rg.writeEntry(t, session, paneregistry.StatusBusy)
	rg.startServe(t)
	rg.startRun(t)
	return rg
}

// transcriptOf returns the path of the transcript of sessionID in the test's home directory.
func (r *rig) transcriptOf(sessionID string) string {
	return paneregistry.TranscriptPath(r.home, cwd, sessionID)
}

// writeEntry publishes the registry entry of the test's process for sessionID with status, the way Claude Code replaces an
// entry: a temporary file is written and renamed over it, so the watch sees one move.
func (r *rig) writeEntry(t *testing.T, sessionID, status string) {
	t.Helper()
	entry := paneregistry.Entry{
		PID:        registryPID,
		SessionID:  sessionID,
		Cwd:        cwd,
		Entrypoint: paneregistry.EntrypointCLI,
		Status:     status,
		Tmux:       "s1:@0." + r.srv.pane,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	tmp := filepath.Join(r.registry, entryName+".tmp")
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", tmp, err)
	}
	if err = os.Rename(tmp, filepath.Join(r.registry, entryName)); err != nil {
		t.Fatalf("rename %s into place: %v", entryName, err)
	}
}

// removeEntry deletes the registry entry of the test's process, which ends its binding.
func (r *rig) removeEntry(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(r.registry, entryName)); err != nil {
		t.Fatalf("remove %s: %v", entryName, err)
	}
}

// addLines appends lines to the transcript of sessionID in one write, which creates the transcript when it does not exist.
func (r *rig) addLines(t *testing.T, sessionID string, lines ...string) {
	t.Helper()
	file, err := os.OpenFile(r.transcriptOf(sessionID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	_, err = file.WriteString(strings.Join(lines, "\n") + "\n")
	closeErr := file.Close()
	if err != nil {
		t.Fatalf("append to transcript: %v", err)
	}
	if closeErr != nil {
		t.Fatalf("close transcript: %v", closeErr)
	}
}

// startServe runs daemon.Serve on the test's tmux server, taking pane updates from the rig, until the test ends.
func (r *rig) startServe(t *testing.T) {
	t.Helper()
	ln, err := daemon.Listen(filepath.Join(t.TempDir(), "orreryd.sock"))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx := t.Context()
	served := make(chan struct{})
	var serveErr error
	go func() {
		serveErr = daemon.Serve(ctx, ln, daemon.Config{Server: r.srv.server, Updates: r.serveUpdates})
		close(served)
	}()
	t.Cleanup(func() {
		<-served
		if serveErr != nil {
			t.Errorf("Serve: %v", serveErr)
		}
	})
}

// startRun runs panewatch.Run over the rig's registry and home directories, with a process table that has the rig's
// session process in the rig's tmux server, until the test ends. The test fails unless Run then returns the context's
// error.
func (r *rig) startRun(t *testing.T) {
	t.Helper()
	procs := fakeProc{env: map[int]map[string]string{
		registryPID: {"TMUX": fmt.Sprintf("%s,1234,0", r.srv.socket)},
	}}
	ctx := t.Context()
	go func() {
		r.runErr = panewatch.Run(ctx, panewatch.Config{
			Dir:     r.registry,
			Home:    r.home,
			Proc:    procs,
			Updates: r.runUpdates,
			Unreadable: func(path string, err error) {
				t.Errorf("unreadable %s: %v", path, err)
			},
		})
		close(r.runEnded)
	}()
	t.Cleanup(func() {
		<-r.runEnded
		if !errors.Is(r.runErr, context.Canceled) {
			t.Errorf("Run returned %v, want the context's error", r.runErr)
		}
	})
}

// await takes Run's updates until one satisfies ok, and returns that update once the daemon has applied it. Each update
// is handed to the daemon as Run sends it. The daemon takes an update only after it has applied the one before, so a
// paneless update, which the daemon drops without a write, is taken only after the matched update is applied.
func (r *rig) await(t *testing.T, ok func(daemon.PaneUpdate) bool) daemon.PaneUpdate {
	t.Helper()
	for {
		select {
		case update := <-r.runUpdates:
			r.serveUpdates <- update
			if ok(update) {
				r.serveUpdates <- daemon.PaneUpdate{}
				return update
			}
		case <-r.runEnded:
			t.Fatalf("Run returned before the update the test waits for: %v", r.runErr)
		}
	}
}

// wantOption checks the option name of the rig's pane: it is value, or unset when value is "".
func (r *rig) wantOption(t *testing.T, name, value string) {
	t.Helper()
	got, ok, err := r.srv.client.PaneOption(t.Context(), r.srv.pane, name)
	if err != nil {
		t.Fatalf("PaneOption(%s): %v", name, err)
	}
	if value == "" {
		if ok {
			t.Fatalf("%s = %q, want unset", name, got)
		}
		return
	}
	if !ok || got != value {
		t.Fatalf("%s = (%q, %v), want (%q, true)", name, got, ok, value)
	}
}

// stamp returns the RFC 3339 stamp of the transcript time sec, in unix seconds.
func stamp(sec int) string {
	return fmt.Sprintf("1970-01-01T00:00:%02dZ", sec)
}

// line returns a transcript line of kind at sec, with the raw JSON members extra before its timestamp and message as
// its message. extra is empty or ends in a comma.
func line(kind string, sec int, extra, message string) string {
	return fmt.Sprintf(`{"type":%q,%s"timestamp":%q,"message":%s}`, kind, extra, stamp(sec), message)
}

// promptLine returns a main-chain prompt at sec.
func promptLine(sec int, text string) string {
	return line("user", sec, "", fmt.Sprintf(`{"role":"user","content":%q}`, text))
}

// toolUseLine returns a main-chain assistant line at sec that calls Bash, with the command "make test", under id.
func toolUseLine(sec int, id string) string {
	return line("assistant", sec, "", fmt.Sprintf(
		`{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":"make test"}}]}`,
		id,
	))
}

// toolResultLine returns a main-chain user line at sec that answers the call id.
func toolResultLine(sec int, id string) string {
	return line("user", sec, "", fmt.Sprintf(
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"ok"}]}`, id))
}

// usageLine returns a main-chain assistant line at sec whose usage is usage.
func usageLine(sec int, usage string) string {
	return line("assistant", sec, "", fmt.Sprintf(
		`{"role":"assistant","content":[{"type":"text","text":"reply"}],"usage":%s}`, usage))
}

// toolJSON is the @orrery_tool option of the Bash call id, which started at sec, for the command "make test".
func toolJSON(id string, sec int) string {
	return fmt.Sprintf(`{"id":%q,"name":"Bash","input":"make test","start":%d}`, id, sec)
}

func TestPaneTranscriptStateToolUntilResult(t *testing.T) {
	rg := newRig(t, promptLine(promptSec, "go"))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.TurnStart == promptSec })

	rg.addLines(t, session, toolUseLine(toolSec, "t1"))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.Tool != nil })
	rg.wantOption(t, protocol.OptionTool, toolJSON("t1", toolSec))

	rg.addLines(t, session, toolResultLine(resultSec, "t1"))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.Tool == nil })
	rg.wantOption(t, protocol.OptionTool, "")
}

func TestPaneTranscriptStateTurnStartNewestPrompt(t *testing.T) {
	rg := newRig(t, promptLine(promptSec, "first"), toolUseLine(toolSec, "t1"))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.TurnStart == promptSec })

	rg.addLines(t, session, promptLine(laterSec, "second"))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.TurnStart == laterSec })
	rg.wantOption(t, protocol.OptionTurnStart, fmt.Sprint(laterSec))
}

func TestPaneTranscriptStateContextFromUsage(t *testing.T) {
	rg := newRig(t, promptLine(promptSec, "go"))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.Context.Tokens == 0 })

	rg.addLines(t, session, usageLine(toolSec, firstUsage))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.Context.Tokens == firstTokens })

	rg.addLines(t, session, usageLine(resultSec, lastUsage))
	update := rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.Context.Tokens == lastTokens })
	want := conversation.Context{Tokens: lastTokens, Window: window}
	if update.Turn.Context != want {
		t.Fatalf("context = %+v, want the usage line's %+v", update.Turn.Context, want)
	}
}

func TestPaneTranscriptStateSidechainAndMetaIgnored(t *testing.T) {
	rg := newRig(t, promptLine(promptSec, "go"), toolUseLine(toolSec, "t1"))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.Tool != nil })

	// The sidechain call, its usage, and the meta prompt would each change the turn if they were read. The main-chain
	// text after them is the line the test waits for.
	rg.addLines(t, session,
		line("assistant", sideSec, `"isSidechain":true,`,
			`{"role":"assistant","content":[{"type":"tool_use","id":"s1","name":"Read","input":{"file_path":"/x"}}]}`),
		line("assistant", sideSec, `"isSidechain":true,`,
			fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":"side"}],"usage":%s}`, sideUsage)),
		line("user", laterSec, `"isMeta":true,`, `{"role":"user","content":"meta"}`),
		line("assistant", finalSec, "", `{"role":"assistant","content":[{"type":"text","text":"after"}]}`),
	)
	update := rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.LastMessage == "after" })
	turn := update.Turn
	if turn.TurnStart != promptSec || turn.Tool == nil || turn.Tool.ID != "t1" || turn.Context.Tokens != 0 {
		t.Fatalf("turn = %+v, want the prompt's turn with t1 running and no context usage", turn)
	}
	rg.wantOption(t, protocol.OptionTool, toolJSON("t1", toolSec))
	rg.wantOption(t, protocol.OptionTurnStart, fmt.Sprint(promptSec))
}

func TestPaneTranscriptStateWaitsForCreation(t *testing.T) {
	rg := newRig(t)
	// The first update is the registry binding. The transcript does not exist yet, so it shows no turn.
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.TurnStart == 0 && update.Turn.Tool == nil })

	rg.addLines(t, session, promptLine(promptSec, "go"), toolUseLine(toolSec, "t1"))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.Tool != nil })
	rg.wantOption(t, protocol.OptionTool, toolJSON("t1", toolSec))
}

func TestPaneTranscriptStateRebindAndUnbind(t *testing.T) {
	rg := newRig(t, promptLine(promptSec, "go"), toolUseLine(toolSec, "t1"))
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.Tool != nil })

	rg.addLines(t, otherSession, promptLine(promptSec, "next"), toolUseLine(resultSec, "t2"))
	rg.writeEntry(t, otherSession, paneregistry.StatusBusy)
	update := rg.await(t, func(update daemon.PaneUpdate) bool { return update.Turn.Tool != nil })
	if update.Transcript != rg.transcriptOf(otherSession) || update.Turn.Tool.ID != "t2" {
		t.Fatalf(
			"rebound update = %s with tool %+v, want the other transcript's t2",
			update.Transcript,
			update.Turn.Tool,
		)
	}
	rg.wantOption(t, protocol.OptionTool, toolJSON("t2", resultSec))

	rg.removeEntry(t)
	rg.await(t, func(update daemon.PaneUpdate) bool { return update.Unbound })
	rg.wantOption(t, protocol.OptionTool, "")
	rg.wantOption(t, protocol.OptionState, "")
}
