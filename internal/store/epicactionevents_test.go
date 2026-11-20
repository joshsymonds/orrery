package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

const actionEventsFreeEpic = "orrery-1a2b3c4/epic-free"

// actionEventsStore opens a migrated store with the sessions engine-1 and engine-2, and returns it with its path.
func actionEventsStore(t *testing.T) (*Store, string) {
	t.Helper()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1", "engine-2")
	return s, path
}

// requireActionEvents fails the test unless ActionEvents returns want.
func requireActionEvents(t *testing.T, s *Store, want []ActionEvent) {
	t.Helper()
	got, err := s.ActionEvents(context.Background())
	if err != nil {
		t.Fatalf("ActionEvents: %v", err)
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActionEvents =\n %+v\nwant\n %+v", got, want)
	}
}

func TestSteerMovedEventsListEachSideUntilItsMark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := actionEventsStore(t)
	row := fullEpic(steerMoveEpic)
	row.Steerer = "engine-1"
	createEpic(t, s, row)
	at := steerMoveAt()

	action, err := s.MoveEpic(ctx, steerMoveEpic, "engine-2", Maker{By: MakerOwner}, at)
	if err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	id := EpicActionEvent(steerMoveEpic, action.Seq)
	recipient := func(session string) CallerSession {
		return CallerSession{ID: session, Kind: KindEngine, State: "idle"}
	}
	leave := ActionEvent{
		ID: id, Kind: CallerKindMoved, Epic: steerMoveEpic, Title: "Synthetic epic", Recipient: recipient("engine-1"),
		Action: ActionMove, Maker: Maker{By: MakerOwner}, From: "engine-1", To: "engine-2", Taken: false,
	}
	take := leave
	take.Recipient, take.Taken = recipient("engine-2"), true
	requireActionEvents(t, s, []ActionEvent{leave, take})

	wantFrom := []PendingEvent{{ID: id, Kind: CallerKindMoved, Epic: steerMoveEpic, Title: "Synthetic epic"}}
	if got := readPending(t, path, "engine-1"); !reflect.DeepEqual(got, wantFrom) {
		t.Fatalf("PendingCallerEvents(engine-1) = %+v, want %+v", got, wantFrom)
	}
	if got := readPending(t, path, "engine-2"); !reflect.DeepEqual(got, wantFrom) {
		t.Fatalf("PendingCallerEvents(engine-2) = %+v, want %+v", got, wantFrom)
	}

	if err := s.MarkCallerEvent(ctx, id, "engine-1", at); err != nil {
		t.Fatalf("MarkCallerEvent engine-1: %v", err)
	}
	requireActionEvents(t, s, []ActionEvent{take})
	if got := readPending(t, path, "engine-1"); len(got) != 0 {
		t.Fatalf("PendingCallerEvents(engine-1) after its mark = %+v, want none", got)
	}
	if got := readPending(t, path, "engine-2"); !reflect.DeepEqual(got, wantFrom) {
		t.Fatalf("PendingCallerEvents(engine-2) after engine-1's mark = %+v, want %+v", got, wantFrom)
	}

	if err := s.MarkCallerEvent(ctx, id, "engine-2", at); err != nil {
		t.Fatalf("MarkCallerEvent engine-2: %v", err)
	}
	requireActionEvents(t, s, nil)
	if got := readPending(t, path, "engine-2"); len(got) != 0 {
		t.Fatalf("PendingCallerEvents(engine-2) after its mark = %+v, want none", got)
	}
}

func TestSteerMovedEventsFromNoSteererListOnlyTheTaker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := actionEventsStore(t)
	createEpic(t, s, fullEpic(actionEventsFreeEpic))

	action, err := s.MoveEpic(ctx, actionEventsFreeEpic, "engine-2", Maker{By: MakerOwner}, steerMoveAt())
	if err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	requireActionEvents(t, s, []ActionEvent{{
		ID: EpicActionEvent(actionEventsFreeEpic, action.Seq), Kind: CallerKindMoved, Epic: actionEventsFreeEpic,
		Title: "Synthetic epic", Recipient: CallerSession{ID: "engine-2", Kind: KindEngine, State: "idle"},
		Action: ActionMove, Maker: Maker{By: MakerOwner}, To: "engine-2", Taken: true,
	}})
}

func TestOwnerActionEventListsActionAndDecisionToTheSteererUntilMarked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := actionEventsStore(t)
	row := fullEpic(steerMoveEpic)
	row.Steerer = "engine-1"
	createEpic(t, s, row)
	at := steerMoveAt()
	owner := Maker{By: MakerOwner}

	// The owner pauses the epic, then kicks it with a decision and answers a question with another.
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if _, err := appendEpicAction(ctx, queries, steerMoveEpic, ActionPause, owner, "", "", at); err != nil {
			return err
		}
		for _, decision := range []sqlcgen.InsertEpicDecisionParams{
			{Epic: steerMoveEpic, Seq: 3, ClientID: "owner-kick:3", Text: "Try the sqlite plan", Time: at.Unix(),
				Maker: MakerOwner, Recipient: "engine-1"},
			{Epic: steerMoveEpic, Seq: 4, ClientID: "keep-old-tasks", Text: "Keep them", Question: 1,
				Time: at.Unix(), Maker: MakerOwner, Recipient: "engine-1"},
		} {
			if err := queries.InsertEpicDecision(ctx, decision); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("write owner actions: %v", err)
	}

	pause := ActionEvent{
		ID: EpicActionEvent(steerMoveEpic, 1), Kind: CallerKindAction, Epic: steerMoveEpic, Title: "Synthetic epic",
		Recipient: CallerSession{ID: "engine-1", Kind: KindEngine, State: "idle"}, Action: ActionPause, Maker: owner,
	}
	kick := ActionEvent{
		ID: EpicDecisionEvent(steerMoveEpic, 3), Kind: CallerKindAction, Epic: steerMoveEpic, Title: "Synthetic epic",
		Recipient: pause.Recipient, Action: "kick", Maker: owner, Text: "Try the sqlite plan",
	}
	answer := ActionEvent{
		ID: EpicDecisionEvent(steerMoveEpic, 4), Kind: CallerKindAction, Epic: steerMoveEpic, Title: "Synthetic epic",
		Recipient: pause.Recipient, Action: "answer", Maker: owner, Question: 1, Text: "Keep them",
	}
	requireActionEvents(t, s, []ActionEvent{pause, kick, answer})

	wantPending := []PendingEvent{
		{ID: pause.ID, Kind: CallerKindAction, Epic: steerMoveEpic, Title: "Synthetic epic"},
		{ID: kick.ID, Kind: CallerKindAction, Epic: steerMoveEpic, Title: "Synthetic epic"},
		{ID: answer.ID, Kind: CallerKindAction, Epic: steerMoveEpic, Title: "Synthetic epic"},
	}
	if got := readPending(t, path, "engine-1"); !reflect.DeepEqual(got, wantPending) {
		t.Fatalf("PendingCallerEvents(engine-1) = %+v, want %+v", got, wantPending)
	}
	if got := readPending(t, path, "engine-2"); len(got) != 0 {
		t.Fatalf("PendingCallerEvents(engine-2) = %+v, want none", got)
	}

	if err := s.MarkCallerEvent(ctx, kick.ID, "engine-1", at); err != nil {
		t.Fatalf("MarkCallerEvent: %v", err)
	}
	wantPending = []PendingEvent{wantPending[0], wantPending[2]}
	if got := readPending(t, path, "engine-1"); !reflect.DeepEqual(got, wantPending) {
		t.Fatalf("PendingCallerEvents(engine-1) after the kick mark = %+v, want %+v", got, wantPending)
	}
	requireActionEvents(t, s, []ActionEvent{pause, answer})
}

func TestOwnerActionEventSkipsRecipientWithoutSessionRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := actionEventsStore(t)
	missing := fullEpic(steerMoveEpic)
	missing.Steerer = "engine-missing"
	createEpic(t, s, missing)
	steered := fullEpic(actionEventsFreeEpic)
	steered.Steerer = "engine-1"
	createEpic(t, s, steered)

	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		for _, epic := range []string{steerMoveEpic, actionEventsFreeEpic} {
			if _, err := appendEpicAction(
				ctx,
				queries,
				epic,
				ActionPause,
				Maker{By: MakerOwner},
				"",
				"",
				steerMoveAt(),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("write owner actions: %v", err)
	}
	requireActionEvents(t, s, []ActionEvent{{
		ID: EpicActionEvent(actionEventsFreeEpic, 1), Kind: CallerKindAction, Epic: actionEventsFreeEpic,
		Title: "Synthetic epic", Recipient: CallerSession{ID: "engine-1", Kind: KindEngine, State: "idle"},
		Action: ActionPause, Maker: Maker{By: MakerOwner},
	}})
}
