// Package hook forwards one Claude Code hook event to the orreryd daemon.
package hook

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// forwardDeadline bounds the dial and the write of one frame. A slow or absent daemon must
// not hold up the Claude Code hook.
const forwardDeadline = 20 * time.Millisecond

// Run forwards the hook JSON read from stdin, with the pane, tmux, working directory and
// patchbay base URL, to the daemon listening on socketPath. It writes nothing to stdout or
// stderr and returns no error: a missing or slow daemon drops the event silently. The
// summarizer's own subprocess sends nothing, and an empty socketPath sends nothing.
func Run(stdin io.Reader, getenv func(string) string, socketPath string) {
	if protocol.IsSummarizer(getenv) || socketPath == "" {
		return
	}
	hookJSON, err := io.ReadAll(stdin)
	if err != nil {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	frame := protocol.HookFrame{
		Hook:             hookJSON,
		TmuxPane:         getenv("TMUX_PANE"),
		Tmux:             getenv("TMUX"),
		Cwd:              cwd,
		AnthropicBaseURL: getenv("ANTHROPIC_BASE_URL"),
	}

	// Encoding runs before the clock starts: the deadline bounds only the dial and the write.
	var payload bytes.Buffer
	if err = protocol.WriteFrame(&payload, frame); err != nil {
		return
	}

	deadline := time.Now().Add(forwardDeadline)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return
	}
	defer func() {
		// A failed close has nowhere to report: Run stays silent and the hook exits 0.
		if closeErr := conn.Close(); closeErr != nil {
			return
		}
	}()
	if err = conn.SetDeadline(deadline); err != nil {
		return
	}
	if _, err = conn.Write(payload.Bytes()); err != nil {
		return
	}
}
