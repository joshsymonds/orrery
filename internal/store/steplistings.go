package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// StepListing is a step as a list of steps reads it, from the step_listings view: the fields of its row a listed step
// carries, without the prompt, the schema, the agents, the owned paths, the gate's argv, source and needs, the checkout
// and the loop's nudges, and with its result cut to what a list reads of it: Summary, the result's string summary (see
// StepRow.Listing).
// Ended is zero while End is empty, LoopLimitResets is zero when the limit's reset is unknown, and LoopStageFrom is zero
// when the stage was stored with no start.
type StepListing struct {
	ID              string
	Agent           string
	Workspace       string
	Revision        string
	Worktree        string
	ReadOnly        bool
	Started         time.Time
	Checks          []StepCheck
	Doing           string
	Verdicts        []StepVerdict
	Status          string
	End             string
	Reason          string
	Ended           time.Time
	Epic            string
	Task            string
	GateName        string
	GateLimit       time.Duration
	LoopLimited     bool
	LoopLimitKind   string
	LoopLimitResets time.Time
	LoopStage       string
	LoopStageFrom   time.Time
	Caller          string
	Summary         string
}

// Listing returns the listing of row, as step_listings reads it from the stored row. The result is read as a JSON
// object: its summary is the string at its summary key, read at its last value when the key is held more than once. A
// result that does not parse, or is not an object, has no summary, and neither has one whose summary is not a string.
func (row StepRow) Listing() StepListing {
	return StepListing{
		ID:              row.ID,
		Agent:           row.Agent,
		Workspace:       row.Workspace,
		Revision:        row.Revision,
		Worktree:        row.Worktree,
		ReadOnly:        row.ReadOnly,
		Started:         row.Started,
		Checks:          row.Checks,
		Doing:           row.Doing,
		Verdicts:        row.Verdicts,
		Status:          row.Status,
		End:             row.End,
		Reason:          row.Reason,
		Ended:           row.Ended,
		Epic:            row.Epic,
		Task:            row.Task,
		GateName:        row.GateName,
		GateLimit:       row.GateLimit,
		LoopLimited:     row.LoopLimited,
		LoopLimitKind:   row.LoopLimitKind,
		LoopLimitResets: row.LoopLimitResets,
		LoopStage:       row.LoopStage,
		LoopStageFrom:   row.LoopStageFrom,
		Caller:          row.Caller,
		Summary:         resultString(stepResultFields(row.Result), "summary"),
	}
}

// StepListings returns the listings of the step rows with a non-empty epic, narrowed as LabelledSteps narrows them and in
// its order. Only a read not narrowed to an epic reads every labeled step.
func (s *Store) StepListings(ctx context.Context, epic, task string) ([]StepListing, error) {
	return readStepListings(ctx, sqlcgen.New(s.db), epic, task)
}

// CallerStepListings returns the listings of the steps whose caller is the sessions row caller, labeled or not,
// narrowed to epic when it is not empty, ordered by start time then id. Of the narrowed steps it keeps every one still
// running and the 20 that ended most recently, by end time then id, so a list holds at most 20 ended steps however many
// the caller started.
func (s *Store) CallerStepListings(ctx context.Context, caller, epic string) ([]StepListing, error) {
	rows, err := sqlcgen.New(s.db).ListCallerStepListings(
		ctx,
		sqlcgen.ListCallerStepListingsParams{Caller: caller, Epic: epic},
	)
	if err != nil {
		return nil, fmt.Errorf("list caller step listings: %w", err)
	}
	return convertInOrder(rows, stepListingOf)
}

// RunningStepListings returns the listings of the labeled steps that have not ended, held ones among them, narrowed to
// epic when it is not empty and to task when it is not empty, ordered by start time then id, and, by epic, how many of
// the labeled steps so narrowed have ended. An epic none of whose narrowed steps has ended has no count. It reads the
// open steps through their partial index and counts the labeled steps by their epic and task labels alone, so no
// ended step's row is read. Both are read in one read transaction, so the counts and the listings describe one state of
// the store.
func (s *Store) RunningStepListings(ctx context.Context, epic, task string) ([]StepListing, map[string]int, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin running steps read: %w", err)
	}
	listings, ended, err := readRunningStepListings(ctx, sqlcgen.New(tx), epic, task)
	if err != nil {
		return nil, nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("end running steps read: %w", err)
	}
	return listings, ended, nil
}

// readStepListings reads the listings StepListings returns with queries.
func readStepListings(ctx context.Context, queries *sqlcgen.Queries, epic, task string) ([]StepListing, error) {
	var rows []sqlcgen.StepListing
	var err error
	switch {
	case epic == "":
		rows, err = queries.ListLabelledStepListings(ctx, task)
	case task == "":
		rows, err = queries.ListEpicStepListings(ctx, epic)
	default:
		rows, err = queries.ListEpicTaskStepListings(
			ctx,
			sqlcgen.ListEpicTaskStepListingsParams{Epic: epic, Task: task},
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list labeled step listings: %w", err)
	}
	return convertInOrder(rows, stepListingOf)
}

// readRunningStepListings reads the listings and ended counts RunningStepListings returns with queries. The ended count of an
// epic is the count of its narrowed labeled steps less its running ones.
func readRunningStepListings(
	ctx context.Context, queries *sqlcgen.Queries, epic, task string,
) ([]StepListing, map[string]int, error) {
	narrowed := func(stepEpic, stepTask string) bool {
		return (epic == "" || stepEpic == epic) && (task == "" || stepTask == task)
	}
	rows, err := queries.ListOpenStepListings(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list running step listings: %w", err)
	}
	counts, err := queries.CountLabelledSteps(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("count labeled steps: %w", err)
	}
	ended := make(map[string]int)
	for _, count := range counts {
		if narrowed(count.Epic, count.Task) {
			ended[count.Epic] += int(count.Steps)
		}
	}
	var listings []StepListing
	for _, row := range rows {
		if !narrowed(row.Epic, row.Task) {
			continue
		}
		listing, convertErr := stepListingOf(row)
		if convertErr != nil {
			return nil, nil, convertErr
		}
		listings = append(listings, listing)
		ended[row.Epic]--
	}
	for id, count := range ended {
		if count <= 0 {
			delete(ended, id)
		}
	}
	return listings, ended, nil
}

// stepListingOf returns the listing a step_listings row holds, its checks and verdicts decoded.
func stepListingOf(row sqlcgen.StepListing) (StepListing, error) {
	checks, err := decodeStepChecks(row.Checks)
	if err != nil {
		return StepListing{}, fmt.Errorf("decode checks of step %s: %w", row.ID, err)
	}
	verdicts, err := decodeStepVerdicts(row.Verdicts)
	if err != nil {
		return StepListing{}, fmt.Errorf("decode verdicts of step %s: %w", row.ID, err)
	}
	listing := StepListing{
		ID:            row.ID,
		Agent:         row.Agent,
		Workspace:     row.Workspace,
		Revision:      row.Revision,
		Worktree:      row.Worktree,
		ReadOnly:      row.ReadOnly,
		Started:       time.Unix(row.Started, 0),
		Checks:        checks,
		Doing:         row.Doing,
		Verdicts:      verdicts,
		Status:        row.Status,
		End:           row.EndValue,
		Reason:        row.Reason,
		Epic:          row.Epic,
		Task:          row.Task,
		GateName:      row.GateName,
		GateLimit:     time.Duration(row.GateLimit),
		LoopLimited:   row.LoopLimited,
		LoopLimitKind: row.LoopLimitKind,
		LoopStage:     row.LoopStage,
		Caller:        row.Caller,
		Summary:       row.Summary,
	}
	if row.EndValue != "" {
		listing.Ended = time.Unix(row.Ended, 0)
	}
	if row.LoopLimitResets != 0 {
		listing.LoopLimitResets = time.Unix(row.LoopLimitResets, 0)
	}
	if row.LoopStageFrom != 0 {
		listing.LoopStageFrom = time.Unix(row.LoopStageFrom, 0)
	}
	return listing, nil
}

// stepResultFields parses a step's result as a JSON object. A result that does not parse, or is not an object, has no
// fields. A key held more than once keeps its last value.
func stepResultFields(result string) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result), &fields); err != nil {
		return nil
	}
	return fields
}

// resultString returns the string fields holds at key. A key fields does not hold, or one whose value is not a string,
// has none.
func resultString(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return text
}
