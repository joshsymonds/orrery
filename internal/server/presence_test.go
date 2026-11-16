package server_test

import (
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
)

// presenceEpochAge is the age in milliseconds of an input dated at the unix epoch, given readFixtureAt as Now.
const presenceEpochAge int64 = readFixtureAt * 1000

// newPresenceClient builds the guarded handler that admits allowedLogin, dates inputs at readFixtureAt, and reports
// each input to report. It returns a Presence client whose requests carry allowedLogin and the client header.
func newPresenceClient(t *testing.T, report server.InputReporter) orreryv1connect.PresenceClient {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:      []string{allowedLogin},
		Tmux:        newTmuxClient(t),
		Push:        newPushStore(t),
		Now:         func() time.Time { return time.Unix(readFixtureAt, 0) },
		ReportInput: report,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewPresenceClient(httpClient, srv.URL)
}

func TestPresenceReportDatesInputByClock(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		age  int64
		want []int64
	}{
		{"age 1500 ms dates the input 1.5 s before Now, floored", 1500, []int64{readFixtureAt - 2}},
		{"age 0 dates the input at Now", 0, []int64{readFixtureAt}},
		{"age reaching the epoch reports nothing", presenceEpochAge, nil},
		{"age past the epoch reports nothing", presenceEpochAge + 1, nil},
		{"huge age reports nothing", math.MaxInt64, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []int64
			client := newPresenceClient(t, func(input int64, _ protocol.ViewKey) { got = append(got, input) })
			_, err := client.Report(
				t.Context(),
				connect.NewRequest(&orreryv1.PresenceReportRequest{InputAgeMs: tc.age}),
			)
			if err != nil {
				t.Fatalf("Report(%d): %v", tc.age, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Report(%d) reported %v, want %v", tc.age, got, tc.want)
			}
		})
	}
}

func TestPresenceReportRefusesNegativeAge(t *testing.T) {
	t.Parallel()
	var got []int64
	client := newPresenceClient(t, func(input int64, _ protocol.ViewKey) { got = append(got, input) })
	_, err := client.Report(t.Context(), connect.NewRequest(&orreryv1.PresenceReportRequest{InputAgeMs: -1}))
	if err == nil {
		t.Fatal("Report(-1) answered OK, want invalid_argument")
	}
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("Report(-1) code = %v, want %v", code, connect.CodeInvalidArgument)
	}
	if len(got) != 0 {
		t.Fatalf("Report(-1) reported %v, want nothing", got)
	}
}

func TestPresenceReportWithoutReporterAnswersOK(t *testing.T) {
	t.Parallel()
	client := newPresenceClient(t, nil)
	_, err := client.Report(t.Context(), connect.NewRequest(&orreryv1.PresenceReportRequest{InputAgeMs: 1500}))
	if err != nil {
		t.Fatalf("Report(1500) with no reporter: %v", err)
	}
}

// presenceReport is one call a Presence.Report made on its reporter.
type presenceReport struct {
	input int64
	view  protocol.ViewKey
}

func TestPresenceReportViewingKeyPassesToReporter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  *orreryv1.PresenceReportRequest
		want protocol.ViewKey
	}{
		{
			"viewing pane",
			&orreryv1.PresenceReportRequest{
				InputAgeMs: 1500,
				Viewing:    &orreryv1.PresenceReportRequest_ViewingPane{ViewingPane: "%synthetic"},
			},
			protocol.ViewKey{Pane: "%synthetic"},
		},
		{
			"viewing session",
			&orreryv1.PresenceReportRequest{
				InputAgeMs: 1500,
				Viewing:    &orreryv1.PresenceReportRequest_ViewingSession{ViewingSession: "synthetic-session"},
			},
			protocol.ViewKey{Session: "synthetic-session"},
		},
		{
			"viewing epic",
			&orreryv1.PresenceReportRequest{
				InputAgeMs: 1500,
				Viewing:    &orreryv1.PresenceReportRequest_ViewingEpic{ViewingEpic: "synthetic-epic"},
			},
			protocol.ViewKey{Epic: "synthetic-epic"},
		},
		{"no viewing key", &orreryv1.PresenceReportRequest{InputAgeMs: 1500}, protocol.ViewKey{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []presenceReport
			client := newPresenceClient(t, func(input int64, view protocol.ViewKey) {
				got = append(got, presenceReport{input: input, view: view})
			})
			_, err := client.Report(t.Context(), connect.NewRequest(tc.req))
			if err != nil {
				t.Fatalf("Report: %v", err)
			}
			// An age of 1500 ms dates the input 1.5 s before Now, floored to the second.
			want := []presenceReport{{input: readFixtureAt - 2, view: tc.want}}
			if !slices.Equal(got, want) {
				t.Fatalf("Report reported %+v, want %+v", got, want)
			}
		})
	}
}
