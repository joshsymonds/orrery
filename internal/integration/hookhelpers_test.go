package integration_test

import (
	"encoding/json"
	"io"
	"net"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// sendHook sends hook for pane, run in cwd, straight to the orreryd on socket as the hook command would, and returns
// once orreryd closes the connection, which it does after it has applied the frame, run its observers and synced the
// pane. So the options the frame sets are set when it returns: a test reads them, and waits only for what orreryd
// writes later on its own, such as a summary. It spawns no orrery process: the hook command exits before orreryd has
// applied its frame, so a wait after it has nothing that ends it. TestOrrerydDrivesHooksEndToEnd runs the command
// itself. The wait has no deadline of its own, and ends when orreryd closes the connection or exits.
func sendHook(t *testing.T, socket, pane, cwd string, hook map[string]any) {
	t.Helper()
	payload, err := json.Marshal(hook)
	if err != nil {
		t.Fatalf("encode hook: %v", err)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(t.Context(), "unix", socket)
	if err != nil {
		t.Fatalf("dial %s: %v", socket, err)
	}
	defer conn.Close()
	frame := protocol.HookFrame{Hook: payload, TmuxPane: pane, Cwd: cwd, AnthropicBaseURL: exampleProxyURL}
	if err = protocol.WriteFrame(conn, frame); err != nil {
		t.Fatalf("write hook frame: %v", err)
	}
	if _, err = io.Copy(io.Discard, conn); err != nil {
		t.Fatalf("wait for orreryd to close the hook connection: %v", err)
	}
}
