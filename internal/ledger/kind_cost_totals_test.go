package ledger

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The models, agent types and Claude sessions the kind-cost totals tests write. Each model and agent type pair is one
// total of kindClaude.
const (
	kindModelA = "model-kind-a"
	kindModelB = "model-kind-b"
	kindModelC = "model-kind-c"
	kindAgentX = "agent-kind-x"
	kindAgentY = "agent-kind-y"
	// kindClaude is the Claude session whose totals the tests read. kindOther is another, whose kinds must not reach
	// them.
	kindClaude = "claude-kind-invented"
	kindOther  = "claude-kind-other"
	// kindCompleted is the second every finalized request of the tests completes at.
	kindCompleted int64 = 1_800_000_099
)

// kindTotal is one total a read is expected to return: its model and agent type, its finalized requests, its list cost
// sum, and its four per-kind list costs as kindsText renders them.
type kindTotal struct {
	model, agentType string
	requests         int64
	listCost         int64
	kinds            string
}

// writeKindRequest starts a request of claude made as agentType and resolved to model, finishes it with listCost as its
// list cost, and returns its id. When kinds is not nil, it then sets the four per-kind list costs with an SQL UPDATE, in
// input, output, cache-read and cache-write order. A nil kinds sets none, so those columns stay NULL.
func writeKindRequest(t *testing.T, l *Ledger, claude, agentType, model string, listCost int64, kinds *[4]int64) int64 {
	t.Helper()
	start := inventedStart()
	start.ClaudeSession = claude
	start.AgentType = agentType
	id, err := l.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	finish := Finish{
		CompletedAt:       kindCompleted,
		ResolvedModel:     model,
		BilledCostNanoUSD: new(listCost),
		ListCostNanoUSD:   new(listCost),
	}
	if err := l.Finish(t.Context(), id, finish); err != nil {
		t.Fatalf("Finish of request %d: %v", id, err)
	}
	if kinds != nil {
		if _, err := l.db.ExecContext(t.Context(), `UPDATE requests SET list_input_nano_usd = ?,
			list_output_nano_usd = ?, list_cache_read_nano_usd = ?, list_cache_write_nano_usd = ? WHERE id = ?`,
			kinds[0], kinds[1], kinds[2], kinds[3], id); err != nil {
			t.Fatalf("set kind costs of request %d: %v", id, err)
		}
	}
	return id
}

// kindsText renders the four per-kind list costs of total, in input, output, cache-read and cache-write order. A kind
// the total leaves unknown reads NULL.
func kindsText(total SessionTotal) string {
	costs := []*int64{total.ListInputNanoUSD, total.ListOutputNanoUSD, total.ListCacheReadNanoUSD, total.ListCacheWriteNanoUSD}
	parts := make([]string, len(costs))
	for i, cost := range costs {
		parts[i] = "NULL"
		if cost != nil {
			parts[i] = strconv.FormatInt(*cost, 10)
		}
	}
	return strings.Join(parts, " ")
}

// requireKindTotal fails the test unless totals holds the total want describes, with the requests, list cost and
// per-kind list costs want gives.
func requireKindTotal(t *testing.T, totals []SessionTotal, want kindTotal) {
	t.Helper()
	for _, total := range totals {
		if total.Model != want.model || total.AgentType != want.agentType {
			continue
		}
		if total.Requests != want.requests || total.ListCostNanoUSD != want.listCost || kindsText(total) != want.kinds {
			t.Errorf("total %s/%s = %d requests, list %d, kinds %q; want %d requests, list %d, kinds %q",
				want.model, want.agentType, total.Requests, total.ListCostNanoUSD, kindsText(total),
				want.requests, want.listCost, want.kinds)
		}
		return
	}
	t.Fatalf("no total for %s/%s", want.model, want.agentType)
}

// sessionKindTotals returns the totals r reads for claude, and fails the test on error.
func sessionKindTotals(t *testing.T, r *Reader, claude string) []SessionTotal {
	t.Helper()
	totals, err := r.SessionTotals(t.Context(), claude)
	if err != nil {
		t.Fatalf("SessionTotals(%s): %v", claude, err)
	}
	return totals
}

// TestLedgerKindCostTotals checks that SessionTotals and EpicTotals sum each per-kind list cost over a total's finalized
// requests, and that a total with a request lacking a kind's cost reads that kind NULL. The requests are written
// through Start and Finish, and their kind columns by SQL UPDATE.
func TestLedgerKindCostTotals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	writeKindRequest(t, l, kindClaude, kindAgentX, kindModelA, 100, &[4]int64{10, 20, 30, 40})
	writeKindRequest(t, l, kindClaude, kindAgentX, kindModelA, 10, &[4]int64{1, 2, 3, 4})
	writeKindRequest(t, l, kindClaude, kindAgentY, kindModelA, 26, &[4]int64{5, 6, 7, 8})
	writeKindRequest(t, l, kindClaude, kindAgentX, kindModelB, 1000, &[4]int64{100, 200, 300, 400})
	writeKindRequest(t, l, kindClaude, kindAgentY, kindModelB, 500, &[4]int64{50, 60, 70, 80})
	writeKindRequest(t, l, kindClaude, kindAgentY, kindModelB, 50, nil)
	writeKindRequest(t, l, kindClaude, kindAgentX, kindModelC, 50, nil)
	writeKindRequest(t, l, kindOther, kindAgentX, kindModelA, 9999, &[4]int64{9999, 9999, 9999, 9999})
	inflight := inventedStart()
	inflight.ClaudeSession = kindClaude
	inflight.AgentType = kindAgentX
	if _, err := l.Start(t.Context(), inflight); err != nil {
		t.Fatalf("Start of in-flight request: %v", err)
	}
	reader := NewReader(path)

	t.Run("each kind sums per total by model and agent type", func(t *testing.T) {
		totals := sessionKindTotals(t, reader, kindClaude)
		requireKindTotal(t, totals, kindTotal{kindModelA, kindAgentX, 2, 110, "11 22 33 44"})
		requireKindTotal(t, totals, kindTotal{kindModelA, kindAgentY, 1, 26, "5 6 7 8"})
		requireKindTotal(t, totals, kindTotal{kindModelB, kindAgentX, 1, 1000, "100 200 300 400"})
	})
	t.Run("a total whose request has no kind cost reads each kind NULL", func(t *testing.T) {
		totals := sessionKindTotals(t, reader, kindClaude)
		requireKindTotal(t, totals, kindTotal{kindModelC, kindAgentX, 1, 50, "NULL NULL NULL NULL"})
	})
	t.Run("a mixed total reads each kind NULL", func(t *testing.T) {
		totals := sessionKindTotals(t, reader, kindClaude)
		requireKindTotal(t, totals, kindTotal{kindModelB, kindAgentY, 2, 550, "NULL NULL NULL NULL"})
	})
	t.Run("totals come in model then agent type order, and the in-flight request is not counted", func(t *testing.T) {
		totals := sessionKindTotals(t, reader, kindClaude)
		want := [][2]string{
			{kindModelA, kindAgentX}, {kindModelA, kindAgentY}, {kindModelB, kindAgentX},
			{kindModelB, kindAgentY}, {kindModelC, kindAgentX},
		}
		if len(totals) != len(want) {
			t.Fatalf("got %d totals, want %d: %+v", len(totals), len(want), totals)
		}
		for i, pair := range want {
			if totals[i].Model != pair[0] || totals[i].AgentType != pair[1] {
				t.Errorf("total %d = %s/%s, want %s/%s", i, totals[i].Model, totals[i].AgentType, pair[0], pair[1])
			}
		}
	})
	t.Run("EpicTotals sums each Claude session's kinds apart", func(t *testing.T) {
		epic, err := reader.EpicTotals(t.Context(), []string{kindClaude, kindOther})
		if err != nil {
			t.Fatalf("EpicTotals: %v", err)
		}
		requireKindTotal(t, epic[kindClaude], kindTotal{kindModelA, kindAgentX, 2, 110, "11 22 33 44"})
		requireKindTotal(t, epic[kindClaude], kindTotal{kindModelB, kindAgentY, 2, 550, "NULL NULL NULL NULL"})
		requireKindTotal(t, epic[kindOther], kindTotal{kindModelA, kindAgentX, 1, 9999, "9999 9999 9999 9999"})
	})
}
