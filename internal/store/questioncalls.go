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

// The calls a planner turn makes on an open question a planner turn asked. CallStands says the question still waits on
// its answer. CallOvertaken says the turn went on without its answer, and it closes the question as overtaken.
const (
	CallStands    = "stands"
	CallOvertaken = "overtaken"
)

// PlanOutcomeComplete is the outcome of a planner return that says the plan is complete. A complete plan leaves no
// question waiting on its answer, so no call beside it stands.
const PlanOutcomeComplete = "complete"

// QuestionCallInput is one call of a planner return on an open question a planner turn asked: the question as Q<n>, the
// call, CallStands or CallOvertaken, and the turn's reason for it.
type QuestionCallInput struct {
	Question string
	Call     string
	Reason   string
}

// QuestionCall is one call a planner turn stored on a question: the seq of the turn that made it, the call, and the
// turn's reason for it.
type QuestionCall struct {
	Turn   int64
	Call   string
	Reason string
}

// QuestionSeq returns the seq a question reference names, n for Q<n> with n >= 1 and no leading zero. ok is false for
// any other reference, the empty one included.
func QuestionSeq(ref string) (int64, bool) {
	seq, ok := replacesSeq(ref)
	return seq, ok && seq != 0
}

// checkQuestionCalls returns ErrBadQuestion for the first call of calls that CompleteTurn refuses before it reads the
// store: a question that is not Q<n>, a question an earlier call names, a call that is not stands or overtaken, a blank
// reason, and a stands call beside outcome PlanOutcomeComplete. The error names the call by its position and question.
func checkQuestionCalls(calls []QuestionCallInput, outcome string) error {
	named := make(map[int64]bool, len(calls))
	for i, call := range calls {
		seq, ok := QuestionSeq(call.Question)
		switch {
		case !ok:
			return fmt.Errorf("%w: question call %d: question %q is not Q<n>", ErrBadQuestion, i+1, call.Question)
		case named[seq]:
			return fmt.Errorf("%w: question call %d: %s is called twice", ErrBadQuestion, i+1, call.Question)
		case call.Call != CallStands && call.Call != CallOvertaken:
			return fmt.Errorf("%w: question call %d %s: call %q is not stands or overtaken",
				ErrBadQuestion, i+1, call.Question, call.Call)
		case strings.TrimSpace(call.Reason) == "":
			return fmt.Errorf("%w: question call %d %s: reason is empty", ErrBadQuestion, i+1, call.Question)
		case call.Call == CallStands && outcome == PlanOutcomeComplete:
			return fmt.Errorf("%w: question call %d %s: stands beside outcome complete", ErrBadQuestion, i+1,
				call.Question)
		}
		named[seq] = true
	}
	return nil
}

// applyQuestionCalls stores each of calls, made by turn of epic, in the caller's write, and closes the question of each
// overtaken call as overtaken (overtakeQuestion), with the why calledOvertaken gives. A call names a question epic holds
// that a planner turn asked, one with a turn above 0. A call on a question epic does not hold, or on one orrery or an
// orchestrator asked, is ErrBadQuestion, and the caller's write stores nothing. A call on a question that is no longer
// open, answered or overtaken while the turn ran, stores nothing and changes nothing, as a replaces of such a question
// does: its answer wins, and its trigger wakes the next turn.
func applyQuestionCalls(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	turn int64,
	calls []QuestionCallInput,
	at time.Time,
) error {
	for i, call := range calls {
		seq, _ := QuestionSeq(call.Question)
		stored, err := queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: seq})
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: question call %d: %s: no such question", ErrBadQuestion, i+1, call.Question)
		}
		if err != nil {
			return fmt.Errorf("read question %d of epic %s: %w", seq, epic, err)
		}
		if stored.Turn == 0 {
			return fmt.Errorf("%w: question call %d: %s was not asked by a planner turn", ErrBadQuestion, i+1,
				call.Question)
		}
		if stored.State != QuestionOpen {
			continue
		}
		if err = queries.InsertEpicQuestionCall(ctx, sqlcgen.InsertEpicQuestionCallParams{
			Epic: epic, Question: seq, Turn: turn, Call: call.Call, Reason: call.Reason,
		}); err != nil {
			return fmt.Errorf("insert call of turn %d on question %d of epic %s: %w", turn, seq, epic, err)
		}
		if call.Call != CallOvertaken {
			continue
		}
		overtakeErr := overtakeQuestion(ctx, queries, epic, stored, calledOvertaken(turn, call.Reason), at)
		if overtakeErr != nil {
			return overtakeErr
		}
	}
	return nil
}

// questionCallsOf returns the calls rows store, keyed by question seq, each question's in the order of rows.
func questionCallsOf(rows []sqlcgen.EpicQuestionCall) map[int64][]QuestionCall {
	out := map[int64][]QuestionCall{}
	for _, row := range rows {
		out[row.Question] = append(out[row.Question], QuestionCall{Turn: row.Turn, Call: row.Call, Reason: row.Reason})
	}
	return out
}
