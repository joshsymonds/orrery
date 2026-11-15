package main

import (
	"log/slog"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/route"
)

// Synthetic values only. The panes run on a private tmux server, and the hooks are written here.
const (
	stopFailureError   = "rate_limit"
	stopFailureBody    = "API error: " + stopFailureError
	stopFailureUnknown = "API error: unknown"
	stopFailureHook    = `{"hook_event_name":"StopFailure","error":"rate_limit"}`
	stopFailureNoError = `{"hook_event_name":"StopFailure"}`
	stopFailureEmpty   = `{"hook_event_name":"StopFailure","error":""}`
	stopHook           = `{"hook_event_name":"Stop","last_assistant_message":"synthetic final message"}`
)

// checkDoneText asserts that incoming, a done notice, has text as its desk body and push summary, and that its Judge is
// set exactly when judged.
func checkDoneText(t *testing.T, incoming route.Notice, judged bool, text string) {
	t.Helper()
	if got := incoming.Judge != nil; got != judged {
		t.Errorf("judge set = %v, want %v", got, judged)
	}
	desk, err := incoming.Desk(t.Context())
	if err != nil {
		t.Fatalf("desk notice: %v", err)
	}
	if want := expectedPost(protocol.StateDone, text); desk != want {
		t.Errorf("desk notice = %+v, want %+v", desk, want)
	}
	note, err := incoming.Push(t.Context())
	if err != nil {
		t.Fatalf("push notification: %v", err)
	}
	want := expectedPush(protocol.StateDone)
	want.Summary = text
	if note != want {
		t.Errorf("push notification = %+v, want %+v", note, want)
	}
}

// TestStopFailureNoticeIsNotJudged checks that a done from a StopFailure hook is not judged and names the API error the
// hook's error field carries, in its desk body and push summary.
func TestStopFailureNoticeIsNotJudged(t *testing.T) {
	cwd := paneCwd(t, t.TempDir())
	server, client, pane := startPane(t, cwd)
	setPaneText(t, client, pane)
	setPaneState(t, client, pane, protocol.StateDone)
	r := &relay{logger: slog.New(slog.DiscardHandler)}

	transition := paneTransition(server, pane, protocol.StateDone, cwd, stopFailureHook)
	checkDoneText(t, r.paneNotice(t.Context(), transition), false, stopFailureBody)
}

// TestStopFailureWithoutErrorNamesUnknown checks that a StopFailure whose error field is absent or empty names the API
// error as unknown.
func TestStopFailureWithoutErrorNamesUnknown(t *testing.T) {
	cwd := paneCwd(t, t.TempDir())
	server, client, pane := startPane(t, cwd)
	setPaneText(t, client, pane)
	setPaneState(t, client, pane, protocol.StateDone)
	r := &relay{logger: slog.New(slog.DiscardHandler)}

	for _, hook := range []string{stopFailureNoError, stopFailureEmpty} {
		transition := paneTransition(server, pane, protocol.StateDone, cwd, hook)
		checkDoneText(t, r.paneNotice(t.Context(), transition), false, stopFailureUnknown)
	}
}

// TestStopDoneIsStillJudged checks that a done from a Stop hook keeps its judge and its summary text.
func TestStopDoneIsStillJudged(t *testing.T) {
	cwd := paneCwd(t, t.TempDir())
	server, client, pane := startPane(t, cwd)
	setPaneText(t, client, pane)
	setPaneState(t, client, pane, protocol.StateDone)
	r := &relay{logger: slog.New(slog.DiscardHandler)}

	transition := paneTransition(server, pane, protocol.StateDone, cwd, stopHook)
	checkDoneText(t, r.paneNotice(t.Context(), transition), true, paneSummary)
}

// TestRestoredStopFailureNoticeIsNotJudged checks that the notice rebuildPane restores from a stored StopFailure done is
// the notice notify built: unjudged, naming the API error.
func TestRestoredStopFailureNoticeIsNotJudged(t *testing.T) {
	cwd := paneCwd(t, t.TempDir())
	server, client, pane := startPane(t, cwd)
	setPaneText(t, client, pane)
	setPaneState(t, client, pane, protocol.StateDone)
	r := &relay{logger: slog.New(slog.DiscardHandler)}

	transition := paneTransition(server, pane, protocol.StateDone, cwd, stopFailureHook)
	restored, err := r.rebuildPane(t.Context(), sourceOf(t, transition), paneClock)
	if err != nil {
		t.Fatalf("rebuild pane: %v", err)
	}
	checkDoneText(t, restored, false, stopFailureBody)
}
