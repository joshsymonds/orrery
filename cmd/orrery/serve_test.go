package main

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	serveTestLogin = "user@example.test"
	serveTestEvent = "orrery-1"
	// serveTestWait bounds every wait on the serve loop or the stream. It is a timeout, not a sleep: each wait returns
	// as soon as the awaited state is observed.
	serveTestWait = 10 * time.Second
)

// serveLoginTransport sends each request with the login and client headers the guard requires.
type serveLoginTransport struct {
	base http.RoundTripper
}

// RoundTrip sends a copy of req with the guard headers set.
func (t serveLoginTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set(server.HeaderLogin, serveTestLogin)
	clone.Header.Set(server.HeaderClient, "synthetic-client")
	return t.base.RoundTrip(clone)
}

// TestServeEndsOpenStreamAtShutdown runs the serve loop on an ephemeral loopback listener over a store that holds
// events. A Watch stream opened after the cursor receives its first message. Canceling the serve context must end
// the stream with Unavailable, and the serve loop must return nil well within serveShutdownTimeout.
func TestServeEndsOpenStreamAtShutdown(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "orrery.db")
	writer, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	at := time.Unix(1_700_000_000, 0)
	if err := writer.SyncServer(ctx, "-L a", []store.Pane{{ID: serveTestEvent, Pane: "%1"}}, at); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	busy := store.Pane{ID: serveTestEvent, Pane: "%1", Fields: store.Fields{State: "busy"}}
	if err := writer.SyncPane(ctx, "-L a", busy, at); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	tmux, err := tmuxctl.New(tmuxctl.Server{Name: "orrery-serve-test-unstarted"})
	if err != nil {
		t.Fatalf("tmuxctl.New: %v", err)
	}
	pushStore, err := push.NewStore(filepath.Join(t.TempDir(), "push.json"), []string{push.DefaultPushHost})
	if err != nil {
		t.Fatalf("push.NewStore: %v", err)
	}
	shutdown := make(chan struct{})
	handler, err := server.New(server.Config{
		Logins:   []string{serveTestLogin},
		Tmux:     tmux,
		Push:     pushStore,
		Sessions: store.NewReader(dbPath),
		Shutdown: shutdown,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	serveCtx, cancelServe := context.WithCancel(ctx)
	serveDone := make(chan struct{})
	var serveErr error
	go func() {
		serveErr = serveUntilDone(serveCtx, ln, handler, shutdown)
		close(serveDone)
	}()
	t.Cleanup(func() {
		cancelServe()
		select {
		case <-serveDone:
		case <-time.After(serveTestWait):
			t.Error("serve loop did not return before the test ended")
		}
	})

	events := orreryv1connect.NewEventsClient(&http.Client{Transport: serveLoginTransport{base: http.DefaultTransport}},
		"http://"+ln.Addr().String())
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, err := events.Watch(streamCtx, connect.NewRequest(&orreryv1.WatchRequest{After: 0}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	first := make(chan struct{}, 1)
	streamDone := make(chan error, 1)
	go func() {
		signaled := false
		for stream.Receive() {
			if !signaled {
				first <- struct{}{}
				signaled = true
			}
		}
		streamDone <- stream.Err()
	}()
	select {
	case <-first:
	case <-time.After(serveTestWait):
		t.Fatalf("no message on the Watch stream within %v", serveTestWait)
	}

	started := time.Now()
	cancelServe()
	select {
	case <-serveDone:
	case <-time.After(serveTestWait):
		t.Fatalf("serve did not return within %v", serveTestWait)
	}
	if serveErr != nil {
		t.Fatalf("serve returned %v, want nil", serveErr)
	}
	if elapsed := time.Since(started); elapsed >= serveShutdownTimeout {
		t.Fatalf("serve returned after %v, want it well within the %v shutdown budget", elapsed, serveShutdownTimeout)
	}

	select {
	case err := <-streamDone:
		if got := connect.CodeOf(err); got != connect.CodeUnavailable {
			t.Fatalf("stream ended with code %v (%v), want %v", got, err, connect.CodeUnavailable)
		}
	case <-time.After(serveTestWait):
		t.Fatalf("Watch stream did not end within %v of shutdown", serveTestWait)
	}
}
