package integration_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// stallPrompt makes the fake claude stream the start of a text block and never finish it.
const stallPrompt = "STALL"

// stoppedDuringTurnBody is the body of the notice a turn running when orreryd stops is sent with.
const stoppedDuringTurnBody = "orreryd stopped during the turn"

// restartedDuringTurnReason is the reason a turn running when orreryd was killed is recorded with.
const restartedDuringTurnReason = "orreryd restarted during the turn"

// stateOf returns the stored state of session id.
func (f *engineFixture) stateOf(t *testing.T, id string) string {
	t.Helper()
	history, err := f.reader.EngineHistory(t.Context(), id, 0, engineHistoryLimit)
	if err != nil {
		t.Fatalf("read history of %s: %v", id, err)
	}
	return history.State
}

// waitForUnfinishedText waits until the store holds unfinished text for session id.
func (f *engineFixture) waitForUnfinishedText(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for engineTextRows(t, f.dbPath, id) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("session %s never stored unfinished text", id)
		}
		time.Sleep(pollEvery)
	}
}

// engineTextRows counts the unfinished text rows the store holds for session id.
func engineTextRows(t *testing.T, dbPath, id string) int {
	t.Helper()
	db := openStoreForInspection(t, dbPath)
	var n int
	const query = "SELECT COUNT(*) FROM engine_text WHERE session_id = ?"
	if err := db.QueryRowContext(t.Context(), query, id).Scan(&n); err != nil {
		t.Fatalf("count engine text of %s: %v", id, err)
	}
	return n
}

// sessionReason returns the reason stored for session id.
func sessionReason(t *testing.T, dbPath, id string) string {
	t.Helper()
	db := openStoreForInspection(t, dbPath)
	var reason string
	const query = "SELECT reason FROM sessions WHERE id = ?"
	if err := db.QueryRowContext(t.Context(), query, id).Scan(&reason); err != nil {
		t.Fatalf("read reason of %s: %v", id, err)
	}
	return reason
}

// TestEngineSigtermInterruptsStalledTurn proves that stopping orreryd during a turn ends its fake claude, records the
// session interrupted with no unfinished text, and posts the interruption to the desk stream. After a restart the
// session is still there, and say resumes it by its id.
func TestEngineSigtermInterruptsStalledTurn(t *testing.T) {
	f := newEngineFixture(t)

	daemon := f.startOrreryd(t, true)
	notices := startNoticeReader(t, f.env, f.socket)
	dir := f.sessionDir(t, "sigterm")
	id := f.start(t, dir, stallPrompt)
	f.waitForUnfinishedText(t, id)
	pid := fakeClaudePID(t, dir)

	wantExit(t, daemon.stop(t), 0)
	waitGone(t, pid)
	if got := f.stateOf(t, id); got != "interrupted" {
		t.Fatalf("state of %s = %q, want interrupted", id, got)
	}
	if n := engineTextRows(t, f.dbPath, id); n != 0 {
		t.Fatalf("session %s keeps %d unfinished text rows", id, n)
	}
	got := notices.finish(t)
	want := notice{
		state: "interrupted",
		title: deskstream.SessionTitle(filepath.Base(dir), id),
		body:  stoppedDuringTurnBody,
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("notices after SIGTERM %+v, want exactly %+v", got, want)
	}

	restarted := f.startOrreryd(t, true)
	if got := f.stateOf(t, id); got != "interrupted" {
		t.Fatalf("state of %s after restart = %q, want interrupted", id, got)
	}
	wantExit(t, f.orrery(t, "session", "say", id, "again"), 0)
	f.waitForBlock(t, id, "fake reply 2")
	args, err := os.ReadFile(filepath.Join(dir, "fakeclaude.args"))
	if err != nil {
		t.Fatalf("read fake claude args: %v", err)
	}
	if !strings.Contains(string(args), "--resume session_1") {
		t.Fatalf("fake claude args %q do not resume session_1", args)
	}

	f.stopSession(t, id, dir)
	wantExit(t, restarted.stop(t), 0)
}

// TestEngineSigkillReconcilesOnRestart proves that a restart after orreryd is killed mid-turn settles the session: its
// row is recorded interrupted with the restart reason and no unfinished text.
func TestEngineSigkillReconcilesOnRestart(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	dir := f.sessionDir(t, "sigkill")
	id := f.start(t, dir, stallPrompt)
	f.waitForUnfinishedText(t, id)
	pid := fakeClaudePID(t, dir)

	if err := daemon.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill orreryd: %v", err)
	}
	daemon.exitOutcome(t)
	// The orphaned fake may already have exited at its stdin's EOF, so a kill that finds no process is not an error.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("kill orphaned fake claude %d: %v", pid, err)
	}
	waitGone(t, pid)

	restarted := f.startOrreryd(t, true)
	if got := f.stateOf(t, id); got != "interrupted" {
		t.Fatalf("state of %s after restart = %q, want interrupted", id, got)
	}
	if n := engineTextRows(t, f.dbPath, id); n != 0 {
		t.Fatalf("session %s keeps %d unfinished text rows after restart", id, n)
	}
	if got := sessionReason(t, f.dbPath, id); got != restartedDuringTurnReason {
		t.Fatalf("reason of %s = %q, want %q", id, got, restartedDuringTurnReason)
	}
	wantExit(t, restarted.stop(t), 0)
}

// TestEngineKilledSessionFailsWhileOthersReply proves that killing one session's fake claude marks that session failed
// with a reason, while another session still replies and a pane hook still updates its pane.
func TestEngineKilledSessionFailsWhileOthersReply(t *testing.T) {
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	dirA := f.sessionDir(t, "killed")
	dirB := f.sessionDir(t, "survivor")

	idA := f.start(t, dirA, "hello")
	f.waitForBlock(t, idA, "fake reply 1")
	idB := f.start(t, dirB, "hello")
	f.waitForBlock(t, idB, "fake reply 1")

	pidA := fakeClaudePID(t, dirA)
	if err := syscall.Kill(pidA, syscall.SIGKILL); err != nil {
		t.Fatalf("kill fake claude %d: %v", pidA, err)
	}
	waitGone(t, pidA)
	f.waitForHistory(t, idA, func(h store.EngineHistory) bool { return h.State == "failed" })
	if reason := sessionReason(t, f.dbPath, idA); reason == "" {
		t.Fatalf("failed session %s has no reason", idA)
	}

	wantExit(t, f.orrery(t, "session", "say", idB, "again"), 0)
	f.waitForBlock(t, idB, "fake reply 2")

	client, err := tmuxctl.New(tmuxctl.Server{Name: f.server})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", f.server, err)
	}
	pane := fixturePane(t, client)
	pulseDir := f.sessionDir(t, "pulse")
	transcript := filepath.Join(pulseDir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)
	q := sqlcgen.New(openStoreForInspection(t, f.dbPath))
	hook := map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      pulseSession,
		"transcript_path": transcript,
	}
	sendHook(t, f.socket, pane, pulseDir, hook)
	waitForPaneState(t, q, client, pane, protocol.StateWorking)

	f.stopSession(t, idB, dirB)
	wantExit(t, daemon.stop(t), 0)
}
