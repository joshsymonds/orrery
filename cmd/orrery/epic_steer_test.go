package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// TestEpicSteerMoveSendsTheIDInOneCall checks that `orrery epic steer ID` makes one MoveEpic call carrying the id and an
// empty session, which names the calling conversation, and prints the acknowledgement on stdout with nothing on stderr.
func TestEpicSteerMoveSendsTheIDInOneCall(t *testing.T) {
	t.Parallel()
	cliFake := newRecordingEpics()

	stdout, stderr, err := runEpicCommand(t, cliFake, nil, "steer", epicTestID)
	if err != nil {
		t.Fatalf("epic steer: %v", err)
	}
	requests := cliFake.recorded()
	if len(requests) != 1 {
		t.Fatalf("epic steer made %d calls, want 1", len(requests))
	}
	want := &v1.MoveEpicRequest{Id: epicTestID}
	if got, ok := requests[0].(*v1.MoveEpicRequest); !ok || !proto.Equal(got, want) {
		t.Fatalf("MoveEpic request = %v, want %v", requests[0], want)
	}
	wantOut := "epic " + epicTestID + ": phase plan\nthis conversation now steers the epic\n"
	if stdout != wantOut {
		t.Errorf("stdout = %q, want the acknowledgement %q", stdout, wantOut)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestEpicSteerMoveReachesTheEpicSteerTool checks that `orrery epic steer ID` sends the same MoveEpic request as the
// epic_steer tool does for the same id, and prints the tool's text.
func TestEpicSteerMoveReachesTheEpicSteerTool(t *testing.T) {
	t.Parallel()
	cliFake := newRecordingEpics()
	stdout, _, err := runEpicCommand(t, cliFake, nil, "steer", epicTestID)
	if err != nil {
		t.Fatalf("epic steer: %v", err)
	}

	mcpFake := newRecordingEpics()
	input, err := json.Marshal(epicIDArgs{ID: epicTestID})
	if err != nil {
		t.Fatalf("encode arguments: %v", err)
	}
	result, err := stepmcp.CallEpicTool(t.Context(), mcpFake, "epic_steer", input)
	if err != nil {
		t.Fatalf("CallEpicTool(epic_steer) = %v, want nil", err)
	}
	if result.IsError {
		t.Fatalf("CallEpicTool(epic_steer) returned an error result: %v", result.Content)
	}

	got, want := cliFake.recorded(), mcpFake.recorded()
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("requests: cli %d, mcp %d; want 1 each", len(got), len(want))
	}
	if !proto.Equal(got[0], want[0]) {
		t.Errorf("epic steer sent %v, want %v", got[0], want[0])
	}
	if wantOut := result.Content[0].Text + "\n"; stdout != wantOut {
		t.Errorf("stdout = %q, want %q", stdout, wantOut)
	}
}

// TestEpicSteerMoveWrongFormsAreUsageErrors checks that `orrery epic steer` with no id, or with two ids, returns a usage
// error naming `steer ID`, and makes no call.
func TestEpicSteerMoveWrongFormsAreUsageErrors(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"steer"},
		{"steer", epicTestID, "extra"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			cliFake := newRecordingEpics()
			stdout, _, err := runEpicCommand(t, cliFake, nil, args...)
			if _, ok := errors.AsType[usageError](err); !ok {
				t.Fatalf("epicCommand(%v) = %v, want a usage error", args, err)
			}
			if !strings.Contains(err.Error(), "steer ID") {
				t.Errorf("epicCommand(%v) error %q does not name the form steer ID", args, err)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if n := len(cliFake.recorded()); n != 0 {
				t.Errorf("epicCommand(%v) made %d calls, want none", args, n)
			}
		})
	}
}

// TestEpicSteerMoveNamesItsFormInTheForms checks that the forms of `orrery epic` name `steer ID`.
func TestEpicSteerMoveNamesItsFormInTheForms(t *testing.T) {
	t.Parallel()
	if !strings.Contains(epicForms, "steer ID") {
		t.Errorf("epicForms = %q, want it to name steer ID", epicForms)
	}
}
