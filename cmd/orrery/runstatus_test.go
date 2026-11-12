package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// statusPools is an in-process Pools service for the run status tests. Its Status returns resp and counts the calls.
type statusPools struct {
	enginev1connect.UnimplementedPoolsHandler

	resp *v1.PoolsStatusResponse

	mu    sync.Mutex
	calls int
}

// Status implements enginev1connect.PoolsHandler.
func (f *statusPools) Status(
	_ context.Context,
	_ *connect.Request[v1.PoolsStatusRequest],
) (*connect.Response[v1.PoolsStatusResponse], error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return connect.NewResponse(f.resp), nil
}

// received returns how many Status calls the fake has received.
func (f *statusPools) received() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// serveStatusPools serves fake as Pools on the session socket beside a daemon socket in a temporary directory. It
// returns the daemon socket path that `--socket` names. The server is closed at cleanup.
func serveStatusPools(t *testing.T, fake *statusPools) string {
	t.Helper()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	ln, err := net.Listen("unix", protocol.SessionSocketPath(daemon))
	if err != nil {
		t.Fatalf("listen on the session socket: %v", err)
	}
	mux := http.NewServeMux()
	path, handler := enginev1connect.NewPoolsHandler(fake)
	mux.Handle(path, handler)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close pools server: %v", err)
		}
	})
	return daemon
}

// statusResponse is the status of one pool, builds, with builds-1 granted to a project for a minute and builds-2 free.
func statusResponse() *v1.PoolsStatusResponse {
	return &v1.PoolsStatusResponse{
		Pools: []*v1.PoolStatus{{
			Name: "builds",
			Cap:  2,
			Members: []*v1.PoolMemberStatus{
				{
					Name:  "builds-1",
					State: "granted",
					Holder: &v1.PoolHolder{
						GrantId:   "grant-1",
						Project:   "/work/app",
						ClientPid: 42,
						Command:   []string{"make", "all"},
						Since:     time.Now().Add(-90 * time.Second).Unix(),
					},
				},
				{Name: "builds-2", State: "free"},
			},
		}},
	}
}

// TestRunStatusPrintsPools prints each pool and each of its members from orreryd's status, and exits 0.
func TestRunStatusPrintsPools(t *testing.T) {
	setupProject(t, "", "")
	fake := &statusPools{resp: statusResponse()}
	daemon := serveStatusPools(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "run", "status"))
	if res.code != 0 {
		t.Fatalf("exit status %d, want 0; stderr %q", res.code, res.stderr)
	}
	if got := fake.received(); got != 1 {
		t.Fatalf("pools received %d status calls, want 1", got)
	}
	lines := strings.Split(stdout.String(), "\n")
	if !hasLine(lines, "builds (cap 2)") {
		t.Errorf("stdout %q does not name the pool with its cap", stdout.String())
	}
	if !hasLineWith(lines, "  builds-1 ", "granted /work/app pid 42 make all 1m") {
		t.Errorf("stdout %q does not show builds-1 granted to its holder", stdout.String())
	}
	if !hasLineWith(lines, "  builds-2 ", "free") {
		t.Errorf("stdout %q does not show builds-2 free", stdout.String())
	}
}

// TestRunStatusReadsNoPoolFiles prints the status when the pool files do not parse, since status reads none.
func TestRunStatusReadsNoPoolFiles(t *testing.T) {
	setupProject(t, badPoolFile, "")
	fake := &statusPools{resp: statusResponse()}
	daemon := serveStatusPools(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "run", "status"))
	if res.code != 0 {
		t.Fatalf("exit status %d, want 0; stderr %q", res.code, res.stderr)
	}
	if !strings.Contains(stdout.String(), "builds (cap 2)") {
		t.Errorf("stdout %q does not print the pool status", stdout.String())
	}
}

// TestRunStatusExtraArgumentExitsTwo refuses an argument after status with a usage error. It asks orreryd nothing.
func TestRunStatusExtraArgumentExitsTwo(t *testing.T) {
	setupProject(t, "", "")
	fake := &statusPools{resp: statusResponse()}
	daemon := serveStatusPools(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "run", "status", "extra"))
	if res.code != exitUsage {
		t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
	}
	first, _, _ := strings.Cut(res.stderr, "\n")
	if !strings.HasPrefix(first, "orrery: run status: ") || !strings.Contains(first, `"extra"`) {
		t.Errorf("first stderr line %q, want run status's usage error naming %q", first, "extra")
	}
	if !strings.HasSuffix(res.stderr, usageText) {
		t.Errorf("stderr does not end with the usage text: %q", res.stderr)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout %q, want none", stdout.String())
	}
	if got := fake.received(); got != 0 {
		t.Errorf("pools received %d status calls, want 0", got)
	}
}

// TestRunStatusWithoutOrreydExitsOne exits 1 with the reason on stderr when nothing listens on the session socket.
func TestRunStatusWithoutOrreydExitsOne(t *testing.T) {
	setupProject(t, "", "")
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "run", "status"))
	if res.code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", res.code, exitFailure, res.stderr)
	}
	if !strings.HasPrefix(res.stderr, "orrery: run status: pool status: ") {
		t.Errorf("stderr %q does not say why the status failed", res.stderr)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout %q, want none", stdout.String())
	}
}

// hasLine reports whether one of lines is exactly want.
func hasLine(lines []string, want string) bool {
	return slices.Contains(lines, want)
}

// hasLineWith reports whether one of lines starts with prefix and then ends with suffix.
func hasLineWith(lines []string, prefix, suffix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, suffix) {
			return true
		}
	}
	return false
}
