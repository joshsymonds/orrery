package enginehost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// onceFailingMarks is a CallerEventStore whose first mark fails, and which records each mark that lands.
type onceFailingMarks struct {
	failed bool
	marked []paneEvent
}

// MarkCallerEvent fails the first mark and records the rest.
func (m *onceFailingMarks) MarkCallerEvent(_ context.Context, event, session string, _ time.Time) error {
	if !m.failed {
		m.failed = true
		return errors.New("mark refused")
	}
	m.marked = append(m.marked, paneEvent{session: session, event: event})
	return nil
}

// TestCallerPaneMarkOnce types a pane caller's keys once when its mark fails: the first send fails with the mark error
// and the next send marks the event without sending the keys again.
func TestCallerPaneMarkOnce(t *testing.T) {
	t.Parallel()
	marks := &onceFailingMarks{}
	wakes := 0
	sender := NewCallerSender(CallerSenderConfig{
		WakePane: func(context.Context, string, string, string) (bool, error) {
			wakes++
			return true, nil
		},
		Store: marks,
		Now:   fixedClock,
	})
	caller := store.CallerSession{ID: "orrery-1", Server: "-S /tmp/tmux-1000/orrery", Pane: "%7"}
	const event = "step:step-1"

	finished, err := sender.Send(t.Context(), caller, event, "step end")
	if err == nil {
		t.Fatalf("first send returned no error, want the mark error")
	}
	if finished {
		t.Fatalf("first send reported the event finished while its mark failed")
	}

	finished, err = sender.Send(t.Context(), caller, event, "step end")
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	if !finished {
		t.Fatalf("second send did not report the event finished")
	}
	if want := (paneEvent{session: "orrery-1", event: event}); len(marks.marked) != 1 || marks.marked[0] != want {
		t.Fatalf("marks recorded = %v, want [%v]", marks.marked, want)
	}
	if wakes != 1 {
		t.Fatalf("WakePane calls = %d, want 1: the keys are typed once", wakes)
	}
}
