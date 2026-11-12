package server_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// readFixtureAt is the synthetic read time the read tests fix as Config.Now, in unix seconds.
const readFixtureAt int64 = 1600000000

// readCall is one report a readRecorder received.
type readCall struct {
	pane string
	at   int64
}

// readRecorder returns a read reporter that appends each call to calls.
func readRecorder(calls *[]readCall) server.ReadReporter {
	return func(pane string, at int64) {
		*calls = append(*calls, readCall{pane: pane, at: at})
	}
}

// newReadHandler builds the guarded handler that admits only allowedLogin, routes to tmux, dates reads at
// readFixtureAt, and reports each read to report.
func newReadHandler(t *testing.T, tmux *tmuxctl.Client, report server.ReadReporter) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:     []string{allowedLogin},
		Tmux:       tmux,
		Push:       newPushStore(t),
		Now:        func() time.Time { return time.Unix(readFixtureAt, 0) },
		ReportRead: report,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// readClient serves handler on an httptest server and returns a generated Panes client whose requests carry
// allowedLogin and the client header. It speaks Connect's JSON codec when protoJSON is set, else the default codec.
func readClient(t *testing.T, handler http.Handler, protoJSON bool) orreryv1connect.PanesClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	var opts []connect.ClientOption
	if protoJSON {
		opts = append(opts, connect.WithProtoJSON())
	}
	return orreryv1connect.NewPanesClient(httpClient, srv.URL, opts...)
}

func TestPanesReadReportsScreenAndConversation(t *testing.T) {
	t.Parallel()

	codecs := []struct {
		name      string
		protoJSON bool
	}{
		{"default codec", false},
		{"proto JSON", true},
	}
	for _, codec := range codecs {
		t.Run(codec.name, func(t *testing.T) {
			t.Parallel()
			tmux := newTmuxServer(t)
			pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 1)))
			var calls []readCall
			client := readClient(t, newReadHandler(t, tmux, readRecorder(&calls)), codec.protoJSON)
			want := []readCall{{pane: pane, at: readFixtureAt}}

			calls = nil
			_, err := client.Screen(t.Context(), connect.NewRequest(&orreryv1.ScreenRequest{Pane: pane}))
			if err != nil {
				t.Fatalf("Panes.Screen: %v", err)
			}
			if !slices.Equal(calls, want) {
				t.Fatalf("Panes.Screen reports = %+v, want %+v", calls, want)
			}

			calls = nil
			if _, err := client.Conversation(t.Context(),
				connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane})); err != nil {
				t.Fatalf("Panes.Conversation: %v", err)
			}
			if !slices.Equal(calls, want) {
				t.Fatalf("Panes.Conversation reports = %+v, want %+v", calls, want)
			}
		})
	}
}

func TestPanesReadReportsNothingWhenRefused(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	// A live pane keeps the server running, so the absent pane is absent from a listing that has panes.
	conversationPane(t, tmux, liveOptions(writeConversation(t, 1)))
	cases := []struct {
		name string
		pane string
	}{
		{"absent pane", "%999999"},
		{"malformed pane id", "abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var calls []readCall
			client := panesClient(t, newReadHandler(t, tmux, readRecorder(&calls)))

			_, err := client.Screen(t.Context(), connect.NewRequest(&orreryv1.ScreenRequest{Pane: c.pane}))
			if err == nil {
				t.Fatalf("Panes.Screen(%q) succeeded, want a refusal", c.pane)
			}
			_, err = client.Conversation(t.Context(),
				connect.NewRequest(&orreryv1.ConversationRequest{Pane: c.pane}))
			if err == nil {
				t.Fatalf("Panes.Conversation(%q) succeeded, want a refusal", c.pane)
			}
			if len(calls) != 0 {
				t.Fatalf("reports = %+v, want none for refused calls", calls)
			}
		})
	}
}

func TestPanesReadNilReporterStillAnswers(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeConversation(t, 1)))
	client := panesClient(t, newReadHandler(t, tmux, nil))

	if _, err := client.Screen(t.Context(), connect.NewRequest(&orreryv1.ScreenRequest{Pane: pane})); err != nil {
		t.Fatalf("Panes.Screen: %v", err)
	}
	got, err := client.Conversation(t.Context(), connect.NewRequest(&orreryv1.ConversationRequest{Pane: pane}))
	if err != nil {
		t.Fatalf("Panes.Conversation: %v", err)
	}
	if got.Msg.GetOrigin() != synthOrigin {
		t.Fatalf("Panes.Conversation origin = %q, want %q", got.Msg.GetOrigin(), synthOrigin)
	}
}
