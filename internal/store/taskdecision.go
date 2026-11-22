package store

import (
	"context"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// TaskDecisionMax is the most bytes the failure of a task keeps when an orchestrator's decision failed it. It is larger
// than TurnReasonMax, which an implementer's failure is cut to, because the orchestrator writes the failure as a digest
// for the planner.
const TaskDecisionMax = 4096

// TaskDecision is the record of an orchestrator's decision on a task: the id and the text of the decision of the epic
// that each write carrying it out appends, and the files the decision refused, which each such write stores as refused
// by that id.
type TaskDecision struct {
	ID, Text string
	Refused  []string
}

// RerunTask carries out an orchestrator's rerun or grant of task seq of epic in one write, when its stored build is
// from: version's files are added, in order, after the task's files, to is stored as its build, version is stored as
// the task's next version carrying decision's id, decision's refused files are stored as refused by decision's id, and
// decision is appended to the epic's decisions. A file another task of the epic owns is added as well, since tasks may
// share a file. A task that is not open, or whose stored build is not from, is ErrTaskMoved and writes nothing.
func (s *Store) RerunTask(
	ctx context.Context,
	epic string,
	seq int64,
	from, to TaskBuild,
	version TaskVersionInput,
	decision TaskDecision,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := grantFiles(ctx, queries, epic, seq, from, to, version, decision.ID, at); err != nil {
			return err
		}
		if err := storeRefusals(ctx, queries, epic, seq, decision, version.Files, at); err != nil {
			return err
		}
		_, err := appendDecision(ctx, queries, epic, decision.ID, decision.Text, 0, Maker{By: MakerOrreryd}, "", at)
		return err
	})
}

// storeRefusals records the files decision refused for task seq of epic in the caller's write, once the write has added
// the files of the version it carries, granted. Each refused file is stored under decision's id, and a file already
// refused keeps the decision that refused it first. A file granted is no longer refused, since the task owns it. A
// refused file the task owns is an error that writes nothing.
func storeRefusals(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq int64,
	decision TaskDecision,
	granted []string,
	at time.Time,
) error {
	if len(decision.Refused) > 0 {
		files, err := queries.ListEpicTaskFiles(ctx, epic)
		if err != nil {
			return fmt.Errorf("read task files of epic %s: %w", epic, err)
		}
		for _, path := range decision.Refused {
			if slices.ContainsFunc(files, func(file sqlcgen.EpicTaskFile) bool {
				return file.Task == seq && file.Path == path
			}) {
				return fmt.Errorf("refuse %s of task %d of epic %s: the task owns it", path, seq, epic)
			}
		}
		for _, path := range decision.Refused {
			if err = queries.InsertEpicTaskRefused(ctx, sqlcgen.InsertEpicTaskRefusedParams{
				Epic: epic, Task: seq, Path: path, Decision: decision.ID, Created: at.Unix(),
			}); err != nil {
				return fmt.Errorf("refuse %s of task %d of epic %s: %w", path, seq, epic, err)
			}
		}
	}
	for _, path := range granted {
		if err := queries.DeleteEpicTaskRefused(ctx, sqlcgen.DeleteEpicTaskRefusedParams{
			Epic: epic, Task: seq, Path: path,
		}); err != nil {
			return fmt.Errorf("clear refusal of %s of task %d of epic %s: %w", path, seq, epic, err)
		}
	}
	return nil
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
		_, err = appendDecision(ctx, queries, epic, decision.ID, decision.Text, 0, Maker{By: MakerOrreryd}, "", at)
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
