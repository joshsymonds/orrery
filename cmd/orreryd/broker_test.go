package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/store"
)

// shutdownGuard bounds how long the test waits for the broker to return a held call once its context has ended.
const shutdownGuard = 10 * time.Second

// TestShutdownKeepsLiveClientsGrant checks that shutdown leaves a live client's grant stored for the next orreryd. The
// broker startBroker starts holds a grant for this test's own process. Its context ends, as a signal ends it, and the
// held call must return ErrClosed: a call whose own context ends first ends its grant, which deletes the stored row.
func TestShutdownKeepsLiveClientsGrant(t *testing.T) {
	const host = "box"
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "orrery.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	ctx, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	h := &sessionHost{}
	h.startBroker(ctx, host, st, slog.New(slog.DiscardHandler))
	t.Cleanup(h.stopBroker)

	argv := []string{"sh", "-c", "exit 0"}
	pool := pools.Pool{
		Name: "p", Host: host, Cap: 1, Members: []string{"m1"},
		Status: argv, StatusLimit: time.Minute,
		Wake: argv, WakeLimit: time.Minute,
		Admit: argv, AdmitLimit: time.Minute,
	}
	// The held call's context ends only where the test ends it, so the broker's close is what ends the call first.
	holdCtx, cancelHold := context.WithCancel(t.Context())
	defer cancelHold()
	granted := make(chan *grants.Grant, 1)
	result := make(chan error, 1)
	go func() {
		result <- h.broker.Acquire(holdCtx, grants.Request{
			Project: "a", Pools: []pools.Pool{pool}, Needs: []grants.Need{{Pool: "p", Count: 1}},
			ClientPID: os.Getpid(),
		}, func(e grants.Event) error {
			if e.Grant != nil {
				granted <- e.Grant
			}
			return nil
		})
	}()

	var grant *grants.Grant
	select {
	case grant = <-granted:
	case err := <-result:
		t.Fatalf("Acquire returned %v before it granted a member", err)
	case <-time.After(shutdownGuard):
		t.Fatal("Acquire granted no member within the guard")
	}
	rows, err := st.Grants(t.Context())
	if err != nil {
		t.Fatalf("read grants before shutdown: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != grant.ID {
		t.Fatalf("stored grants before shutdown = %+v, want only grant %s", rows, grant.ID)
	}

	shutdown()
	select {
	case err := <-result:
		if !errors.Is(err, grants.ErrClosed) {
			t.Fatalf("Acquire after shutdown = %v, want ErrClosed", err)
		}
	case <-time.After(shutdownGuard):
		t.Errorf("Acquire did not return after shutdown: the broker did not close")
		cancelHold()
		<-result
	}

	rows, err = st.Grants(t.Context())
	if err != nil {
		t.Fatalf("read grants after shutdown: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != grant.ID {
		t.Fatalf("stored grants after shutdown = %+v, want only grant %s", rows, grant.ID)
	}
}
