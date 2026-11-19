package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// claimGapsQuestion is the gaps question endClaimEpicWithGaps asks: two gaps, each with its reason, and release
// recommended.
func claimGapsQuestion() GapsQuestion {
	return GapsQuestion{
		Gaps:        []string{"security-1: open after 2 rounds", "R2: no check ran"},
		Recommended: ChoiceRelease,
		Why:         "the open finding is minor and the check that did not run covers docs only",
	}
}

// endClaimEpicWithGaps ends claimEpic with ended_with_gaps and the gaps of claimGapsQuestion, at claimedAt.
func endClaimEpicWithGaps(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	question := claimGapsQuestion()
	if err := s.EndEpicWithGaps(ctx, claimEpic, "Ended with 2 gaps.", question, nil, claimedAt()); err != nil {
		t.Fatalf("EndEpicWithGaps: %v", err)
	}
}

// claimGapsOpen is the open gaps question that endClaimEpicWithGaps leaves on claimEpic, as the store reads it back.
func claimGapsOpen() EpicQuestion {
	return EpicQuestion{
		Seq: 1, Turn: 0, State: QuestionOpen, Label: GapsLabel,
		Text: "Gaps orrery could not resolve:\n- security-1: open after 2 rounds\n- R2: no check ran",
		Choices: []EpicChoice{
			{ID: "release", Does: "release the epic with these gaps, recorded as your decision"},
			{ID: "correct", Does: "give your text to the planner as a correction and return the epic to correcting"},
			{ID: "close", Does: "close the epic as abandoned"},
		},
		Recommended: "release",
		Why:         "the open finding is minor and the check that did not run covers docs only",
		Confidence:  "medium",
	}
}

// triggersOf returns the triggers of claimEpic as the store holds them.
func triggersOf(t *testing.T, s *Store) []sqlcgen.EpicTrigger {
	t.Helper()
	triggers, err := s.fixtureQueries().ListEpicTriggers(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("ListEpicTriggers: %v", err)
	}
	return triggers
}

// TestEndEpicWithGapsOpensGapQuestion checks that an epic ended with gaps is ended_with_gaps and holds one open gaps
// question whose text lists each gap with its reason, whose choices are release, correct and close, and which
// recommends release, and that the question is owed to the session that created the epic.
func TestEndEpicWithGapsOpensGapQuestion(t *testing.T) {
	t.Parallel()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1")
	createEpic(t, s, fullEpic(claimEpic))
	recordPendingCreator(t, s, claimEpic, "engine-1")
	endClaimEpicWithGaps(t, s)

	row := storedEpicRow(t, s)
	if row.Phase != phaseEndedWithGaps {
		t.Errorf("phase after the gaps = %q, want ended_with_gaps", row.Phase)
	}
	if want := []EpicQuestion{claimGapsOpen()}; !reflect.DeepEqual(row.Questions, want) {
		t.Errorf("questions after the gaps = %+v, want %+v", row.Questions, want)
	}
	want := []PendingEvent{
		{ID: EpicQuestionEvent(claimEpic, 1), Kind: CallerKindQuestion, Epic: claimEpic, Title: "Synthetic epic"},
		{
			ID: EpicPhaseEvent(claimEpic, phaseEndedWithGaps), Kind: CallerKindEnd, Epic: claimEpic,
			Title: "Synthetic epic",
		},
	}
	if got := readPending(t, path, "engine-1"); !reflect.DeepEqual(got, want) {
		t.Errorf("pending events of the creating session = %+v, want %+v", got, want)
	}
}

// TestEndEpicWithGapsRefusesBadGapQuestion checks that a gaps question with no gap, or one that recommends neither
// release nor correct, is refused and writes nothing.
func TestEndEpicWithGapsRefusesBadGapQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	before := storedEpicRow(t, s)
	refused := []GapsQuestion{
		{Gaps: nil, Recommended: ChoiceRelease, Why: "no gap is named"},
		{Gaps: []string{"R2: no check ran"}, Recommended: ChoiceClose, Why: "close is not a recommendation here"},
	}
	for _, question := range refused {
		err := s.EndEpicWithGaps(ctx, claimEpic, "Ended with gaps.", question, nil, claimedAt())
		if _, ok := errors.AsType[RefusalError](err); !ok {
			t.Errorf("EndEpicWithGaps(%+v) = %v, want a RefusalError", question, err)
		}
	}
	if after := storedEpicRow(t, s); !reflect.DeepEqual(after, before) {
		t.Errorf("row after the refusals = %+v, want %+v", after, before)
	}
}

// TestReleaseAnswerReleasesGapQuestion checks that answering release to the gaps question leaves the epic clean, with
// the owner's decision stored against the question.
func TestReleaseAnswerReleasesGapQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endClaimEpicWithGaps(t, s)

	if _, err := s.AnswerQuestion(ctx, claimEpic, "owner-release", "", 1, ChoiceRelease, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion release: %v", err)
	}
	row := storedEpicRow(t, s)
	if row.Phase != PhaseClean {
		t.Errorf("phase after release = %q, want clean", row.Phase)
	}
	answered := claimGapsOpen()
	answered.State, answered.Choice = QuestionAnswered, ChoiceRelease
	if want := []EpicQuestion{answered}; !reflect.DeepEqual(row.Questions, want) {
		t.Errorf("questions after release = %+v, want %+v", row.Questions, want)
	}
	if !slices.ContainsFunc(row.Decisions, func(d EpicDecision) bool {
		return d.ID == "owner-release" && d.Question == 1
	}) {
		t.Errorf("decisions after release = %+v, want owner-release answering question 1", row.Decisions)
	}
}

// TestCorrectAnswerReturnsToCorrectingGapQuestion checks that blank text is refused and writes nothing, and that
// correct with text stores the decision, adds the pending trigger gaps:<id> whose cause holds the text, and returns the
// epic to correcting.
func TestCorrectAnswerReturnsToCorrectingGapQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endClaimEpicWithGaps(t, s)

	before := storedEpicRow(t, s)
	_, err := s.AnswerQuestion(ctx, claimEpic, "owner-correct", " ", 1, ChoiceCorrect, claimedAt())
	if !errors.Is(err, ErrOtherNeedsText) {
		t.Errorf("AnswerQuestion correct with blank text = %v, want ErrOtherNeedsText", err)
	}
	if after := storedEpicRow(t, s); !reflect.DeepEqual(after, before) {
		t.Errorf("row after the blank correction = %+v, want %+v", after, before)
	}
	created := sqlcgen.EpicTrigger{
		Epic: claimEpic, ID: "created", Cause: "epic created", State: "pending", Created: epicCreated().Unix(),
	}
	if triggers := triggersOf(t, s); !reflect.DeepEqual(triggers, []sqlcgen.EpicTrigger{created}) {
		t.Errorf("triggers after the blank correction = %+v, want only the create trigger", triggers)
	}

	at := claimedAt().Add(time.Minute)
	_, err = s.AnswerQuestion(ctx, claimEpic, "owner-correct", "Keep the old tasks", 1, ChoiceCorrect, at)
	if err != nil {
		t.Fatalf("AnswerQuestion correct: %v", err)
	}
	row := storedEpicRow(t, s)
	if row.Phase != "correcting" {
		t.Errorf("phase after correct = %q, want correcting", row.Phase)
	}
	if !slices.ContainsFunc(row.Decisions, func(d EpicDecision) bool {
		return d.ID == "owner-correct" && d.Text == "Keep the old tasks" && d.Question == 1
	}) {
		t.Errorf("decisions after correct = %+v, want owner-correct with its text answering question 1", row.Decisions)
	}
	want := []sqlcgen.EpicTrigger{
		created,
		{
			Epic: claimEpic, ID: "answer:owner-correct", Cause: "answer owner-correct to Q1", State: "pending",
			Created: at.Unix(),
		},
		{
			Epic: claimEpic, ID: "gaps:owner-correct", Cause: "owner correction of the gaps: Keep the old tasks",
			State: "pending", Created: at.Unix(),
		},
	}
	if got := triggersOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("triggers after correct = %+v, want %+v", got, want)
	}
}

// TestCloseAnswerAbandonsGapQuestion checks that answering close to the gaps question closes the epic abandoned.
func TestCloseAnswerAbandonsGapQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endClaimEpicWithGaps(t, s)

	if _, err := s.AnswerQuestion(ctx, claimEpic, "owner-close", "", 1, ChoiceClose, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion close: %v", err)
	}
	row := storedEpicRow(t, s)
	if row.Phase != PhaseClosedAbandoned {
		t.Errorf("phase after close = %q, want closed_abandoned", row.Phase)
	}
	answered := claimGapsOpen()
	answered.State, answered.Choice = QuestionAnswered, ChoiceClose
	if want := []EpicQuestion{answered}; !reflect.DeepEqual(row.Questions, want) {
		t.Errorf("questions after close = %+v, want %+v", row.Questions, want)
	}
}

// TestEpicReleaseAnswersOpenGapQuestion checks that releasing an epic that ended with gaps through ReleaseEpic leaves
// no open gaps question, the question answered with release.
func TestEpicReleaseAnswersOpenGapQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endClaimEpicWithGaps(t, s)

	if err := s.ReleaseEpic(ctx, claimEpic, nil, claimedAt()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	open, err := s.OpenQuestions(ctx, claimEpic)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if len(open) != 0 {
		t.Errorf("open questions after ReleaseEpic = %+v, want none", open)
	}
	answered := claimGapsOpen()
	answered.State, answered.Choice = QuestionAnswered, ChoiceRelease
	if row := storedEpicRow(t, s); !reflect.DeepEqual(row.Questions, []EpicQuestion{answered}) {
		t.Errorf("questions after ReleaseEpic = %+v, want the gaps question answered with release", row.Questions)
	}
}
