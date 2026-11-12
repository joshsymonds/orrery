package integration_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// nextDeskLine returns the next line the desk stream sent, failing the test after limit.
func nextDeskLine(t *testing.T, conn net.Conn, r *bufio.Reader, limit time.Duration) deskstream.Line {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(limit)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	raw, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read desk stream: %v", err)
	}
	var line deskstream.Line
	if err = json.Unmarshal(raw, &line); err != nil {
		t.Fatalf("decode desk line %q: %v", raw, err)
	}
	return line
}

func TestOrrerydStreamsNoticesToTheDesk(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	daemon, socket, dir := startSummaryDaemon(t, name, fakeClaudeScript)
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)
	project := filepath.Join(dir, exampleProjectName)
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatalf("make project: %v", err)
	}

	noticesSocket := deskstream.SocketPath(socket)
	if filepath.Base(noticesSocket) != "notices.sock" || filepath.Dir(noticesSocket) != dir {
		t.Fatalf("notices socket %q, want notices.sock beside %q", noticesSocket, socket)
	}
	conn, err := net.Dial("unix", noticesSocket)
	if err != nil {
		t.Fatalf("dial notices socket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	r := bufio.NewReader(conn)

	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("host name: %v", err)
	}
	hello := nextDeskLine(t, conn, r, waitLimit)
	if hello.Kind != deskstream.KindHello || hello.Host != host {
		t.Fatalf("hello %+v, want kind %q host %q", hello, deskstream.KindHello, host)
	}

	sendHook(t, socket, pane, project, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"transcript_path": transcript,
	})
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateWorking))
	waitForOption(t, client, pane, protocol.OptionSummary, exampleSummary)

	sendHook(t, socket, pane, project, map[string]any{
		"hook_event_name": "PermissionRequest",
		"tool_name":       "example-tool",
		"tool_input":      map[string]any{"command": "example"},
	})
	waiting := nextDeskLine(t, conn, r, waitLimit)
	question := readOption(t, client, pane, protocol.OptionQuestion)
	want := deskstream.Line{
		Kind: deskstream.KindNotice, Host: host, State: "waiting", Title: exampleTitle, Body: question,
	}
	if question == "" || waiting != want {
		t.Fatalf("waiting notice %+v, want %+v", waiting, want)
	}

	sendHook(t, socket, pane, project, map[string]any{
		"hook_event_name":        "Stop",
		"transcript_path":        transcript,
		"last_assistant_message": exampleLastMessage,
	})
	done := nextDeskLine(t, conn, r, judgedLimit)
	want = deskstream.Line{
		Kind: deskstream.KindNotice, Host: host, State: "done", Title: exampleTitle, Body: exampleSummary,
	}
	if done != want {
		t.Fatalf("done notice %+v, want %+v", done, want)
	}

	wantExit(t, daemon.stop(t), 0)
}
