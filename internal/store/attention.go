package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoTell reports that the epic has no tell with the seq asked for.
var ErrNoTell = errors.New("no tell")

// stateStopped is the state internal/enginehost/record.go:18 records for a stopped session.
const stateStopped = "stopped"

// refMoveText matches the text of a tell that reports a ref move: an implementer's, which build writes as `T<n>
// implementer moved …`, or a workflow node's, which interpret writes as `workflow node <path> moved …`.
var refMoveText = regexp.MustCompile(`^(T[0-9]+ implementer|workflow node \S+) moved `)

// SilentMoveTell reports whether a tell with text, which holds a park question when hasQuestion is set, is a silent move
// tell: a ref move that carries no park question. Such a tell is stored and marked told, and it notifies no owner, so
// it is not the epic's latest tell either.
func SilentMoveTell(text string, hasQuestion bool) bool {
	return !hasQuestion && refMoveText.MatchString(text)
}

// EpicAttention is what an epic needs from its owner or its steerer: its phase, the reason of its park when the park
// opens a park question (a planner park reads empty, since the planner answers it), whether it is paused, the
// lowest release step its release runs failed (0 when none did), its open questions in seq order, its untold tells in
// seq order, the time of its newest tell that is not silent (see SilentMoveTell), its latest ended turn with the number
// of questions that turn asked, and whether its steerer hears it. Heard is true when the epic has a steerer whose
// sessions row exists and is not stopped. It is false when the epic has no steerer, when the steerer's row is gone,
// and when the row is stopped. While it is true, an open question that is not escalated is the steerer's to answer
// (SteererHolds), and the owner is not asked it.
type EpicAttention struct {
	Epic        string
	Title       string
	Phase       string
	Parked      string
	Paused      bool
	FailedStep  int64
	Open        []EpicQuestion
	Tells       []EpicTell
	LatestTell  time.Time
	Latest      EpicTurn
	LatestAsked int
	Heard       bool
}

// Attention returns one entry per epic with at least one turn or at least one untold tell, ordered by epic id. An epic
// with neither has no entry. Latest is the epic's highest-seq turn that has ended, zero when none has, and LatestAsked
// counts the questions that turn asked.
func (s *Store) Attention(ctx context.Context) ([]EpicAttention, error) {
	return s.attention(ctx, func(string) {})
}

// attention is Attention with afterLatest called after each epic's latest-turn read. Tests use it to put a write
// between the reads.
func (s *Store) attention(ctx context.Context, afterLatest func(epic string)) ([]EpicAttention, error) {
	queries := sqlcgen.New(s.db)
	epics, err := queries.ListEpicsWithTurnsOrUntoldTells(ctx)
	if err != nil {
		return nil, fmt.Errorf("list epics with turns or untold tells: %w", err)
	}
	var out []EpicAttention
	for _, epic := range epics {
		var entry EpicAttention
		if entry, err = attentionOf(ctx, queries, epic.ID, afterLatest); err != nil {
			return nil, fmt.Errorf("attention of epic %s: %w", epic.ID, err)
		}
		out = append(out, entry)
	}
	return out, nil
}

// attentionOf returns the attention of epic: its head, the lowest release step its release runs failed, its open
// questions in seq order, its untold tells in seq order with the time of its newest tell that is not silent, its
// latest ended turn with the questions that turn asked, and whether its steerer hears it. The latest ended turn is
// read first, so every write that lands between the reads either leaves the turn as the latest or adds the questions
// it asked to Open; a turn that ends after the read is reported on a later pass.
func attentionOf(
	ctx context.Context,
	queries *sqlcgen.Queries,
	id string,
	afterLatest func(epic string),
) (EpicAttention, error) {
	ended, err := queries.GetLatestEndedEpicTurn(ctx, id)
	noEnded := errors.Is(err, sql.ErrNoRows)
	if err != nil && !noEnded {
		return EpicAttention{}, fmt.Errorf("read latest ended turn: %w", err)
	}
	afterLatest(id)
	head, err := queries.GetEpic(ctx, id)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("read epic: %w", err)
	}
	open, err := queries.ListOpenEpicQuestions(ctx, id)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("list open questions: %w", err)
	}
	questions, err := convertInOrder(open, questionOf)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("open questions: %w", err)
	}
	tells, err := queries.ListEpicTells(ctx, id)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("list tells: %w", err)
	}
	untold, err := untoldTellsOf(ctx, queries, id, tells)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("untold tells: %w", err)
	}
	releases, err := readReleaseRuns(ctx, queries, id)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("release runs: %w", err)
	}
	heard, err := steererHeard(ctx, queries, id)
	if err != nil {
		return EpicAttention{}, fmt.Errorf("steerer: %w", err)
	}
	entry := EpicAttention{
		Epic: head.ID, Title: head.Title, Phase: head.Phase,
		Parked: questionPark(head.Parked, head.ParkClass), Paused: head.Paused != 0,
		FailedStep: failedReleaseStep(releases), Open: questions, Tells: untold, Heard: heard,
	}
	for _, tell := range slices.Backward(tells) {
		if !SilentMoveTell(tell.Text, tell.Question != 0) {
			entry.LatestTell = time.Unix(tell.At, 0)
			break
		}
	}
	if noEnded {
		return entry, nil
	}
	var latest EpicTurn
	if latest, err = turnOf(ctx, queries, ended); err != nil {
		return EpicAttention{}, fmt.Errorf("turn %d: %w", ended.Seq, err)
	}
	asked, err := queries.CountEpicQuestionsOfTurn(ctx, sqlcgen.CountEpicQuestionsOfTurnParams{
		Epic: id, Turn: ended.Seq,
	})
	if err != nil {
		return EpicAttention{}, fmt.Errorf("count questions of turn %d: %w", ended.Seq, err)
	}
	entry.Latest = latest
	entry.LatestAsked = int(asked)
	return entry, nil
}

// steererHeard reports whether epic has a steerer whose sessions row exists and is not stopped. It reads the steerer
// through the query Store.EpicSteerer reads it with, so a missing steerer and a gone row both read as not heard.
func steererHeard(ctx context.Context, queries *sqlcgen.Queries, epic string) (bool, error) {
	steerer, err := queries.GetEpicSteerer(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read steerer of epic %s: %w", epic, err)
	}
	return steerer.State != stateStopped, nil
}

// untoldTellsOf returns the tells of epic not yet told, in the seq order the rows come in. A tell that belongs to a
// question carries that question, read by its seq.
func untoldTellsOf(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	tells []sqlcgen.EpicTell,
) ([]EpicTell, error) {
	var out []EpicTell
	for _, tell := range tells {
		if tell.Told != 0 {
			continue
		}
		untold := EpicTell{Seq: tell.Seq, Text: tell.Text, At: time.Unix(tell.At, 0)}
		if tell.Question != 0 {
			stored, err := queries.GetEpicQuestion(ctx, sqlcgen.GetEpicQuestionParams{Epic: epic, Seq: tell.Question})
			if err != nil {
				return nil, fmt.Errorf("read question %d of tell %d: %w", tell.Question, tell.Seq, err)
			}
			question, err := questionOf(stored)
			if err != nil {
				return nil, fmt.Errorf("question %d of tell %d: %w", tell.Question, tell.Seq, err)
			}
			untold.Question = &question
		}
		out = append(out, untold)
	}
	return out, nil
}

// failedReleaseStep returns the lowest position of a release step that has a release run of part release that did not
// pass and no release run at that position that passed, or 0 when no step has.
func failedReleaseStep(runs []ReleaseRun) int64 {
	passed := make(map[int64]bool)
	failed := make(map[int64]bool)
	for _, run := range runs {
		if run.Part != ReleasePartStep {
			continue
		}
		if run.Passed {
			passed[run.Position] = true
		} else {
			failed[run.Position] = true
		}
	}
	var lowest int64
	for position := range failed {
		if passed[position] {
			continue
		}
		if lowest == 0 || position < lowest {
			lowest = position
		}
	}
	return lowest
}

// MarkTellTold sets the told marker of tell seq of epic in one write. A tell that does not exist is an error matching
// ErrNoTell.
func (s *Store) MarkTellTold(ctx context.Context, epic string, seq int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.MarkEpicTellTold(ctx, sqlcgen.MarkEpicTellToldParams{Epic: epic, Seq: seq})
		if err != nil {
			return fmt.Errorf("mark tell %d of epic %s told: %w", seq, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("tell %d of epic %s: %w", seq, epic, ErrNoTell)
		}
		return nil
	})
}

// MarkTold sets the told marker of turn seq of epic in one write. A turn that does not exist is an error matching
// ErrNoTurn.
func (s *Store) MarkTold(ctx context.Context, epic string, seq int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.MarkEpicTurnTold(ctx, sqlcgen.MarkEpicTurnToldParams{Epic: epic, Seq: seq})
		if err != nil {
			return fmt.Errorf("mark turn %d of epic %s told: %w", seq, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("turn %d of epic %s: %w", seq, epic, ErrNoTurn)
		}
		return nil
	})
}
