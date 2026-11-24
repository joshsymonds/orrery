package server

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// EpicTotals answers the totals of each session of an epic: its step sessions, and its steerer or, for a record epic,
// the callers of its steps, each marked shared. Each session joins the ledger through its Claude session, and the
// ledger is read once for all of them. A session with no Claude session, or with no finalized request, is left out, so
// an epic with no such sessions, and an unknown epic, answer none. The sessions are ordered by session id. A nil ledger
// or sessions reader answers unavailable.
func (u usageServer) EpicTotals(
	ctx context.Context,
	req *connect.Request[orreryv1.UsageEpicTotalsRequest],
) (*connect.Response[orreryv1.UsageEpicTotalsResponse], error) {
	if u.ledger == nil || u.sessions == nil {
		return nil, listError(connect.CodeUnavailable, usageUnavailable)
	}
	sessions, err := u.sessions.EpicUsageSessions(ctx, req.Msg.GetEpic())
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read epic sessions")
	}
	claudeSessions := make([]string, 0, len(sessions))
	for _, session := range sessions {
		claudeSessions = append(claudeSessions, session.ClaudeSession)
	}
	totals, err := u.ledger.EpicTotals(ctx, claudeSessions)
	if err != nil {
		return nil, listError(connect.CodeInternal, "read epic totals")
	}
	answered := make([]*orreryv1.UsageEpicSession, 0, len(sessions))
	for _, session := range sessions {
		if len(totals[session.ClaudeSession]) == 0 {
			continue
		}
		answered = append(answered, &orreryv1.UsageEpicSession{
			Session: session.Session,
			Shared:  session.Shared,
			Totals:  usageTotalMessages(totals[session.ClaudeSession]),
		})
	}
	slices.SortFunc(answered, func(a, b *orreryv1.UsageEpicSession) int {
		return cmp.Compare(a.GetSession(), b.GetSession())
	})
	return connect.NewResponse(&orreryv1.UsageEpicTotalsResponse{Sessions: answered}), nil
}
