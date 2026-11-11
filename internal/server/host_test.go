package server_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/hoststatus"
)

// hostBody is the GET /v1/host body, decoded by the keys its JSON tags name.
type hostBody struct {
	Host  string            `json:"host"`
	Stats []hoststatus.Stat `json:"stats"`
}

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

// TestHostStatusParity answers one fixture through one handler on both the REST route and Host.Status. The host
// and every stat field agree, and the percent is present exactly where the command gave one.
func TestHostStatusParity(t *testing.T) {
	handler := newHostHandler(t, hoststatus.New(writeHostStatusScript(t, hostFixtureOutput, 0), time.Now))

	rec := get(t, handler, hostPath, allowedLogin)
	assertStatus(t, rec, http.StatusOK)
	var rest hostBody
	if err := json.Unmarshal(rec.Body.Bytes(), &rest); err != nil {
		t.Fatalf("decode GET /v1/host %q: %v", rec.Body.String(), err)
	}

	got, err := hostClient(t, handler).Status(t.Context(), connect.NewRequest(&orreryv1.HostStatusRequest{}))
	if err != nil {
		t.Fatalf("Host.Status: %v", err)
	}
	msg := got.Msg
	if msg.GetHost() != rest.Host {
		t.Fatalf("Host.Status host = %q, want the REST host %q", msg.GetHost(), rest.Host)
	}
	if len(msg.GetStats()) != len(rest.Stats) {
		t.Fatalf("Host.Status has %d stats, want the REST %d", len(msg.GetStats()), len(rest.Stats))
	}
	for i, want := range rest.Stats {
		stat := msg.GetStats()[i]
		if stat.GetLabel() != want.Label || stat.GetValue() != want.Value || stat.GetLevel() != want.Level {
			t.Fatalf("Host.Status stat %d = %+v, want the REST stat %+v", i, stat, want)
		}
		if !reflect.DeepEqual(stat.Percent, want.Percent) {
			t.Fatalf("Host.Status stat %d percent = %v, want the REST percent %v", i, stat.Percent, want.Percent)
		}
	}
	if rest.Stats[0].Percent == nil || rest.Stats[1].Percent != nil {
		t.Fatalf("fixture percents = %v and %v, want a percent on the first stat only",
			rest.Stats[0].Percent, rest.Stats[1].Percent)
	}
}

// TestHostStatusErrorParity checks that each REST status Host.Status can answer in a test carries the table's
// Connect code and the REST body without its trailing newline as its message.
func TestHostStatusErrorParity(t *testing.T) {
	cases := []struct {
		name   string
		runner *hoststatus.Runner
		status int
		code   connect.Code
	}{
		{
			name:   "no host status command configured",
			runner: nil,
			status: http.StatusNotFound,
			code:   connect.CodeNotFound,
		},
		{
			name:   "host status command exits non-zero",
			runner: hoststatus.New(writeHostStatusScript(t, "", 3), time.Now),
			status: http.StatusBadGateway,
			code:   connect.CodeUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := newHostHandler(t, tc.runner)

			rec := get(t, handler, hostPath, allowedLogin)
			assertStatus(t, rec, tc.status)
			wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

			_, err := hostClient(t, handler).Status(t.Context(), connect.NewRequest(&orreryv1.HostStatusRequest{}))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Host.Status error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != tc.code {
				t.Fatalf("Host.Status code = %v, want %v", connectErr.Code(), tc.code)
			}
			if connectErr.Message() != wantMessage {
				t.Fatalf("Host.Status message = %q, want the REST body %q", connectErr.Message(), wantMessage)
			}
		})
	}
}
