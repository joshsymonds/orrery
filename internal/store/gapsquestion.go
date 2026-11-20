package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// GapsQuestion is what an epic that ends with gaps asks its owner. Gaps holds the gaps orrery could not resolve, one
// entry each with its reason. Recommended is the choice orrery recommends, release or correct, and Why says why.
type GapsQuestion struct {
	Gaps        []string
	Recommended string
	Why         string
}

// The advice a gaps question gives: what each of its choices does, and how confident orrery is in its recommendation.
const (
	gapsReleaseDoes = "release the epic with these gaps, recorded as your decision"
	gapsCorrectDoes = "give your text to the planner as a correction and return the epic to correcting"
	gapsCloseDoes   = "close the epic as abandoned"
	gapsConfidence  = "medium"
)

// gapsTextHead opens the text of a gaps question, above one line per gap.
const gapsTextHead = "Gaps orrery could not resolve:"

// The id and cause of the trigger a correction adds: the id is gapsTriggerPrefix followed by the id of the decision
// that carries the correction, and the cause is gapsCausePrefix followed by the text of the correction.
const (
	gapsTriggerPrefix = "gaps:"
	gapsCausePrefix   = "owner correction of the gaps: "
)

// EndEpicWithGaps ends epic at ended_with_gaps in one write, as EndEpic does for that phase: it ends each running node
// run of epic that ends names, adds body as the epic's next tell, at time at, and sets its phase. When the phase moves,
// the same write opens question as the next question of epic: a turn-0 question labeled gaps, whose text lists each
// gap of question on its own line, that offers release, correct and close, and that recommends question.Recommended
// with question.Why. The write marks an open gaps question of epic replaced by it. An epic already in ended_with_gaps
// gets no new tell, phase or question, and its runs in ends still end. A question with no gap, or that recommends
// neither release nor correct, is a RefusalError and writes nothing.
func (s *Store) EndEpicWithGaps(
	ctx context.Context, epic, body string, question GapsQuestion, ends []NodeEnd, at time.Time,
) error {
	if err := checkGapsQuestion(question); err != nil {
		return err
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := endNodeRuns(ctx, queries, epic, ends, at); err != nil {
			return err
		}
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if head.Phase == phaseEndedWithGaps {
			return nil
		}
		if err = insertTell(ctx, queries, epic, body, at); err != nil {
			return fmt.Errorf("end epic %s: %w", epic, err)
		}
		return endWithGaps(ctx, queries, epic, question, at)
	})
}

// endWithGaps sets the phase of epic to ended_with_gaps and opens question for it, in the caller's write.
func endWithGaps(
	ctx context.Context, queries *sqlcgen.Queries, epic string, question GapsQuestion, at time.Time,
) error {
	if err := setEpicPhase(ctx, queries, epic, phaseEndedWithGaps, at); err != nil {
		return err
	}
	return insertGapsQuestion(ctx, queries, epic, gapsQuestionInput(question))
}

// checkGapsQuestion is the RefusalError of a gaps question that names no gap, or that recommends neither release nor
// correct.
func checkGapsQuestion(question GapsQuestion) error {
	if len(question.Gaps) == 0 {
		return RefusalError("gaps question names no gap")
	}
	if question.Recommended != ChoiceRelease && question.Recommended != ChoiceCorrect {
		return RefusalError(fmt.Sprintf("gaps question recommends %q, not release or correct", question.Recommended))
	}
	return nil
}

// gapsQuestionInput returns the question EndEpicWithGaps opens for question: its text opens with gapsTextHead and lists
// each gap on a line of its own, and it declares release, correct and close.
func gapsQuestionInput(question GapsQuestion) EpicQuestionInput {
	lines := make([]string, 0, len(question.Gaps)+1)
	lines = append(lines, gapsTextHead)
	for _, gap := range question.Gaps {
		lines = append(lines, "- "+gap)
	}
	return EpicQuestionInput{
		Label: GapsLabel,
		Text:  strings.Join(lines, "\n"),
		Choices: []EpicChoice{
			{ID: ChoiceRelease, Does: gapsReleaseDoes},
			{ID: ChoiceCorrect, Does: gapsCorrectDoes},
			{ID: ChoiceClose, Does: gapsCloseDoes},
		},
		Recommended: question.Recommended,
		Why:         question.Why,
		Confidence:  gapsConfidence,
	}
}

// insertGapsQuestion opens question, a gaps question, for epic in the caller's write, as the next question seq of epic
// at turn 0. Each open gaps question of epic is replaced by it, and the last one it replaces is the one it names.
func insertGapsQuestion(ctx context.Context, queries *sqlcgen.Queries, epic string, question EpicQuestionInput) error {
	open, err := queries.ListOpenEpicQuestions(ctx, epic)
	if err != nil {
		return fmt.Errorf("read open questions of epic %s: %w", epic, err)
	}
	highest, err := queries.MaxEpicQuestionSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read question count of epic %s: %w", epic, err)
	}
	seq := highest + 1
	var replaces int64
	for _, stored := range open {
		if stored.Label != GapsLabel {
			continue
		}
		if _, err = queries.SetEpicQuestionReplaced(ctx, sqlcgen.SetEpicQuestionReplacedParams{
			ReplacedBy: seq, Epic: epic, Seq: stored.Seq,
		}); err != nil {
			return fmt.Errorf("replace gaps question %d of epic %s: %w", stored.Seq, epic, err)
		}
		replaces = stored.Seq
	}
	choices, err := json.Marshal(question.Choices)
	if err != nil {
		return fmt.Errorf("choices of gaps question %d of epic %s: %w", seq, epic, err)
	}
	if err = queries.InsertEpicQuestion(withEpicEvent(ctx), sqlcgen.InsertEpicQuestionParams{
		Epic: epic, Seq: seq, Turn: 0, Text: question.Text, Label: question.Label, Choices: string(choices),
		Recommended: question.Recommended, Why: question.Why, Confidence: question.Confidence, Replaces: replaces,
	}); err != nil {
		return fmt.Errorf("insert gaps question %d of epic %s: %w", seq, epic, err)
	}
	return nil
}

// isGapsQuestion reports whether question is a gaps question at turn 0, the question EndEpicWithGaps opens. An answer
// to it carries out its choice.
func isGapsQuestion(question sqlcgen.EpicQuestion) bool {
	return question.Label == GapsLabel && question.Turn == 0
}

// answerGapsQuestion carries out choice, which maker answered to question with text as decision id, in the caller's
// write, when question is a gaps question. release releases epic as releaseEndedWithGaps does, with no undo-none, its
// decision made by maker.
// correct adds the text as a correction for the planner and returns epic to correcting. close closes epic as abandoned,
// as CloseEpic does. Any other question, and other, changes nothing here.
func answerGapsQuestion(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic, id, text string,
	question sqlcgen.EpicQuestion,
	choice string,
	maker Maker,
	at time.Time,
) error {
	if !isGapsQuestion(question) {
		return nil
	}
	switch choice {
	case ChoiceRelease:
		return releaseEndedWithGaps(ctx, queries, epic, nil, maker, at)
	case ChoiceCorrect:
		return correctGaps(ctx, queries, epic, id, text, at)
	case ChoiceClose:
		_, err := closeEpicIn(ctx, queries, epic, kindAbandoned, "", CloserOwner, at)
		return err
	default:
		return nil
	}
}

// correctGaps gives text, the owner's correction recorded as decision id, to the planner in the caller's write: a
// pending trigger gapsTriggerPrefix+id whose cause holds the text is added, and epic moves to correcting.
func correctGaps(ctx context.Context, queries *sqlcgen.Queries, epic, id, text string, at time.Time) error {
	if err := queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: epic, ID: gapsTriggerPrefix + id, Cause: gapsCausePrefix + text, State: triggerPending, Turn: 0,
		Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert trigger of correction %s on epic %s: %w", id, epic, err)
	}
	return setEpicPhase(ctx, queries, epic, phaseCorrecting, at)
}

// StartCorrectionPlan stores plan as a running node run of epic, as StartNodes stores its runs, in one write. The plan
// is the replan of a correcting epic, whose owner's correction woke the planner through the trigger gapsTriggerPrefix
// names. The last such trigger epic holds decides the mark: once a planner turn has claimed it, plan's mark is lowered
// to that turn's seq minus one, when it is above it, so the turn's tasks build. While the trigger is unclaimed, the mark
// stays as given. A plan whose path is stored already is left as it is, so a repeat changes nothing.
func (s *Store) StartCorrectionPlan(ctx context.Context, epic string, plan NodeRun, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		triggers, err := queries.ListEpicTriggers(ctx, epic)
		if err != nil {
			return fmt.Errorf("read triggers of epic %s: %w", epic, err)
		}
		if turn := claimedCorrectionTurn(triggers); turn > 0 {
			plan.Mark = min(plan.Mark, turn-1)
		}
		return insertNodeRuns(ctx, queries, epic, []NodeRun{plan}, at)
	})
}

// claimedCorrectionTurn returns the seq of the planner turn that claimed the last gaps trigger among triggers, which
// are in the order the store lists them, or 0 when that trigger is not claimed or there is none.
func claimedCorrectionTurn(triggers []sqlcgen.EpicTrigger) int64 {
	var last sqlcgen.EpicTrigger
	found := false
	for _, trigger := range triggers {
		if strings.HasPrefix(trigger.ID, gapsTriggerPrefix) {
			last, found = trigger, true
		}
	}
	if !found {
		return 0
	}
	return last.Turn
}

// answerOpenGapsQuestions answers each open gaps question of epic with choice in the caller's write, so that no gaps
// question stays open once the owner has released the epic.
func answerOpenGapsQuestions(ctx context.Context, queries *sqlcgen.Queries, epic, choice string) error {
	open, err := queries.ListOpenEpicQuestions(ctx, epic)
	if err != nil {
		return fmt.Errorf("read open questions of epic %s: %w", epic, err)
	}
	for _, question := range open {
		if question.Label != GapsLabel {
			continue
		}
		if err = queries.SetEpicQuestionAnswered(ctx, sqlcgen.SetEpicQuestionAnsweredParams{
			Choice: choice, Epic: epic, Seq: question.Seq,
		}); err != nil {
			return fmt.Errorf("answer question %d of epic %s: %w", question.Seq, epic, err)
		}
	}
	return nil
}
