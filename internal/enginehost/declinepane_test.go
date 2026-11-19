package enginehost

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// declinePanes returns the panes the decline tests read: %4 waiting on h-ask, and %2 not waiting, on h-idle.
func declinePanes() *fakePanes {
	return &fakePanes{panes: map[string]PaneQuestion{
		"%4": {Waiting: true, Hash: "h-ask", Changed: 1_700_000_100},
		"%2": {Waiting: false, Hash: "h-idle", Changed: 1_700_000_050},
	}}
}

// TestDeclinePaneEscapesAndEndsNotice declines the question pane %4 asks. The pane is sent its Escape once, and then
// it is reported seen once at the manager's time, which ends its pending notice.
func TestDeclinePaneEscapesAndEndsNotice(t *testing.T) {
	t.Parallel()
	panes := declinePanes()
	m := NewManager(Config{Now: fixedClock, Panes: panes})

	if err := m.DeclinePane(t.Context(), "%4", "h-ask"); err != nil {
		t.Fatalf("DeclinePane: %v", err)
	}
	if got, want := panes.declineCalls(), []paneDecline{{pane: "%4", questionHash: "h-ask"}}; !slices.Equal(got, want) {
		t.Fatalf("pane declines = %+v, want %+v", got, want)
	}
	if got := panes.seenCalls(); len(got) != 1 || got[0].pane != "%4" || !got[0].seen.Equal(fixedClock()) {
		t.Fatalf("pane seen calls = %+v, want one for %%4 at %v", got, fixedClock())
	}
}

// TestDeclinePaneRefusesQuestionNotOpen declines a question that is not open, or names no question. Each is refused
// without sending an Escape or reporting the pane seen.
func TestDeclinePaneRefusesQuestionNotOpen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, pane, questionHash string
		want                     error
		text                     string
	}{
		{
			name:         "pane not waiting",
			pane:         "%2",
			questionHash: "h-idle",
			want:         ErrQuestionNotOpen,
			text:         "no open question",
		},
		{
			name:         "different hash",
			pane:         "%4",
			questionHash: "h-other",
			want:         ErrQuestionNotOpen,
			text:         "another question",
		},
		{name: "empty hash", pane: "%4", want: ErrQuestionNotOpen, text: "question_hash"},
		{name: "empty pane", questionHash: "h-ask", want: ErrQuestionNotOpen, text: "pane id"},
		{name: "unknown pane", pane: "%9", questionHash: "h-ask", want: ErrNoPane, text: "%9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			panes := declinePanes()
			m := NewManager(Config{Now: fixedClock, Panes: panes})

			wantRefused(t, "DeclinePane", m.DeclinePane(t.Context(), tc.pane, tc.questionHash), tc.want, tc.text)
			if got := panes.declineCalls(); len(got) != 0 {
				t.Fatalf("refused DeclinePane sent %+v, want none", got)
			}
			if got := panes.seenCalls(); len(got) != 0 {
				t.Fatalf("refused DeclinePane reported panes seen %+v, want none", got)
			}
		})
	}
}

// TestDeclinePaneWithoutPanesIsUnavailable declines on a manager that has no Panes. The call is refused as unavailable.
func TestDeclinePaneWithoutPanesIsUnavailable(t *testing.T) {
	t.Parallel()
	m := NewManager(Config{Now: fixedClock})

	if err := m.DeclinePane(t.Context(), "%4", "h-ask"); !errors.Is(err, ErrPutOffUnavailable) {
		t.Fatalf("DeclinePane without panes = %v, want ErrPutOffUnavailable", err)
	}
}

// TestDeclinePanePassesPaneRefusal makes the pane refuse the Escape because it is in a tmux mode. The manager returns
// that refusal and reports no pane seen, so the notice stays pending.
func TestDeclinePanePassesPaneRefusal(t *testing.T) {
	t.Parallel()
	panes := declinePanes()
	panes.declineErr = fmt.Errorf("synthetic pane in copy mode: %w", ErrPaneInMode)
	m := NewManager(Config{Now: fixedClock, Panes: panes})

	err := m.DeclinePane(t.Context(), "%4", "h-ask")
	if !errors.Is(err, ErrPaneInMode) {
		t.Fatalf("DeclinePane in a tmux mode = %v, want ErrPaneInMode", err)
	}
	if got := panes.declineCalls(); len(got) != 1 {
		t.Fatalf("pane declines = %+v, want the one refused attempt", got)
	}
	if got := panes.seenCalls(); len(got) != 0 {
		t.Fatalf("refused DeclinePane reported panes seen %+v, want none", got)
	}
}

// TestDeclinePaneServiceRoutesByKind declines through the Engine service. A pane decline reaches DeclinePane with the
// pane's id and question hash, a pane refusal answers failed_precondition, and a kind that is neither headless nor pane
// answers invalid_argument before any command runs.
func TestDeclinePaneServiceRoutesByKind(t *testing.T) {
	t.Parallel()
	fake := &fakeCommands{}
	socket, _ := startServer(t, fake)
	client := newClient(t, socket)
	ctx := t.Context()

	if _, err := client.Decline(ctx, connect.NewRequest(&v1.DeclineRequest{
		Kind: store.PutOffPane, Id: "%4", QuestionHash: "h-ask",
	})); err != nil {
		t.Fatalf("Decline pane: %v", err)
	}
	want := []call{{method: "DeclinePane", id: "%4", questionHash: "h-ask"}}
	if got := fake.recorded(); !slices.Equal(got, want) {
		t.Fatalf("commands after a pane Decline = %+v, want %+v", got, want)
	}

	fake.setErr(fmt.Errorf("synthetic: %w", ErrPaneInMode))
	_, err := client.Decline(ctx, connect.NewRequest(&v1.DeclineRequest{
		Kind: store.PutOffPane, Id: "%4", QuestionHash: "h-ask",
	}))
	wantCode(t, "Decline pane in a tmux mode", err, connect.CodeFailedPrecondition, ErrPaneInMode.Error())

	_, err = client.Decline(ctx, connect.NewRequest(&v1.DeclineRequest{Kind: "bogus", Id: "%4"}))
	wantCode(t, `Decline of kind "bogus"`, err, connect.CodeInvalidArgument, `"bogus"`)
	if got := fake.recorded(); len(got) != 2 {
		t.Fatalf("commands after a bogus-kind Decline = %+v, want the two pane calls only", got)
	}
}
