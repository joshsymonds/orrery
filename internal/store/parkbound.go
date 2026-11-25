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

// boundActions is how many planner actions taken on a chain bring it to its bound. A judgment of a park at the bound,
// park_recurs or park_new, is not an action on the chain and is not counted (countChain).
const boundActions = 2

// boundRefusals is how many refused planner actions on a chain bring it to its bound. Refusals are counted apart from the
// actions taken, so a refusal spends none of the bound of the actions taken. A refusal that brings the chain of the
// planner park the epic holds to it sends that park to the owner (boundHeldPark).
const boundRefusals = 2

// streakBound is how many planner parks in a row the planner may answer with no progress since the first of them
// (noProgressStreak). The planner park after them goes to the owner, whatever its chain holds and however the planner
// called the parks before it.
const streakBound = 4

// The states of a planner park row: a park the planner answers, a park stored at the bound of its chain that waits on
// the planner's call whether it recurs or is new, and a park sent to the owner as retry_bound.
const (
	parkStatePlanner = "planner"
	parkStateBound   = "bound"
	parkStateOwner   = "owner"
)

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

// chainCount is what a chain holds toward its bound: the planner actions taken on it and those refused on it.
type chainCount struct {
	taken, refused int
}

// atBound reports whether a chain that holds count is at its bound: boundActions actions taken on it, or boundRefusals
// refused.
func (c chainCount) atBound() bool {
	return c.taken >= boundActions || c.refused >= boundRefusals
}

// countChain returns what chain of epic holds toward its bound. A taken park_recurs or park_new is the planner's call on a
// park at the bound, not an action on the chain, so it is not counted. A refused action counts as refused whatever its
// kind.
func countChain(ctx context.Context, queries *sqlcgen.Queries, epic string, chain int64) (chainCount, error) {
	actions, err := chainActions(ctx, queries, epic, chain)
	if err != nil {
		return chainCount{}, err
	}
	var count chainCount
	for _, action := range actions {
		switch {
		case action.State == actionRefused:
			count.refused++
		case !isJudgment(action.Kind):
			count.taken++
		}
	}
	return count, nil
}

// noProgressStreak returns how many of the epic's latest park rows in a row the planner answered with no progress since
// the first of them. Walking back from the latest row, each row of a park the planner answers or is judging counts,
// until a row of a park sent to the owner, which the owner has seen, or a row the epic has made progress since. The epic
// has made progress since a park when a node run ended passed or a release step passed after the park was stored, or
// its stored base is not the one the park recorded. head is the epic's head row. At most streakBound rows are read.
func noProgressStreak(ctx context.Context, queries *sqlcgen.Queries, epic string, head sqlcgen.Epic) (int, error) {
	passed, err := queries.LatestEpicPass(ctx, epic)
	if err != nil {
		return 0, fmt.Errorf("read the latest pass of epic %s: %w", epic, err)
	}
	parks, err := queries.ListLatestEpicParks(ctx, sqlcgen.ListLatestEpicParksParams{Epic: epic, Count: streakBound})
	if err != nil {
		return 0, fmt.Errorf("list the latest parks of epic %s: %w", epic, err)
	}
	streak := 0
	for _, park := range parks {
		if park.State == parkStateOwner || passed > park.Created || park.StoredBase != head.StoredBase {
			break
		}
		streak++
	}
	return streak, nil
}

// arrivingState returns the state a planner park of epic that joins chain is stored in, and the no-progress streak of
// the parks before it (noProgressStreak). It is the owner's once that streak reaches streakBound, bound when chain is at
// its bound, and the planner's otherwise. chain is 0 for a park that starts a chain. head is the epic's head row.
func arrivingState(
	ctx context.Context, queries *sqlcgen.Queries, epic string, head sqlcgen.Epic, chain int64,
) (string, int, error) {
	streak, err := noProgressStreak(ctx, queries, epic, head)
	if err != nil {
		return "", 0, err
	}
	if streak >= streakBound {
		return parkStateOwner, streak, nil
	}
	if chain == 0 {
		return parkStatePlanner, streak, nil
	}
	count, err := countChain(ctx, queries, epic, chain)
	if err != nil {
		return "", 0, err
	}
	if count.atBound() {
		return parkStateBound, streak, nil
	}
	return parkStatePlanner, streak, nil
}

// parkPlanner parks epic with park as the planner class of subject, in the caller's write, and stores the park as a row
// of epic_parks that records the epic's stored base and candidate. The park joins the chain parkChainOf gives it, or
// starts one. Once the planner has answered streakBound parks in a row with no progress, the park is the owner's:
// boundPark opens its retry_bound question with streakNote, and its seq is returned. Otherwise the park parks epic as
// class and returns 0, so the caller wakes the planner. A park on a chain at its bound is stored waiting on the planner's
// call, which park_recurs or park_new makes; any other park is the planner's to answer.
func parkPlanner(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	class ParkClass,
	subject string,
	park string,
	at time.Time,
) (int64, error) {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return 0, err
	}
	chain, err := parkChainOf(ctx, queries, epic, class, subject)
	if err != nil {
		return 0, err
	}
	state, streak, err := arrivingState(ctx, queries, epic, head, chain)
	if err != nil {
		return 0, err
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
		State: state, StoredBase: head.StoredBase, Candidate: head.Candidate,
	}); err != nil {
		return 0, fmt.Errorf("insert park %d of epic %s: %w", seq, epic, err)
	}
	if state == parkStateOwner {
		return boundPark(ctx, queries, epic, park, chain, seq, streakNote(streak), at)
	}
	return 0, setPark(ctx, queries, epic, park, class)
}

// streakNote returns the line the owner question of a park sent to the owner by the no-progress streak holds: how many
// parks in a row the planner answered with no progress.
func streakNote(streak int) string {
	return fmt.Sprintf("The planner answered the %d parks before this one with no progress since: no node run or "+
		"release step passed, and the stored base did not move.", streak)
}

// boundPark stores park as the retry_bound park of epic in the caller's write, and stores held, the seq of the park's
// row, as the owner's. It opens the park question a question park opens, whose text is boundQuestionText: park, then
// note when it is not empty, then the reasons of the parks of chain before held, then each action taken or refused on
// chain. A node park's question names the run its row's subject is, so a retry of the question reruns that run, as a
// question park of a run does (parkNodeRun). The question's text is also added as a tell. boundPark returns the seq of
// the question.
func boundPark(
	ctx context.Context, queries *sqlcgen.Queries, epic, park string, chain, held int64, note string, at time.Time,
) (int64, error) {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return 0, err
	}
	if err = queries.SetEpicParkState(ctx, sqlcgen.SetEpicParkStateParams{
		State: parkStateOwner, Epic: epic, Seq: held,
	}); err != nil {
		return 0, fmt.Errorf("send park %d of epic %s to the owner: %w", held, epic, err)
	}
	parks, err := queries.ListEpicChainParks(ctx, sqlcgen.ListEpicChainParksParams{Epic: epic, Chain: chain})
	if err != nil {
		return 0, fmt.Errorf("list parks of chain %d of epic %s: %w", chain, epic, err)
	}
	var earlier []string
	var row sqlcgen.EpicPark
	for _, stored := range parks {
		if stored.Seq < held {
			earlier = append(earlier, stored.Reason)
		}
		if stored.Seq == held {
			row = stored
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
	text := boundQuestionText(park, note, earlier, lines)
	question, err := openPark(ctx, queries, epic, park, ParkClassRetryBound,
		parkQuestion(text, head.Phase == phaseMetAtBase))
	if err != nil {
		return 0, err
	}
	if row.Class == string(ParkClassNode) && row.Subject != "" {
		if linkErr := insertParkQuestionRun(ctx, queries, epic, question, row.Subject); linkErr != nil {
			return 0, linkErr
		}
	}
	err = insertQuestionTell(ctx, queries, epic, text, question, at)
	if err != nil {
		return 0, err
	}
	return question, nil
}

// boundQuestionText returns the text of the question a park sent to the owner asks: park, then note when it is not
// empty, then each earlier park of the chain as its reason, then each action on the chain as its line. The earlier parks
// are listed only when there are some, and so are the actions.
func boundQuestionText(park, note string, earlier, actions []string) string {
	var text strings.Builder
	text.WriteString(park)
	if note != "" {
		text.WriteString("\n" + note)
	}
	if len(earlier) > 0 {
		text.WriteString("\nEarlier parks on this chain:")
		for _, reason := range earlier {
			text.WriteString("\n- " + reason)
		}
	}
	if len(actions) > 0 {
		text.WriteString("\nPlanner actions on this chain:")
		for _, line := range actions {
			text.WriteString("\n- " + line)
		}
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

// boundHeldPark sends the planner park epic holds to the owner as retry_bound, in the caller's write, after a refusal,
// and reports whether it did. It does so when the park waits on the planner's call, since an action refused before the
// call leaves the park with no call, and when the chain of the park holds boundRefusals refused actions or more. A planner
// park stored before 00051_park_bound.sql has no row, so it starts no chain and is never bound.
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
	if held.State != parkStateBound {
		count, countErr := countChain(ctx, queries, epic, held.Chain)
		if countErr != nil {
			return false, countErr
		}
		if count.refused < boundRefusals {
			return false, nil
		}
	}
	if _, err = boundPark(ctx, queries, epic, head.Parked, held.Chain, held.Seq, "", at); err != nil {
		return false, err
	}
	return true, nil
}

// heldBoundPark returns the row of the planner park epic holds when that park is at the bound of its chain and waits on
// the planner's call, and whether epic holds such a park. head is the epic's head row.
func heldBoundPark(
	ctx context.Context, queries *sqlcgen.Queries, epic string, head sqlcgen.Epic,
) (sqlcgen.EpicPark, bool, error) {
	if !plannerParked(head.Parked, head.ParkClass) {
		return sqlcgen.EpicPark{}, false, nil
	}
	held, found, err := latestEpicPark(ctx, queries, epic)
	if err != nil || !found || held.State != parkStateBound {
		return sqlcgen.EpicPark{}, false, err
	}
	return held, true, nil
}

// noCallRefusal is the RefusalError of a park_recurs or park_new action on epic, which holds no park that waits on the
// planner's call.
func noCallRefusal(epic string) error {
	return RefusalError(fmt.Sprintf("epic %s holds no park at the bound of its chain that waits on a call", epic))
}

// parkRecurs carries out a park_recurs action in the caller's write: the planner's call that the park its epic holds at
// the bound of its chain is the same problem as an earlier park of the chain, back with no progress. boundPark sends the
// park to the owner as retry_bound. An epic that holds no park waiting on the planner's call is a RefusalError.
func parkRecurs(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, at time.Time) error {
	head, err := epicHead(ctx, queries, action.Epic)
	if err != nil {
		return err
	}
	held, found, err := heldBoundPark(ctx, queries, action.Epic, head)
	if err != nil {
		return err
	}
	if !found {
		return noCallRefusal(action.Epic)
	}
	_, err = boundPark(ctx, queries, action.Epic, head.Parked, held.Chain, held.Seq, "", at)
	return err
}

// parkNew carries out a park_new action in the caller's write: the planner's call that the park its epic holds at the
// bound of its chain is a new problem. The park moves to a chain of its own and is the planner's to answer, so the
// actions of its turn after the call count on that chain, and a later park of its class and subject joins it. An epic
// that holds no park waiting on the planner's call is a RefusalError.
func parkNew(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction) error {
	head, err := epicHead(ctx, queries, action.Epic)
	if err != nil {
		return err
	}
	held, found, err := heldBoundPark(ctx, queries, action.Epic, head)
	if err != nil {
		return err
	}
	if !found {
		return noCallRefusal(action.Epic)
	}
	if _, err = queries.RenewEpicParkChain(ctx, sqlcgen.RenewEpicParkChainParams{
		Epic: action.Epic, Seq: held.Seq,
	}); err != nil {
		return fmt.Errorf("move park %d of epic %s to a chain of its own: %w", held.Seq, action.Epic, err)
	}
	return nil
}

// refuseUncalled returns the RefusalError of action when it is not a judgment and its epic holds a park at the bound of
// its chain that waits on the planner's call, since the call comes before any action that answers the park. Its refusal
// sends the park to the owner (boundHeldPark). It returns nil otherwise. head is the epic's head row.
func refuseUncalled(ctx context.Context, queries *sqlcgen.Queries, action PlannerAction, head sqlcgen.Epic) error {
	if isJudgment(action.Kind) {
		return nil
	}
	_, found, err := heldBoundPark(ctx, queries, action.Epic, head)
	if err != nil || !found {
		return err
	}
	return RefusalError(fmt.Sprintf(
		"the park of epic %s is at the bound of its chain and waits on the planner's call: park_recurs, or park_new "+
			"before the actions that answer the park", action.Epic))
}
