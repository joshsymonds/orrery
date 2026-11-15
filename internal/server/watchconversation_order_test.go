package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/conversation"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// orderGoalLine is a goal record that is not met, which makes its condition the transcript's goal.
const orderGoalLine = `{"type":"attachment","attachment":{"type":"goal_status","condition":"synthetic goal",` +
	`"met":false,"sentinel":true}}` + "\n"

// TestWatchStepSendsNoStateAheadOfItsSeq appends a goal record between the two reads of one step, the transcript
// state's and the delta's. Whichever step sends the goal, its seq must cover the goal record: a client that resumes at
// that seq must not be told again of a line whose state it already holds.
func TestWatchStepSendsNoStateAheadOfItsSeq(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	base := `{"type":"user","message":{"role":"user","content":"synthetic prompt"}}` + "\n"
	if err := os.WriteFile(path, []byte(base), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	w := &conversationWatch{
		panes: panesServer{goals: conversation.NewGoals(), queues: conversation.NewQueues()},
		limit: 10,
	}
	pane := &v1.Pane{State: string(protocol.StateWorking)}
	if _, _, err := w.stepAt(pane, path); err != nil {
		t.Fatalf("first step: %v", err)
	}

	goalEnd := int64(len(base) + len(orderGoalLine))
	w.betweenReads = func() {
		w.betweenReads = nil
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("open transcript: %v", err)
		}
		if _, err = file.WriteString(orderGoalLine); err != nil {
			t.Fatalf("append goal record: %v", err)
		}
		if err = file.Close(); err != nil {
			t.Fatalf("close transcript: %v", err)
		}
	}
	sentGoal := false
	for step := range 2 {
		msg, send, err := w.stepAt(pane, path)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if !send || msg.GetState().GetGoal() == "" {
			continue
		}
		sentGoal = true
		if msg.GetSeq() < goalEnd {
			t.Fatalf("step %d sent goal %q at seq %d, want a seq that covers the goal record, at least %d",
				step, msg.GetState().GetGoal(), msg.GetSeq(), goalEnd)
		}
	}
	if !sentGoal {
		t.Fatal("no step sent the goal record's goal")
	}
}
