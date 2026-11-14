package integration_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// The tests in this file kill orreryd with SIGKILL while a turn's lines are not yet stored, or while a prompt is sent or
// its write is in flight, then restart it on the same store and state directory. Each line and each prompt must reach
// the store and the fake claude exactly once, and each turn must complete.

// TestEngineHolderScopeSigkillReplaysFloodOnce proves that a FLOOD turn whose lines the store has not all taken when
// orreryd is killed ends after the restart with each of its 50 blocks stored once, in order, and the turn done.
func TestEngineHolderScopeSigkillReplaysFloodOnce(t *testing.T) {
	f, scope, _ := scopedFixture(t)
	daemon := f.startOrreryd(t, true, "--holder-scope", scope)
	stateDir := filepath.Dir(f.dbPath)
	dir := f.sessionDir(t, "readopt-flood")
	id := f.start(t, dir, "hello")
	holderDir := filepath.Join(stateDir, "holders", id)
	t.Cleanup(func() { clearHolder(t, holderDir) })
	f.waitForBlock(t, id, "fake reply 1")
	f.waitForHistory(t, id, turnDone)

	// The fake numbers its replies by prompt, and FLOOD is the second prompt of the session.
	say, release := startUnstoredTurn(t, f, id, holderDir, "FLOOD", "fake flood 2.", "fake flood 2.50")
	killOrreryd(t, daemon)
	say.exitOutcome(t)
	release()

	restarted := f.startOrreryd(t, true, "--holder-scope", scope)
	f.waitForHistory(t, id, turnDone)
	var want []string
	for k := 1; k <= 50; k++ {
		want = append(want, fmt.Sprintf("fake flood 2.%d", k))
	}
	if got := assistantTexts(t, f.historyOf(t, id), "fake flood 2."); !slices.Equal(got, want) {
		t.Fatalf("flood blocks of %s = %q, want each of the 50 once, in order", id, got)
	}
	if n := promptsWritten(t, dir, "FLOOD"); n != 1 {
		t.Fatalf("fake claude read the FLOOD prompt %d times, want 1", n)
	}
	f.stopScoped(t, id, dir)
	wantExit(t, restarted.stop(t), 0)
}

// TestEngineHolderScopeSigkillReplaysPairOnce proves that a PAIR turn killed while its two-block line is unstored ends
// after the restart with both blocks of that line stored once, and the turn done.
func TestEngineHolderScopeSigkillReplaysPairOnce(t *testing.T) {
	f, scope, _ := scopedFixture(t)
	daemon := f.startOrreryd(t, true, "--holder-scope", scope)
	stateDir := filepath.Dir(f.dbPath)
	dir := f.sessionDir(t, "readopt-pair")
	id := f.start(t, dir, "hello")
	holderDir := filepath.Join(stateDir, "holders", id)
	t.Cleanup(func() { clearHolder(t, holderDir) })
	f.waitForBlock(t, id, "fake reply 1")
	f.waitForHistory(t, id, turnDone)

	say, release := startUnstoredTurn(t, f, id, holderDir, "PAIR", "fake pair 2.", "fake pair 2.2")
	killOrreryd(t, daemon)
	say.exitOutcome(t)
	release()

	restarted := f.startOrreryd(t, true, "--holder-scope", scope)
	f.waitForHistory(t, id, turnDone)
	want := []string{"fake pair 2.1", "fake pair 2.2"}
	if got := assistantTexts(t, f.historyOf(t, id), "fake pair 2."); !slices.Equal(got, want) {
		t.Fatalf("pair blocks of %s = %q, want %q each once", id, got, want)
	}
	if n := promptsWritten(t, dir, "PAIR"); n != 1 {
		t.Fatalf("fake claude read the PAIR prompt %d times, want 1", n)
	}
	f.stopScoped(t, id, dir)
	wantExit(t, restarted.stop(t), 0)
}

// TestEngineHolderScopeSigkillKeepsSentPromptOnce proves that a prompt sent with session say just before orreryd is
// killed, stored and written to the holder, reaches the fake claude once after the restart, and its turn completes once.
func TestEngineHolderScopeSigkillKeepsSentPromptOnce(t *testing.T) {
	f, scope, _ := scopedFixture(t)
	daemon := f.startOrreryd(t, true, "--holder-scope", scope)
	stateDir := filepath.Dir(f.dbPath)
	dir := f.sessionDir(t, "readopt-say")
	id := f.start(t, dir, "hello")
	t.Cleanup(func() { clearHolder(t, filepath.Join(stateDir, "holders", id)) })
	f.waitForBlock(t, id, "fake reply 1")
	f.waitForHistory(t, id, turnDone)

	wantExit(t, f.orrery(t, "session", "say", id, "again"), 0)
	killOrreryd(t, daemon)
	restarted := f.startOrreryd(t, true, "--holder-scope", scope)

	f.waitForHistory(t, id, turnDone)
	want := []string{"fake reply 1", "fake reply 2"}
	if got := assistantTexts(t, f.historyOf(t, id), "fake reply "); !slices.Equal(got, want) {
		t.Fatalf("replies of %s = %q, want %q each once", id, got, want)
	}
	if n := promptsWritten(t, dir, "again"); n != 1 {
		t.Fatalf("fake claude read the prompt %d times, want 1", n)
	}
	f.stopScoped(t, id, dir)
	wantExit(t, restarted.stop(t), 0)
}

// TestEngineHolderScopeSigkillKeepsInFlightPromptOnce proves that a prompt whose write is in flight when orreryd is
// killed reaches the fake claude once after the restart, and that each turn completes once. The fake reads its stdin
// only between turns, so a prompt sent during a TRICKLE turn and larger than the pipe blocks in the holder: its write id
// is recorded and the write is part-way when orreryd dies.
func TestEngineHolderScopeSigkillKeepsInFlightPromptOnce(t *testing.T) {
	f, scope, _ := scopedFixture(t)
	daemon := f.startOrreryd(t, true, "--holder-scope", scope)
	stateDir := filepath.Dir(f.dbPath)
	dir := f.sessionDir(t, "readopt-inflight")
	id := f.start(t, dir, "hello")
	t.Cleanup(func() { clearHolder(t, filepath.Join(stateDir, "holders", id)) })
	f.waitForBlock(t, id, "fake reply 1")
	f.waitForHistory(t, id, turnDone)
	pid := fakeClaudePID(t, dir)

	// The fake numbers its replies by prompt, and TRICKLE is the second prompt of the session. The prompt after it is
	// 96 KiB, under the 128 KiB a single exec argument may hold and over the 64 KiB a Linux pipe holds.
	wantExit(t, f.orrery(t, "session", "say", id, "TRICKLE"), 0)
	f.waitForBlock(t, id, "fake trickle 2.1")
	say := startBinary(t, "orrery", f.env, "session", "say", id, "BIG "+strings.Repeat("x", 96<<10))
	waitUntil(t, "the stdin pipe of the fake claude full", func() bool { return !stdinWritable(t, pid) })
	killOrreryd(t, daemon)

	// The say retries its cut call with the same prompt id until the restarted orreryd answers it.
	restarted := f.startOrreryd(t, true, "--holder-scope", scope)
	wantExit(t, say.exitOutcome(t), 0)
	f.waitForBlock(t, id, "fake reply 3")
	f.waitForHistory(t, id, turnDone)
	var wantTrickle []string
	for k := 1; k <= 60; k++ {
		wantTrickle = append(wantTrickle, fmt.Sprintf("fake trickle 2.%d", k))
	}
	history := f.historyOf(t, id)
	if got := assistantTexts(t, history, "fake trickle 2."); !slices.Equal(got, wantTrickle) {
		t.Fatalf("trickle blocks of %s = %q, want each of the 60 once, in order", id, got)
	}
	if got := assistantTexts(t, history, "fake reply "); !slices.Equal(got, []string{"fake reply 1", "fake reply 3"}) {
		t.Fatalf("replies of %s = %q, want fake reply 1 and fake reply 3 once each", id, got)
	}
	if n := completedTurns(history); n != 3 {
		t.Fatalf("completed turns of %s = %d, want 3", id, n)
	}
	for _, marker := range []string{"TRICKLE", "BIG"} {
		if n := promptsWritten(t, dir, marker); n != 1 {
			t.Fatalf("fake claude read the %s prompt %d times, want 1", marker, n)
		}
	}
	f.stopScoped(t, id, dir)
	wantExit(t, restarted.stop(t), 0)
}

// startUnstoredTurn sends prompt to session id with its holder paused and the store write-locked, so the lines the turn
// writes reach the holder's spool but not the store. The holder resumes once the lock is held, and the call returns when
// the holder's spool holds marker. It fails unless the store holds no block whose text starts with prefix, the turn's
// blocks, at that point. It returns the say process and the function that releases the lock. The release is not
// registered at cleanup: the lock's release needs the test's context, which ends before cleanups run.
func startUnstoredTurn(
	t *testing.T, f *engineFixture, id, holderDir, prompt, prefix, marker string,
) (*orreryProcess, func()) {
	t.Helper()
	resume := pauseHolder(t, holderPID(t, holderDir))
	say := startBinary(t, "orrery", f.env, "session", "say", id, prompt)
	f.waitForEvent(t, id, store.EventEnginePrompt, fmt.Sprintf(`"text":"%s"`, prompt))
	release := holdWriteLock(t, f.dbPath)
	resume()
	waitForOutput(t, holderDir, marker)
	if got := assistantTexts(t, f.historyOf(t, id), prefix); len(got) != 0 {
		t.Fatalf("store holds %q of the %s turn before the kill, want none", got, prompt)
	}
	return say, release
}

// holderPID returns the pid of the holder that runs a session, from the pids record in its holder directory.
func holderPID(t *testing.T, holderDir string) int {
	t.Helper()
	pids, err := holder.ReadPids(holderDir)
	if err != nil {
		t.Fatalf("read pids of holder %s: %v", holderDir, err)
	}
	return pids.Holder
}

// pauseHolder stops the process pid with SIGSTOP, waits until /proc shows it stopped, and returns the function that
// continues it. The function also runs at cleanup, so a test that fails first does not leave the holder stopped.
func pauseHolder(t *testing.T, pid int) func() {
	t.Helper()
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop holder %d: %v", pid, err)
	}
	waitUntil(t, fmt.Sprintf("holder %d stopped", pid), func() bool { return procState(t, pid) == "T" })
	var once sync.Once
	resume := func() {
		once.Do(func() {
			if err := syscall.Kill(pid, syscall.SIGCONT); err != nil && !errors.Is(err, syscall.ESRCH) {
				t.Errorf("continue holder %d: %v", pid, err)
			}
		})
	}
	t.Cleanup(resume)
	return resume
}

// procState returns the state letter /proc gives pid, or "" when /proc holds no stat for it.
func procState(t *testing.T, pid int) string {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read stat of pid %d: %v", pid, err)
	}
	_, after, found := strings.Cut(string(raw), ") ")
	fields := strings.Fields(after)
	if !found || len(fields) == 0 {
		t.Fatalf("stat of pid %d has no state: %q", pid, raw)
	}
	return fields[0]
}

// stdinWritable reports whether the pipe that feeds the fake claude pid its stdin has room for a write. It polls the
// pipe's write end, which /proc opens for it, and writes nothing, so the pipe keeps its data.
func stdinWritable(t *testing.T, pid int) bool {
	t.Helper()
	fd, err := unix.Open(fmt.Sprintf("/proc/%d/fd/0", pid), unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open stdin of fake claude %d: %v", pid, err)
	}
	defer unix.Close(fd)
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
	if _, err = unix.Poll(fds, 0); err != nil {
		t.Fatalf("poll stdin of fake claude %d: %v", pid, err)
	}
	if fds[0].Revents&unix.POLLERR != 0 {
		t.Fatalf("stdin of fake claude %d has no reader", pid)
	}
	return fds[0].Revents&unix.POLLOUT != 0
}

// waitProcessExited waits until /proc shows pid as ended: no stat, or a zombie that has ended and is not yet reaped.
func waitProcessExited(t *testing.T, pid int) {
	t.Helper()
	waitUntil(t, fmt.Sprintf("process %d exited", pid), func() bool {
		state := procState(t, pid)
		return state == "" || state == "Z" || state == "X"
	})
}

// holderOutput returns the stdout that the holder in holderDir spools, in order. A segment the holder deletes while it is
// read is skipped: the holder deletes only segments below a replay-from the store has passed.
func holderOutput(t *testing.T, holderDir string) string {
	t.Helper()
	entries, err := os.ReadDir(holderDir)
	if err != nil {
		t.Fatalf("read holder dir %s: %v", holderDir, err)
	}
	var segments []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "stdout-") {
			segments = append(segments, entry.Name())
		}
	}
	// Segment names carry a fixed-width offset, so their lexical order is their stream order.
	slices.Sort(segments)
	var out strings.Builder
	for _, name := range segments {
		raw, err := os.ReadFile(filepath.Join(holderDir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read spool segment %s: %v", name, err)
		}
		out.Write(raw)
	}
	return out.String()
}

// waitForOutput waits until the holder in holderDir has spooled marker from the fake claude's stdout.
func waitForOutput(t *testing.T, holderDir, marker string) {
	t.Helper()
	waitUntil(t, fmt.Sprintf("holder output %q", marker), func() bool {
		return strings.Contains(holderOutput(t, holderDir), marker)
	})
}

// promptsWritten counts the lines of the fake claude's stdin log in dir that hold marker: one for each prompt it read.
func promptsWritten(t *testing.T, dir, marker string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
	if err != nil {
		t.Fatalf("read fake claude stdin log: %v", err)
	}
	n := 0
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.Contains(line, marker) {
			n++
		}
	}
	return n
}

// assistantTexts returns the text of each finished assistant text block in history whose text starts with prefix, in
// stored order.
func assistantTexts(t *testing.T, history store.EngineHistory, prefix string) []string {
	t.Helper()
	var texts []string
	for _, ev := range history.Events {
		if ev.Kind != store.EventEngineBlock {
			continue
		}
		var block engineBlock
		if err := json.Unmarshal([]byte(ev.Payload), &block); err != nil {
			t.Fatalf("decode block %s: %v", ev.Payload, err)
		}
		if block.Role == "assistant" && block.Kind == "text" && strings.HasPrefix(block.Text, prefix) {
			texts = append(texts, block.Text)
		}
	}
	return texts
}

// completedTurns counts the turns history records as completed: one engine_turn_ended event for each turn whose result
// reported the completed terminal reason.
func completedTurns(history store.EngineHistory) int {
	n := 0
	for _, ev := range history.Events {
		if ev.Kind == store.EventEngineTurnEnded && strings.Contains(ev.Payload, `"terminalReason":"completed"`) {
			n++
		}
	}
	return n
}

// unfinishedIs reports whether history holds exactly one unfinished text, and that text is want.
func unfinishedIs(history store.EngineHistory, want string) bool {
	return len(history.Unfinished) == 1 && history.Unfinished[0].Text == want
}

// turnDone reports whether the stored state of a session is done, the state a finished turn leaves it in.
func turnDone(history store.EngineHistory) bool {
	return history.State == "done"
}

// waitForUnfinished waits until the stored unfinished text of session id is exactly want.
func (f *engineFixture) waitForUnfinished(t *testing.T, id, want string) {
	t.Helper()
	f.waitForHistory(t, id, func(h store.EngineHistory) bool { return unfinishedIs(h, want) })
}

// historyOf reads the stored history of session id once, failing the test when the read fails.
func (f *engineFixture) historyOf(t *testing.T, id string) store.EngineHistory {
	t.Helper()
	history, err := f.reader.EngineHistory(t.Context(), id, 0, engineHistoryLimit)
	if err != nil {
		t.Fatalf("read history of %s: %v", id, err)
	}
	return history
}

// stopScoped asks session id to stop, waits for the store to record it stopped, and waits until /proc shows the fake
// claude it started in dir has ended.
func (f *engineFixture) stopScoped(t *testing.T, id, dir string) {
	t.Helper()
	wantExit(t, f.orrery(t, "session", "stop", id), 0)
	f.waitForHistory(t, id, func(h store.EngineHistory) bool { return h.State == "stopped" })
	waitProcessExited(t, fakeClaudePID(t, dir))
}
