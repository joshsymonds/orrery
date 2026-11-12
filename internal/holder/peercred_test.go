package holder

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPeerUIDBothEnds(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "peer.sock")
	addr := &net.UnixAddr{Name: sock, Net: "unix"}

	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	dialed, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer dialed.Close()

	accepted, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()

	want := os.Getuid()
	for _, side := range []struct {
		name string
		conn *net.UnixConn
	}{
		{"accepted", accepted},
		{"dialed", dialed},
	} {
		got, err := PeerUID(side.conn)
		if err != nil {
			t.Fatalf("%s: PeerUID: %v", side.name, err)
		}
		if got != want {
			t.Errorf("%s: PeerUID = %d, want os.Getuid() = %d", side.name, got, want)
		}
	}
}

func TestPeerUIDClosedConn(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "closed.sock")
	addr := &net.UnixAddr{Name: sock, Net: "unix"}

	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	dialed, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()

	if err := dialed.Close(); err != nil {
		t.Fatal(err)
	}
	if uid, err := PeerUID(dialed); err == nil {
		t.Errorf("PeerUID on closed conn = %d, want an error", uid)
	}
}
