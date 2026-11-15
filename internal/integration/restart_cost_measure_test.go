//go:build measure

package integration_test

import (
	"net"
	"strings"
	"testing"
	"time"
)

// restartBound is the most time from the store-open line to the first accepted connection on sessions.sock. It is the
// contract's bound (DL11) and is never tightened. It is a performance requirement of the machine the measure runs on,
// so only this measure, run on demand with `just measure-restart-cost`, checks it; the suite never times a restart.
const restartBound = 5 * time.Second

// storeOpenTime returns the time of the store-open line in stderr. slog's text handler writes it as time=.
func storeOpenTime(t *testing.T, stderr string) time.Time {
	t.Helper()
	for line := range strings.SplitSeq(stderr, "\n") {
		if !strings.Contains(line, storeOpenLog) {
			continue
		}
		_, rest, found := strings.Cut(line, "time=")
		if !found {
			t.Fatalf("store-open line %q has no time", line)
		}
		stamp, _, _ := strings.Cut(rest, " ")
		opened, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			t.Fatalf("store-open time %q: %v", stamp, err)
		}
		return opened
	}
	t.Fatalf("stderr %q holds no %s line", stderr, storeOpenLog)
	return time.Time{}
}

// acceptedAt polls the unix socket at path until a client connects, and returns the time of that connection. It fails
// the measure if the daemon exits first, or if no connection is made by deadline.
func acceptedAt(t *testing.T, daemon *orreryProcess, path string, deadline time.Time) time.Time {
	t.Helper()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		conn, err := net.Dial("unix", path)
		at := time.Now()
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Fatalf("close probe connection: %v", closeErr)
			}
			return at
		}
		if at.After(deadline) {
			t.Fatalf("%s accepted no connection by the deadline; last dial error %v; stderr %q",
				path, err, daemon.stderr.String())
		}
		select {
		case <-daemon.done:
			t.Fatalf("%s exited before %s accepted a connection; stderr %q", daemon.name, path, daemon.stderr.String())
		case <-ticker.C:
		}
	}
}

// TestMeasureRestartCost measures how soon an orreryd with --sessions restarting on a store of restartRowCount engine
// rows serves: its sessions.sock must accept a connection within restartBound of its store-open line. The time counts
// the restart's reconcile and its store writes, so it holds on an idle machine and is inflated by load; run it alone.
func TestMeasureRestartCost(t *testing.T) {
	f := newEngineFixture(t)
	daemon := startRestartDaemon(t, f)
	opened := storeOpenTime(t, daemon.stderr.String())
	elapsed := acceptedAt(t, daemon, f.sessions, opened.Add(restartBound)).Sub(opened)
	t.Logf("%s accepted a connection %v after the store-open line (bound %v)", f.sessions, elapsed, restartBound)
	if elapsed > restartBound {
		t.Fatalf("%s accepted a connection %v after the store-open line, want within %v", f.sessions, elapsed,
			restartBound)
	}
	requireRestartSettled(t, f)
	wantExit(t, daemon.stop(t), 0)
}
