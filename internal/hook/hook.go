// Package hook forwards one Claude Code hook event to the orreryd daemon.
package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// forwardDeadline bounds the dial and the write of one frame. A slow or absent daemon must
// not hold up the Claude Code hook.
const forwardDeadline = 20 * time.Millisecond

// seenDeadline bounds the dial and the write of one seen report.
const seenDeadline = time.Second

// Run forwards the hook JSON read from stdin, with the pane, tmux, working directory and
// patchbay base URL, to the daemon listening on socketPath. It writes nothing to stdout or
// stderr and returns no error: a missing or slow daemon drops the event silently. The
// summarizer's own subprocess sends nothing, an orrery-started headless session (one whose
// protocol.EnvSession marker is non-empty) sends nothing and returns without reading stdin,
// and an empty socketPath sends nothing.
func Run(stdin io.Reader, getenv func(string) string, socketPath string) {
	if protocol.IsSummarizer(getenv) || getenv(protocol.EnvSession) != "" || socketPath == "" {
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

// ReportSeen tells orreryd that pane was seen at seen, so the daemon clears the notices older than that. A missing
// socket file or a refused connection means no daemon is listening and holds no notice, so both return nil. An empty
// socketPath sends nothing. Any other dial or write failure is returned.
func ReportSeen(socketPath, pane string, seen int64) error {
	if socketPath == "" {
		return nil
	}
	var payload bytes.Buffer
	if err := protocol.WriteFrame(&payload, protocol.HookFrame{TmuxPane: pane, Seen: seen}); err != nil {
		return fmt.Errorf("encode seen report: %w", err)
	}

	deadline := time.Now().Add(seenDeadline)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil
		}
		return fmt.Errorf("dial orreryd: %w", err)
	}
	defer func() {
		// The frame is already written when the connection closes, so a failed close loses nothing.
		if closeErr := conn.Close(); closeErr != nil {
			return
		}
	}()
	if err = conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set seen deadline: %w", err)
	}
	if _, err = conn.Write(payload.Bytes()); err != nil {
		return fmt.Errorf("write seen report: %w", err)
	}
	return nil
}
