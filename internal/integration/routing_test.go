package integration_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/hook"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/route"
)

// loggedNotice is the state, route and reason one line of the notice log must carry.
type loggedNotice struct {
	state, routed, reason string
}

// noticeLogPath is the notice log orreryd writes beside the push store under stateHome.
func noticeLogPath(stateHome string) string {
	return filepath.Join(stateHome, "orrery", "notices.jsonl")
}

// readNoticeLog returns the lines of the notice log at path, decoded.
func readNoticeLog(t *testing.T, path string) []route.Line {
	t.Helper()
	raws := readLines(t, path)
	lines := make([]route.Line, 0, len(raws))
	for _, raw := range raws {
		var line route.Line
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("decode notice line %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

// assertNoticeLog fails unless the notice log at path holds exactly the lines want names, in order. Each line must name
// this host and pane, and carry a time from since to now.
func assertNoticeLog(t *testing.T, path string, since int64, pane string, want ...loggedNotice) {
	t.Helper()
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("host name: %v", err)
	}
	got := readNoticeLog(t, path)
	if len(got) != len(want) {
		t.Fatalf("notice log %+v, want %d lines %+v", got, len(want), want)
	}
	now := time.Now().Unix()
	for i, line := range got {
		if line.Host != host || line.Pane != pane || line.Time < since || line.Time > now {
			t.Fatalf("notice line %d %+v, want host %q, pane %q, time from %d to %d", i, line, host, pane, since, now)
		}
		if line.State != want[i].state || line.Route != want[i].routed || line.Reason != want[i].reason {
			t.Fatalf("notice line %d state %q route %q reason %q, want state %q route %q reason %q",
				i, line.State, line.Route, line.Reason, want[i].state, want[i].routed, want[i].reason)
		}
	}
}

// noticeLogHas reports whether the notice log at path holds a line with reason. A missing log holds none.
func noticeLogHas(path, reason string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), `"reason":"`+reason+`"`)
}

// permissionRequestHook is a PermissionRequest hook input, which leaves its pane waiting for input.
func permissionRequestHook() map[string]any {
	return map[string]any{
		"hook_event_name": "PermissionRequest",
		"tool_name":       "example-tool",
		"tool_input":      map[string]any{"command": "example"},
	}
}

// sendDeskActivity writes one desk activity line on conn, reporting that the desk's user gave input at input.
func sendDeskActivity(t *testing.T, conn net.Conn, input int64) {
	t.Helper()
	raw, err := json.Marshal(deskstream.Activity{Input: input})
	if err != nil {
		t.Fatalf("encode desk activity: %v", err)
	}
	if _, err = conn.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write desk activity: %v", err)
	}
}

func TestOrrerydRouteAwayPushesAndLogs(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	endpoint := newPushEndpoint(t)
	registerPush(t, stateHome, endpoint)
	daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint)
	since := time.Now().Unix()

	sendHook(t, socket, pane, dir, permissionRequestHook())
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateWaiting))
	// The router decides the notice after the frame is applied, so the push is awaited before orreryd stops. Stopping
	// earlier would end the undecided notice with a shutdown line.
	assertPushed(t, endpoint, pane, protocol.StateWaiting, waitLimit)
	wantExit(t, daemon.stop(t), 0)

	if extra := len(endpoint.requests); extra != 0 {
		t.Fatalf("%d push requests beyond the waiting one", extra)
	}
	if judges := judgeRuns(t, dir); len(judges) != 0 {
		t.Fatalf("%d judge runs for a permission prompt, want none", len(judges))
	}
	assertNoticeLog(t, noticeLogPath(stateHome), since, pane, loggedNotice{"waiting", route.DeskPhone, "away"})
}

func TestOrrerydRouteSilencesBackgroundWorkStop(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	endpoint := newPushEndpoint(t)
	registerPush(t, stateHome, endpoint)
	daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint)
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)
	since := time.Now().Unix()

	sendHook(t, socket, pane, dir, stopHookInput(t, transcript, quietSubagentTasks))
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateDone))
	wantExit(t, daemon.stop(t), 0)

	if extra := len(endpoint.requests); extra != 0 {
		t.Fatalf("%d push requests for a Stop with a background subagent, want none", extra)
	}
	assertNoticeLog(t, noticeLogPath(stateHome), since, pane, loggedNotice{"done", route.None, "background work"})
}

func TestOrrerydRouteHoldsWatchedPane(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	endpoint := newPushEndpoint(t)
	registerPush(t, stateHome, endpoint)
	daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint)
	logPath := noticeLogPath(stateHome)
	since := time.Now().Unix()

	// The read report is a connection of its own, written before the waiting hook starts its process, so orreryd
	// records the read before it decides the waiting notice.
	if err := hook.ReportRead(socket, pane, time.Now().Unix()); err != nil {
		t.Fatalf("report read: %v", err)
	}
	sendHook(t, socket, pane, dir, permissionRequestHook())
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateWaiting))
	// The held notice logs its watching line from its own decision, so that line is awaited before orreryd stops. The
	// store is attached once its open line is logged, so stopping then keeps the held notice for the next start, with
	// no shutdown line.
	waitUntil(t, "the watching line", func() bool { return noticeLogHas(logPath, "watching") })
	daemon.waitForStoreOpen(t)
	wantExit(t, daemon.stop(t), 0)

	if extra := len(endpoint.requests); extra != 0 {
		t.Fatalf("%d push requests for a watched waiting pane, want none", extra)
	}
	assertNoticeLog(t, logPath, since, pane, loggedNotice{"waiting", route.None, "watching"})
}

func TestOrrerydRouteAtDeskReachesStreamOnly(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	endpoint := newPushEndpoint(t)
	registerPush(t, stateHome, endpoint)
	daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint)
	project := filepath.Join(dir, exampleProjectName)
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatalf("make project: %v", err)
	}
	conn, err := net.Dial("unix", deskstream.SocketPath(socket))
	if err != nil {
		t.Fatalf("dial notices socket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	r := bufio.NewReader(conn)
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("host name: %v", err)
	}
	if hello := nextDeskLine(t, conn, r, waitLimit); hello.Kind != deskstream.KindHello || hello.Host != host {
		t.Fatalf("hello %+v, want kind %q host %q", hello, deskstream.KindHello, host)
	}
	since := time.Now().Unix()

	// The desk's input is recorded on the stream's own reader, which the waiting hook's process and tmux calls outlast
	// before orreryd decides the notice.
	sendDeskActivity(t, conn, time.Now().Unix())
	sendHook(t, socket, pane, project, permissionRequestHook())
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateWaiting))
	line := nextDeskLine(t, conn, r, waitLimit)
	if line.Kind != deskstream.KindNotice || line.Host != host || line.State != string(protocol.StateWaiting) ||
		line.Title != exampleTitle {
		t.Fatalf("desk line %+v, want a waiting notice from host %q titled %q", line, host, exampleTitle)
	}

	// The store is attached once its open line is logged, so stopping then keeps the notice at the desk for the next
	// start, with no shutdown line.
	daemon.waitForStoreOpen(t)
	wantExit(t, daemon.stop(t), 0)
	if extra := len(endpoint.requests); extra != 0 {
		t.Fatalf("%d push requests for a waiting pane at the desk, want none", extra)
	}
	assertNoticeLog(t, noticeLogPath(stateHome), since, pane, loggedNotice{"waiting", route.Desk, "at the desk"})
}

func TestOrrerydRouteWithoutSubscriptionReachesDeskOnly(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	endpoint := newPushEndpoint(t)
	daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint)
	since := time.Now().Unix()

	sendHook(t, socket, pane, dir, permissionRequestHook())
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateWaiting))
	// No push marks the decision, so the notice's own line is awaited before orreryd stops.
	logPath := noticeLogPath(stateHome)
	waitUntil(t, "the no-subscription line", func() bool { return noticeLogHas(logPath, "away; no push subscription") })
	wantExit(t, daemon.stop(t), 0)

	if extra := len(endpoint.requests); extra != 0 {
		t.Fatalf("%d push requests without a push subscription, want none", extra)
	}
	assertNoticeLog(t, logPath, since, pane,
		loggedNotice{"waiting", route.Desk, "away; no push subscription"},
	)
}

func TestOrrerydRouteNoticeLogOpenFailureExitsOne(t *testing.T) {
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	// A directory where the log file belongs makes the open fail, so orreryd exits before it listens.
	if err := os.MkdirAll(noticeLogPath(stateHome), 0o700); err != nil {
		t.Fatalf("make directory at the log path: %v", err)
	}
	socket := filepath.Join(dir, "orreryd.sock")
	daemon := startBinary(t, "orreryd", scrubbedEnv("XDG_STATE_HOME="+stateHome), "--socket", socket)
	out := daemon.exitOutcome(t)
	wantExit(t, out, 1)
	if !strings.Contains(out.stderr, "notice log") {
		t.Fatalf("stderr %q, want a notice log error", out.stderr)
	}
}

// stopHook is a Stop hook input for the session whose transcript is transcript. Its last assistant message is
// exampleLastMessage, so the done it makes is judged.
func stopHook(transcript string) map[string]any {
	return map[string]any{
		"hook_event_name":        "Stop",
		"transcript_path":        transcript,
		"last_assistant_message": exampleLastMessage,
	}
}

// dialDesk dials the desk stream beside socket, reads its hello line, and returns the connection and its reader.
func dialDesk(t *testing.T, socket string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("unix", deskstream.SocketPath(socket))
	if err != nil {
		t.Fatalf("dial notices socket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	r := bufio.NewReader(conn)
	if hello := nextDeskLine(t, conn, r, waitLimit); hello.Kind != deskstream.KindHello {
		t.Fatalf("hello %+v, want kind %q", hello, deskstream.KindHello)
	}
	return conn, r
}

// TestOrrerydRouteJudgedDoneRoutesByVerdict drives one plain done per case, judged after route.JudgeDelay. The verdict
// or the failure sets the desk line's quiet key, the push's silent key and the log reason. The judge runs once, through
// the pane's base URL and summary model.
func TestOrrerydRouteJudgedDoneRoutesByVerdict(t *testing.T) {
	const failedReason = "away; judge failed: done judge: haiku: result reported is_error"
	tests := []struct {
		name   string
		reply  string
		quiet  bool
		silent bool
		want   loggedNotice
	}{
		{
			name: "unneeded", reply: judgeUnneededReply, quiet: true, silent: true,
			want: loggedNotice{"done", route.DeskPhone, "unneeded: example finished work"},
		},
		{
			name: "needed", reply: judgeNeededReply,
			want: loggedNotice{"done", route.DeskPhone, "away; needed: example question"},
		},
		{
			name: "failed", reply: judgeFailedReply,
			want: loggedNotice{"done", route.DeskPhone, failedReason},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, name := newServer(t)
			pane := startPane(t, client, "sleep 300")
			dir := t.TempDir()
			stateHome := filepath.Join(dir, "state")
			endpoint := newPushEndpoint(t)
			registerPush(t, stateHome, endpoint)
			daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint)
			writeJudgeReply(t, dir, tc.reply)
			conn, r := dialDesk(t, socket)
			project := filepath.Join(dir, exampleProjectName)
			if err := os.Mkdir(project, 0o700); err != nil {
				t.Fatalf("make project: %v", err)
			}
			transcript := filepath.Join(dir, "transcript.jsonl")
			writeFile(t, transcript, exampleTranscript, 0o600)
			since := time.Now().Unix()

			started := time.Now()
			sendHook(t, socket, pane, project, stopHook(transcript))
			line := nextDeskLine(t, conn, r, judgedLimit)
			if elapsed := time.Since(started); elapsed < route.JudgeDelay {
				t.Fatalf("desk notice after %v, want no sooner than the judge delay %v", elapsed, route.JudgeDelay)
			}
			if line.Kind != deskstream.KindNotice || line.State != string(protocol.StateDone) ||
				line.Title != exampleTitle || line.Body != exampleSummary || line.Quiet != tc.quiet {
				t.Fatalf("desk line %+v, want a done notice titled %q with body %q, quiet %t",
					line, exampleTitle, exampleSummary, tc.quiet)
			}
			note := assertPushed(t, endpoint, pane, protocol.StateDone, waitLimit)
			if _, silent := note["silent"]; silent != tc.silent {
				t.Fatalf("notification %v silent %t, want %t", note, silent, tc.silent)
			}

			wantExit(t, daemon.stop(t), 0)
			if extra := len(endpoint.requests); extra != 0 {
				t.Fatalf("%d push requests beyond the done one", extra)
			}
			judges := judgeRuns(t, dir)
			if len(judges) != 1 {
				t.Fatalf("judge runs %v, want exactly one", judges)
			}
			assertSummaryRun(t, judges[0])
			assertNoticeLog(t, noticeLogPath(stateHome), since, pane, tc.want)
		})
	}
}

// TestOrrerydRouteWorkAfterDoneIsChanged proves that work which starts before a done's judge delay ends the done as
// changed, and that no judge runs for it.
func TestOrrerydRouteWorkAfterDoneIsChanged(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	endpoint := newPushEndpoint(t)
	registerPush(t, stateHome, endpoint)
	daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint)
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)
	since := time.Now().Unix()

	sendHook(t, socket, pane, dir, stopHook(transcript))
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateDone))
	sendHook(t, socket, pane, dir, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"transcript_path": transcript,
	})
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateWorking))
	wantExit(t, daemon.stop(t), 0)

	if extra := len(endpoint.requests); extra != 0 {
		t.Fatalf("%d push requests for a done that work ended, want none", extra)
	}
	if judges := judgeRuns(t, dir); len(judges) != 0 {
		t.Fatalf("%d judge runs for a done that work ended, want none", len(judges))
	}
	assertNoticeLog(t, noticeLogPath(stateHome), since, pane, loggedNotice{"done", route.None, "changed"})
}

// TestOrrerydJudgesDoneWithSessions proves the judge runs when orreryd also serves the engine sessions.
func TestOrrerydJudgesDoneWithSessions(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	endpoint := newPushEndpoint(t)
	registerPush(t, stateHome, endpoint)
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint, "--sessions", "--session-shell", shell)
	// sessions.sock listens only after the store open, which has no bound, so the listen's wait limit starts at the
	// store-open line.
	daemon.waitForStoreOpen(t)
	daemon.waitForListener(t, "unix", protocol.SessionSocketPath(socket))
	writeJudgeReply(t, dir, judgeUnneededReply)
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)
	since := time.Now().Unix()

	sendHook(t, socket, pane, dir, stopHook(transcript))
	note := assertPushed(t, endpoint, pane, protocol.StateDone, judgedLimit)
	if _, silent := note["silent"]; !silent {
		t.Fatalf("notification %v, want the unneeded done silent", note)
	}
	wantExit(t, daemon.stop(t), 0)

	judges := judgeRuns(t, dir)
	if len(judges) != 1 {
		t.Fatalf("judge runs %v, want exactly one", judges)
	}
	assertSummaryRun(t, judges[0])
	assertNoticeLog(t, noticeLogPath(stateHome), since, pane,
		loggedNotice{"done", route.DeskPhone, "unneeded: example finished work"})
}
