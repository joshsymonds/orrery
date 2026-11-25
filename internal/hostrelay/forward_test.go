package hostrelay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

// streamPath is the path that the forwarder tests request.
const streamPath = "/stream"

// startForward serves Forward on a unix socket in a temporary directory, relaying to address, until the test ends. It
// returns the socket's path. The test fails when Forward returns an error.
func startForward(t *testing.T, address string) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	forwarded := make(chan error, 1)
	go func() { forwarded <- Forward(ctx, ln, ForwardConfig{Address: address}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-forwarded; err != nil {
			t.Error(err)
		}
	})
	return sock
}

// unixClient returns an HTTP client whose every connection dials the unix socket at sock.
func unixClient(t *testing.T, sock string) *http.Client {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, "unix", sock)
			if err != nil {
				return nil, fmt.Errorf("dial %s: %w", sock, err)
			}
			return conn, nil
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

// TestNodeRelayForwardStreams checks that Forward streams a server stream: the first message reaches the client before
// the upstream sends the second, and the stream ends after the second. The upstream holds the stream open until the
// client has read the first message, so a forwarder that buffers the response never delivers it.
func TestNodeRelayForwardStreams(t *testing.T) {
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if _, err := io.WriteString(w, "first\n"); err != nil {
			t.Errorf("write first message: %v", err)
			return
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush first message: %v", err)
			return
		}
		select {
		case <-release:
		case <-req.Context().Done():
			return
		}
		if _, err := io.WriteString(w, "second\n"); err != nil {
			t.Errorf("write second message: %v", err)
		}
	}))
	// Cleanups run last in first-registered order: the release comes before upstream.Close, which waits for the handler.
	t.Cleanup(upstream.Close)
	t.Cleanup(releaseOnce)

	sock := startForward(t, upstream.Listener.Addr().String())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://forward"+streamPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := unixClient(t, sock).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want %d", resp.StatusCode, http.StatusOK)
	}
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first message: %v", err)
	}
	if first != "first\n" {
		t.Fatalf("first message = %q, want %q", first, "first\n")
	}
	releaseOnce()
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read the rest of the stream: %v", err)
	}
	if string(rest) != "second\n" {
		t.Fatalf("rest of the stream = %q, want %q", rest, "second\n")
	}
	if err := resp.Body.Close(); err != nil {
		t.Error(err)
	}
}

// TestNodeRelayForwardUnreachable checks that Forward answers 502 when the controller it relays to has no listener.
func TestNodeRelayForwardUnreachable(t *testing.T) {
	sock := startForward(t, freeLoopbackAddr(t))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://forward"+streamPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := unixClient(t, sock).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Error(err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}
