package daemon_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// sessionsQuestion returns a sessions question. It names a pane and a tmux socket, so a question that wrongly fell
// through to the seen handler would reach Config.Seen.
func sessionsQuestion() protocol.HookFrame {
	return protocol.HookFrame{
		Ask:      protocol.SessionsAsk,
		TmuxPane: "%1",
		Tmux:     "/synthetic/tmux.sock,1234,0",
	}
}

// exchangeFrame writes frame to the daemon socket and returns every byte the daemon writes before it closes the
// connection. The close follows the handler, so the returned bytes are the whole reply.
func exchangeFrame(t *testing.T, socketPath string, frame protocol.HookFrame) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := protocol.WriteFrame(&buf, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), ioTimeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		t.Fatalf("dial %s: %v", socketPath, err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	}()
	if _, err := conn.Write(buf.Bytes()); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(ioTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	reply, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return reply
}

func TestDaemonAnswersSessionsQuestion(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprintf("sessions=%t", on), func(t *testing.T) {
			t.Parallel()
			seens := make(chan daemon.SeenReport, 4)
			reads := make(chan daemon.ReadReport, 4)
			inputs := make(chan int64, 4)
			transitions := make(chan daemon.Transition, 4)
			socket := serve(t, daemon.Config{
				Sessions:  on,
				Seen:      func(report daemon.SeenReport) { seens <- report },
				Read:      func(report daemon.ReadReport) { reads <- report },
				Input:     func(input int64) { inputs <- input },
				Observers: []daemon.Observer{func(transition daemon.Transition) { transitions <- transition }},
			})

			reply := exchangeFrame(t, socket, sessionsQuestion())
			got, err := protocol.ReadSessionsAnswer(bytes.NewReader(reply))
			if err != nil {
				t.Fatalf("ReadSessionsAnswer(%q): %v", reply, err)
			}
			if got != on {
				t.Fatalf("sessions answer = %t, want %t", got, on)
			}
			var want bytes.Buffer
			if err := protocol.WriteSessionsAnswer(&want, on); err != nil {
				t.Fatalf("WriteSessionsAnswer: %v", err)
			}
			if !bytes.Equal(reply, want.Bytes()) {
				t.Fatalf("reply = %q, want exactly the answer %q", reply, want.Bytes())
			}
			if got := drain(seens); len(got) != 0 {
				t.Fatalf("sessions question reached Config.Seen: %+v", got)
			}
			if got := drain(reads); len(got) != 0 {
				t.Fatalf("sessions question reached Config.Read: %+v", got)
			}
			if got := drain(inputs); len(got) != 0 {
				t.Fatalf("sessions question reached Config.Input: %v", got)
			}
			if got := drain(transitions); len(got) != 0 {
				t.Fatalf("sessions question notified observers: %+v", got)
			}
		})
	}
}

func TestDaemonInputReportGetsNoAnswer(t *testing.T) {
	t.Parallel()
	inputs := make(chan int64, 4)
	socket := serve(t, daemon.Config{
		Sessions: true,
		Input:    func(input int64) { inputs <- input },
	})

	reply := exchangeFrame(t, socket, inputFrameFor("%1", "/synthetic/tmux.sock"))
	if len(reply) != 0 {
		t.Fatalf("input report got reply bytes %q, want none", reply)
	}
	if got := drain(inputs); len(got) != 1 || got[0] != inputAt {
		t.Fatalf("input reports = %v, want exactly [%d]", got, inputAt)
	}
}
