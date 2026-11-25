package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// BoundChain is the chain of the planner park an epic holds at the bound of its chain, which waits on the planner's call:
// whether the park is the same problem as an earlier park of the chain, back with no progress, or a new one. Seq and Park
// are the seq and reason of the held park. Earlier holds the reason of each earlier park of the chain, in park order.
// Actions holds one line for each planner action taken or refused on the chain, as actionLines gives it. Changes holds
// what moved in the epic since the chain's previous park, as changeLines gives it. The zero BoundChain is no such park.
type BoundChain struct {
	Seq     int64
	Park    string
	Earlier []string
	Actions []string
	Changes []string
}

// BoundChain returns the chain of the park epic holds at the bound of its chain and waiting on the planner's call, or the
// zero BoundChain when it holds none. An unknown epic is ErrNoEpic.
func (s *Store) BoundChain(ctx context.Context, epic string) (BoundChain, error) {
	queries := sqlcgen.New(s.read)
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return BoundChain{}, err
	}
	held, found, err := heldBoundPark(ctx, queries, epic, head)
	if err != nil || !found {
		return BoundChain{}, err
	}
	parks, err := queries.ListEpicChainParks(ctx, sqlcgen.ListEpicChainParksParams{Epic: epic, Chain: held.Chain})
	if err != nil {
		return BoundChain{}, fmt.Errorf("list parks of chain %d of epic %s: %w", held.Chain, epic, err)
	}
	chain := BoundChain{Seq: held.Seq, Park: held.Reason}
	var previous sqlcgen.EpicPark
	for _, park := range parks {
		if park.Seq < held.Seq {
			chain.Earlier = append(chain.Earlier, park.Reason)
			previous = park
		}
	}
	actions, err := chainActions(ctx, queries, epic, held.Chain)
	if err != nil {
		return BoundChain{}, err
	}
	if chain.Actions, err = actionLines(actions); err != nil {
		return BoundChain{}, err
	}
	if previous.Seq != 0 {
		if chain.Changes, err = changeLines(ctx, queries, epic, previous, held); err != nil {
			return BoundChain{}, err
		}
	}
	return chain, nil
}

// changeLines returns what moved in epic between park previous and park held, as the store records it: the stored base
// and the candidate each park recorded, then the node runs that ended passed after previous was stored, by path, then
// the release steps that passed after it, by position and attempt. A head that did not move is named as unchanged, and an
// empty one as none.
func changeLines(
	ctx context.Context, queries *sqlcgen.Queries, epic string, previous, held sqlcgen.EpicPark,
) ([]string, error) {
	passed, err := queries.ListEpicNodeRunsPassedAfter(ctx, sqlcgen.ListEpicNodeRunsPassedAfterParams{
		Epic: epic, After: previous.Created,
	})
	if err != nil {
		return nil, fmt.Errorf("list node runs of epic %s passed since park %d: %w", epic, previous.Seq, err)
	}
	steps, err := queries.ListEpicReleaseStepsPassedAfter(ctx, sqlcgen.ListEpicReleaseStepsPassedAfterParams{
		Epic: epic, After: previous.Created,
	})
	if err != nil {
		return nil, fmt.Errorf("list release steps of epic %s passed since park %d: %w", epic, previous.Seq, err)
	}
	released := make([]string, 0, len(steps))
	for _, step := range steps {
		released = append(released, fmt.Sprintf("step %d at attempt %d", step.Position, step.Attempt))
	}
	return []string{
		headLine("stored base", previous.StoredBase, held.StoredBase),
		headLine("candidate", previous.Candidate, held.Candidate),
		"node runs passed since: " + joinedOrNone(passed),
		"release steps passed since: " + joinedOrNone(released),
	}, nil
}

// headLine returns the line that names head as it was at the earlier park, then, and at the held park, now: unchanged
// when the two are the same, and each as none when it is empty.
func headLine(head, then, now string) string {
	if then == now {
		return fmt.Sprintf("%s: unchanged at %s", head, commitOrNone(now))
	}
	return fmt.Sprintf("%s: %s then, %s now", head, commitOrNone(then), commitOrNone(now))
}

// commitOrNone returns commit, or none when it is empty.
func commitOrNone(commit string) string {
	if commit == "" {
		return "none"
	}
	return commit
}

// joinedOrNone returns items joined by commas, or none when there are none.
func joinedOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
