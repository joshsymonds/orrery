package store

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// TaskDecisionMax is the most bytes the failure of a task keeps when an orchestrator's decision failed it. It is larger
// than TurnReasonMax, which an implementer's failure is cut to, because the orchestrator writes the failure as a digest
// for the planner.
const TaskDecisionMax = 4096

// TaskDecision is the record of an orchestrator's decision on a task: the id and the text of the decision of the epic
// that each write carrying it out appends.
type TaskDecision struct {
	ID, Text string
}

// RerunTask carries out an orchestrator's rerun or grant of task seq of epic in one write, when its stored build is
// from: version's files are added, in order, after the task's files, to is stored as its build, version is stored as
// the task's next version carrying decision's id, and decision is appended to the epic's decisions. A file another open
// task of the epic owns is a FileOwnedError, unless that task is among behind, the tasks to waits for, and writes
// nothing. A task that is not open, or whose stored build is not from, is ErrTaskMoved and writes nothing.
func (s *Store) RerunTask(
	ctx context.Context,
	epic string,
	seq int64,
	from, to TaskBuild,
	version TaskVersionInput,
	behind []int64,
	decision TaskDecision,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := grantFiles(ctx, queries, epic, seq, from, to, version, behind, decision.ID, at); err != nil {
			return err
		}
		_, err := appendDecision(ctx, queries, epic, decision.ID, decision.Text, 0, Maker{By: MakerOrreryd}, at)
		return err
	})
}

// AskTask carries out an orchestrator's question to the owner on task seq of epic in one write, when its stored build
// is from. question is checked as a planner's question is (QuestionRefusals), and a question that breaks a rule is
// ErrBadQuestion and writes nothing. Otherwise it opens as the epic's next question, asked by no turn, a tell with
// tell as its text carries it, to is stored as the task's build with Question set to the question's seq, and decision
// is appended to the epic's decisions. A task that is not open, or whose stored build is not from, is ErrTaskMoved and
// writes nothing.
func (s *Store) AskTask(
	ctx context.Context,
	epic string,
	seq int64,
	from, to TaskBuild,
	question EpicQuestionInput,
	tell string,
	decision TaskDecision,
	at time.Time,
) error {
	if err := checkQuestions([]EpicQuestionInput{question}); err != nil {
		return err
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := insertQuestions(ctx, queries, epic, 0, []EpicQuestionInput{question}); err != nil {
			return err
		}
		asked, err := queries.MaxEpicQuestionSeq(ctx, epic)
		if err != nil {
			return fmt.Errorf("read question count of epic %s: %w", epic, err)
		}
		to.Question = asked
		if moveErr := moveTask(ctx, queries, epic, seq, from, to); moveErr != nil {
			return moveErr
		}
		if tellErr := insertQuestionTell(ctx, queries, epic, tell, asked, at); tellErr != nil {
			return tellErr
		}
		_, err = appendDecision(ctx, queries, epic, decision.ID, decision.Text, 0, Maker{By: MakerOrreryd}, at)
		return err
	})
}

// capText cuts text to at most limit bytes at a UTF-8 rune start.
func capText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
