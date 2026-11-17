package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// TestCloseEpicCLISendsTheToolsRequest checks that `orrery epic close` sends, for each kind, the request the epic_close
// tool sends for the same input, and that `close ID --as superseded` without --by is a usage error that calls nothing.
func TestCloseEpicCLISendsTheToolsRequest(t *testing.T) {
	t.Parallel()
	const next = "demo-0a1b2c3/epic-next"
	cases := []struct {
		name  string
		args  []string
		input string
	}{
		{
			name:  "superseded names the epic that replaces it",
			args:  []string{"close", epicTestID, "--as", "superseded", "--by", next},
			input: `{"id":"demo-0a1b2c3/epic-login","kind":"superseded","by":"demo-0a1b2c3/epic-next"}`,
		},
		{
			name:  "shipped",
			args:  []string{"close", epicTestID, "--as", "shipped"},
			input: `{"id":"demo-0a1b2c3/epic-login","kind":"shipped"}`,
		},
		{
			name:  "abandoned",
			args:  []string{"close", epicTestID, "--as", "abandoned"},
			input: `{"id":"demo-0a1b2c3/epic-login","kind":"abandoned"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cliFake := newRecordingEpics()
			if _, _, err := runEpicCommand(t, cliFake, nil, tc.args...); err != nil {
				t.Fatalf("epicCommand(%v) = %v, want nil", tc.args, err)
			}

			mcpFake := newRecordingEpics()
			result, err := stepmcp.CallEpicTool(t.Context(), mcpFake, "epic_close", json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("CallEpicTool(epic_close) = %v, want nil", err)
			}
			if result.IsError {
				t.Fatalf("CallEpicTool(epic_close) returned an error result: %v", result.Content)
			}

			got, want := cliFake.recorded(), mcpFake.recorded()
			if len(got) != 1 || len(want) != 1 {
				t.Fatalf("requests: cli %d, mcp %d; want 1 each", len(got), len(want))
			}
			if !proto.Equal(got[0], want[0]) {
				t.Errorf("epicCommand(%v) sent %v, want the epic_close tool's request %v", tc.args, got[0], want[0])
			}
		})
	}

	t.Run("superseded without by is a usage error that calls nothing", func(t *testing.T) {
		t.Parallel()
		cliFake := newRecordingEpics()
		_, _, err := runEpicCommand(t, cliFake, nil, "close", epicTestID, "--as", "superseded")
		if _, ok := errors.AsType[usageError](err); !ok {
			t.Fatalf("epicCommand(close --as superseded) = %v, want a usage error", err)
		}
		if want := "close ID --as superseded|shipped|abandoned [--by EPIC]"; !strings.Contains(err.Error(), want) {
			t.Errorf("usage error %q does not name the form %q", err, want)
		}
		if n := len(cliFake.recorded()); n != 0 {
			t.Errorf("epicCommand(close --as superseded) made %d calls, want none", n)
		}
	})
}
