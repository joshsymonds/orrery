package planner

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// callerEpicParkedText is the reason the park tests park the runner epic with.
const callerEpicParkedText = "the reason"

// fakeCallerDeliver is the Deliver of the tests. It accepts each request and records it in delivered, unless err is set:
// then it refuses the request with err and records nothing.
type fakeCallerDeliver struct {
	err       error
	delivered []enginehost.DeliverRequest
}

func (f *fakeCallerDeliver) deliver(_ context.Context, req enginehost.DeliverRequest) error {
	if f.err != nil {
		return f.err
	}
	f.delivered = append(f.delivered, req)
	return nil
}

// wakeCall is one WakePane call a CallerSender made.
type wakeCall struct {
	server, pane, text string
}

// fakeCallerWake is the WakePane of the tests. It records each call, and takes the keys only while idle.
type fakeCallerWake struct {
	idle  bool
	calls []wakeCall
}

func (f *fakeCallerWake) wake(_ context.Context, server, pane, text string) (bool, error) {
	f.calls = append(f.calls, wakeCall{server: server, pane: pane, text: text})
	return f.idle, nil
}

// noWake returns a WakePane that fails the test when it is called, for the tests with no pane caller.
func noWake(t *testing.T) func(context.Context, string, string, string) (bool, error) {
	t.Helper()
	return func(_ context.Context, server, pane, _ string) (bool, error) {
		t.Errorf("WakePane to %q %q, want no pane caller", server, pane)
		return false, nil
	}
}

// callerSenderOver returns a CallerSender over s that delivers through deliver and wakes panes through wake.
func callerSenderOver(
	s *store.Store,
	deliver func(context.Context, enginehost.DeliverRequest) error,
	wake func(context.Context, string, string, string) (bool, error),
) *enginehost.CallerSender {
	return enginehost.NewCallerSender(enginehost.CallerSenderConfig{
		Deliver: deliver, WakePane: wake, Store: s, Now: claimedAt,
	})
}

// callerEpicEventsOver returns an EpicCallerEvents over s that sends through sender.
func callerEpicEventsOver(s *store.Store, sender *enginehost.CallerSender) *EpicCallerEvents {
	return NewEpicCallerEvents(EpicCallerEventsConfig{Store: s, Sender: sender})
}

// passEpics runs pass once over ctx, and fails the test on its error.
func passEpics(ctx context.Context, t *testing.T, pass *EpicCallerEvents) {
	t.Helper()
	if err := pass.Pass(ctx); err != nil {
		t.Fatalf("epic caller-event pass: %v", err)
	}
}

// plannedCallerEpic stores the runner epic with one ended planner turn that carries plan.
func plannedCallerEpic(t *testing.T, s *store.Store, plan store.EpicPlan) {
	t.Helper()
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(t.Context(), runnerEpicID, turn, plan, claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// addCallerEngine stores a headless session of kind with row id id.
func addCallerEngine(t *testing.T, s *store.Store, id, kind string) {
	t.Helper()
	add := store.EngineAdd{ID: id, Dir: t.TempDir(), State: "done", Kind: kind}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{add}, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine add session %s: %v", id, err)
	}
}

// recordRunnerCall records call of the runner epic by caller.
func recordRunnerCall(t *testing.T, s *store.Store, caller store.EpicCaller, call string) {
	t.Helper()
	recorded, err := s.RecordEpicCall(t.Context(), runnerEpicID, caller, call, claimedAt())
	if err != nil || !recorded {
		t.Fatalf("RecordEpicCall %s = %v, %v, want recorded", call, recorded, err)
	}
}

// callerEpicQuestionPlan is a planner turn that asks the one question the question event carries.
func callerEpicQuestionPlan() store.EpicPlan {
	return store.EpicPlan{
		Questions: []store.EpicQuestionInput{shapedQuestion("which-store", "which store?")},
		Outcome:   "wait",
	}
}

// callerEpicQuestionEvent is the id of the event of the question callerEpicQuestionPlan asks.
const callerEpicQuestionEvent = "epic:" + runnerEpicID + ":Q1"

// callerEpicQuestionBlock is the block of the question callerEpicQuestionPlan asks, as the creator receives it.
const callerEpicQuestionBlock = `<orrery-event kind="question" id="epic:orrery-1a2b3c4/planner-runner:Q1">
Epic orrery-1a2b3c4/planner-runner (Plan the runner) asks a question.
Q1 which-store: which store?
  first: Synthetic first choice.
  second: Synthetic second choice.
  other: answer in your own words
  recommended: first (medium): Synthetic reason.
epic_answer answers it.
</orrery-event>`

func TestCallerEventEpicKindsReachTheCreatorOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		plan  store.EpicPlan
		reach func(ctx context.Context, s *store.Store) error
		event string
		block string
	}{
		{
			name: "park",
			plan: store.EpicPlan{Outcome: "wait"},
			reach: func(ctx context.Context, s *store.Store) error {
				return s.ParkEpic(ctx, runnerEpicID, store.ParkClassBranch, callerEpicParkedText, claimedAt())
			},
			event: "epic:" + runnerEpicID + ":Q1",
			block: `<orrery-event kind="park" id="epic:orrery-1a2b3c4/planner-runner:Q1">
Epic orrery-1a2b3c4/planner-runner (Plan the runner) is parked.
Q1 park:
  retry: end the park and start a turn on the waiting triggers, as orrery epic retry does
  other: answer in your own words
  recommended: retry (low): no model judges a park; retry runs the epic again on its waiting triggers
parked: the reason
epic_answer answers it.
</orrery-event>`,
		},
		{
			name:  "question",
			plan:  callerEpicQuestionPlan(),
			reach: func(context.Context, *store.Store) error { return nil },
			event: callerEpicQuestionEvent,
			block: callerEpicQuestionBlock,
		},
		{
			name: "release",
			plan: store.EpicPlan{Outcome: "wait"},
			reach: func(ctx context.Context, s *store.Store) error {
				return s.EndEpic(ctx, runnerEpicID, "released", "released it", nil, claimedAt())
			},
			event: "epic:" + runnerEpicID + ":released",
			block: `<orrery-event kind="release" id="epic:orrery-1a2b3c4/planner-runner:released">
Epic orrery-1a2b3c4/planner-runner (Plan the runner) is released. epic_get gives its report.
</orrery-event>`,
		},
		{
			name: "end",
			plan: store.EpicPlan{Outcome: "wait"},
			reach: func(ctx context.Context, s *store.Store) error {
				return s.EndEpic(ctx, runnerEpicID, "done", "done it", nil, claimedAt())
			},
			event: "epic:" + runnerEpicID + ":done",
			block: `<orrery-event kind="end" id="epic:orrery-1a2b3c4/planner-runner:done">
Epic orrery-1a2b3c4/planner-runner (Plan the runner) is done. epic_get gives its report.
</orrery-event>`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			s := openRunnerStore(t)
			plannedCallerEpic(t, s, c.plan)
			addCallerEngine(t, s, "creator", store.KindEngine)
			addCallerEngine(t, s, "reader", store.KindEngine)
			recordRunnerCall(t, s, store.EpicCaller{Session: "creator"}, "epic_create")
			recordRunnerCall(t, s, store.EpicCaller{Session: "reader"}, "epic_get")
			if err := c.reach(ctx, s); err != nil {
				t.Fatalf("reach %s: %v", c.name, err)
			}
			deliver := &fakeCallerDeliver{}
			passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))

			want := []enginehost.DeliverRequest{{ID: "creator", Text: c.block, PromptID: c.event}}
			if !reflect.DeepEqual(deliver.delivered, want) {
				t.Fatalf("delivered = %+v, want %+v: the creator only, once", deliver.delivered, want)
			}
		})
	}
}

func TestCallerEventEpicDeliveredOnceAcrossPasses(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, callerEpicQuestionPlan())
	addCallerEngine(t, s, "creator", store.KindEngine)
	recordRunnerCall(t, s, store.EpicCaller{Session: "creator"}, "epic_create")
	deliver := &fakeCallerDeliver{}
	sender := callerSenderOver(s, deliver.deliver, noWake(t))
	passEpics(ctx, t, callerEpicEventsOver(s, sender))

	passEpics(ctx, t, callerEpicEventsOver(s, sender))
	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))

	want := []enginehost.DeliverRequest{
		{ID: "creator", Text: callerEpicQuestionBlock, PromptID: callerEpicQuestionEvent},
	}
	if !reflect.DeepEqual(deliver.delivered, want) {
		t.Fatalf("delivered after later passes = %+v, want %+v", deliver.delivered, want)
	}
}

func TestCallerEventEpicBusyCreatorHeldThenDelivered(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, callerEpicQuestionPlan())
	addCallerEngine(t, s, "creator", store.KindEngine)
	recordRunnerCall(t, s, store.EpicCaller{Session: "creator"}, "epic_create")
	deliver := &fakeCallerDeliver{err: enginehost.ErrSessionBusy}
	pass := callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t)))

	passEpics(ctx, t, pass)
	if len(deliver.delivered) != 0 {
		t.Fatalf("delivered while the creator is busy = %+v, want none", deliver.delivered)
	}
	if marked, err := s.CallerEventMarked(ctx, callerEpicQuestionEvent, "creator"); err != nil || marked {
		t.Fatalf("CallerEventMarked while busy = %v, %v, want not marked", marked, err)
	}

	deliver.err = nil
	passEpics(ctx, t, pass)
	want := []enginehost.DeliverRequest{
		{ID: "creator", Text: callerEpicQuestionBlock, PromptID: callerEpicQuestionEvent},
	}
	if !reflect.DeepEqual(deliver.delivered, want) {
		t.Fatalf("delivered once the creator is idle = %+v, want %+v", deliver.delivered, want)
	}
}

func TestCallerEventEpicStoppedCreatorNotDelivered(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, callerEpicQuestionPlan())
	addCallerEngine(t, s, "creator", store.KindEngine)
	recordRunnerCall(t, s, store.EpicCaller{Session: "creator"}, "epic_create")
	deliver := &fakeCallerDeliver{err: enginehost.ErrCallerStopped}

	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))

	if len(deliver.delivered) != 0 {
		t.Fatalf("delivered to a stopped creator = %+v, want none", deliver.delivered)
	}
	if marked, err := s.CallerEventMarked(ctx, callerEpicQuestionEvent, "creator"); err != nil || marked {
		t.Fatalf("CallerEventMarked for a stopped creator = %v, %v, want not marked", marked, err)
	}
}

func TestCallerEventEpicStepCreatorReceivesNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, callerEpicQuestionPlan())
	addCallerEngine(t, s, "step-creator", store.KindStep)
	recordRunnerCall(t, s, store.EpicCaller{Session: "step-creator"}, "epic_create")
	deliver := &fakeCallerDeliver{}

	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))

	if len(deliver.delivered) != 0 {
		t.Fatalf("delivered to a step creator = %+v, want none", deliver.delivered)
	}
}

func TestCallerEventEpicPaneCreatorHeldUntilKeysSentThenMarked(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	const server, pane = "-S /tmp/tmux-1000/orrery", "%7"
	if err := s.SyncPane(ctx, server, store.Pane{ID: "orrery-1", Pane: pane}, claimedAt()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	plannedCallerEpic(t, s, callerEpicQuestionPlan())
	recordRunnerCall(t, s, store.EpicCaller{Server: server, Pane: pane}, "epic_create")
	wake := &fakeCallerWake{}
	pass := callerEpicEventsOver(s, callerSenderOver(s, noDeliver(t), wake.wake))
	want := wakeCall{server: server, pane: pane, text: callerEpicQuestionBlock}

	passEpics(ctx, t, pass)
	if len(wake.calls) != 1 || wake.calls[0] != want {
		t.Fatalf("wakes while the pane is not idle = %+v, want one %+v", wake.calls, want)
	}
	if marked, err := s.CallerEventMarked(ctx, callerEpicQuestionEvent, "orrery-1"); err != nil || marked {
		t.Fatalf("CallerEventMarked while the pane is not idle = %v, %v, want not marked", marked, err)
	}

	wake.idle = true
	passEpics(ctx, t, pass)
	if len(wake.calls) != 2 || wake.calls[1] != want {
		t.Fatalf("wakes once the pane is idle = %+v, want a second %+v", wake.calls, want)
	}
	if marked, err := s.CallerEventMarked(ctx, callerEpicQuestionEvent, "orrery-1"); err != nil || !marked {
		t.Fatalf("CallerEventMarked after the keys were sent = %v, %v, want marked", marked, err)
	}

	passEpics(ctx, t, pass)
	if len(wake.calls) != 2 {
		t.Fatalf("wakes after the mark = %d, want still 2", len(wake.calls))
	}
}

// noDeliver returns a Deliver that fails the test when it is called, for the tests with no headless caller.
func noDeliver(t *testing.T) func(context.Context, enginehost.DeliverRequest) error {
	t.Helper()
	return func(_ context.Context, req enginehost.DeliverRequest) error {
		t.Errorf("Deliver to %s, want no headless caller", req.ID)
		return nil
	}
}

func TestCallerEventEpicNoCreatorSendsNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, callerEpicQuestionPlan())
	addCallerEngine(t, s, "reader", store.KindEngine)
	recordRunnerCall(t, s, store.EpicCaller{Session: "reader"}, "epic_get")

	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, noDeliver(t), noWake(t))))
}

func TestCallerEventEpicCleanSendsNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, store.EpicPlan{Outcome: "wait"})
	addCallerEngine(t, s, "creator", store.KindEngine)
	recordRunnerCall(t, s, store.EpicCaller{Session: "creator"}, "epic_create")
	if err := s.EndEpic(ctx, runnerEpicID, store.PhaseClean, "", nil, claimedAt()); err != nil {
		t.Fatalf("EndEpic clean: %v", err)
	}

	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, noDeliver(t), noWake(t))))
}
