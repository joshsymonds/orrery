package integration_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/deskstream"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// stallPrompt makes the fake claude stream the start of a text block and never finish it.
const stallPrompt = "STALL"

// stoppedDuringTurnBody is the body of the notice a turn running when orreryd stops is sent with.
const stoppedDuringTurnBody = "orreryd stopped during the turn"

// stateOf returns the stored state of session id.
func (f *engineFixture) stateOf(t *testing.T, id string) string {
	t.Helper()
	history, err := f.reader.EngineHistory(t.Context(), id, 0, engineHistoryLimit)
	if err != nil {
		t.Fatalf("read history of %s: %v", id, err)
	}
	return history.State
}

// waitForUnfinishedText waits until the store holds unfinished text for session id. The change stream sends a message
// after each write that changes a session's unfinished text commits, so the wait blocks on the next message and then
// counts the rows again.
func (f *engineFixture) waitForUnfinishedText(t *testing.T, id string) {
	t.Helper()
	changes, endChanges := f.openChanges(t)
	defer endChanges()
	for engineTextRows(t, f.dbPath, id) == 0 {
		stopIfFailed(t)
		if !changes.Receive() {
			t.Fatalf("change stream ended before session %s had unfinished text: %v", id, changes.Err())
		}
	}
}

// openChanges subscribes to the change stream on the session socket and returns once the stream has opened, so every
// change after that reaches the stream. The caller ends the stream with the returned func when its wait is over: an
// open stream holds orreryd's graceful shutdown until the shutdown's timeout, and the test's cleanup ends it too.
func (f *engineFixture) openChanges(
	t *testing.T,
) (*connect.ServerStreamForClient[enginev1.ChangesWatchResponse], context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	client := enginev1connect.NewChangesClient(f.client, cli.SessionBaseURL)
	stream, err := client.Watch(ctx, connect.NewRequest(&enginev1.ChangesWatchRequest{}))
	if err != nil {
		t.Fatalf("watch changes: %v", err)
	}
	if !stream.Receive() {
		t.Fatalf("open change stream: %v", stream.Err())
	}
	if !stream.Msg().GetOpened() {
		t.Fatalf("first change message %v, want the opened message", stream.Msg())
	}
	return stream, cancel
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
	t.Parallel()
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

// TestEngineSigkillReconcilesOnRestart proves that a restart after orreryd is killed mid-turn settles the session. The
// fake is killed while orreryd is down, so its holder ends with an exit record of that death, and the restart drains it
// into the row: failed with the signal, and no unfinished text.
func TestEngineSigkillReconcilesOnRestart(t *testing.T) {
	t.Parallel()
	f := newEngineFixture(t)
	daemon := f.startOrreryd(t, true)
	dir := f.sessionDir(t, "sigkill")
	id := f.start(t, dir, stallPrompt)
	f.waitForUnfinishedText(t, id)
	pid := fakeClaudePID(t, dir)

	killOrreryd(t, daemon)
	// The orphaned fake may already have exited at its stdin's EOF, so a kill that finds no process is not an error.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("kill orphaned fake claude %d: %v", pid, err)
	}
	waitGone(t, pid)
	// The restart must find the holder ended, with its exit record written and its lock free. A holder that still runs
	// when orreryd starts is re-adopted as live, and the row stays working until its end is streamed.
	waitHolderEnded(t, filepath.Join(filepath.Dir(f.dbPath), "holders", id))

	restarted := f.startOrreryd(t, true)
	if got := f.stateOf(t, id); got != "failed" {
		t.Fatalf("state of %s after restart = %q, want failed", id, got)
	}
	if n := engineTextRows(t, f.dbPath, id); n != 0 {
		t.Fatalf("session %s keeps %d unfinished text rows after restart", id, n)
	}
	if got := sessionReason(t, f.dbPath, id); !strings.HasPrefix(got, "signal: killed") {
		t.Fatalf("reason of %s = %q, want the drained exit, signal: killed", id, got)
	}
	wantExit(t, restarted.stop(t), 0)
}

// TestEngineKilledSessionFailsWhileOthersReply proves that killing one session's fake claude marks that session failed
// with a reason, while another session still replies and a registry update still moves its pane.
func TestEngineKilledSessionFailsWhileOthersReply(t *testing.T) {
	t.Parallel()
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
	pulse := startClaudeSession(t, client, filepath.Join(f.root, "home"), pane, pulseDir)
	q := sqlcgen.New(openStoreForInspection(t, f.dbPath))
	pulse.submitPrompt(t)
	waitForPaneState(t, q, client, pane, "", protocol.StateWorking)

	f.stopSession(t, idB, dirB)
	wantExit(t, daemon.stop(t), 0)
}
