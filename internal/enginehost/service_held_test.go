package enginehost

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// heldQueueClient returns a real Manager on a new store, and an Engine client that serves it on a socket. The manager
// runs the fake claude, and its store path is returned for reading held messages.
func heldQueueClient(t *testing.T) (enginev1connect.EngineClient, string) {
	t.Helper()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	socket, _ := startServer(t, m)
	return newClient(t, socket), path
}

// startHeldSession starts a JUDGEHOLDS session through client, so a message said to it while its turn runs is held. It
// returns the session's id and its directory, which releaseHeldTurn ends the turn through.
func startHeldSession(t *testing.T, client enginev1connect.EngineClient) (string, string) {
	t.Helper()
	dir := t.TempDir()
	resp, err := client.Start(t.Context(), connect.NewRequest(&v1.StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"}))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return resp.Msg.GetId(), dir
}

// sayHeld says text under promptID to session id through client.
func sayHeld(t *testing.T, client enginev1connect.EngineClient, id, text, promptID string) {
	t.Helper()
	if _, err := client.Say(t.Context(), connect.NewRequest(&v1.SayRequest{
		Id: id, Text: text, PromptId: promptID,
	})); err != nil {
		t.Fatalf("Say %q: %v", text, err)
	}
}

// TestHeldQueueEngineWithdrawReturnsTheHeldText holds a message under a prompt id, then withdraws it by that id through
// the Engine client. The call returns its text, and the store no longer holds it.
func TestHeldQueueEngineWithdrawReturnsTheHeldText(t *testing.T) {
	t.Parallel()
	client, path := heldQueueClient(t)
	id, dir := startHeldSession(t, client)
	sayHeld(t, client, id, "held one", "p-held")

	resp, err := client.Withdraw(t.Context(), connect.NewRequest(&v1.WithdrawRequest{Id: id, PromptId: "p-held"}))
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if got := resp.Msg.GetTexts(); !slices.Equal(got, []string{"held one"}) {
		t.Fatalf("Withdraw texts = %q, want [held one]", got)
	}
	if held := heldTexts(t, path, id); len(held) != 0 {
		t.Fatalf("held messages after the withdrawal = %q, want none", held)
	}
	releaseHeldTurn(t, dir)
}

// TestHeldQueueEngineWithdrawAllReturnsEveryTextInOrder holds two messages and withdraws with an empty prompt id through
// the Engine client. The call returns both texts, oldest first.
func TestHeldQueueEngineWithdrawAllReturnsEveryTextInOrder(t *testing.T) {
	t.Parallel()
	client, path := heldQueueClient(t)
	id, dir := startHeldSession(t, client)
	sayHeld(t, client, id, "first held", "p-1")
	sayHeld(t, client, id, "second held", "p-2")

	resp, err := client.Withdraw(t.Context(), connect.NewRequest(&v1.WithdrawRequest{Id: id}))
	if err != nil {
		t.Fatalf("Withdraw all: %v", err)
	}
	if want := []string{"first held", "second held"}; !slices.Equal(resp.Msg.GetTexts(), want) {
		t.Fatalf("Withdraw all texts = %q, want %q", resp.Msg.GetTexts(), want)
	}
	if held := heldTexts(t, path, id); len(held) != 0 {
		t.Fatalf("held messages after withdrawing all = %q, want none", held)
	}
	releaseHeldTurn(t, dir)
}

// TestHeldQueueEngineWithdrawOfAMessageNoLongerHeldIsFailedPrecondition withdraws a message, then withdraws its prompt
// id again. The second call finds nothing held under the id and answers failed_precondition.
func TestHeldQueueEngineWithdrawOfAMessageNoLongerHeldIsFailedPrecondition(t *testing.T) {
	t.Parallel()
	client, _ := heldQueueClient(t)
	id, dir := startHeldSession(t, client)
	sayHeld(t, client, id, "held once", "p-gone")
	if _, err := client.Withdraw(
		t.Context(),
		connect.NewRequest(&v1.WithdrawRequest{Id: id, PromptId: "p-gone"}),
	); err != nil {
		t.Fatalf("first Withdraw: %v", err)
	}

	_, err := client.Withdraw(t.Context(), connect.NewRequest(&v1.WithdrawRequest{Id: id, PromptId: "p-gone"}))
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("Withdraw of a message no longer held: code = %v, want %v (error %v)",
			got, connect.CodeFailedPrecondition, err)
	}
	if !strings.Contains(err.Error(), ErrNotHeld.Error()) {
		t.Fatalf("Withdraw error = %v, want the not-held reason %q", err, ErrNotHeld.Error())
	}
	releaseHeldTurn(t, dir)
}

// TestHeldQueueEngineWithdrawOfAnUnknownSessionIsNotFound withdraws from an id with no engine row.
func TestHeldQueueEngineWithdrawOfAnUnknownSessionIsNotFound(t *testing.T) {
	t.Parallel()
	client, _ := heldQueueClient(t)

	_, err := client.Withdraw(t.Context(), connect.NewRequest(&v1.WithdrawRequest{
		Id: "0123456789abcdef0123456789abcdef", PromptId: "p-none",
	}))
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("Withdraw from an unknown session: code = %v, want %v (error %v)", got, connect.CodeNotFound, err)
	}
}

// TestHeldQueueEngineWithdrawRefusesAStepID withdraws with a step id while a real session holds a message. The step id
// is refused as failed_precondition before the manager is asked, and the session's message stays held.
func TestHeldQueueEngineWithdrawRefusesAStepID(t *testing.T) {
	t.Parallel()
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	socket, _ := startServerWithSteps(t, m, &stepIDs{steps: map[string]bool{"s1": true}})
	client := newClient(t, socket)
	id, dir := startHeldSession(t, client)
	sayHeld(t, client, id, "stays held", "p-stay")

	_, err := client.Withdraw(t.Context(), connect.NewRequest(&v1.WithdrawRequest{Id: "s1"}))
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("Withdraw of a step id: code = %v, want %v (error %v)", got, connect.CodeFailedPrecondition, err)
	}
	if msg := err.Error(); !strings.Contains(msg, "s1") || !strings.Contains(msg, "Steps") {
		t.Fatalf("Withdraw of a step id error = %v, want the id s1 and a pointer to the Steps service", err)
	}
	if held := heldTexts(t, path, id); !slices.Equal(held, []string{"stays held"}) {
		t.Fatalf("held messages after a refused step withdrawal = %q, want [stays held]", held)
	}
	releaseHeldTurn(t, dir)
}
