package hook

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

const testPane = "%1"

// listen starts a unix listener on a fresh socket path inside the test's temp directory.
func listen(t *testing.T) (string, net.Listener) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "h.sock")
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return path, ln
}

type received struct {
	frame protocol.HookFrame
	err   error
}

// testSeen is a seen timestamp for the seen-report tests.
const testSeen int64 = 1_700_000_000_000

func TestReportSeenFrame(t *testing.T) {
	path, ln := listen(t)

	got := make(chan received, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- received{err: err}
			return
		}
		defer func() { _ = conn.Close() }()
		frame, err := protocol.ReadFrame(conn)
		got <- received{frame: frame, err: err}
	}()

	if err := ReportSeen(path, testPane, testSeen); err != nil {
		t.Fatalf("ReportSeen: %v", err)
	}

	result := <-got
	if result.err != nil {
		t.Fatalf("read frame: %v", result.err)
	}
	want := protocol.HookFrame{TmuxPane: testPane, Seen: testSeen}
	if !reflect.DeepEqual(result.frame, want) {
		t.Errorf("frame = %+v, want %+v", result.frame, want)
	}
}

func TestReportSeenNoSocketFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")
	if err := ReportSeen(path, testPane, testSeen); err != nil {
		t.Fatalf("ReportSeen with no socket file: %v, want nil", err)
	}
}

func TestReportSeenStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	// Closing leaves the socket file behind, so the path exists with no listener behind it.
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket file missing: %v", err)
	}

	if err := ReportSeen(path, testPane, testSeen); err != nil {
		t.Fatalf("ReportSeen with stale socket: %v, want nil", err)
	}
}

func TestReportSeenEmptySocketPath(t *testing.T) {
	if err := ReportSeen("", testPane, testSeen); err != nil {
		t.Fatalf("ReportSeen with empty socket path: %v, want nil", err)
	}
}

func TestReportSeenFailure(t *testing.T) {
	// A regular file in place of the socket's directory makes stat fail with ENOTDIR, which is
	// neither a missing socket nor a refused connection, so it must surface as an error.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	path := filepath.Join(blocker, "h.sock")

	if err := ReportSeen(path, testPane, testSeen); err == nil {
		t.Fatal("ReportSeen with an unreachable socket path returned nil, want an error")
	}
}

// testRead is a read timestamp for the read-report tests.
const testRead int64 = 1_700_000_001_000

func TestReportReadFrame(t *testing.T) {
	path, ln := listen(t)

	got := make(chan received, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- received{err: err}
			return
		}
		defer func() { _ = conn.Close() }()
		frame, err := protocol.ReadFrame(conn)
		got <- received{frame: frame, err: err}
	}()

	if err := ReportRead(path, testPane, testRead); err != nil {
		t.Fatalf("ReportRead: %v", err)
	}

	result := <-got
	if result.err != nil {
		t.Fatalf("read frame: %v", result.err)
	}
	want := protocol.HookFrame{TmuxPane: testPane, Read: testRead}
	if !reflect.DeepEqual(result.frame, want) {
		t.Errorf("frame = %+v, want %+v", result.frame, want)
	}
}

func TestReportReadNoSocketFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")
	if err := ReportRead(path, testPane, testRead); err != nil {
		t.Fatalf("ReportRead with no socket file: %v, want nil", err)
	}
}

func TestReportReadStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	// Closing leaves the socket file behind, so the path exists with no listener behind it.
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket file missing: %v", err)
	}

	if err := ReportRead(path, testPane, testRead); err != nil {
		t.Fatalf("ReportRead with stale socket: %v, want nil", err)
	}
}

func TestReportReadEmptySocketPath(t *testing.T) {
	if err := ReportRead("", testPane, testRead); err != nil {
		t.Fatalf("ReportRead with empty socket path: %v, want nil", err)
	}
}

func TestReportReadFailure(t *testing.T) {
	// A regular file in place of the socket's directory makes stat fail with ENOTDIR, which is
	// neither a missing socket nor a refused connection, so it must surface as an error.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	path := filepath.Join(blocker, "h.sock")

	if err := ReportRead(path, testPane, testRead); err == nil {
		t.Fatal("ReportRead with an unreachable socket path returned nil, want an error")
	}
}

// testInput is an input timestamp for the input-report tests.
const testInput int64 = 1_700_000_002_000

func TestReportInputFrame(t *testing.T) {
	cases := []struct {
		name string
		view protocol.ViewKey
	}{
		{"no viewing key", protocol.ViewKey{}},
		{"viewing a session", protocol.ViewKey{Session: "synthetic-session"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, ln := listen(t)

			got := make(chan received, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					got <- received{err: err}
					return
				}
				defer func() { _ = conn.Close() }()
				frame, err := protocol.ReadFrame(conn)
				got <- received{frame: frame, err: err}
			}()

			if err := ReportInput(path, testInput, tc.view); err != nil {
				t.Fatalf("ReportInput: %v", err)
			}

			result := <-got
			if result.err != nil {
				t.Fatalf("read frame: %v", result.err)
			}
			want := protocol.HookFrame{Input: testInput, View: tc.view}
			if !reflect.DeepEqual(result.frame, want) {
				t.Errorf("frame = %+v, want %+v", result.frame, want)
			}
		})
	}
}

func TestReportInputNoSocketFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")
	if err := ReportInput(path, testInput, protocol.ViewKey{}); err != nil {
		t.Fatalf("ReportInput with no socket file: %v, want nil", err)
	}
}

func TestReportInputStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	// Closing leaves the socket file behind, so the path exists with no listener behind it.
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket file missing: %v", err)
	}

	if err := ReportInput(path, testInput, protocol.ViewKey{}); err != nil {
		t.Fatalf("ReportInput with stale socket: %v, want nil", err)
	}
}

func TestReportInputEmptySocketPath(t *testing.T) {
	if err := ReportInput("", testInput, protocol.ViewKey{}); err != nil {
		t.Fatalf("ReportInput with empty socket path: %v, want nil", err)
	}
}

func TestReportInputFailure(t *testing.T) {
	// A regular file in place of the socket's directory makes stat fail with ENOTDIR, which is
	// neither a missing socket nor a refused connection, so it must surface as an error.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	path := filepath.Join(blocker, "h.sock")

	if err := ReportInput(path, testInput, protocol.ViewKey{}); err == nil {
		t.Fatal("ReportInput with an unreachable socket path returned nil, want an error")
	}
}
