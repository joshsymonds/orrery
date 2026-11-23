package main

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// remoteFakeClaude is the scripted claude that the summary tests run. The controller must run none for a remote pane.
const remoteFakeClaude = "../../internal/summary/testdata/fake-claude.sh"

// recordingClaude copies the fake claude into a fresh directory as claude, and returns the path of the copy. Each run
// it starts is recorded under calls beside it.
func recordingClaude(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(remoteFakeClaude)
	if err != nil {
		t.Fatalf("read fake claude: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "claude")
	exectest.WriteFile(t, bin, src, 0o755)
	return bin
}

// claudeRuns returns how many runs the fake beside bin has started.
func claudeRuns(t *testing.T, bin string) int {
	t.Helper()
	runs, err := filepath.Glob(filepath.Join(filepath.Dir(bin), "calls", "*"))
	if err != nil {
		t.Fatalf("glob calls: %v", err)
	}
	return len(runs)
}

// tmuxCallCounter puts a tmux shim first on PATH. Each tmux command run through it appends one line to the record it
// returns, and then fails, so the controller's tmux calls are counted and none of them reaches a server.
func tmuxCallCounter(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho call >> " + record + "\nexit 1\n"
	exectest.WriteFile(t, filepath.Join(dir, "tmux"), []byte(script), 0o700)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

// tmuxCalls returns how many tmux commands the shim that wrote record has run.
func tmuxCalls(t *testing.T, record string) int {
	t.Helper()
	data, err := os.ReadFile(record)
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read tmux record: %v", err)
	}
	return strings.Count(string(data), "\n")
}

// remoteDoneDesk is the desk notice of the done pane %3 on host, as its snapshot builds it.
func remoteDoneDesk(host string) deskstream.Notice {
	return deskstream.Notice{
		State: string(protocol.StateDone),
		Title: deskstream.PaneTitle(noticeHostProject, "synthetic-session-"+host, 2),
		Body:  "synthetic summary of " + host,
	}
}

// TestPaneSummariesNotOnController checks that a remote pane's done transition, routed through the controller's remote
// path, starts nothing on the controller: it creates no summarizer, runs no claude and calls no tmux. Its transcript
// exists and its claude is set, so a summary the controller did start would run claude. The done notice still routes,
// from the snapshot the host sent.
func TestPaneSummariesNotOnController(t *testing.T) {
	sinks := newPaneSinks(noticeHostStart, true)
	sender := &recordSender{}
	notices := notice.New(noticeHostController, sender, sinks.Now, slog.New(slog.DiscardHandler))
	hub := newNoticeHostRelay(t, sinks, notices)
	hub.summarizers = make(map[tmuxctl.Server]*summary.Summarizer)
	bin := recordingClaude(t)
	hub.claude = bin
	tmuxRecord := tmuxCallCounter(t)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user","message":{"role":"user","content":"x"}}`+"\n"),
		0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	event := remoteEvent(noticeHostA, protocol.StateWorking, protocol.StateDone)
	event.Transition.Transcript = transcript
	hub.observeRemote(t.Context(), noticeHostA, event)
	hub.router.Wait()

	wantDesk(t, sinks, remoteDoneDesk(noticeHostA))
	if n := len(hub.summarizers); n != 0 {
		t.Errorf("summarizers created for a remote pane = %d, want 0", n)
	}
	if runs := claudeRuns(t, bin); runs != 0 {
		t.Errorf("claude runs for a remote pane = %d, want 0", runs)
	}
	if calls := tmuxCalls(t, tmuxRecord); calls != 0 {
		t.Errorf("controller tmux calls for a remote pane = %d, want 0", calls)
	}
}
