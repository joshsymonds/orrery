package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// overtakenAt is the second the overtaken tests end or start their epic at.
func overtakenAt() time.Time {
	return time.Unix(1700002000, 0)
}

// overtakenKeepOld is the question the overtaken tests leave open: the planner's first question on claimEpic.
func overtakenKeepOld() EpicQuestionInput {
	return shapedQuestion("keep-old-tasks", "Keep the old tasks?")
}

// overtakenKeepOldQ1 returns overtakenKeepOld, stored as Q1 and asked by turn 1, as the store reads it back once
// overtaken.
func overtakenKeepOldQ1() EpicQuestion {
	stored := openStored(overtakenKeepOld(), 1, 1)
	stored.State = QuestionOvertaken
	return stored
}

// lastDecision returns the newest decision of claimEpic.
func lastDecision(t *testing.T, s *Store) EpicDecision {
	t.Helper()
	decisions := storedEpicRow(t, s).Decisions
	if len(decisions) == 0 {
		t.Fatalf("claimEpic holds no decision")
	}
	return decisions[len(decisions)-1]
}

// overtakenDecisionsOf returns the decisions of claimEpic whose id names an overtaken question.
func overtakenDecisionsOf(t *testing.T, s *Store) []EpicDecision {
	t.Helper()
	var out []EpicDecision
	for _, decision := range storedEpicRow(t, s).Decisions {
		if strings.HasPrefix(decision.ID, "overtaken:") {
			out = append(out, decision)
		}
	}
	return out
}

// claimTells returns the texts of the tells of claimEpic, in seq order, as EpicHistory reads them.
func claimTells(t *testing.T, s *Store) []string {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	texts := make([]string, 0, len(history.Tells))
	for _, tell := range history.Tells {
		texts = append(texts, tell.Text)
	}
	return texts
}

// releaseClaimEpic ends claimEpic released at at, as the release node's ending does.
func releaseClaimEpic(t *testing.T, s *Store, at time.Time) {
	t.Helper()
	if err := s.EndEpic(context.Background(), claimEpic, "released", "Released. No gaps.", nil, at); err != nil {
		t.Fatalf("EndEpic released: %v", err)
	}
}

// TestReleaseOvertakesEachOpenQuestionWithADecisionAndATell checks that the write that releases an epic closes its open
// question as overtaken, leaves its answered question as it is, records orreryd's decision that names the question and
// why, adds a tell its steerer has not been sent and its owner has not been told, and adds no trigger, since no planner
// turn takes an answer once the epic is released. The answer's trigger, still pending, is consumed in the same write,
// so the planner lists the released epic no more. A second release changes nothing.
func TestReleaseOvertakesEachOpenQuestionWithADecisionAndATell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	keepOld := overtakenKeepOld()
	whichStore := shapedQuestion("which-store", "Which store?")
	questionedEpic(t, s, keepOld, whichStore)
	if _, err := s.AnswerQuestion(
		ctx,
		claimEpic,
		"D3",
		"",
		2,
		"first",
		Maker{By: MakerOwner},
		completedAt(),
	); err != nil {
		t.Fatalf("AnswerQuestion of Q2: %v", err)
	}
	triggers := triggersOf(t, s)

	at := overtakenAt()
	releaseClaimEpic(t, s, at)

	answered := openStored(whichStore, 2, 1)
	answered.State, answered.Choice = QuestionAnswered, "first"
	if got, want := questionsOf(t, s), []EpicQuestion{overtakenKeepOldQ1(), answered}; !reflect.DeepEqual(
		got, want,
	) {
		t.Errorf("questions after the release = %+v, want %+v", got, want)
	}
	const why = "the epic is released, and it ended without its answer"
	wantDecision := EpicDecision{
		Seq: 4, ID: "overtaken:Q1", Text: "Q1 keep-old-tasks closed as overtaken: " + why + ".", Time: at,
		Maker: Maker{By: MakerOrreryd},
	}
	if got := lastDecision(t, s); !reflect.DeepEqual(got, wantDecision) {
		t.Errorf("last decision after the release = %+v, want %+v", got, wantDecision)
	}
	tell := "Q1 keep-old-tasks is overtaken and takes no answer: " + why + "."
	undelivered, err := s.UndeliveredTells(ctx)
	if err != nil {
		t.Fatalf("UndeliveredTells: %v", err)
	}
	wantUndelivered := []SteererTell{{Epic: claimEpic, Title: "Synthetic epic", Seq: 2, Text: tell, At: at}}
	if !reflect.DeepEqual(undelivered, wantUndelivered) {
		t.Errorf("tells the steerer is owed = %+v, want %+v: the release tell rides the phase event, the "+
			"overtaken one is sent", undelivered, wantUndelivered)
	}
	wantMarks := [][]any{{"Released. No gaps.", int64(0), int64(1)}, {tell, int64(0), int64(0)}}
	if got := tellMarksOf(t, s, claimEpic); !reflect.DeepEqual(got, wantMarks) {
		t.Errorf(
			"tells after the release (text, told, delivered) = %v, want %v: the owner is told both",
			got,
			wantMarks,
		)
	}
	consumed := slices.Clone(triggers)
	for i := range consumed {
		consumed[i].State = "consumed"
	}
	if got := triggersOf(t, s); !reflect.DeepEqual(got, consumed) {
		t.Errorf("triggers after the release = %+v, want the same triggers, each consumed: %+v", got, consumed)
	}

	before := answerRowsOf(t, s)
	releaseClaimEpic(t, s, at.Add(time.Minute))
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("a second release changed the store:\n got %v\nwant %v", got, before)
	}
}

// TestDoneOvertakesTheOpenQuestion checks that ending an epic done, the release ending of an epic with no release
// steps, overtakes its open question as releasing it does, naming done as why.
func TestDoneOvertakesTheOpenQuestion(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	keepOld := overtakenKeepOld()
	questionedEpic(t, s, keepOld)

	at := overtakenAt()
	if err := s.EndEpic(context.Background(), claimEpic, "done", "Done. No gaps.", nil, at); err != nil {
		t.Fatalf("EndEpic done: %v", err)
	}

	if got, want := questionsOf(t, s), []EpicQuestion{overtakenKeepOldQ1()}; !reflect.DeepEqual(got, want) {
		t.Errorf("questions after done = %+v, want %+v", got, want)
	}
	const why = "the epic is done, and it ended without its answer"
	want := []EpicDecision{{
		Seq: 3, ID: "overtaken:Q1", Text: "Q1 keep-old-tasks closed as overtaken: " + why + ".", Time: at,
		Maker: Maker{By: MakerOrreryd},
	}}
	if got := overtakenDecisionsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("overtaken decisions after done = %+v, want %+v", got, want)
	}
}

// TestMetAtBaseEndOvertakesTheOtherOpenQuestions checks that the owner's end answer to a met-at-base park question,
// which ends the epic met_at_base, overtakes the planner's question still open in the same write, and leaves the park
// question answered end.
func TestMetAtBaseEndOvertakesTheOtherOpenQuestions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	keepOld := overtakenKeepOld()
	questionedEpic(t, s, keepOld)
	parkRedMetAtBase(t, s)

	at := overtakenAt()
	if _, err := s.AnswerQuestion(ctx, claimEpic, "D3", "", 2, ChoiceEnd, Maker{By: MakerOwner}, at); err != nil {
		t.Fatalf("AnswerQuestion end: %v", err)
	}

	if row := storedEpicRow(t, s); row.Phase != phaseMetAtBase {
		t.Fatalf("phase after the end answer = %q, want met_at_base", row.Phase)
	}
	park := metAtBaseQuestionR1()
	park.Seq, park.State, park.Choice = 2, QuestionAnswered, ChoiceEnd
	if got, want := questionsOf(t, s), []EpicQuestion{overtakenKeepOldQ1(), park}; !reflect.DeepEqual(
		got, want,
	) {
		t.Errorf("questions after the end answer = %+v, want %+v", got, want)
	}
	const why = "the epic is met_at_base, and it ended without its answer"
	want := []EpicDecision{{
		Seq: 4, ID: "overtaken:Q1", Text: "Q1 keep-old-tasks closed as overtaken: " + why + ".", Time: at,
		Maker: Maker{By: MakerOrreryd},
	}}
	if got := overtakenDecisionsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("overtaken decisions after the end answer = %+v, want %+v", got, want)
	}
}

// TestAnEpicNotEndedForGoodKeepsItsOpenQuestions checks that an ending a release, a correction or a replan still moves
// the epic on from overtakes nothing: an epic that ends clean, before its release, and one that ends with gaps, whose
// owner may still correct it, keep the planner's question open and record no overtaken decision.
func TestAnEpicNotEndedForGoodKeepsItsOpenQuestions(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, s *Store){
		"clean": func(t *testing.T, s *Store) {
			t.Helper()
			if err := s.EndEpic(context.Background(), claimEpic, PhaseClean, "", nil, overtakenAt()); err != nil {
				t.Fatalf("EndEpic clean: %v", err)
			}
		},
		"ended_with_gaps": func(t *testing.T, s *Store) {
			t.Helper()
			if err := s.EndEpicWithGaps(
				context.Background(), claimEpic, "Ended with 2 gaps.", claimGapsQuestion(), nil, overtakenAt(),
			); err != nil {
				t.Fatalf("EndEpicWithGaps: %v", err)
			}
		},
	}
	for name, end := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			keepOld := overtakenKeepOld()
			questionedEpic(t, s, keepOld)

			end(t, s)

			if got := questionsOf(t, s)[0]; !reflect.DeepEqual(got, openStored(keepOld, 1, 1)) {
				t.Errorf("Q1 after the %s ending = %+v, want it open", name, got)
			}
			if got := overtakenDecisionsOf(t, s); len(got) != 0 {
				t.Errorf("overtaken decisions after the %s ending = %+v, want none", name, got)
			}
		})
	}
}

// TestAQuestionLeftOpenThroughGapsIsOvertakenWhenTheReleaseEnds checks that the owner's release of an epic that ended
// with gaps answers its gaps question release and keeps the planner's question open while the release runs, and that
// the release's ending then overtakes it.
func TestAQuestionLeftOpenThroughGapsIsOvertakenWhenTheReleaseEnds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	keepOld := overtakenKeepOld()
	questionedEpic(t, s, keepOld)
	if err := s.EndEpicWithGaps(
		ctx, claimEpic, "Ended with 2 gaps.", claimGapsQuestion(), nil, completedAt(),
	); err != nil {
		t.Fatalf("EndEpicWithGaps: %v", err)
	}
	if err := s.ReleaseEpic(ctx, claimEpic, Maker{By: MakerOwner}, completedAt()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	if got := questionsOf(t, s)[0]; !reflect.DeepEqual(got, openStored(keepOld, 1, 1)) {
		t.Fatalf("Q1 once the release starts = %+v, want it open", got)
	}

	releaseClaimEpic(t, s, overtakenAt())

	gaps := claimGapsOpen()
	gaps.Seq, gaps.State, gaps.Choice = 2, QuestionAnswered, ChoiceRelease
	if got, want := questionsOf(t, s), []EpicQuestion{overtakenKeepOldQ1(), gaps}; !reflect.DeepEqual(
		got, want,
	) {
		t.Errorf("questions after the release = %+v, want %+v", got, want)
	}
}

// TestAnswerToAnOvertakenQuestionIsRefusedAndWritesNothing checks that an answer to a question the release overtook is
// a *QuestionOvertakenError that names the question and says the epic moved on, and that the refusal writes no decision,
// trigger, question or tell.
func TestAnswerToAnOvertakenQuestionIsRefusedAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	questionedEpic(t, s, overtakenKeepOld())
	releaseClaimEpic(t, s, overtakenAt())
	before := answerRowsOf(t, s)
	tells := claimTells(t, s)

	_, err := s.AnswerQuestion(
		ctx,
		claimEpic,
		"DL-Q1",
		"Fix the engine",
		1,
		"first",
		Maker{By: MakerOwner},
		overtakenAt(),
	)

	var overtaken *QuestionOvertakenError
	if !errors.As(err, &overtaken) || overtaken.Seq != 1 {
		t.Fatalf("AnswerQuestion of an overtaken question err = %v, want a *QuestionOvertakenError for question 1", err)
	}
	const want = "question 1 was overtaken: the epic moved on without its answer, so it takes none; " +
		"decision overtaken:Q1 records why"
	if err.Error() != want {
		t.Errorf("refusal = %q, want %q", err.Error(), want)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("a refused answer changed the store:\n got %v\nwant %v", got, before)
	}
	if got := claimTells(t, s); !reflect.DeepEqual(got, tells) {
		t.Errorf("tells after a refused answer = %q, want %q", got, tells)
	}
}

// TestStartHeldEpicOvertakesAnOpenHeldQuestion checks that a held epic started once its predecessors shipped, while its
// held question is still open, closes that question as overtaken in the start's write, with orreryd's decision after
// held-start and a tell that names the base it started at.
func TestStartHeldEpicOvertakesAnOpenHeldQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	held := fullEpic(claimEpic)
	held.Phase = PhaseHeld
	createEpic(t, s, held)
	if err := s.AskHeldEpic(ctx, claimEpic, heldQuestionText, claimedAt()); err != nil {
		t.Fatalf("AskHeldEpic: %v", err)
	}

	const base = "0123456789abcdef0123456789abcdef01234567"
	at := overtakenAt()
	if err := s.StartHeldEpic(ctx, claimEpic, base, at); err != nil {
		t.Fatalf("StartHeldEpic: %v", err)
	}

	question := heldQuestionOpen()
	question.State = QuestionOvertaken
	if got := questionsOf(t, s); !reflect.DeepEqual(got, []EpicQuestion{question}) {
		t.Errorf("questions after the start = %+v, want the held question overtaken", got)
	}
	const why = "the epic started at base " + base + " once its predecessors shipped, without its answer"
	want := []EpicDecision{{
		Seq: 4, ID: "overtaken:Q1", Text: "Q1 held closed as overtaken: " + why + ".", Time: at,
		Maker: Maker{By: MakerOrreryd},
	}}
	if got := overtakenDecisionsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("overtaken decisions after the start = %+v, want %+v", got, want)
	}
	wantTells := []string{"Q1 held is overtaken and takes no answer: " + why + "."}
	if got := claimTells(t, s); !reflect.DeepEqual(got, wantTells) {
		t.Errorf("tells after the start = %q, want %q", got, wantTells)
	}
}

// TestStartHeldEpicKeepsAHeldQuestionAnsweredStart checks that a held epic started on its held question's start answer
// keeps that question answered and records no overtaken decision.
func TestStartHeldEpicKeepsAHeldQuestionAnsweredStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	held := fullEpic(claimEpic)
	held.Phase = PhaseHeld
	createEpic(t, s, held)
	if err := s.AskHeldEpic(ctx, claimEpic, heldQuestionText, claimedAt()); err != nil {
		t.Fatalf("AskHeldEpic: %v", err)
	}
	if _, err := s.AnswerQuestion(
		ctx, claimEpic, "owner-start", "", 1, ChoiceStart, Maker{By: MakerOwner}, completedAt(),
	); err != nil {
		t.Fatalf("AnswerQuestion start: %v", err)
	}

	if err := s.StartHeldEpic(ctx, claimEpic, "0123456789abcdef0123456789abcdef01234567", overtakenAt()); err != nil {
		t.Fatalf("StartHeldEpic: %v", err)
	}

	answered := heldQuestionOpen()
	answered.State, answered.Choice = QuestionAnswered, ChoiceStart
	if got := questionsOf(t, s); !reflect.DeepEqual(got, []EpicQuestion{answered}) {
		t.Errorf("questions after the start = %+v, want the held question answered start", got)
	}
	if got := overtakenDecisionsOf(t, s); len(got) != 0 {
		t.Errorf("overtaken decisions after the start = %+v, want none", got)
	}
}

// TestFinalPhaseNamesThePhasesAnEpicEndsInForGood checks that met_at_base, released, done and the closed phases are
// final, and that every other phase, the ended phases a release, a correction or a replan moves on from among them, is
// not.
func TestFinalPhaseNamesThePhasesAnEpicEndsInForGood(t *testing.T) {
	t.Parallel()
	for phase, want := range map[string]bool{
		"met_at_base": true, "released": true, "done": true,
		PhaseClosedSuperseded: true, PhaseClosedShipped: true, PhaseClosedAbandoned: true,
		"clean": false, "ended_with_gaps": false, "stopped": false, "releasing": false, "correcting": false,
		"accepted": false, "held": false, "built": false, "reviewing": false,
	} {
		if got := FinalPhase(phase); got != want {
			t.Errorf("FinalPhase(%q) = %v, want %v", phase, got, want)
		}
	}
}
