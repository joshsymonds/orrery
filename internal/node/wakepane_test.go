package node

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestAgentWakePaneCallsWakePane asks an agent to wake a pane. Its WakePane is called with the request's server, pane and
// text, and the Response reports that the text was typed.
func TestAgentWakePaneCallsWakePane(t *testing.T) {
	type wakeCall struct{ server, pane, text string }
	calls := make(chan wakeCall, 1)
	addr := startConfiguredAgent(t, AgentConfig{
		WakePane: func(_ context.Context, server, pane, text string) (bool, error) {
			calls <- wakeCall{server: server, pane: pane, text: text}
			return true, nil
		},
	})

	resp := ask(t, addr, Request{Version: Version, Op: OpWakePane, Server: "work", Pane: "%4", Text: "continue"})
	if resp.Code != "" {
		t.Fatalf("wake-pane: %+v", resp)
	}
	if !resp.Woke {
		t.Fatal("wake-pane reports that it did not type the text, want it to")
	}
	select {
	case got := <-calls:
		if want := (wakeCall{server: "work", pane: "%4", text: "continue"}); got != want {
			t.Fatalf("WakePane got %+v, want %+v", got, want)
		}
	default:
		t.Fatal("wake-pane did not call WakePane")
	}
}

// TestAgentWakePaneWithoutWakerRefused asks, through a Client, an agent with no WakePane to wake a pane. The agent refuses
// the op, so the error of the Client's WakePane wraps ErrRefused and holds the agent's message.
func TestAgentWakePaneWithoutWakerRefused(t *testing.T) {
	addr := startConfiguredAgent(t, AgentConfig{})

	woke, err := newTestClient(addr).WakePane(t.Context(), "work", "%4", "continue")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("wake-pane with no waker: err = %v, want one that wraps ErrRefused", err)
	}
	if !strings.Contains(err.Error(), "agent wakes no panes") {
		t.Fatalf("wake-pane with no waker: err = %v, want the agent's message", err)
	}
	if woke {
		t.Fatal("wake-pane with no waker reports that it typed the text")
	}
}
