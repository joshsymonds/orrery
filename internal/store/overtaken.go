package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// overtakenDecisionPrefix starts the id of the decision that records a question closed as overtaken. The id is the
// prefix followed by the question's id, Q<seq>, so the decision of question 1 is overtaken:Q1.
const overtakenDecisionPrefix = "overtaken:"

// overtakenDecisionID returns the id of the decision that records question seq closed as overtaken.
func overtakenDecisionID(seq int64) string {
	return fmt.Sprintf("%sQ%d", overtakenDecisionPrefix, seq)
}

// FinalPhase reports whether phase is one an epic ends in for good: met_at_base, released, done, or a closed phase
// (IsClosedPhase). No planner turn runs for an epic in a final phase (checkClaimable), so an answer recorded there has no
// turn to take it and writes no trigger (recordQuestionAnswer), an epic that moves into one overtakes its open
// questions, and a write that sets one consumes its pending triggers (setEpicPhase). The other ended phases (EndedPhase)
// are not final: a release moves an epic on from clean, a correction or a release from ended_with_gaps, and a resume
// or a replan from stopped, and a planner turn may then take an answer.
func FinalPhase(phase string) bool {
	switch phase {
	case phaseMetAtBase, phaseReleased, phaseDone:
		return true
	}
	return IsClosedPhase(phase)
}

// overtaking is how a write overtakes the open questions of an epic: why the epic moved on without their answers, which
// open questions it takes, and whether the tells it adds are stored told, for a write the owner made (insertToldTell).
// takes is read only by overtakeOpenQuestions; an overtaking of one question a write names leaves it nil.
type overtaking struct {
	why   string
	takes func(sqlcgen.EpicQuestion) bool
	told  bool
}

// endedForGood is the overtaking of every open question of an epic that ends for good in phase, a final phase. Its why
// names the phase. Its tells are not told, so the owner is told of each.
func endedForGood(phase string) overtaking {
	return overtaking{
		why:   fmt.Sprintf("the epic is %s, and it ended without its answer", phase),
		takes: func(sqlcgen.EpicQuestion) bool { return true },
	}
}

// calledOvertaken is the overtaking of a planner question that planner turn turn called overtaken for reason. Its why is
// `turn <turn> went on without its answer: <reason>`, with the reason's trailing white space and periods cut, since the
// decision and the tell end the why with a period of their own. Its tell is not told, so the owner is told of it.
func calledOvertaken(turn int64, reason string) overtaking {
	return overtaking{
		why: fmt.Sprintf("turn %d went on without its answer: %s", turn, strings.TrimRight(reason, " \t\n.")),
	}
}

// movedOn is the overtaking of the question an orchestrator asked on task seq, once a write moved the task on from the
// build that waited on its answer, as how says. Its why is `T<seq> moved on without its answer: <how>`. Its tell is not
// told, so the owner is told of it.
func movedOn(seq int64, how string) overtaking {
	return overtaking{why: fmt.Sprintf("T%d moved on without its answer: %s", seq, how)}
}

// overtakeAsk closes the question build asks the owner as overtaken in the caller's write, when it is still open: build
// is the build of task seq of epic that the write moves the task on from, as how says (movedOn), so nothing waits on the
// answer any more. A build that holds no question, a question that is not stored, and one that is no longer open
// overtake nothing. A question that a planner question replaced is not open, and the planner calls its replacement each
// turn as it calls any question it asked.
func overtakeAsk(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq int64,
	build TaskBuild,
	how string,
	at time.Time,
) error {
	if build.Question == 0 {
		return nil
	}
	question, err := queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: build.Question})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read question %d of task %d of epic %s: %w", build.Question, seq, epic, err)
	}
	if question.State != QuestionOpen {
		return nil
	}
	return overtakeQuestion(ctx, queries, epic, question, movedOn(seq, how), at)
}

// consumeAtFinalPhase consumes each pending trigger of epic in the caller's write when phase is a final phase
// (FinalPhase): no planner turn runs for an epic in one (checkClaimable), so a trigger left pending there would be claimed
// by none, and the planner would list the epic on every pass for it. Any other phase consumes nothing.
func consumeAtFinalPhase(ctx context.Context, queries *sqlcgen.Queries, epic, phase string) error {
	if !FinalPhase(phase) {
		return nil
	}
	if err := queries.ConsumePendingEpicTriggers(ctx, epic); err != nil {
		return fmt.Errorf("consume pending triggers of epic %s: %w", epic, err)
	}
	return nil
}

// overtakeAtFinalPhase overtakes each open question of epic in the caller's write when phase is a final phase
// (FinalPhase) that epic is not in yet, since the epic ends there for good without their answers (endedForGood). Any
// other phase overtakes nothing, and so does the final phase epic is in already, so a question opened once the epic is
// there, such as the final park question of an epic in met_at_base, stays open. An unknown epic is ErrNoEpic.
func overtakeAtFinalPhase(ctx context.Context, queries *sqlcgen.Queries, epic, phase string, at time.Time) error {
	if !FinalPhase(phase) {
		return nil
	}
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if head.Phase == phase {
		return nil
	}
	return overtakeOpenQuestions(ctx, queries, epic, endedForGood(phase), at)
}

// overtakeOpenQuestions closes each open question of epic that how takes as overtaken, in seq order, in the caller's
// write, as overtakeQuestion does.
func overtakeOpenQuestions(
	ctx context.Context, queries *sqlcgen.Queries, epic string, how overtaking, at time.Time,
) error {
	open, listErr := queries.ListOpenEpicQuestions(ctx, epic)
	if listErr != nil {
		return fmt.Errorf("read open questions of epic %s: %w", epic, listErr)
	}
	for _, question := range open {
		if !how.takes(question) {
			continue
		}
		if err := overtakeQuestion(ctx, queries, epic, question, how, at); err != nil {
			return err
		}
	}
	return nil
}

// overtakeQuestion closes question, an open question of epic, as overtaken in the caller's write, at at: the epic moved
// on without its answer, for the reason how.why gives. The question becomes overtaken, with no choice. orreryd appends
// the decision overtaken:Q<seq>, a note that answers no question, whose text is
// `Q<seq> <label> closed as overtaken: <why>.`. The epic's next tell is
// `Q<seq> <label> is overtaken and takes no answer: <why>.`, which carries no question, so its steerer is sent it. Its
// owner is told it too, unless how.told stores it told already. A question with no label is named Q<seq> alone. No
// trigger is added, since an overtaken question has no answer for a planner turn to take, and an answer to it is
// refused (requireAnswerable).
func overtakeQuestion(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	question sqlcgen.EpicQuestion,
	how overtaking,
	at time.Time,
) error {
	if _, err := queries.SetEpicQuestionOvertaken(ctx, sqlcgen.SetEpicQuestionOvertakenParams{
		Epic: epic, Seq: question.Seq,
	}); err != nil {
		return fmt.Errorf("overtake question %d of epic %s: %w", question.Seq, epic, err)
	}
	name := fmt.Sprintf("Q%d", question.Seq)
	if question.Label != "" {
		name += " " + question.Label
	}
	text := fmt.Sprintf("%s closed as overtaken: %s.", name, how.why)
	if _, err := appendDecision(
		ctx, queries, epic, overtakenDecisionID(question.Seq), text, 0, Maker{By: MakerOrreryd}, "", at,
	); err != nil {
		return err
	}
	tell := fmt.Sprintf("%s is overtaken and takes no answer: %s.", name, how.why)
	if how.told {
		return insertToldTell(ctx, queries, epic, tell, at)
	}
	return insertTell(ctx, queries, epic, tell, at)
}
