package ledger

import "context"

// Claim is one usage limit claim a request reported. A nil Utilization or Reset is a reading that gives none.
type Claim struct {
	Name        string
	Utilization *float64
	Reset       *int64
	Status      string
}

// Start is a request as it starts, before its tokens and cost are known.
type Start struct {
	Account, CallerAccount, Seat, Context, RequestedModel, ClaudeSession, AgentID, AgentType string
	StartedAt                                                                                int64
}

// Finish is a request as it completes. A nil BilledCostNanoUSD or ListCostNanoUSD is an unknown cost.
type Finish struct {
	CompletedAt       int64
	ResolvedModel     string
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	BilledCostNanoUSD *int64
	ListCostNanoUSD   *int64
	Claims            []Claim
}

// Start records the start of a request and returns the id the request is known by.
func (l *Ledger) Start(_ context.Context, _ Start) (int64, error) {
	return 0, errNotImplemented
}

// Finish records the completion of the request id, with its tokens, its costs and the claims it reported.
func (l *Ledger) Finish(_ context.Context, _ int64, _ Finish) error {
	return errNotImplemented
}
