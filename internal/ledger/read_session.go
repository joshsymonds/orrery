package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SessionTotalsQuery totals the finalized requests of the Claude session its one ? names, for each resolved model and
// agent type, in model then agent type order. It reads only requests, and it finds the session's rows through the
// requests_claude_session index. A SUM skips a NULL cost, and a cost sum with no known cost is 0. Each per-kind list
// cost is the sum of its column when every finalized request of the total has it, and NULL when any lacks it.
const SessionTotalsQuery = `SELECT resolved_model, agent_type, COUNT(*),
	SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens),
	COALESCE(SUM(billed_cost_nano_usd), 0), COALESCE(SUM(list_cost_nano_usd), 0),
	CASE WHEN COUNT(list_input_nano_usd) = COUNT(*) THEN SUM(list_input_nano_usd) END,
	CASE WHEN COUNT(list_output_nano_usd) = COUNT(*) THEN SUM(list_output_nano_usd) END,
	CASE WHEN COUNT(list_cache_read_nano_usd) = COUNT(*) THEN SUM(list_cache_read_nano_usd) END,
	CASE WHEN COUNT(list_cache_write_nano_usd) = COUNT(*) THEN SUM(list_cache_write_nano_usd) END
	FROM requests
	WHERE claude_session = ? AND completed_at IS NOT NULL
	GROUP BY resolved_model, agent_type
	ORDER BY resolved_model, agent_type`

// SessionTotal is the requests and tokens one session made for one model and agent type. The cost fields are in nano
// US dollars. Each ListInput, ListOutput, ListCacheRead and ListCacheWrite cost is the list cost of that token kind,
// and it is nil when any finalized request of the total has no cost for that kind.
type SessionTotal struct {
	Model                 string
	AgentType             string
	Requests              int64
	InputTokens           int64
	OutputTokens          int64
	CacheReadTokens       int64
	CacheWriteTokens      int64
	BilledCostNanoUSD     int64
	ListCostNanoUSD       int64
	ListInputNanoUSD      *int64
	ListOutputNanoUSD     *int64
	ListCacheReadNanoUSD  *int64
	ListCacheWriteNanoUSD *int64
}

// SessionTotals returns the totals of the finalized requests the Claude session made, one for each model and agent
// type. An in-flight request is not counted. It returns nil, and no error, when the ledger file is absent.
func (r *Reader) SessionTotals(ctx context.Context, claudeSession string) ([]SessionTotal, error) {
	db, ok, err := r.open(ctx)
	if err != nil || !ok {
		return nil, err
	}
	totals, err := querySessionTotals(ctx, db, claudeSession)
	return totals, errors.Join(err, db.Close())
}

// querySessionTotals runs SessionTotalsQuery on db for claudeSession, and scans each total it returns.
func querySessionTotals(ctx context.Context, db *sql.DB, claudeSession string) (totals []SessionTotal, err error) {
	rows, err := db.QueryContext(ctx, SessionTotalsQuery, claudeSession)
	if err != nil {
		return nil, fmt.Errorf("query session totals: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	totals, err = scanSessionTotals(rows)
	return totals, err
}

// scanSessionTotals scans each total rows holds, and returns the error that stopped the scan, if any.
func scanSessionTotals(rows *sql.Rows) ([]SessionTotal, error) {
	totals := []SessionTotal{}
	for rows.Next() {
		var total SessionTotal
		if err := rows.Scan(
			&total.Model,
			&total.AgentType,
			&total.Requests,
			&total.InputTokens,
			&total.OutputTokens,
			&total.CacheReadTokens,
			&total.CacheWriteTokens,
			&total.BilledCostNanoUSD,
			&total.ListCostNanoUSD,
			&total.ListInputNanoUSD,
			&total.ListOutputNanoUSD,
			&total.ListCacheReadNanoUSD,
			&total.ListCacheWriteNanoUSD,
		); err != nil {
			return nil, fmt.Errorf("scan session total: %w", err)
		}
		totals = append(totals, total)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read session totals: %w", err)
	}
	return totals, nil
}
