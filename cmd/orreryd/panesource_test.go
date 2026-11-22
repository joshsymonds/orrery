package main

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// TestPaneSourceRoundTrip checks that a stored pane transition keeps every field: its cwd, base URL, transcript, last
// message, failed flag and API error all come back unchanged.
func TestPaneSourceRoundTrip(t *testing.T) {
	want := daemon.Transition{
		PaneID:      "%7",
		Server:      tmuxctl.Server{Name: "synthetic-server", Socket: "/synthetic/tmux.sock"},
		From:        protocol.StateWorking,
		To:          protocol.StateDone,
		Cwd:         "/synthetic/work",
		BaseURL:     "https://synthetic.example",
		Transcript:  "/synthetic/transcripts/one.jsonl",
		LastMessage: "synthetic final message",
		Failed:      true,
		APIError:    "rate_limit",
	}
	data, err := encodePaneSource(want)
	if err != nil {
		t.Fatalf("encode pane source: %v", err)
	}
	got, err := decodePaneSource(data, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("decode pane source: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded pane source = %+v, want %+v", got, want)
	}
}

// legacyRow is a row stored in the frame form, before a transition carried its facts: its frame holds the hook, the
// tmux pane, the cwd and the base URL.
func legacyRow(hook string) []byte {
	return []byte(`{"paneId":"%7","server":{"name":"synthetic-server","socket":"/synthetic/tmux.sock"},` +
		`"from":"working","to":"done","frame":{"hook":` + hook + `,"tmuxPane":"%7",` +
		`"tmux":"/synthetic/tmux.sock,1234,0","cwd":"/synthetic/work",` +
		`"anthropicBaseUrl":"https://synthetic.example"}}`)
}

// TestPaneSourceLegacyFrame checks that a row stored in the frame form decodes into the same fields a transition
// carries: a Stop's transcript and last message, and a StopFailure's failed flag and API error. A Stop's
// background_tasks is no fact a transition carries, so it decodes to none.
func TestPaneSourceLegacyFrame(t *testing.T) {
	base := daemon.Transition{
		PaneID:  "%7",
		Server:  tmuxctl.Server{Name: "synthetic-server", Socket: "/synthetic/tmux.sock"},
		From:    protocol.StateWorking,
		To:      protocol.StateDone,
		Cwd:     "/synthetic/work",
		BaseURL: "https://synthetic.example",
	}
	cases := []struct {
		name string
		hook string
		want daemon.Transition
	}{
		{
			name: "stop with background work",
			hook: `{"hook_event_name":"Stop","transcript_path":"/synthetic/transcripts/one.jsonl",` +
				`"last_assistant_message":"synthetic final message",` +
				`"background_tasks":[{"id":"a1","type":"subagent","status":"running","description":"search the tree"}]}`,
			want: daemon.Transition{
				PaneID: base.PaneID, Server: base.Server, From: base.From, To: base.To,
				Cwd: base.Cwd, BaseURL: base.BaseURL,
				Transcript:  "/synthetic/transcripts/one.jsonl",
				LastMessage: "synthetic final message",
			},
		},
		{
			name: "stop failure",
			hook: `{"hook_event_name":"StopFailure","error":"rate_limit"}`,
			want: daemon.Transition{
				PaneID: base.PaneID, Server: base.Server, From: base.From, To: base.To,
				Cwd: base.Cwd, BaseURL: base.BaseURL,
				Failed:   true,
				APIError: "rate_limit",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodePaneSource(legacyRow(tc.hook), slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatalf("decode legacy pane source: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decoded legacy pane source = %+v, want %+v", got, tc.want)
			}
		})
	}
}
