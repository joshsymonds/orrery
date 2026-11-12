package cli_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// poolStatusFake is an in-process Pools. Its Status returns resp, or err when err is set.
type poolStatusFake struct {
	enginev1connect.UnimplementedPoolsHandler
	resp *v1.PoolsStatusResponse
	err  error
}

// Status implements enginev1connect.PoolsHandler.
func (f *poolStatusFake) Status(
	_ context.Context,
	_ *connect.Request[v1.PoolsStatusRequest],
) (*connect.Response[v1.PoolsStatusResponse], error) {
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.resp), nil
}

// poolStatusClient serves fake as Pools on a unix socket at the session socket path beside a daemon socket in a
// short temp directory, and returns a client for it. The server is closed at cleanup.
func poolStatusClient(t *testing.T, fake *poolStatusFake) enginev1connect.PoolsClient {
	t.Helper()
	socket := protocol.SessionSocketPath(filepath.Join(t.TempDir(), "orreryd.sock"))
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
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
	return enginev1connect.NewPoolsClient(cli.SessionHTTPClient(socket), cli.SessionBaseURL)
}

func TestPoolStatusRendersPoolsAndOutsideHolds(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ago := func(d time.Duration) int64 { return now.Add(-d).Unix() }
	fake := &poolStatusFake{resp: &v1.PoolsStatusResponse{
		Pools: []*v1.PoolStatus{
			{
				Name: "builds",
				Cap:  2,
				Members: []*v1.PoolMemberStatus{
					{
						Name:  "m1",
						State: "awake",
						Holder: &v1.PoolHolder{
							GrantId:   "g-1",
							Project:   "/work/orrery",
							ClientPid: 4242,
							Command:   []string{"make", "test"},
							Since:     ago(12 * time.Minute),
						},
					},
					{Name: "m2", State: "asleep", HeldSince: ago(3 * time.Minute)},
					{
						Name:      "m3",
						State:     "awake",
						HeldSince: ago(30 * time.Second),
						Holder: &v1.PoolHolder{
							GrantId:   "g-2",
							Project:   "/work/app",
							ClientPid: 77,
							Command:   []string{"npm", "run", "dev"},
							Since:     ago(4 * time.Hour),
							Stopping:  true,
						},
					},
					{Name: "m4", State: "asleep"},
				},
				Waiters: []*v1.PoolWaiter{
					{
						Position: 1,
						Project:  "/work/other",
						Command:  []string{"npm", "test"},
						Count:    2,
						Since:    ago(5 * time.Second),
					},
					{
						Position: 2,
						Project:  "/work/third",
						Command:  []string{"go", "test", "./..."},
						Count:    1,
						Since:    ago(50 * time.Hour),
					},
				},
			},
			{
				Name: "deploy",
				Cap:  1,
				Members: []*v1.PoolMemberStatus{
					{Name: "d1", State: "asleep"},
				},
			},
		},
		Holds: []*v1.PoolHold{
			{Member: "m2", Since: ago(3 * time.Minute)},
			{Member: "spare", Since: ago(time.Hour)},
		},
	}}

	var buf bytes.Buffer
	if err := cli.PoolStatus(context.Background(), poolStatusClient(t, fake), now, &buf); err != nil {
		t.Fatalf("PoolStatus: %v", err)
	}

	want := strings.Join([]string{
		"builds (cap 2)",
		"  m1           awake        granted /work/orrery pid 4242 make test 12m",
		"  m2           asleep       held 3m",
		"  m3           awake        granted /work/app pid 77 npm run dev 4h; held 30s",
		"  m4           asleep       free",
		"  waiters",
		"    1  /work/other  npm test  count 2  5s",
		"    2  /work/third  go test ./...  count 1  2d",
		"",
		"deploy (cap 1)",
		"  d1           asleep       free",
		"",
		"holds outside pools in use",
		"  spare        held 1h",
		"",
	}, "\n")
	if got := buf.String(); got != want {
		t.Errorf("PoolStatus output:\n%s\nwant:\n%s", got, want)
	}
}

func TestPoolStatusEmptyPrintsNoPoolsInUse(t *testing.T) {
	fake := &poolStatusFake{resp: &v1.PoolsStatusResponse{}}
	now := time.Unix(1_800_000_000, 0)

	var buf bytes.Buffer
	if err := cli.PoolStatus(context.Background(), poolStatusClient(t, fake), now, &buf); err != nil {
		t.Fatalf("PoolStatus: %v", err)
	}
	if got, want := buf.String(), "no pools in use\n"; got != want {
		t.Errorf("PoolStatus output = %q, want %q", got, want)
	}
}

func TestPoolStatusErrorNamesPoolStatus(t *testing.T) {
	fake := &poolStatusFake{err: connect.NewError(connect.CodeUnavailable, errors.New("orreryd has no store"))}

	var buf bytes.Buffer
	err := cli.PoolStatus(context.Background(), poolStatusClient(t, fake), time.Unix(1_800_000_000, 0), &buf)
	if err == nil || !strings.Contains(err.Error(), "pool status") {
		t.Fatalf("PoolStatus error = %v, want an error naming pool status", err)
	}
}
