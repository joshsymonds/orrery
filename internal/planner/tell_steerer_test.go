package planner

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// tellAlpha is the invented epic the tell tests tell. It is titled "Tell alpha".
const tellAlpha = "orrery-7d8e9f0/tell-alpha"

// tellAt is the second the tell tests store a tell at.
func tellAt() time.Time {
	return time.Unix(1700000600, 0)
}

// createTellEpic stores tellAlpha in the accepted phase, steered by steerer, which is no steerer when it is "".
func createTellEpic(t *testing.T, s *store.Store, steerer string) {
	t.Helper()
	if err := s.CreateEpic(t.Context(), store.EpicRow{
		ID: tellAlpha, Repository: runnerRepo, Title: "Tell alpha", Approved: "approved text",
		Intent: "plan the next tasks", Care: "serious", Approach: "one planner turn", ReleaseThen: "release it",
		Checks: "just check", Phase: "accepted", Created: claimedAt().Add(-time.Minute),
		Changed: claimedAt().Add(-time.Minute), Steerer: steerer,
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "a trigger is claimed", Evidence: "runner test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", tellAlpha, err)
	}
}

// addTellSession stores a headless session with row id id in state.
func addTellSession(t *testing.T, s *store.Store, id, state string) {
	t.Helper()
	add := store.EngineAdd{ID: id, Dir: t.TempDir(), State: state, Kind: store.KindEngine}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{add}, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine add session %s: %v", id, err)
	}
}

// tellEpic stores text as a tell of tellAlpha, at tellAt.
func tellEpic(t *testing.T, s *store.Store, text string) {
	t.Helper()
	if err := s.TellEpic(t.Context(), tellAlpha, text, tellAt()); err != nil {
		t.Fatalf("TellEpic %q: %v", text, err)
	}
}

// passTells runs one pass of the caller events over s, sending headless callers through deliver, and fails the test on
// the pass's error.
func passTells(t *testing.T, s *store.Store, deliver func(context.Context, enginehost.DeliverRequest) error) {
	t.Helper()
	passEpics(t.Context(), t, callerEpicEventsOver(s, callerSenderOver(s, deliver, noWake(t))))
}

// moveTellEpic hands tellAlpha to the session to, as the owner's move at tellAt.
func moveTellEpic(t *testing.T, s *store.Store, to string) {
	t.Helper()
	if _, err := s.MoveEpic(t.Context(), tellAlpha, to, store.Maker{By: store.MakerOwner}, tellAt()); err != nil {
		t.Fatalf("MoveEpic to %s: %v", to, err)
	}
}

// undeliveredEvents returns the event id of each tell the store lists as undelivered, in its order.
func undeliveredEvents(t *testing.T, s *store.Store) []string {
	t.Helper()
	tells, err := s.UndeliveredTells(t.Context())
	if err != nil {
		t.Fatalf("UndeliveredTells: %v", err)
	}
	events := make([]string, 0, len(tells))
	for _, tell := range tells {
		events = append(events, store.EpicTellEvent(tell.Epic, tell.Seq))
	}
	return events
}

// promptIDsOf returns the prompt id of each request, in order.
func promptIDsOf(reqs []enginehost.DeliverRequest) []string {
	ids := make([]string, 0, len(reqs))
	for _, req := range reqs {
		ids = append(ids, req.PromptID)
	}
	return ids
}

// tellRequestsOf returns the requests whose text carries a tell event.
func tellRequestsOf(reqs []enginehost.DeliverRequest) []enginehost.DeliverRequest {
	var tells []enginehost.DeliverRequest
	for _, req := range reqs {
		if strings.Contains(req.Text, `<orrery-event kind="tell"`) {
			tells = append(tells, req)
		}
	}
	return tells
}

// tellIDsOf returns the prompt id of each request that carries a tell event, in order.
func tellIDsOf(reqs []enginehost.DeliverRequest) []string {
	return promptIDsOf(tellRequestsOf(reqs))
}

// tellSentCase is a tell that reaches its steerer as a tell event: the text the tell stores, and the block the steerer
// is sent. The met-at-base text is the one decide's metAtBaseTell writes, and the release-resumed text is the one the
// release resume writes; both writers are unexported, so the tests store the same texts.
type tellSentCase struct {
	name, text, block string
}

// tellSentCases returns a case for each kind of tell that reaches its steerer as a tell event.
func tellSentCases() []tellSentCase {
	return []tellSentCase{
		{
			name: "plain",
			text: "check the gate",
			block: `<orrery-event kind="tell" id="epic:orrery-7d8e9f0/tell-alpha:tell:1" epic="orrery-7d8e9f0/tell-alpha" seq="1">
Epic orrery-7d8e9f0/tell-alpha (Tell alpha) tells: check the gate
</orrery-event>`,
		},
		{
			name: "met-at-base",
			text: "workflow node red: R1 met at the epic's base main, recorded as met there, and the epic goes on",
			block: `<orrery-event kind="tell" id="epic:orrery-7d8e9f0/tell-alpha:tell:1" epic="orrery-7d8e9f0/tell-alpha" seq="1">
Epic orrery-7d8e9f0/tell-alpha (Tell alpha) tells: workflow node red: R1 met at the epic's base main, recorded as met there, and the epic goes on
</orrery-event>`,
		},
		{
			name: "release-resumed",
			text: "release resumed at step 2, publish",
			block: `<orrery-event kind="tell" id="epic:orrery-7d8e9f0/tell-alpha:tell:1" epic="orrery-7d8e9f0/tell-alpha" seq="1">
Epic orrery-7d8e9f0/tell-alpha (Tell alpha) tells: release resumed at step 2, publish
</orrery-event>`,
		},
	}
}

// TestTellReachesSteererTellSentAsTellEvent: a plain tell, a met-at-base tell and a release resumed tell each reach the
// steerer once, as one tell event that holds its epic, seq and text.
func TestTellReachesSteererTellSentAsTellEvent(t *testing.T) {
	t.Parallel()
	for _, c := range tellSentCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := openRunnerStore(t)
			createTellEpic(t, s, "steerer")
			addTellSession(t, s, "steerer", "done")
			tellEpic(t, s, c.text)
			deliver := &fakeCallerDeliver{}

			passTells(t, s, deliver.deliver)

			want := []enginehost.DeliverRequest{{
				ID: "steerer", Text: c.block, PromptID: store.EpicTellEvent(tellAlpha, 1),
			}}
			if !reflect.DeepEqual(deliver.delivered, want) {
				t.Fatalf("delivered = %+v, want %+v", deliver.delivered, want)
			}
			if left := undeliveredEvents(t, s); len(left) != 0 {
				t.Fatalf("undelivered after the send = %q, want none", left)
			}
		})
	}
}

// TestTellReachesSteererSilentMoveTellSentAsNone: a silent move tell is marked delivered, and no tell event is sent for
// it.
func TestTellReachesSteererSilentMoveTellSentAsNone(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "steerer")
	addTellSession(t, s, "steerer", "done")
	tellEpic(t, s, "T1 implementer moved to a fresh worktree")
	deliver := &fakeCallerDeliver{}

	passTells(t, s, deliver.deliver)

	if got := tellIDsOf(deliver.delivered); len(got) != 0 {
		t.Fatalf("tell events sent = %q, want none", got)
	}
	if left := undeliveredEvents(t, s); len(left) != 0 {
		t.Fatalf("undelivered after the pass = %q, want none", left)
	}
}

// TestTellReachesSteererParkQuestionTellSentAsNone: the tell of a park question is marked delivered and no tell event
// is sent for it. The park reaches the steerer as its own event, and a plain tell after it is still sent as a tell
// event.
func TestTellReachesSteererParkQuestionTellSentAsNone(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "steerer")
	addTellSession(t, s, "steerer", "done")
	if err := s.ParkEpic(t.Context(), tellAlpha, store.ParkClassBranch, "the reason", tellAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	tellEpic(t, s, "check the gate")
	deliver := &fakeCallerDeliver{}

	passTells(t, s, deliver.deliver)

	want := []string{store.EpicQuestionEvent(tellAlpha, 1), store.EpicTellEvent(tellAlpha, 2)}
	if got := promptIDsOf(deliver.delivered); !slices.Equal(got, want) {
		t.Fatalf("prompt ids sent = %q, want %q: the park's own event, then the plain tell only", got, want)
	}
	if left := undeliveredEvents(t, s); len(left) != 0 {
		t.Fatalf("undelivered after the pass = %q, want none", left)
	}
}

// TestTellReachesSteererEndEpicTellSentAsNone: the tell an ending writes is stored delivered, so no tell event is sent
// for it. The ending reaches the steerer as its own event, and a plain tell before it is still sent as a tell event.
func TestTellReachesSteererEndEpicTellSentAsNone(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "steerer")
	addTellSession(t, s, "steerer", "done")
	tellEpic(t, s, "check the gate")
	if err := s.EndEpic(t.Context(), tellAlpha, "done", "done it", nil, tellAt()); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}
	deliver := &fakeCallerDeliver{}

	passTells(t, s, deliver.deliver)

	want := []string{store.EpicPhaseEvent(tellAlpha, "done"), store.EpicTellEvent(tellAlpha, 1)}
	if got := promptIDsOf(deliver.delivered); !slices.Equal(got, want) {
		t.Fatalf("prompt ids sent = %q, want %q: the ending's own event, then the plain tell only", got, want)
	}
	if left := undeliveredEvents(t, s); len(left) != 0 {
		t.Fatalf("undelivered after the pass = %q, want none", left)
	}
}

// TestTellDeliveredOnceRetriedAfterFailedDeliver: a tell whose delivery fails stays undelivered, and the next pass
// sends it.
func TestTellDeliveredOnceRetriedAfterFailedDeliver(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "steerer")
	addTellSession(t, s, "steerer", "done")
	tellEpic(t, s, "check the gate")
	errDeliver := errors.New("synthetic deliver failure")
	deliver := &fakeCallerDeliver{err: errDeliver}
	pass := callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t)))
	first := store.EpicTellEvent(tellAlpha, 1)

	if err := pass.Pass(t.Context()); !errors.Is(err, errDeliver) {
		t.Fatalf("Pass with a failed delivery = %v, want the delivery error", err)
	}
	if left := undeliveredEvents(t, s); !slices.Equal(left, []string{first}) {
		t.Fatalf("undelivered after the failed delivery = %q, want %q", left, []string{first})
	}

	deliver.err = nil
	passTells(t, s, deliver.deliver)

	if got := promptIDsOf(deliver.delivered); !slices.Equal(got, []string{first}) {
		t.Fatalf("prompt ids sent by the next pass = %q, want %q", got, []string{first})
	}
	if left := undeliveredEvents(t, s); len(left) != 0 {
		t.Fatalf("undelivered after the next pass = %q, want none", left)
	}
}

// TestTellDeliveredOnceNotResentAfterLaterPassOrRestart: a delivered tell is not sent by a later pass, nor by a new
// EpicCallerEvents over the store that is closed and reopened from its file.
func TestTellDeliveredOnceNotResentAfterLaterPassOrRestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	createTellEpic(t, s, "steerer")
	addTellSession(t, s, "steerer", "done")
	tellEpic(t, s, "check the gate")
	deliver := &fakeCallerDeliver{}
	passTells(t, s, deliver.deliver)
	passTells(t, s, deliver.deliver)
	if err = s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Errorf("Close reopened store: %v", closeErr)
		}
	})
	restarted := &fakeCallerDeliver{}
	passTells(t, reopened, restarted.deliver)

	first := store.EpicTellEvent(tellAlpha, 1)
	if got := promptIDsOf(deliver.delivered); !slices.Equal(got, []string{first}) {
		t.Fatalf("prompt ids sent before the restart = %q, want %q once", got, []string{first})
	}
	if got := promptIDsOf(restarted.delivered); len(got) != 0 {
		t.Fatalf("prompt ids sent after the restart = %q, want none", got)
	}
}

// TestTellDeliveredOnceSentInSeqOrder: the tells of an epic go out in seq order.
func TestTellDeliveredOnceSentInSeqOrder(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "steerer")
	addTellSession(t, s, "steerer", "done")
	tellEpic(t, s, "first")
	tellEpic(t, s, "second")
	deliver := &fakeCallerDeliver{}

	passTells(t, s, deliver.deliver)

	want := []string{store.EpicTellEvent(tellAlpha, 1), store.EpicTellEvent(tellAlpha, 2)}
	if got := promptIDsOf(deliver.delivered); !slices.Equal(got, want) {
		t.Fatalf("prompt ids sent = %q, want %q in seq order", got, want)
	}
}

// TestTellDeliveredOnceHeldSendBlocksLaterTells: a busy steerer holds the first tell, so the later tell is not
// attempted in that pass. Once the steerer is idle, both go out in seq order.
func TestTellDeliveredOnceHeldSendBlocksLaterTells(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "steerer")
	addTellSession(t, s, "steerer", "done")
	tellEpic(t, s, "first")
	tellEpic(t, s, "second")
	first := store.EpicTellEvent(tellAlpha, 1)
	second := store.EpicTellEvent(tellAlpha, 2)
	busy := true
	var attempted []string
	var delivered []enginehost.DeliverRequest
	deliver := func(_ context.Context, req enginehost.DeliverRequest) error {
		attempted = append(attempted, req.PromptID)
		if busy {
			return enginehost.ErrSessionBusy
		}
		delivered = append(delivered, req)
		return nil
	}

	passTells(t, s, deliver)
	if !slices.Equal(attempted, []string{first}) {
		t.Fatalf("attempts while the steerer is busy = %q, want only %q", attempted, first)
	}
	if left := undeliveredEvents(t, s); !slices.Equal(left, []string{first, second}) {
		t.Fatalf("undelivered while the steerer is busy = %q, want both tells", left)
	}

	busy = false
	passTells(t, s, deliver)
	if got := promptIDsOf(delivered); !slices.Equal(got, []string{first, second}) {
		t.Fatalf("prompt ids sent once idle = %q, want %q in seq order", got, []string{first, second})
	}
	if left := undeliveredEvents(t, s); len(left) != 0 {
		t.Fatalf("undelivered once idle = %q, want none", left)
	}
}

// TestTellDeliveredOnceNoSteererSendsNothing: an epic with no steerer sends none of its tells, and they stay
// undelivered until a steerer is given the epic, which then gets them.
func TestTellDeliveredOnceNoSteererSendsNothing(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "")
	addTellSession(t, s, "steerer", "done")
	tellEpic(t, s, "check the gate")
	first := store.EpicTellEvent(tellAlpha, 1)

	passTells(t, s, noDeliver(t))

	if left := undeliveredEvents(t, s); !slices.Equal(left, []string{first}) {
		t.Fatalf("undelivered with no steerer = %q, want the tell", left)
	}
	moveTellEpic(t, s, "steerer")
	given := &fakeCallerDeliver{}
	passTells(t, s, given.deliver)

	if got := tellIDsOf(given.delivered); !slices.Equal(got, []string{first}) {
		t.Fatalf("tell events sent to the steerer given the epic = %q, want %q", got, []string{first})
	}
}

// TestTellDeliveredOnceStoppedSteererSendsNothing: a steerer whose sessions row is stopped is sent none of the epic's
// tells, and they stay undelivered until a live steerer is given the epic, which then gets them.
func TestTellDeliveredOnceStoppedSteererSendsNothing(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "steerer")
	addTellSession(t, s, "steerer", "stopped")
	addTellSession(t, s, "live-steerer", "done")
	tellEpic(t, s, "check the gate")
	first := store.EpicTellEvent(tellAlpha, 1)

	passTells(t, s, noDeliver(t))

	if left := undeliveredEvents(t, s); !slices.Equal(left, []string{first}) {
		t.Fatalf("undelivered for a stopped steerer = %q, want the tell", left)
	}
	moveTellEpic(t, s, "live-steerer")
	live := &fakeCallerDeliver{}
	passTells(t, s, live.deliver)

	if got := tellIDsOf(live.delivered); !slices.Equal(got, []string{first}) {
		t.Fatalf("tell events sent to the live steerer = %q, want %q", got, []string{first})
	}
}

// TestTellDeliveredOnceHandoverGivesNewSteererOnlyUndelivered: after the steerer changes, the new steerer is sent only
// the tells the old steerer was not sent.
func TestTellDeliveredOnceHandoverGivesNewSteererOnlyUndelivered(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "old-steerer")
	addTellSession(t, s, "old-steerer", "done")
	addTellSession(t, s, "new-steerer", "done")
	tellEpic(t, s, "one")
	oldDeliver := &fakeCallerDeliver{}
	passTells(t, s, oldDeliver.deliver)

	tellEpic(t, s, "two")
	moveTellEpic(t, s, "new-steerer")
	newDeliver := &fakeCallerDeliver{}
	passTells(t, s, newDeliver.deliver)

	if got := tellIDsOf(oldDeliver.delivered); !slices.Equal(got, []string{store.EpicTellEvent(tellAlpha, 1)}) {
		t.Fatalf("tell events sent to the old steerer = %q, want only tell 1", got)
	}
	if got := tellIDsOf(newDeliver.delivered); !slices.Equal(got, []string{store.EpicTellEvent(tellAlpha, 2)}) {
		t.Fatalf("tell events sent to the new steerer = %q, want only tell 2", got)
	}
	if left := undeliveredEvents(t, s); len(left) != 0 {
		t.Fatalf("undelivered after the handover pass = %q, want none", left)
	}
}
