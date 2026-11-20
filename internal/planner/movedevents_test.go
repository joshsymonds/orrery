package planner

import (
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// movedSteeringInstruction is the first line of a steering prompt, as enginehost.SteeringPrompt leads it.
const movedSteeringInstruction = "orreryd builds this epic; this session steers it by answering its questions and events, " +
	"and changes nothing in the repository."

// movedLeftBlock is the moved event the conversation left, session A, hears when the runner epic moves to session B by the
// owner. event is its id.
func movedLeftBlock(event string) string {
	return `<orrery-event kind="moved" id="` + event + `">
Epic orrery-1a2b3c4/planner-runner (Plan the runner) moved to session B, by the owner.
</orrery-event>`
}

// requireMovedTakerBlock fails the test unless got is the moved event with id event, sent to the taker B, whose body is the
// steering prompt of the runner epic: its instruction first, then its contract, then the Log: line that the decisions
// follow.
func requireMovedTakerBlock(t *testing.T, got enginehost.DeliverRequest, event string) {
	t.Helper()
	const id = "B"
	prefix := `<orrery-event kind="moved" id="` + event + `">` + "\n" + movedSteeringInstruction +
		"\nEpic: " + runnerEpicID + "\nTitle: Plan the runner\n"
	contractEnd := "\nRelease then: release it\nChecks: just check\nLog:\n"
	if got.ID != id || got.PromptID != event || !strings.HasPrefix(got.Text, prefix) ||
		!strings.Contains(got.Text, contractEnd) || !strings.HasSuffix(got.Text, "\n</orrery-event>") {
		t.Fatalf(
			"delivered %+v, want the steering prompt of epic %s to session %s under %s",
			got,
			runnerEpicID,
			id,
			event,
		)
	}
}

func TestMovedEventsOneEachSideThenNoneOnLaterPasses(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, "A", store.EpicPlan{Outcome: "wait"})
	addCallerEngine(t, s, "A", store.KindEngine)
	addCallerEngine(t, s, "B", store.KindEngine)
	action, err := s.MoveEpic(ctx, runnerEpicID, "B", store.Maker{By: store.MakerOwner}, claimedAt())
	if err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	event := store.EpicActionEvent(runnerEpicID, action.Seq)
	deliver := &fakeCallerDeliver{}
	pass := callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t)))

	passEpics(ctx, t, pass)
	if len(deliver.delivered) != 2 {
		t.Fatalf("delivered after the move = %+v, want one moved event to each side", deliver.delivered)
	}
	if got := deliver.delivered[0]; got.ID != "A" || got.PromptID != event || got.Text != movedLeftBlock(event) {
		t.Fatalf("left delivery = %+v, want the moved event naming B and the owner", got)
	}
	requireMovedTakerBlock(t, deliver.delivered[1], event)

	passEpics(ctx, t, pass)
	if len(deliver.delivered) != 2 {
		t.Fatalf("delivered after a second pass = %+v, want nothing more", deliver.delivered)
	}
}

func TestMovedEventsOpenQuestionGoesToTakerNotAgain(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, "A", callerEpicQuestionPlan())
	addCallerEngine(t, s, "A", store.KindEngine)
	addCallerEngine(t, s, "B", store.KindEngine)
	deliver := &fakeCallerDeliver{}
	pass := callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t)))
	passEpics(ctx, t, pass)

	action, err := s.MoveEpic(ctx, runnerEpicID, "B", store.Maker{By: store.MakerOwner}, claimedAt())
	if err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	event := store.EpicActionEvent(runnerEpicID, action.Seq)
	passEpics(ctx, t, pass)

	if len(deliver.delivered) != 4 {
		t.Fatalf("delivered = %+v, want the question to A, the moved events, and the question to B", deliver.delivered)
	}
	if got := deliver.delivered[0]; got.ID != "A" || got.PromptID != callerEpicQuestionEvent {
		t.Fatalf("first delivery = %+v, want the question to A before the move", got)
	}
	if got := deliver.delivered[1]; got.ID != "A" || got.PromptID != event || got.Text != movedLeftBlock(event) {
		t.Fatalf("second delivery = %+v, want the moved event to A", got)
	}
	requireMovedTakerBlock(t, deliver.delivered[2], event)
	want := enginehost.DeliverRequest{ID: "B", Text: callerEpicQuestionBlock, PromptID: callerEpicQuestionEvent}
	if !reflect.DeepEqual(deliver.delivered[3], want) {
		t.Fatalf("fourth delivery = %+v, want the question to B only, %+v", deliver.delivered[3], want)
	}
}

func TestMovedEventsReleaseAfterMoveGoesToTakerOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, "A", store.EpicPlan{Outcome: "wait"})
	addCallerEngine(t, s, "A", store.KindEngine)
	addCallerEngine(t, s, "B", store.KindEngine)
	if _, err := s.MoveEpic(ctx, runnerEpicID, "B", store.Maker{By: store.MakerOwner}, claimedAt()); err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	deliver := &fakeCallerDeliver{}
	pass := callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t)))
	passEpics(ctx, t, pass)
	if err := s.EndEpic(ctx, runnerEpicID, "released", "released it", nil, claimedAt()); err != nil {
		t.Fatalf("EndEpic released: %v", err)
	}
	before := len(deliver.delivered)

	passEpics(ctx, t, pass)

	want := enginehost.DeliverRequest{
		ID: "B",
		Text: `<orrery-event kind="release" id="epic:orrery-1a2b3c4/planner-runner:released">
Epic orrery-1a2b3c4/planner-runner (Plan the runner) is released.
epic_get with part report reads its report.
</orrery-event>`,
		PromptID: "epic:" + runnerEpicID + ":released",
	}
	if got := deliver.delivered[before:]; !reflect.DeepEqual(got, []enginehost.DeliverRequest{want}) {
		t.Fatalf("delivered after the release = %+v, want the release to B only, %+v", got, want)
	}
}

func TestOwnerActionEventNoteAndKickReachSteererOnceNotTaker(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, "A", store.EpicPlan{Outcome: "wait"})
	addCallerEngine(t, s, "A", store.KindEngine)
	addCallerEngine(t, s, "B", store.KindEngine)
	owner := store.Maker{By: store.MakerOwner}
	if _, err := s.AnswerEpic(ctx, runnerEpicID, "note-1", "a note", owner, claimedAt()); err != nil {
		t.Fatalf("AnswerEpic note: %v", err)
	}
	if err := s.KickEpic(ctx, runnerEpicID, "kick text", owner, claimedAt()); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	deliver := &fakeCallerDeliver{}
	pass := callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t)))

	passEpics(ctx, t, pass)
	wantBefore := []enginehost.DeliverRequest{
		{ID: "A", Text: `<orrery-event kind="action" id="epic:orrery-1a2b3c4/planner-runner:D1">
Epic orrery-1a2b3c4/planner-runner (Plan the runner): the owner added a note.
text: a note
</orrery-event>`, PromptID: "epic:" + runnerEpicID + ":D1"},
		{ID: "A", Text: `<orrery-event kind="action" id="epic:orrery-1a2b3c4/planner-runner:D2">
Epic orrery-1a2b3c4/planner-runner (Plan the runner): the owner kicked the epic.
text: kick text
</orrery-event>`, PromptID: "epic:" + runnerEpicID + ":D2"},
	}
	if !reflect.DeepEqual(deliver.delivered, wantBefore) {
		t.Fatalf("delivered = %+v, want the note and the kick to A once, %+v", deliver.delivered, wantBefore)
	}

	action, err := s.MoveEpic(ctx, runnerEpicID, "B", owner, claimedAt())
	if err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}
	passEpics(ctx, t, pass)

	after := deliver.delivered[len(wantBefore):]
	if len(after) != 2 || after[0].ID != "A" ||
		after[0].Text != movedLeftBlock(store.EpicActionEvent(runnerEpicID, action.Seq)) {
		t.Fatalf("delivered after the move = %+v, want the moved event to A and the moved event to B only", after)
	}
	requireMovedTakerBlock(t, after[1], store.EpicActionEvent(runnerEpicID, action.Seq))
}

func TestOwnerActionEventNoteTextCutToSixHundredRunes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, "A", store.EpicPlan{Outcome: "wait"})
	addCallerEngine(t, s, "A", store.KindEngine)
	long := strings.Repeat("n", 601)
	if _, err := s.AnswerEpic(
		ctx,
		runnerEpicID,
		"note-1",
		long,
		store.Maker{By: store.MakerOwner},
		claimedAt(),
	); err != nil {
		t.Fatalf("AnswerEpic note: %v", err)
	}
	deliver := &fakeCallerDeliver{}

	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))

	wantText := "text: " + strings.Repeat("n", 600) + "…"
	if len(deliver.delivered) != 1 || !strings.Contains(deliver.delivered[0].Text, "\n"+wantText+"\n") {
		t.Fatalf("delivered = %+v, want one note event whose text is cut to 600 runes", deliver.delivered)
	}
}
