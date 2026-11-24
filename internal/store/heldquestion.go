package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The advice a held question gives: what each of its choices does. A held question recommends neither.
const (
	heldStartDoes = "Start the epic now from origin main as it is then"
	heldCloseDoes = "Close the epic abandoned"
)

// AskHeldEpic opens a question with text as its text, as the next question at turn 0 of the held epic named epic. The
// question is labeled HeldLabel, offers ChoiceStart and ChoiceClose, and recommends neither. The question has no time
// of its own, so at is not stored. An epic that already has a question labeled HeldLabel, in any state, is left as it
// is and nothing is written. It returns an error that matches ErrNoEpic when no epic has the id, and ErrNotHeld when the
// epic is not held.
func (s *Store) AskHeldEpic(ctx context.Context, epic, text string, _ time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if head.Phase != PhaseHeld {
			return fmt.Errorf("epic %s: %w", epic, ErrNotHeld)
		}
		questions, err := queries.ListEpicQuestions(ctx, epic)
		if err != nil {
			return fmt.Errorf("read questions of epic %s: %w", epic, err)
		}
		if slices.ContainsFunc(questions, isHeldQuestionLabel) {
			return nil
		}
		return insertHeldQuestion(ctx, queries, epic, text)
	})
}

// isHeldQuestionLabel reports whether question carries the label of a held question.
func isHeldQuestionLabel(question sqlcgen.EpicQuestion) bool {
	return question.Label == HeldLabel
}

// insertHeldQuestion opens the held question of epic with text in the caller's write, as the next question seq of epic
// at turn 0. It offers ChoiceStart and ChoiceClose, each with what it does, and recommends nothing.
func insertHeldQuestion(ctx context.Context, queries *sqlcgen.Queries, epic, text string) error {
	highest, err := queries.MaxEpicQuestionSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read question count of epic %s: %w", epic, err)
	}
	choices, err := json.Marshal([]EpicChoice{
		{ID: ChoiceStart, Does: heldStartDoes},
		{ID: ChoiceClose, Does: heldCloseDoes},
	})
	if err != nil {
		return fmt.Errorf("choices of held question of epic %s: %w", epic, err)
	}
	if err = queries.InsertEpicQuestion(withEpicEvent(ctx), sqlcgen.InsertEpicQuestionParams{
		Epic: epic, Seq: highest + 1, Turn: 0, Text: text, Label: HeldLabel, Choices: string(choices),
	}); err != nil {
		return fmt.Errorf("insert held question of epic %s: %w", epic, err)
	}
	return nil
}

// isHeldQuestion reports whether question is the held question at turn 0, the question AskHeldEpic opens. An answer to
// it carries out its choice.
func isHeldQuestion(question sqlcgen.EpicQuestion) bool {
	return isHeldQuestionLabel(question) && question.Turn == 0
}

// answerHeldQuestion carries out choice, answered to question in the caller's write, when question is a held question.
// close closes epic as abandoned, as answerGapsQuestion's close does. start carries nothing more here: the held pass
// starts the epic on its next pass. Any other question, and any other choice, changes nothing here.
func answerHeldQuestion(
	ctx context.Context, queries *sqlcgen.Queries, epic, choice string, question sqlcgen.EpicQuestion, at time.Time,
) error {
	if !isHeldQuestion(question) || choice != ChoiceClose {
		return nil
	}
	_, err := closeEpicIn(ctx, queries, epic, kindAbandoned, "", CloserOwner, at)
	return err
}
