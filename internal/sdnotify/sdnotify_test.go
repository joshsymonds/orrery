package sdnotify

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

// readOneDatagram listens on a unixgram socket and returns the single datagram Ready sends. It fails the test if
// no datagram arrives or if a second one follows.
func readOneDatagram(t *testing.T, ln *net.UnixConn, send func() error) string {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- send() }()

	if err := ln.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 64)
	n, _, err := ln.ReadFromUnix(buf)
	if err != nil {
		t.Fatalf("ReadFromUnix: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("Ready: %v", err)
	}

	if err := ln.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if extra, _, err := ln.ReadFromUnix(buf); err == nil {
		t.Fatalf("got a second datagram %q, want exactly one", buf[:extra])
	}
	return string(buf[:n])
}

func TestReadyPathSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.sock")
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatalf("ListenUnixgram: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	got := readOneDatagram(t, ln, func() error { return Ready(path) })
	if got != "READY=1" {
		t.Fatalf("datagram = %q, want %q", got, "READY=1")
	}
}

func TestReadyAbstractSocket(t *testing.T) {
	// The abstract name is the test's own temporary directory, whose parent MkdirTemp names at random, so concurrent
	// runs on the host never share a socket in the abstract namespace.
	name := t.TempDir()
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: "\x00" + name, Net: "unixgram"})
	if err != nil {
		t.Fatalf("ListenUnixgram: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	got := readOneDatagram(t, ln, func() error { return Ready("@" + name) })
	if got != "READY=1" {
		t.Fatalf("datagram = %q, want %q", got, "READY=1")
	}
}

func TestReadyErrors(t *testing.T) {
	t.Run("no listener at path", func(t *testing.T) {
		if err := Ready(filepath.Join(t.TempDir(), "missing.sock")); err == nil {
			t.Fatal("Ready with no listener returned nil, want error")
		}
	})
	t.Run("empty address", func(t *testing.T) {
		if err := Ready(""); err == nil {
			t.Fatal("Ready with empty addr returned nil, want error")
		}
	})
}
