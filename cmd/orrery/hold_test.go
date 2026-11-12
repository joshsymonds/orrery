package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// holdPools is an in-process Pools service for the hold tests. It records each Hold and Free as "Hold MEMBER" or
// "Free MEMBER", and refuses every call with refuse when that is set.
type holdPools struct {
	enginev1connect.UnimplementedPoolsHandler

	refuse connect.Code

	mu    sync.Mutex
	calls []string
}

// received returns the calls the fake has received, in order.
func (f *holdPools) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// record appends call and returns the error the fake refuses with, if any.
func (f *holdPools) record(call string) error {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if f.refuse != 0 {
		return connect.NewError(f.refuse, errors.New("fake pools refused the request"))
	}
	return nil
}

// Hold implements enginev1connect.PoolsHandler.
func (f *holdPools) Hold(
	_ context.Context,
	req *connect.Request[v1.PoolsHoldRequest],
) (*connect.Response[v1.PoolsHoldResponse], error) {
	if err := f.record("Hold " + req.Msg.GetMember()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.PoolsHoldResponse{}), nil
}

// Free implements enginev1connect.PoolsHandler.
func (f *holdPools) Free(
	_ context.Context,
	req *connect.Request[v1.PoolsFreeRequest],
) (*connect.Response[v1.PoolsFreeResponse], error) {
	if err := f.record("Free " + req.Msg.GetMember()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.PoolsFreeResponse{}), nil
}

// serveHoldPools serves fake as Pools on the session socket beside a daemon socket in a temporary directory. It
// returns the daemon socket path that `--socket` names. The server is closed at cleanup.
func serveHoldPools(t *testing.T, fake *holdPools) string {
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

// TestHoldSendsHold sends Hold for a member the pool files declare. It exits 0 and writes nothing.
func TestHoldSendsHold(t *testing.T) {
	setupProject(t, buildsPoolFile, "")
	fake := &holdPools{}
	daemon := serveHoldPools(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "hold", "builds-1"))
	if res.code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Hold builds-1"}) {
		t.Errorf("orreryd received %q, want [Hold builds-1]", got)
	}
	if stdout.Len() != 0 || res.stderr != "" {
		t.Errorf("hold wrote stdout %q and stderr %q, want neither", stdout.String(), res.stderr)
	}
}

// TestHoldRefusesUndeclaredMember exits 1 with the reason when the pool files do not declare the member. Nothing is
// sent to orreryd, and the refusal is not a usage error.
func TestHoldRefusesUndeclaredMember(t *testing.T) {
	setupProject(t, buildsPoolFile, "")
	fake := &holdPools{}
	daemon := serveHoldPools(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "hold", "m9"))
	if res.code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", res.code, exitFailure, res.stderr)
	}
	if !strings.HasPrefix(res.stderr, "orrery hold: ") || !strings.Contains(res.stderr, `"m9"`) {
		t.Errorf("stderr %q, want the reason prefixed with orrery hold:", res.stderr)
	}
	if strings.Contains(res.stderr, usageText) {
		t.Errorf("stderr %q carries the usage text; a refusal is not a usage error", res.stderr)
	}
	if got := fake.received(); len(got) != 0 {
		t.Errorf("orreryd received %q, want nothing", got)
	}
}

// TestHoldOffSendsFree sends Free for the member. --off reads no pool files, so a member the files do not declare is
// freed too.
func TestHoldOffSendsFree(t *testing.T) {
	cases := []struct {
		name    string
		project string
		member  string
	}{
		{"declared member", buildsPoolFile, "builds-1"},
		{"member the files do not declare", "", "m9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupProject(t, tc.project, "")
			fake := &holdPools{}
			daemon := serveHoldPools(t, fake)
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "hold", "--off", tc.member))
			if res.code != exitOK {
				t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
			}
			want := []string{"Free " + tc.member}
			if got := fake.received(); !slices.Equal(got, want) {
				t.Errorf("orreryd received %q, want %q", got, want)
			}
			if stdout.Len() != 0 || res.stderr != "" {
				t.Errorf("hold wrote stdout %q and stderr %q, want neither", stdout.String(), res.stderr)
			}
		})
	}
}

// TestHoldRefusedByOrreydExitsOne exits 1 with orreryd's reason on stderr when orreryd refuses the call.
func TestHoldRefusedByOrreydExitsOne(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		refuse connect.Code
		want   string
		call   string
	}{
		{"free of a member not held", []string{"hold", "--off", "builds-1"}, connect.CodeNotFound, "not_found",
			"Free builds-1"},
		{"hold refused as invalid", []string{"hold", "builds-1"}, connect.CodeInvalidArgument, "invalid_argument",
			"Hold builds-1"},
		{"hold while orreryd has no store", []string{"hold", "builds-1"}, connect.CodeUnavailable, "unavailable",
			"Hold builds-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupProject(t, buildsPoolFile, "")
			fake := &holdPools{refuse: tc.refuse}
			daemon := serveHoldPools(t, fake)
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, append([]string{"--socket", daemon}, tc.args...)...))
			if res.code != exitFailure {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitFailure, res.stderr)
			}
			if !strings.HasPrefix(res.stderr, "orrery hold: ") || !strings.Contains(res.stderr, tc.want) {
				t.Errorf("stderr %q, want the reason %q prefixed with orrery hold:", res.stderr, tc.want)
			}
			if got := fake.received(); !slices.Equal(got, []string{tc.call}) {
				t.Errorf("orreryd received %q, want [%s]", got, tc.call)
			}
		})
	}
}

// TestHoldWithoutOrreydExitsOne exits 1 with the reason when nothing listens on the session socket.
func TestHoldWithoutOrreydExitsOne(t *testing.T) {
	setupProject(t, buildsPoolFile, "")
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "hold", "builds-1"))
	if res.code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", res.code, exitFailure, res.stderr)
	}
	if !strings.HasPrefix(res.stderr, "orrery hold: ") {
		t.Errorf("stderr %q, want the reason prefixed with orrery hold:", res.stderr)
	}
}

// TestHoldUsageErrorsExitTwo refuses each command line whose arguments are wrong. Each exits 2 with the usage text and
// sends nothing.
func TestHoldUsageErrorsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no member", []string{"hold"}},
		{"no member after --off", []string{"hold", "--off"}},
		{"two members", []string{"hold", "builds-1", "builds-2"}},
		{"unknown flag", []string{"hold", "--on", "builds-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupProject(t, buildsPoolFile, "")
			fake := &holdPools{}
			daemon := serveHoldPools(t, fake)
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, append([]string{"--socket", daemon}, tc.args...)...))
			if res.code != exitUsage {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
			if !strings.HasPrefix(res.stderr, "orrery: hold: ") {
				t.Errorf("stderr %q, want a usage error naming hold", res.stderr)
			}
			if !strings.HasSuffix(res.stderr, usageText) {
				t.Errorf("stderr does not end with the usage text: %q", res.stderr)
			}
			if got := fake.received(); len(got) != 0 {
				t.Errorf("orreryd received %q, want nothing", got)
			}
		})
	}
}

// TestUsageTextListsHold lists the hold subcommand and its arguments in the usage text.
func TestUsageTextListsHold(t *testing.T) {
	if !strings.Contains(usageText, "\n  hold [--off] MEMBER") {
		t.Errorf("usage text does not list hold:\n%s", usageText)
	}
}
