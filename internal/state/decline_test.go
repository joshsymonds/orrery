package state_test

import (
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// declineQuestion is the @orrery_question value a PermissionRequest for the synthetic Bash command captures.
const declineQuestion = `{"tool":"Bash","input":{"command":"echo synthetic"}}`

func TestDeclineWaitingPaneEndsOnInterruption(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	apply(t, client, pane, userPromptSubmitHook, t0)
	apply(t, client, pane, permissionHook, t0.Add(time.Second))
	wantQuestion(t, client, pane, declineQuestion)

	if !endInterrupted(t, client, pane, t0.Unix()+2, t0.Add(3*time.Second)) {
		t.Fatal("EndInterruptedTurn on a waiting pane with a later interruption = false, want true")
	}
	wantState(t, client, pane, protocol.StateDone)
	wantQuestion(t, client, pane, "")
}

func TestDeclineWaitingPaneStaysWithoutInterruption(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	client, pane := startPane(t)
	apply(t, client, pane, userPromptSubmitHook, t0)
	apply(t, client, pane, permissionHook, t0.Add(time.Second))
	wantQuestion(t, client, pane, declineQuestion)

	if endInterrupted(t, client, pane, 0, t0.Add(2*time.Second)) {
		t.Fatal("EndInterruptedTurn(0) on a waiting pane = true, want false")
	}
	wantState(t, client, pane, protocol.StateWaiting)
	wantQuestion(t, client, pane, declineQuestion)

	// The interruption is before the waiting pane's changed time, so it belongs to an earlier turn.
	if endInterrupted(t, client, pane, t0.Unix(), t0.Add(2*time.Second)) {
		t.Fatal("EndInterruptedTurn before changed on a waiting pane = true, want false")
	}
	wantState(t, client, pane, protocol.StateWaiting)
	wantQuestion(t, client, pane, declineQuestion)
}
