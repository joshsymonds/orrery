package daemon_test

import (
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// TestHookFrameIgnored checks that a frame which carries a hook object and none of seen, read, input or ask is dropped:
// it writes no pane option, calls no observer, passes no seen report, and logs a warning.
func TestHookFrameIgnored(t *testing.T) {
	srv := startServer(t)
	var observed, seen atomic.Int32
	logs := &lockedBuffer{}
	socketPath := serve(t, daemon.Config{
		Observers: []daemon.Observer{func(daemon.Transition) { observed.Add(1) }},
		Seen:      func(daemon.SeenReport) { seen.Add(1) },
		Logger:    slog.New(slog.NewTextHandler(logs, nil)),
	})
	frame := fmt.Sprintf(
		`{"hook":{"hook_event_name":"Stop","last_assistant_message":"synthetic final message"},`+
			`"tmuxPane":%q,"tmux":%q,"cwd":"/synthetic/work","anthropicBaseUrl":"https://synthetic.example"}`+"\n",
		srv.pane, srv.socket+",1234,0")
	sendBytes(t, socketPath, []byte(frame))

	for _, name := range []string{protocol.OptionState, protocol.OptionChanged, protocol.OptionContext} {
		wantOption(t, srv, name, "")
	}
	if n := observed.Load(); n != 0 {
		t.Errorf("observers called %d times, want none", n)
	}
	if n := seen.Load(); n != 0 {
		t.Errorf("seen reports = %d, want none", n)
	}
	if !strings.Contains(logs.String(), `level=WARN msg="read hook frame"`) {
		t.Errorf("log = %q, want a warning for the dropped frame", logs.String())
	}
}
