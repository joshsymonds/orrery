package planner

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// ownerActionEpicLine is the epic line of an owner action event on the runner epic.
const ownerActionEpicLine = "Epic " + runnerEpicID + " (Plan the runner): "

// ownerActionBlock is the action event block with id and body, as the steerer of the runner epic receives it.
func ownerActionBlock(id, body string) string {
	return `<orrery-event kind="action" id="` + id + `">
` + body + `
</orrery-event>`
}

// ownerActionEventOf returns the one action event the store owes, and fails the test when it owes any other number.
func ownerActionEventOf(t *testing.T, s *store.Store) store.ActionEvent {
	t.Helper()
	events, err := s.ActionEvents(t.Context())
	if err != nil || len(events) != 1 {
		t.Fatalf("ActionEvents = %+v, %v, want one owner action event", events, err)
	}
	return events[0]
}

// ownerKindStore is the EpicCallerStore of the synthesized kind tests. Its ActionEvents lists events, each one to the
// steerer of the runner epic that the real store names, and drops the ones the real store marks as sent. Its other reads
// are the real store's.
type ownerKindStore struct {
	*store.Store
	events []store.ActionEvent
}

// ActionEvents returns each event of the store that its steerer's mark does not hold.
func (s ownerKindStore) ActionEvents(ctx context.Context) ([]store.ActionEvent, error) {
	steerer, ok, err := s.EpicSteerer(ctx, runnerEpicID)
	if err != nil {
		return nil, fmt.Errorf("read steerer: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("epic %s has no steerer", runnerEpicID)
	}
	var owed []store.ActionEvent
	for _, event := range s.events {
		marked, err := s.CallerEventMarked(ctx, event.ID, steerer.ID)
		if err != nil {
			return nil, fmt.Errorf("read mark of %s: %w", event.ID, err)
		}
		if marked {
			continue
		}
		event.Recipient = steerer
		owed = append(owed, event)
	}
	return owed, nil
}

func TestOwnerActionEventRealCallsReachSteererOnce(t *testing.T) {
	t.Parallel()
	owner := store.Maker{By: store.MakerOwner}
	cases := []struct {
		name string
		act  func(ctx context.Context, s *store.Store) error
		body string
	}{
		{
			name: "pause",
			act: func(ctx context.Context, s *store.Store) error {
				return s.PauseEpic(ctx, runnerEpicID, owner, claimedAt())
			},
			body: ownerActionEpicLine + "the owner paused the epic.",
		},
	}
	for _, kind := range cases {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			s := openRunnerStore(t)
			plannedCallerEpic(t, s, "A", store.EpicPlan{Outcome: "wait"})
			addCallerEngine(t, s, "A", store.KindEngine)
			if err := kind.act(ctx, s); err != nil {
				t.Fatalf("owner %s: %v", kind.name, err)
			}
			event := ownerActionEventOf(t, s)
			deliver := &fakeCallerDeliver{}
			pass := callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t)))
			want := []enginehost.DeliverRequest{
				{ID: "A", Text: ownerActionBlock(event.ID, kind.body), PromptID: event.ID},
			}

			passEpics(ctx, t, pass)
			if !reflect.DeepEqual(deliver.delivered, want) {
				t.Fatalf("delivered = %+v, want the owner's %s once to A, %+v", deliver.delivered, kind.name, want)
			}

			passEpics(ctx, t, pass)
			if !reflect.DeepEqual(deliver.delivered, want) {
				t.Fatalf("delivered after a second pass = %+v, want nothing more", deliver.delivered)
			}
		})
	}
}

func TestOwnerActionEventSynthesizedKindsReachSteererOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		event store.ActionEvent
		body  string
	}{
		{
			name: "resume",
			event: store.ActionEvent{
				ID: store.EpicActionEvent(runnerEpicID, 1), Kind: store.CallerKindAction, Epic: runnerEpicID,
				Title: "Plan the runner", Action: store.ActionResume, Maker: store.Maker{By: store.MakerOwner},
			},
			body: ownerActionEpicLine + "the owner resumed the epic.",
		},
		{
			name: "stop",
			event: store.ActionEvent{
				ID: store.EpicActionEvent(runnerEpicID, 1), Kind: store.CallerKindAction, Epic: runnerEpicID,
				Title: "Plan the runner", Action: store.ActionStop, Maker: store.Maker{By: store.MakerOwner},
			},
			body: ownerActionEpicLine + "the owner stopped the epic.",
		},
		{
			name: "retry",
			event: store.ActionEvent{
				ID: store.EpicActionEvent(runnerEpicID, 1), Kind: store.CallerKindAction, Epic: runnerEpicID,
				Title: "Plan the runner", Action: store.ActionRetry, Maker: store.Maker{By: store.MakerOwner},
			},
			body: ownerActionEpicLine + "the owner retried the epic.",
		},
		{
			name: "release",
			event: store.ActionEvent{
				ID: store.EpicActionEvent(runnerEpicID, 1), Kind: store.CallerKindAction, Epic: runnerEpicID,
				Title: "Plan the runner", Action: store.ActionRelease, Maker: store.Maker{By: store.MakerOwner},
			},
			body: ownerActionEpicLine + "the owner released the epic.",
		},
		{
			name: "close",
			event: store.ActionEvent{
				ID: store.EpicActionEvent(runnerEpicID, 1), Kind: store.CallerKindAction, Epic: runnerEpicID,
				Title: "Plan the runner", Action: store.ActionClose, Maker: store.Maker{By: store.MakerOwner},
			},
			body: ownerActionEpicLine + "the owner closed the epic.",
		},
		{
			name: "amend",
			event: store.ActionEvent{
				ID: store.EpicActionEvent(runnerEpicID, 1), Kind: store.CallerKindAction, Epic: runnerEpicID,
				Title: "Plan the runner", Action: store.ActionAmend, Maker: store.Maker{By: store.MakerOwner},
			},
			body: ownerActionEpicLine + "the owner amended the epic's evidence.",
		},
		{
			name: "answer",
			event: store.ActionEvent{
				ID: store.EpicDecisionEvent(runnerEpicID, 1), Kind: store.CallerKindAction, Epic: runnerEpicID,
				Title: "Plan the runner", Action: "answer", Maker: store.Maker{By: store.MakerOwner},
				Question: 1, Text: "the chosen store",
			},
			body: ownerActionEpicLine + "the owner answered Q1.\ntext: the chosen store",
		},
	}
	for _, kind := range cases {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			s := openRunnerStore(t)
			plannedCallerEpic(t, s, "A", store.EpicPlan{Outcome: "wait"})
			addCallerEngine(t, s, "A", store.KindEngine)
			deliver := &fakeCallerDeliver{}
			pass := NewEpicCallerEvents(EpicCallerEventsConfig{
				Store:  ownerKindStore{Store: s, events: []store.ActionEvent{kind.event}},
				Sender: callerSenderOver(s, deliver.deliver, noWake(t)),
			})
			want := []enginehost.DeliverRequest{{
				ID: "A", Text: ownerActionBlock(kind.event.ID, kind.body), PromptID: kind.event.ID,
			}}

			passEpics(ctx, t, pass)
			if !reflect.DeepEqual(deliver.delivered, want) {
				t.Fatalf("delivered = %+v, want the owner's %s once to A, %+v", deliver.delivered, kind.name, want)
			}

			passEpics(ctx, t, pass)
			if !reflect.DeepEqual(deliver.delivered, want) {
				t.Fatalf("delivered after a second pass = %+v, want nothing more", deliver.delivered)
			}
		})
	}
}

func TestOwnerActionEventOwedToOldSteererNotNewer(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, "A", store.EpicPlan{Outcome: "wait"})
	addCallerEngine(t, s, "A", store.KindEngine)
	addCallerEngine(t, s, "B", store.KindEngine)
	owner := store.Maker{By: store.MakerOwner}
	if err := s.PauseEpic(ctx, runnerEpicID, owner, claimedAt()); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	pause := ownerActionEventOf(t, s)
	action, err := s.MoveEpic(ctx, runnerEpicID, "B", owner, claimedAt())
	if err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	move := store.EpicActionEvent(runnerEpicID, action.Seq)
	deliver := &fakeCallerDeliver{}

	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))

	if len(deliver.delivered) != 3 {
		t.Fatalf(
			"delivered = %+v, want the pause and the moved event to A, and the moved event to B",
			deliver.delivered,
		)
	}
	if got := deliver.delivered[0]; got.ID != "A" || got.PromptID != pause.ID ||
		got.Text != ownerActionBlock(pause.ID, ownerActionEpicLine+"the owner paused the epic.") {
		t.Fatalf("first delivery = %+v, want the pause to A before the move", got)
	}
	if got := deliver.delivered[1]; got.ID != "A" || got.PromptID != move || got.Text != movedLeftBlock(move) {
		t.Fatalf("second delivery = %+v, want the moved event to A", got)
	}
	requireMovedTakerBlock(t, deliver.delivered[2], move)
}
