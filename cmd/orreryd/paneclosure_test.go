package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/closurejudge"
	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Synthetic values only; no test reaches the real claude binary, tmux or a store file.
const (
	closurePaneID    = "example-pane-id"
	closureChanged   = "1700000123"
	closureChangedAt = int64(1700000123)
	closureNow       = int64(1700000500)
	closurePrompt    = "example prompt"
	closureEarlier   = "example earlier reply"
	closureFinal     = "example final message"
	closureReply     = `{"closed":true,"why":"example closed reason"}`
	closureWhy       = "example closed reason"
)

// errClosureCall is the failure a fake closure caller returns.
var errClosureCall = errors.New("example closure caller failure")

// closureCall is what one fake CallVia call recorded.
type closureCall struct {
	baseURL string
	env     map[string]string
	input   string
}

// closureCaller is a closurejudge.Caller that records its calls and returns a canned reply and error.
type closureCaller struct {
	reply json.RawMessage
	err   error
	calls []closureCall
}

func (c *closureCaller) CallVia(
	_ context.Context, baseURL, _ string, env map[string]string, _, input string,
) (json.RawMessage, error) {
	c.calls = append(c.calls, closureCall{baseURL: baseURL, env: env, input: input})
	return c.reply, c.err
}

// closureWrite is what the fake verdict writer recorded for one write.
type closureWrite struct {
	verdict store.ClosureVerdict
	at      time.Time
}

// closureWriter is a verdict writer that records each write.
type closureWriter struct {
	writes []closureWrite
}

func (w *closureWriter) SetClosureVerdict(_ context.Context, verdict store.ClosureVerdict, at time.Time) error {
	w.writes = append(w.writes, closureWrite{verdict: verdict, at: at})
	return nil
}

// closureKeys returns a pane-key read that answers id and changed, and err.
func closureKeys(id, changed string, err error) paneKeyReader {
	return func(context.Context, tmuxctl.Server, string) (string, string, error) {
		return id, changed, err
	}
}

// closureRelay returns a relay that judges through caller, reads each pane's key through keys, reads the stamps under
// home and writes its verdicts to writer. A nil writer leaves the relay with no store.
func closureRelay(home string, caller closurejudge.Caller, keys paneKeyReader, writer verdictStore) *relay {
	r := &relay{
		home:     home,
		logger:   slog.New(slog.DiscardHandler),
		closures: closurejudge.New(caller, nil),
		paneKeys: keys,
		now:      func() time.Time { return time.Unix(closureNow, 0) },
	}
	if writer != nil {
		r.verdicts.Store(&writer)
	}
	return r
}

// closureTranscript writes a transcript holding one earlier exchange under dir, and returns its path.
func closureTranscript(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := `{"type":"user","message":{"content":"` + closurePrompt + `"}}` + "\n" +
		`{"type":"assistant","message":{"content":"` + closureEarlier + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// closureTransition is a transition of a pane to state in cwd through the base URL frameURL, whose turn's transcript
// is transcript and whose last assistant message is the final message.
func closureTransition(t *testing.T, state protocol.State, cwd, frameURL, transcript string) daemon.Transition {
	t.Helper()
	return daemon.Transition{
		PaneID:      closurePaneID,
		To:          state,
		Cwd:         cwd,
		BaseURL:     frameURL,
		Transcript:  transcript,
		LastMessage: closureFinal,
	}
}

// closurePlainCwd makes a cwd under home that no context stamp covers, and returns it.
func closurePlainCwd(t *testing.T, home string) string {
	t.Helper()
	cwd := filepath.Join(home, "work", "plain")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatalf("make cwd: %v", err)
	}
	return cwd
}

// awaitClosure starts the closure check of tr on r and waits for it to end. It fails when the check starts nothing.
func awaitClosure(t *testing.T, r *relay, tr daemon.Transition) {
	t.Helper()
	done := r.checkClosure(t.Context(), tr)
	if done == nil {
		t.Fatalf("checkClosure(%s) started no check", tr.To)
	}
	<-done
}

// TestInboxClosureJudgeDoneSessionGetsOneCallAndStoredVerdict checks that a pane's transition to done makes exactly one
// closure call, whose input starts with the closure instruction and holds the transcript's recent text, and stores the
// call's verdict under the pane's id and change time at the relay's clock.
func TestInboxClosureJudgeDoneSessionGetsOneCallAndStoredVerdict(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	caller := &closureCaller{reply: json.RawMessage(closureReply)}
	writer := &closureWriter{}
	r := closureRelay(home, caller, closureKeys(closurePaneID, closureChanged, nil), writer)

	awaitClosure(t, r, closureTransition(t, protocol.StateDone, closurePlainCwd(t, home), "",
		closureTranscript(t, t.TempDir())))

	if len(caller.calls) != 1 {
		t.Fatalf("closure calls = %d, want 1", len(caller.calls))
	}
	input := caller.calls[0].input
	if !strings.HasPrefix(input, closurejudge.Instruction) {
		t.Errorf("input does not start with the closure instruction: %q", input)
	}
	for _, want := range []string{"User: " + closurePrompt, "Assistant: " + closureEarlier, "Assistant: " + closureFinal} {
		if !strings.Contains(input, want) {
			t.Errorf("input = %q, want it to hold %q", input, want)
		}
	}
	if len(writer.writes) != 1 {
		t.Fatalf("verdict writes = %d, want 1", len(writer.writes))
	}
	got := writer.writes[0]
	want := store.ClosureVerdict{Session: closurePaneID, Changed: closureChangedAt, Closed: true, Why: closureWhy}
	if got.verdict != want {
		t.Errorf("stored verdict = %+v, want %+v", got.verdict, want)
	}
	if !got.at.Equal(time.Unix(closureNow, 0)) {
		t.Errorf("stored at = %v, want the relay's clock %v", got.at, time.Unix(closureNow, 0))
	}
}

// TestInboxClosureJudgeFailedCallStoresNothing checks that a closure call that fails stores no verdict, after the one
// call it made.
func TestInboxClosureJudgeFailedCallStoresNothing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	caller := &closureCaller{err: errClosureCall}
	writer := &closureWriter{}
	r := closureRelay(home, caller, closureKeys(closurePaneID, closureChanged, nil), writer)

	awaitClosure(t, r, closureTransition(t, protocol.StateDone, closurePlainCwd(t, home), "",
		closureTranscript(t, t.TempDir())))

	if len(caller.calls) != 1 {
		t.Fatalf("closure calls = %d, want 1", len(caller.calls))
	}
	if len(writer.writes) != 0 {
		t.Errorf("verdict writes = %d, want none after a failed call", len(writer.writes))
	}
}

// TestInboxClosureJudgeWorkingOrWaitingSessionMakesNoCall checks that a pane's transition to working or waiting starts
// no closure check, so it makes no call and stores nothing.
func TestInboxClosureJudgeWorkingOrWaitingSessionMakesNoCall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		state protocol.State
	}{
		{"working", protocol.StateWorking},
		{"waiting", protocol.StateWaiting},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			caller := &closureCaller{reply: json.RawMessage(closureReply)}
			writer := &closureWriter{}
			r := closureRelay(home, caller, closureKeys(closurePaneID, closureChanged, nil), writer)

			tr := closureTransition(t, tc.state, closurePlainCwd(t, home), "", closureTranscript(t, t.TempDir()))
			if done := r.checkClosure(t.Context(), tr); done != nil {
				t.Fatalf("checkClosure(%s) started a check, want none", tc.state)
			}
			if len(caller.calls) != 0 {
				t.Errorf("closure calls = %d, want none", len(caller.calls))
			}
			if len(writer.writes) != 0 {
				t.Errorf("verdict writes = %d, want none", len(writer.writes))
			}
		})
	}
}

// TestInboxClosureJudgeWithoutStoreMakesNoCall checks that a done transition with no store attached makes no call and
// stores nothing.
func TestInboxClosureJudgeWithoutStoreMakesNoCall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	caller := &closureCaller{reply: json.RawMessage(closureReply)}
	r := closureRelay(home, caller, closureKeys(closurePaneID, closureChanged, nil), nil)

	awaitClosure(t, r, closureTransition(t, protocol.StateDone, closurePlainCwd(t, home), "",
		closureTranscript(t, t.TempDir())))

	if len(caller.calls) != 0 {
		t.Errorf("closure calls = %d, want none with no store", len(caller.calls))
	}
}

// TestInboxClosureJudgeUnkeyedOrUnreadablePaneMakesNoCall checks that a pane with no id, a change time that does not
// parse, a key read that fails or a transcript that cannot be read stores nothing and makes no call.
func TestInboxClosureJudgeUnkeyedOrUnreadablePaneMakesNoCall(t *testing.T) {
	t.Parallel()
	transcript := closureTranscript(t, t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	tests := []struct {
		name       string
		keys       paneKeyReader
		transcript string
	}{
		{"empty id", closureKeys("", closureChanged, nil), transcript},
		{"changed does not parse", closureKeys(closurePaneID, "not-a-time", nil), transcript},
		{"changed empty", closureKeys(closurePaneID, "", nil), transcript},
		{"key read fails", closureKeys("", "", errClosureCall), transcript},
		{"transcript unreadable", closureKeys(closurePaneID, closureChanged, nil), missing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			caller := &closureCaller{reply: json.RawMessage(closureReply)}
			writer := &closureWriter{}
			r := closureRelay(home, caller, tc.keys, writer)

			awaitClosure(t, r, closureTransition(t, protocol.StateDone, closurePlainCwd(t, home), "", tc.transcript))

			if len(caller.calls) != 0 {
				t.Errorf("closure calls = %d, want none", len(caller.calls))
			}
			if len(writer.writes) != 0 {
				t.Errorf("verdict writes = %d, want none", len(writer.writes))
			}
		})
	}
}

// TestInboxClosureStampStampedPaneCallsWithStampAccount checks that a pane whose cwd lies under a context stamp makes its
// closure call through the stamp's base URL and whole env, not through its frame's base URL.
func TestInboxClosureStampStampedPaneCallsWithStampAccount(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	stamped := judgeStampFolder(t, home, "work/attain",
		`{"env":{"ANTHROPIC_BASE_URL":"`+judgeStampURL+`","CTX_EXAMPLE":"example"}}`)
	caller := &closureCaller{reply: json.RawMessage(closureReply)}
	r := closureRelay(home, caller, closureKeys(closurePaneID, closureChanged, nil), &closureWriter{})

	awaitClosure(t, r, closureTransition(t, protocol.StateDone, judgeStampCwd(t, stamped), judgeStampFrame,
		closureTranscript(t, t.TempDir())))

	if len(caller.calls) != 1 {
		t.Fatalf("closure calls = %d, want 1", len(caller.calls))
	}
	got := caller.calls[0]
	if got.baseURL != judgeStampURL {
		t.Errorf("baseURL = %q, want the stamp's %q", got.baseURL, judgeStampURL)
	}
	wantEnv := map[string]string{"ANTHROPIC_BASE_URL": judgeStampURL, "CTX_EXAMPLE": "example"}
	if !maps.Equal(got.env, wantEnv) {
		t.Errorf("env = %v, want the stamp's whole env %v", got.env, wantEnv)
	}
}

// TestInboxClosureStampUnstampedPaneCallsWithFrameURL checks that a pane under no context stamp makes its closure call
// through its frame's base URL, with that URL as the whole env.
func TestInboxClosureStampUnstampedPaneCallsWithFrameURL(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	caller := &closureCaller{reply: json.RawMessage(closureReply)}
	r := closureRelay(home, caller, closureKeys(closurePaneID, closureChanged, nil), &closureWriter{})

	awaitClosure(t, r, closureTransition(t, protocol.StateDone, closurePlainCwd(t, home), judgeStampFrame,
		closureTranscript(t, t.TempDir())))

	if len(caller.calls) != 1 {
		t.Fatalf("closure calls = %d, want 1", len(caller.calls))
	}
	got := caller.calls[0]
	if got.baseURL != judgeStampFrame {
		t.Errorf("baseURL = %q, want the frame's %q", got.baseURL, judgeStampFrame)
	}
	if wantEnv := map[string]string{"ANTHROPIC_BASE_URL": judgeStampFrame}; !maps.Equal(got.env, wantEnv) {
		t.Errorf("env = %v, want %v", got.env, wantEnv)
	}
}

// TestInboxClosureStampUnreadableStampMakesNoCall checks that a stamp the lookup cannot read stores nothing and makes no
// closure call.
func TestInboxClosureStampUnreadableStampMakesNoCall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	stamped := judgeStampFolder(t, home, "work/attain", `{"env": not json`)
	caller := &closureCaller{reply: json.RawMessage(closureReply)}
	writer := &closureWriter{}
	r := closureRelay(home, caller, closureKeys(closurePaneID, closureChanged, nil), writer)

	awaitClosure(t, r, closureTransition(t, protocol.StateDone, judgeStampCwd(t, stamped), judgeStampFrame,
		closureTranscript(t, t.TempDir())))

	if len(caller.calls) != 0 {
		t.Errorf("closure calls = %d, want none under an unreadable stamp", len(caller.calls))
	}
	if len(writer.writes) != 0 {
		t.Errorf("verdict writes = %d, want none under an unreadable stamp", len(writer.writes))
	}
}
