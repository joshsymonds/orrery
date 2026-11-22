package integration_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// nextDeskLine returns the next line the desk stream sent. It has no deadline of its own.
func nextDeskLine(t *testing.T, r *bufio.Reader) deskstream.Line {
	t.Helper()
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
	t.Parallel()
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	daemon, socket, dir := startSummaryDaemon(t, name, fakeClaudeScript)
	project := filepath.Join(dir, exampleProjectName)
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatalf("make project: %v", err)
	}
	session := startClaudeSession(t, client, dir, pane, project)

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

	host := testHost
	hello := nextDeskLine(t, r)
	if hello.Kind != deskstream.KindHello || hello.Host != host {
		t.Fatalf("hello %+v, want kind %q host %q", hello, deskstream.KindHello, host)
	}

	session.submitPrompt(t)
	waitForOption(t, daemon, client, pane, protocol.OptionState, string(protocol.StateWorking))
	waitForOption(t, daemon, client, pane, protocol.OptionSummary, exampleSummary)

	session.requestPermission(t)
	waiting := nextDeskLine(t, r)
	question := readOption(t, client, pane, protocol.OptionQuestion)
	want := deskstream.Line{
		Kind: deskstream.KindNotice, Host: host, State: "waiting", Title: exampleTitle, Body: question,
	}
	if question == "" || waiting != want {
		t.Fatalf("waiting notice %+v, want %+v", waiting, want)
	}

	session.finishTurn(t, exampleLastMessage)
	done := nextDeskLine(t, r)
	want = deskstream.Line{
		Kind: deskstream.KindNotice, Host: host, State: "done", Title: exampleTitle, Body: exampleSummary,
	}
	if done != want {
		t.Fatalf("done notice %+v, want %+v", done, want)
	}

	wantExit(t, daemon.stop(t), 0)
}
