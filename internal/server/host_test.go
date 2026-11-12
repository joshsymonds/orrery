package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/hoststatus"
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
			handler := newHostHandler(t, tc.runner)

			_, err := hostClient(t, handler).Status(t.Context(), connect.NewRequest(&orreryv1.HostStatusRequest{}))
			assertRPCError(t, "Host.Status", err, tc.code, tc.message)
		})
	}
}
