package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// boundActions is how many planner actions a chain holds when a park that returns on it goes to the owner. A planner park
// that finds this many actions on its chain is stored as ParkClassRetryBound instead.
const boundActions = 2

// taskSubject returns the subject of the park of task seq: T<seq>, the name the task has in the store's rows.
func taskSubject(seq int64) string {
	return "T" + strconv.FormatInt(seq, 10)
}

// plannerParked reports whether an epic parked with reason, of class, holds a planner park: one the planner answers.
func plannerParked(reason, class string) bool {
	return reason != "" && !ParkClass(class).OpensQuestion()
}

// parkChainOf returns the chain a planner park of class with subject joins: the chain of the epic's latest park row with
// that class and subject. A task park with no such row joins the chain of the task it replaces, followed back through the
// replacements of that task. It returns 0 when the park starts a chain.
func parkChainOf(
	ctx context.Context, queries *sqlcgen.Queries, epic string, class ParkClass, subject string,
) (int64, error) {
	latest, found, err := latestParkOf(ctx, queries, epic, class, subject)
	if err != nil {
		return 0, err
	}
	if found {
		return latest.Chain, nil
	}
	if class != ParkClassTask {
		return 0, nil
	}
	replaced, err := queries.ReplacedEpicTask(ctx, sqlcgen.ReplacedEpicTaskParams{Epic: epic, Subject: subject})
	if err != nil {
		return 0, fmt.Errorf("read the task that %s of epic %s replaces: %w", subject, epic, err)
	}
	if replaced == 0 {
		return 0, nil
	}
	return parkChainOf(ctx, queries, epic, class, taskSubject(replaced))
}

// latestParkOf returns the epic's latest park row of class with subject, and whether it has one.
func latestParkOf(
	ctx context.Context, queries *sqlcgen.Queries, epic string, class ParkClass, subject string,
) (sqlcgen.EpicPark, bool, error) {
	park, err := queries.GetLatestEpicParkOf(ctx, sqlcgen.GetLatestEpicParkOfParams{
		Epic: epic, Class: string(class), Subject: subject,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.EpicPark{}, false, nil
	}
	if err != nil {
		return sqlcgen.EpicPark{}, false, fmt.Errorf("read the latest park of epic %s: %w", epic, err)
	}
	return park, true, nil
}

// latestEpicPark returns the epic's latest park row, which is the planner park the epic holds when it holds one, and
// whether it has one.
func latestEpicPark(ctx context.Context, queries *sqlcgen.Queries, epic string) (sqlcgen.EpicPark, bool, error) {
	park, err := queries.GetLatestEpicPark(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.EpicPark{}, false, nil
	}
	if err != nil {
		return sqlcgen.EpicPark{}, false, fmt.Errorf("read the latest park of epic %s: %w", epic, err)
	}
	return park, true, nil
}

// chainActions returns the actions taken or refused on chain of epic, in turn then position order.
func chainActions(
	ctx context.Context, queries *sqlcgen.Queries, epic string, chain int64,
) ([]sqlcgen.PlannerAction, error) {
	actions, err := queries.ListEpicChainActions(ctx, sqlcgen.ListEpicChainActionsParams{Epic: epic, Chain: chain})
	if err != nil {
		return nil, fmt.Errorf("list actions of chain %d of epic %s: %w", chain, epic, err)
	}
	return actions, nil
}

// parkPlanner parks epic with park as the planner class of subject, in the caller's write. The park joins the chain
// parkChainOf gives it, or starts one. A chain that already holds boundActions actions takes the park as retry_bound
// instead, and boundPark opens its question. Any other park is stored as a row of epic_parks, parks epic as class, and
// returns 0, so the caller wakes the planner.
func parkPlanner(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	class ParkClass,
	subject string,
	park string,
	at time.Time,
) (int64, error) {
	chain, err := parkChainOf(ctx, queries, epic, class, subject)
	if err != nil {
		return 0, err
	}
	if chain != 0 {
		actions, listErr := chainActions(ctx, queries, epic, chain)
		if listErr != nil {
			return 0, listErr
		}
		if len(actions) >= boundActions {
			return boundPark(ctx, queries, epic, park, chain, 0, at)
		}
	}
	highest, err := queries.MaxEpicParkSeq(ctx, epic)
	if err != nil {
		return 0, fmt.Errorf("read park count of epic %s: %w", epic, err)
	}
	seq := highest + 1
	if chain == 0 {
		chain = seq
	}
	if err = queries.InsertEpicPark(ctx, sqlcgen.InsertEpicParkParams{
		Epic: epic, Seq: seq, Class: string(class), Subject: subject, Chain: chain, Reason: park, Created: at.Unix(),
	}); err != nil {
		return 0, fmt.Errorf("insert park %d of epic %s: %w", seq, epic, err)
	}
	return 0, setPark(ctx, queries, epic, park, class)
}

// boundPark stores park as the retry_bound park of epic in the caller's write. It opens the park question a question park
// opens, whose text is boundQuestionText: park, then the reasons of the earlier parks of chain, then each action taken or
// refused on chain. The question's text is also added as a tell. held is the seq of the planner park the epic holds, which
// the question does not list as earlier, or 0 when park is not stored yet. boundPark returns the seq of the question.
func boundPark(
	ctx context.Context, queries *sqlcgen.Queries, epic, park string, chain, held int64, at time.Time,
) (int64, error) {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return 0, err
	}
	parks, err := queries.ListEpicChainParks(ctx, sqlcgen.ListEpicChainParksParams{Epic: epic, Chain: chain})
	if err != nil {
		return 0, fmt.Errorf("list parks of chain %d of epic %s: %w", chain, epic, err)
	}
	var earlier []string
	for _, stored := range parks {
		if stored.Seq != held {
			earlier = append(earlier, stored.Reason)
		}
	}
	actions, err := chainActions(ctx, queries, epic, chain)
	if err != nil {
		return 0, err
	}
	lines, err := actionLines(actions)
	if err != nil {
		return 0, err
	}
	text := boundQuestionText(park, earlier, lines)
	question, err := openPark(ctx, queries, epic, park, ParkClassRetryBound,
		parkQuestion(text, head.Phase == phaseMetAtBase))
	if err != nil {
		return 0, err
	}
	err = insertQuestionTell(ctx, queries, epic, text, question, at)
	if err != nil {
		return 0, err
	}
	return question, nil
}

// boundQuestionText returns the text of the question a park at the bound of its chain asks: park, then each earlier park
// of the chain as its reason, then each action on the chain as its line. The earlier parks are listed only when there are
// some.
func boundQuestionText(park string, earlier, actions []string) string {
	var text strings.Builder
	text.WriteString(park)
	if len(earlier) > 0 {
		text.WriteString("\nEarlier parks on this chain:")
		for _, reason := range earlier {
			text.WriteString("\n- " + reason)
		}
	}
	text.WriteString("\nPlanner actions on this chain:")
	for _, line := range actions {
		text.WriteString("\n- " + line)
	}
	return text.String()
}

// actionLines returns one line per stored action, naming it as actionSummary does, with the planner's reason and its
// outcome: taken, or refused with the refusal.
func actionLines(actions []sqlcgen.PlannerAction) ([]string, error) {
	lines := make([]string, 0, len(actions))
	for _, row := range actions {
		action, err := plannerActionOf(row)
		if err != nil {
			return nil, err
		}
		outcome := actionTaken
		if row.State == actionRefused {
			outcome = "refused: " + row.Refusal
		}
		lines = append(lines, fmt.Sprintf("%s: %s (%s)", actionSummary(action), action.Reason, outcome))
	}
	return lines, nil
}

// actionPark returns the seq of the planner park action answers, or 0 when it answers none. An action answers the planner
// park its epic holds when its control runs. Every action of a turn counts, though an earlier action of the turn may have
// ended the park, so an action with no planner park held answers the park an earlier action of its turn answered.
func actionPark(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, head sqlcgen.Epic) (int64, error) {
	if plannerParked(head.Parked, head.ParkClass) {
		held, found, err := latestEpicPark(ctx, queries, action.Epic)
		if err != nil || !found {
			return 0, err
		}
		return held.Seq, nil
	}
	park, err := queries.MaxTurnActionPark(ctx, sqlcgen.MaxTurnActionParkParams{Epic: action.Epic, Turn: action.Turn})
	if err != nil {
		return 0, fmt.Errorf("read the park of turn %d of epic %s: %w", action.Turn, action.Epic, err)
	}
	return park, nil
}

// boundHeldPark stores the planner park epic holds as retry_bound, in the caller's write, when the chain of that park holds
// boundActions actions or more, and reports whether it did. A planner park stored before 00051_park_bound.sql has no row,
// so it starts no chain and is never bound.
func boundHeldPark(
	ctx context.Context, queries *sqlcgen.Queries, epic string, head sqlcgen.Epic, at time.Time,
) (bool, error) {
	if !plannerParked(head.Parked, head.ParkClass) {
		return false, nil
	}
	held, found, err := latestEpicPark(ctx, queries, epic)
	if err != nil || !found {
		return false, err
	}
	actions, err := chainActions(ctx, queries, epic, held.Chain)
	if err != nil {
		return false, err
	}
	if len(actions) < boundActions {
		return false, nil
	}
	if _, err = boundPark(ctx, queries, epic, head.Parked, held.Chain, held.Seq, at); err != nil {
		return false, err
	}
	return true, nil
}
