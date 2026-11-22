// Package hook forwards one Claude Code hook event to the orreryd daemon.
package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// reportDeadline bounds how long one seen or read report waits on a daemon that is not reading it.
const reportDeadline = time.Second

// deliver writes payload to the daemon listening on socketPath, waiting on the daemon for at most wait. It never waits on
// a daemon that is keeping up, so no stall of this process, however long, drops a frame such a daemon would take. A unix
// connect does not wait: it connects, or a missing socket, a refused connection or a full backlog fails it at once. The
// frame is then written without waiting, and goes whole into the socket's buffer when it fits there. Only the part that
// does not fit waits for the daemon to read, until wait after the time now reads then. An error says why the frame did
// not go whole.
func deliver(socketPath string, payload []byte, wait time.Duration, now func() time.Time) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(context.Background(), "unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial orreryd: %w", err)
	}
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.Join(fmt.Errorf("dial orreryd: %T is no unix connection", conn), conn.Close())
	}
	err = writeWaiting(unix, payload, wait, now)
	// The frame is written, or dropped, by the time the connection closes, so a failed close loses nothing.
	if closeErr := conn.Close(); closeErr != nil {
		return err
	}
	return err
}

// writeWaiting writes payload to conn: first as much as conn's socket takes without waiting, then the rest, waiting for
// the daemon to read it until wait after the time now reads once the first write is done.
func writeWaiting(conn *net.UnixConn, payload []byte, wait time.Duration, now func() time.Time) error {
	written, err := writeNoWait(conn, payload)
	if err != nil {
		return err
	}
	if written == len(payload) {
		return nil
	}
	if err = conn.SetDeadline(now().Add(wait)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if _, err = conn.Write(payload[written:]); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

// writeNoWait writes as much of payload to conn as its socket takes at once and returns how much. It does not wait and
// reads no deadline: the socket is non-blocking, so a full buffer writes nothing and reports EAGAIN, which is no error.
func writeNoWait(conn *net.UnixConn, payload []byte) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("raw connection: %w", err)
	}
	var written int
	var writeErr error
	controlErr := raw.Control(func(fd uintptr) {
		for {
			written, writeErr = syscall.Write(int(fd), payload)
			if !errors.Is(writeErr, syscall.EINTR) {
				return
			}
		}
	})
	switch {
	case controlErr != nil:
		return 0, fmt.Errorf("raw connection: %w", controlErr)
	case errors.Is(writeErr, syscall.EAGAIN):
		return 0, nil
	case writeErr != nil:
		return 0, fmt.Errorf("write frame: %w", writeErr)
	default:
		return written, nil
	}
}

// ReportSeen tells orreryd that pane was seen at seen, so the daemon clears the notices older than that. A missing
// socket file or a refused connection means no daemon is listening and holds no notice, so both return nil. An empty
// socketPath sends nothing. Any other dial or write failure is returned.
func ReportSeen(socketPath, pane string, seen int64) error {
	return sendReport(socketPath, protocol.HookFrame{TmuxPane: pane, Seen: seen})
}

// ReportRead tells orreryd that the phone read pane at read. It shares ReportSeen's nil and error rules.
func ReportRead(socketPath, pane string, read int64) error {
	return sendReport(socketPath, protocol.HookFrame{TmuxPane: pane, Read: read})
}

// ReportInput tells orreryd that the web view had input at input, in unix seconds, and that it views view. A zero view
// names no key. It shares ReportSeen's nil and error rules.
func ReportInput(socketPath string, input int64, view protocol.ViewKey) error {
	return sendReport(socketPath, protocol.HookFrame{Input: input, View: view})
}

// sendReport writes frame, a seen or read report, to the daemon listening on socketPath. A missing socket file or a
// refused connection means no daemon is listening and so holds nothing to update, so both return nil. An empty
// socketPath sends nothing. Any other dial or write failure is returned.
func sendReport(socketPath string, frame protocol.HookFrame) error {
	if socketPath == "" {
		return nil
	}
	var payload bytes.Buffer
	if err := protocol.WriteFrame(&payload, frame); err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	err := deliver(socketPath, payload.Bytes(), reportDeadline, time.Now)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}
	return nil
}
