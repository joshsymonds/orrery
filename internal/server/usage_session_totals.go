package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/store"
)

// SessionTotals answers the totals of the requests one orrery session made, for each model and agent type. The session,
// of any kind, joins the ledger through its Claude session: a session id no row holds answers not found, and a session
// with no Claude session answers no totals. A nil ledger or sessions reader answers unavailable.
func (u usageServer) SessionTotals(
	ctx context.Context,
	req *connect.Request[orreryv1.UsageSessionTotalsRequest],
) (*connect.Response[orreryv1.UsageSessionTotalsResponse], error) {
	if u.ledger == nil || u.sessions == nil {
		return nil, listError(connect.CodeUnavailable, usageUnavailable)
	}
	claudeSession, found, err := u.sessions.SessionClaude(ctx, req.Msg.GetSession())
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read sessions")
	}
	if !found {
		return nil, listError(connect.CodeNotFound, "session not found")
	}
	if claudeSession == "" {
		return connect.NewResponse(&orreryv1.UsageSessionTotalsResponse{}), nil
	}
	totals, err := u.ledger.SessionTotals(ctx, claudeSession)
	if err != nil {
		return nil, listError(connect.CodeInternal, "read session totals")
	}
	return connect.NewResponse(&orreryv1.UsageSessionTotalsResponse{Totals: usageTotalMessages(totals)}), nil
}

// usageTotalMessages converts each total to its proto message, in the order given. A per-kind list cost the total
// leaves nil is unset in its message.
func usageTotalMessages(totals []ledger.SessionTotal) []*orreryv1.UsageSessionTotal {
	messages := make([]*orreryv1.UsageSessionTotal, 0, len(totals))
	for _, total := range totals {
		messages = append(messages, &orreryv1.UsageSessionTotal{
			Model:                 total.Model,
			AgentType:             total.AgentType,
			Requests:              total.Requests,
			InputTokens:           total.InputTokens,
			OutputTokens:          total.OutputTokens,
			CacheReadTokens:       total.CacheReadTokens,
			CacheWriteTokens:      total.CacheWriteTokens,
			BilledCostNanoUsd:     total.BilledCostNanoUSD,
			ListCostNanoUsd:       total.ListCostNanoUSD,
			ListInputNanoUsd:      total.ListInputNanoUSD,
			ListOutputNanoUsd:     total.ListOutputNanoUSD,
			ListCacheReadNanoUsd:  total.ListCacheReadNanoUSD,
			ListCacheWriteNanoUsd: total.ListCacheWriteNanoUSD,
		})
	}
	return messages
}
