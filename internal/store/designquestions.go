package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrQuestionAnswered reports that a design question already has an answer, and an answer does not replace it.
var ErrQuestionAnswered = errors.New("question already answered")

// AskDesignQuestion stores question on its design, numbered as the design's next question: counted from 1 within the
// design, one more than the highest number the design already holds. question.Seq is ignored. It returns the question
// as stored, with its Seq, and unanswered.
func (s *Store) AskDesignQuestion(ctx context.Context, question DesignQuestion) (DesignQuestion, error) {
	options, err := encodeList(question.Options)
	if err != nil {
		return DesignQuestion{}, fmt.Errorf("encode options of a question of design %s: %w", question.Design, err)
	}
	var seq int64
	err = s.write(ctx, func(queries *sqlcgen.Queries) error {
		next, nextErr := queries.NextDesignQuestionSeq(ctx, question.Design)
		if nextErr != nil {
			return fmt.Errorf("number a question of design %s: %w", question.Design, nextErr)
		}
		if insertErr := queries.InsertDesignQuestion(ctx, sqlcgen.InsertDesignQuestionParams{
			Design: question.Design, Seq: next, Contract: question.Contract, Text: question.Text, Options: options,
			Recommendation: question.Recommendation, Asker: question.Asker, Asked: question.Asked.Unix(),
		}); insertErr != nil {
			return fmt.Errorf("insert question %d of design %s: %w", next, question.Design, insertErr)
		}
		seq = next
		return nil
	})
	if err != nil {
		return DesignQuestion{}, err
	}
	return DesignQuestion{
		Design:         question.Design,
		Seq:            seq,
		Contract:       question.Contract,
		Text:           question.Text,
		Options:        question.Options,
		Recommendation: question.Recommendation,
		Asker:          question.Asker,
		Asked:          question.Asked,
	}, nil
}

// AnswerDesignQuestion stores answer's words, accepted flag, maker, answerer and answered time on the question of
// answer.Design numbered answer.Seq, and returns the question as stored. A question no design holds is ErrNoQuestion,
// and a question already answered is ErrQuestionAnswered. Either refusal stores nothing.
func (s *Store) AnswerDesignQuestion(ctx context.Context, answer DesignQuestion) (DesignQuestion, error) {
	var stored DesignQuestion
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		key := sqlcgen.GetDesignQuestionParams{Design: answer.Design, Seq: answer.Seq}
		row, readErr := queries.GetDesignQuestion(ctx, key)
		if errors.Is(readErr, sql.ErrNoRows) {
			return fmt.Errorf("question %d of design %s: %w", answer.Seq, answer.Design, ErrNoQuestion)
		}
		if readErr != nil {
			return fmt.Errorf("read question %d of design %s: %w", answer.Seq, answer.Design, readErr)
		}
		if row.Answered != 0 {
			return fmt.Errorf("question %d of design %s: %w", answer.Seq, answer.Design, ErrQuestionAnswered)
		}
		changed, updateErr := queries.AnswerDesignQuestion(ctx, sqlcgen.AnswerDesignQuestionParams{
			Answer: answer.Answer, Accepted: boolInt(answer.Accepted), Maker: answer.Maker.Kind,
			Quote: answer.Maker.Quote, MakerReason: answer.Maker.Reason, Delegation: answer.Maker.Delegation,
			Answerer: answer.Answerer, Answered: answer.Answered.Unix(), Design: answer.Design, Seq: answer.Seq,
		})
		if updateErr != nil {
			return fmt.Errorf("answer question %d of design %s: %w", answer.Seq, answer.Design, updateErr)
		}
		if changed != 1 {
			return fmt.Errorf("answer question %d of design %s: %d rows changed", answer.Seq, answer.Design, changed)
		}
		row, readErr = queries.GetDesignQuestion(ctx, key)
		if readErr != nil {
			return fmt.Errorf("read answered question %d of design %s: %w", answer.Seq, answer.Design, readErr)
		}
		var convertErr error
		stored, convertErr = designQuestionOf(row)
		return convertErr
	})
	if err != nil {
		return DesignQuestion{}, err
	}
	return stored, nil
}

// DesignQuestion returns the question numbered seq within design as the store holds it. It returns an error that
// matches ErrNoQuestion when the design has no question seq.
func (s *Store) DesignQuestion(ctx context.Context, design string, seq int64) (DesignQuestion, error) {
	row, err := sqlcgen.New(s.read).GetDesignQuestion(ctx, sqlcgen.GetDesignQuestionParams{Design: design, Seq: seq})
	if errors.Is(err, sql.ErrNoRows) {
		return DesignQuestion{}, fmt.Errorf("question %d of design %s: %w", seq, design, ErrNoQuestion)
	}
	if err != nil {
		return DesignQuestion{}, fmt.Errorf("read question %d of design %s: %w", seq, design, err)
	}
	return designQuestionOf(row)
}

// DesignQuestions returns the questions stored against the design, oldest first, which is by seq. A design with no
// questions has none.
func (s *Store) DesignQuestions(ctx context.Context, design string) ([]DesignQuestion, error) {
	rows, err := sqlcgen.New(s.read).ListDesignQuestions(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list questions of design %s: %w", design, err)
	}
	questions := make([]DesignQuestion, 0, len(rows))
	for _, row := range rows {
		question, convertErr := designQuestionOf(row)
		if convertErr != nil {
			return nil, convertErr
		}
		questions = append(questions, question)
	}
	return questions, nil
}

// designQuestionOf converts a stored question row to a DesignQuestion. Its answer fields are zero while it is open,
// which the row's answered column of 0 says.
func designQuestionOf(row sqlcgen.DesignQuestion) (DesignQuestion, error) {
	options, err := decodeList[string](row.Options)
	if err != nil {
		return DesignQuestion{}, fmt.Errorf("decode options of question %d of design %s: %w", row.Seq, row.Design, err)
	}
	question := DesignQuestion{
		Design: row.Design, Seq: row.Seq, Contract: row.Contract, Text: row.Text, Options: options,
		Recommendation: row.Recommendation, Asker: row.Asker, Asked: time.Unix(row.Asked, 0),
	}
	if row.Answered == 0 {
		return question, nil
	}
	question.Answer = row.Answer
	question.Accepted = row.Accepted != 0
	question.Maker = StageMaker{Kind: row.Maker, Quote: row.Quote, Reason: row.MakerReason, Delegation: row.Delegation}
	question.Answerer = row.Answerer
	question.Answered = time.Unix(row.Answered, 0)
	return question, nil
}
