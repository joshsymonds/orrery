package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/server"
)

// hostFixtureOutput is the host status command output of the parity fixture: one stat with a percent and one
// without, so the percent is present on the first and absent on the second.
const hostFixtureOutput = `[{"label":"cpu","value":"12%","percent":12.5,"level":"ok"},` +
	`{"label":"uptime","value":"3d","level":"warn"}]`

// hostClient returns a generated Host client on an httptest server of handler.
func hostClient(t *testing.T, handler http.Handler) orreryv1connect.HostClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewHostClient(httpClient, srv.URL)
}

// TestHostStatusParity answers the fixture through Host.Status and checks the whole response against the
// fixture's concrete values: the machine's host name, and each stat with its percent present exactly where the
// command gave one.
func TestHostStatusParity(t *testing.T) {
	t.Parallel()
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("Hostname: %v", err)
	}
	handler := newHostHandler(t, hoststatus.New(writeHostStatusScript(t, hostFixtureOutput, 0), time.Now))

	got, err := hostClient(t, handler).Status(t.Context(), connect.NewRequest(&orreryv1.HostStatusRequest{}))
	if err != nil {
		t.Fatalf("Host.Status: %v", err)
	}
	percent := 12.5
	want := &orreryv1.HostStatusResponse{
		Host: host,
		Stats: []*orreryv1.HostStat{
			{Label: "cpu", Value: "12%", Percent: &percent, Level: "ok"},
			{Label: "uptime", Value: "3d", Level: "warn"},
		},
	}
	if !proto.Equal(want, got.Msg) {
		t.Fatalf("Host.Status = %v, want %v", got.Msg, want)
	}
}

// TestHostStatusErrorParity checks that each failure Host.Status answers carries its concrete Connect code and
// message.
func TestHostStatusErrorParity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		runner  *hoststatus.Runner
		code    connect.Code
		message string
	}{
		{
			name:    "no host status command configured",
			runner:  nil,
			code:    connect.CodeNotFound,
			message: "404 page not found",
		},
		{
			name:    "host status command exits non-zero",
			runner:  hoststatus.New(writeHostStatusScript(t, "", 3), time.Now),
			code:    connect.CodeUnavailable,
			message: "host status unavailable",
		},
		{
			name:    "host status command prints invalid stats",
			runner:  hoststatus.New(writeHostStatusScript(t, "not json", 0), time.Now),
			code:    connect.CodeUnavailable,
			message: "host status unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler := newHostHandler(t, tc.runner)

			_, err := hostClient(t, handler).Status(t.Context(), connect.NewRequest(&orreryv1.HostStatusRequest{}))
			assertRPCError(t, "Host.Status", err, tc.code, tc.message)
		})
	}
}

// TestHostStatusNamesThePageBuild answers Host.Status over a page whose build.txt names its build, trimmed, and over
// the same page without one, which names no build, so a page never takes a missing build for a changed one.
func TestHostStatusNamesThePageBuild(t *testing.T) {
	t.Parallel()
	withBuild := pageFS()
	withBuild["build.txt"] = &fstest.MapFile{Data: []byte("synthetic-build-1\n")}
	for _, tc := range []struct {
		name string
		web  fstest.MapFS
		want string
	}{
		{name: "build.txt names the build", web: withBuild, want: "synthetic-build-1"},
		{name: "no build.txt", web: pageFS(), want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler, err := server.New(server.Config{
				Logins:     []string{allowedLogin},
				Tmux:       newTmuxClient(t),
				Push:       newPushStore(t),
				HostStatus: hoststatus.New(writeHostStatusScript(t, hostFixtureOutput, 0), time.Now),
				Web:        tc.web,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err := hostClient(t, handler).Status(t.Context(),
				connect.NewRequest(&orreryv1.HostStatusRequest{}))
			if err != nil {
				t.Fatalf("Host.Status: %v", err)
			}
			if got.Msg.GetPageBuild() != tc.want {
				t.Errorf("page_build = %q, want %q", got.Msg.GetPageBuild(), tc.want)
			}
		})
	}
}
