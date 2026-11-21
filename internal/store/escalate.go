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

// EscalateQuestion marks question seq of epic escalated at at, with reason and maker, in one write, and returns the
// question as stored. An unknown epic is ErrNoEpic and an unknown question is ErrNoQuestion. A refusal is a
// RefusalError and writes nothing: a reason that is empty after trimming, a question that is not open (answered or
// replaced), and a question that is already escalated, which keeps the reason and maker it has.
func (s *Store) EscalateQuestion(
	ctx context.Context,
	epic string,
	question int64,
	reason string,
	maker Maker,
	at time.Time,
) (EpicQuestion, error) {
	var escalated EpicQuestion
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireEpic(ctx, queries, epic); err != nil {
			return err
		}
		stored, err := requireQuestion(ctx, queries, epic, question)
		if err != nil {
			return err
		}
		if strings.TrimSpace(reason) == "" {
			return RefusalError(fmt.Sprintf("question %d of epic %s has no escalation reason", question, epic))
		}
		rows, err := queries.EscalateEpicQuestion(ctx, sqlcgen.EscalateEpicQuestionParams{
			Escalated: at.Unix(), EscalationReason: reason, EscalationMaker: maker.By,
			EscalationSession: maker.Session, Epic: epic, Seq: question,
		})
		if err != nil {
			return fmt.Errorf("escalate question %d of epic %s: %w", question, epic, err)
		}
		if rows == 0 {
			return escalationRefusal(stored)
		}
		stored, err = queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: question})
		if err != nil {
			return fmt.Errorf("read escalated question %d of epic %s: %w", question, epic, err)
		}
		escalated, err = questionOf(stored)
		return err
	})
	if err != nil {
		return EpicQuestion{}, err
	}
	return escalated, nil
}

// MarkEscalationTold sets the told mark of the escalation of question seq of epic, once the owner has been told of it.
// An unknown question, which includes a question of an unknown epic, is ErrNoQuestion.
func (s *Store) MarkEscalationTold(ctx context.Context, epic string, question int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.SetEpicQuestionEscalationTold(ctx, sqlcgen.SetEpicQuestionEscalationToldParams{
			Epic: epic, Seq: question,
		})
		if err != nil {
			return fmt.Errorf("mark escalation of question %d of epic %s told: %w", question, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("question %d of epic %s: %w", question, epic, ErrNoQuestion)
		}
		return nil
	})
}

// requireQuestion returns the stored question seq of epic, or an error matching ErrNoQuestion when epic has none.
func requireQuestion(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	question int64,
) (sqlcgen.EpicQuestion, error) {
	stored, err := queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: question})
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.EpicQuestion{}, fmt.Errorf("question %d on epic %s: %w", question, epic, ErrNoQuestion)
	}
	if err != nil {
		return sqlcgen.EpicQuestion{}, fmt.Errorf("read question %d on epic %s: %w", question, epic, err)
	}
	return stored, nil
}

// escalationRefusal is the RefusalError of an escalation the write did not make: stored, the question as it was read,
// is not open, or is open and already escalated. The write makes an escalation only of an open question not yet
// escalated, so when it made none, one of the two holds.
func escalationRefusal(stored sqlcgen.EpicQuestion) error {
	switch {
	case stored.State == QuestionReplaced:
		return RefusalError(fmt.Sprintf("question %d of epic %s is replaced, not open", stored.Seq, stored.Epic))
	case stored.State != QuestionOpen:
		return RefusalError(fmt.Sprintf(
			"question %d of epic %s is %s, not open", stored.Seq, stored.Epic, stored.State,
		))
	default:
		return RefusalError(fmt.Sprintf("question %d of epic %s is already escalated", stored.Seq, stored.Epic))
	}
}
