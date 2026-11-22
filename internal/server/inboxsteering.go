package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// readSteered reads, once, the session ids that a list keeps unended: the steerer of each live orreryd epic that epics
// lists, and the conversation of each proposed contract that reader lists. The ids are session row ids. A nil epics
// lists no epics, and so does an epic store whose ListEpics is unimplemented, as listStored answers. Any other ListEpics
// failure answers unavailable. A failed contracts read answers as readClosureVerdicts maps store errors.
func readSteered(ctx context.Context, epics EpicStoreClient, reader *store.Reader) (map[string]bool, error) {
	summaries, err := listSteerEpics(ctx, epics)
	if err != nil {
		return nil, err
	}
	steered := map[string]bool{}
	for _, epic := range summaries {
		if storedPhaseLive(epic.GetPhase()) {
			steered[epic.GetSteerer()] = true
		}
	}
	contracts, err := reader.Contracts(ctx)
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read contracts")
	}
	for _, contract := range contracts {
		if contract.State == store.ContractProposed {
			steered[contract.Conversation] = true
		}
	}
	return steered, nil
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
