package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// EpicTotalsQuery totals the finalized requests of the Claude sessions its n ? marks name, for each Claude session,
// resolved model and agent type, in Claude session, model then agent type order. It selects the columns of
// SessionTotalsQuery with claude_session first. It reads only requests, and it finds the rows through the
// requests_claude_session index. A SUM skips a NULL cost, and a cost sum with no known cost is 0.
func EpicTotalsQuery(n int) string {
	var marks strings.Builder
	for i := range n {
		if i > 0 {
			marks.WriteString(", ")
		}
		marks.WriteString("?")
	}
	return fmt.Sprintf(`SELECT claude_session, resolved_model, agent_type, COUNT(*),
	SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens),
	COALESCE(SUM(billed_cost_nano_usd), 0), COALESCE(SUM(list_cost_nano_usd), 0)
	FROM requests
	WHERE claude_session IN (%s) AND completed_at IS NOT NULL
	GROUP BY claude_session, resolved_model, agent_type
	ORDER BY claude_session, resolved_model, agent_type`, marks.String())
}

// EpicTotals returns the totals of the finalized requests each of claudeSessions made, one for each model and agent
// type, as SessionTotals does, keyed by the Claude session. A Claude session with no finalized request has no entry.
// An empty id is skipped, and a duplicate is queried once. It returns nil, and no error, when no id is left or when the
// ledger file is absent, and in those cases it runs no query.
func (r *Reader) EpicTotals(ctx context.Context, claudeSessions []string) (map[string][]SessionTotal, error) {
	db, ok, err := r.open(ctx)
	if err != nil || !ok {
		return nil, err
	}
	ids := distinctClaudeSessions(claudeSessions)
	if len(ids) == 0 {
		return nil, errors.Join(db.Close())
	}
	totals, err := queryEpicTotals(ctx, db, ids)
	return totals, errors.Join(err, db.Close())
}

// distinctClaudeSessions returns the non-empty ids of claudeSessions, each once, in the order they first appear.
func distinctClaudeSessions(claudeSessions []string) []string {
	seen := make(map[string]bool, len(claudeSessions))
	ids := make([]string, 0, len(claudeSessions))
	for _, id := range claudeSessions {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// queryEpicTotals runs EpicTotalsQuery on db for claudeSessions, and scans each total it returns.
func queryEpicTotals(
	ctx context.Context,
	db *sql.DB,
	claudeSessions []string,
) (totals map[string][]SessionTotal, err error) {
	args := make([]any, len(claudeSessions))
	for i, id := range claudeSessions {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx, EpicTotalsQuery(len(claudeSessions)), args...)
	if err != nil {
		return nil, fmt.Errorf("query epic totals: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	totals, err = scanEpicTotals(rows)
	return totals, err
}

// scanEpicTotals scans each total rows holds into the entry of its Claude session, and returns the error that stopped
// the scan, if any.
func scanEpicTotals(rows *sql.Rows) (map[string][]SessionTotal, error) {
	totals := map[string][]SessionTotal{}
	for rows.Next() {
		var claudeSession string
		var total SessionTotal
		if err := rows.Scan(
			&claudeSession, &total.Model, &total.AgentType, &total.Requests, &total.InputTokens, &total.OutputTokens,
			&total.CacheReadTokens, &total.CacheWriteTokens, &total.BilledCostNanoUSD, &total.ListCostNanoUSD,
		); err != nil {
			return nil, fmt.Errorf("scan epic total: %w", err)
		}
		totals[claudeSession] = append(totals[claudeSession], total)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read epic totals: %w", err)
	}
	return totals, nil
}
