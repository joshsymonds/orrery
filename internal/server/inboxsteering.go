package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// steerReads is what a list reads once about the sessions that steer or run a step. kept holds the ids a list keeps
// unended: the steerer of each live epic and the conversation of each proposed contract. busy holds the ids a done row
// lists as working: the steerer of each live epic and the caller of each running step.
type steerReads struct {
	kept map[string]bool
	busy map[string]bool
}

// readSteered reads, once, the session ids that a list keeps unended and the ones that list a done row as working. The
// steerer of each live orreryd epic that epics lists is in both sets. The conversation of each proposed contract that
// reader lists is in kept only, and the caller of each running step that steps lists is in busy only. The ids are
// session row ids. A nil epics lists no epics, and so does an epic store whose ListEpics is unimplemented, as
// listStored answers. Any other ListEpics failure answers unavailable. A nil steps lists no steps, and a failed
// ListSteps answers as readRunningCallers reads it. A failed contracts read answers as readClosureVerdicts maps store
// errors.
func readSteered(
	ctx context.Context, epics EpicStoreClient, steps StepClient, reader *store.Reader,
) (steerReads, error) {
	summaries, err := listSteerEpics(ctx, epics)
	if err != nil {
		return steerReads{}, err
	}
	reads := steerReads{kept: map[string]bool{}, busy: map[string]bool{}}
	for _, epic := range summaries {
		if storedPhaseLive(epic.GetPhase()) {
			reads.kept[epic.GetSteerer()] = true
			reads.busy[epic.GetSteerer()] = true
		}
	}
	callers, err := readRunningCallers(ctx, steps)
	if err != nil {
		return steerReads{}, err
	}
	for caller := range callers {
		reads.busy[caller] = true
	}
	contracts, err := reader.Contracts(ctx)
	if errors.Is(err, store.ErrUnavailable) {
		return steerReads{}, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return steerReads{}, listError(connect.CodeInternal, "read contracts")
	}
	for _, contract := range contracts {
		if contract.State == store.ContractProposed {
			reads.kept[contract.Conversation] = true
		}
	}
	return reads, nil
}

// readRunningCallers asks steps for the running steps in one call under ctx, with no retry, and returns the caller of
// each step that names one. A nil steps lists none. Any ListSteps failure answers unavailable with stepsUnavailable.
func readRunningCallers(ctx context.Context, steps StepClient) (map[string]bool, error) {
	callers := map[string]bool{}
	if steps == nil {
		return callers, nil
	}
	resp, err := steps.ListSteps(ctx, connect.NewRequest(&enginev1.ListStepsRequest{Running: true}))
	if err != nil {
		return nil, listError(connect.CodeUnavailable, stepsUnavailable)
	}
	for _, step := range resp.Msg.GetSteps() {
		if caller := step.GetCaller(); caller != "" {
			callers[caller] = true
		}
	}
	return callers, nil
}

// busyState returns the state a list answers for the row id, whose stored state is state. A done row that busy holds
// lists as working. Any other row keeps its stored state.
func busyState(busy map[string]bool, id, state string) string {
	if state == string(protocol.StateDone) && busy[id] {
		return string(protocol.StateWorking)
	}
	return state
}

// listSteerEpics asks epics for its epic summaries in one call under ctx, with no retry. A nil epics lists none, and so
// does an unimplemented answer. Any other failure answers unavailable with epicStoreUnavailable.
func listSteerEpics(ctx context.Context, epics EpicStoreClient) ([]*enginev1.EpicSummary, error) {
	if epics == nil {
		return nil, nil
	}
	resp, err := epics.ListEpics(ctx, connect.NewRequest(&enginev1.ListEpicsRequest{}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return nil, nil
		}
		return nil, listError(connect.CodeUnavailable, epicStoreUnavailable)
	}
	return resp.Msg.GetEpics(), nil
}

// steeredEnd returns the ended flag and ended time of the row id: ended and endedAt, unless steered holds id. A steered
// row is never ended, so the ended aging does not move it, and it returns false and 0.
func steeredEnd(steered map[string]bool, id string, ended bool, endedAt int64) (bool, int64) {
	if steered[id] {
		return false, 0
	}
	return ended, endedAt
}
