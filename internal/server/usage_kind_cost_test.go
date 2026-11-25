package server_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

// kindFinish is the finish of a request that completed as model, with listCost as its billed and list cost. Its
// per-kind list costs are set, or left NULL, by finishKindRequest.
func kindFinish(model string, listCost int64) ledger.Finish {
	return ledger.Finish{
		CompletedAt: usageDone, ResolvedModel: model,
		BilledCostNanoUSD: new(listCost), ListCostNanoUSD: new(listCost),
	}
}

// finishKindRequest writes the start of a request in l, finishes it with finish, and then sets the four per-kind list
// costs of the request with an SQL UPDATE through raw, a handle on the same ledger file, in input, output, cache-read
// and cache-write order. A nil kinds sets none. It fails the test on error.
func finishKindRequest(
	t *testing.T,
	l *ledger.Ledger,
	raw *sql.DB,
	start ledger.Start,
	finish ledger.Finish,
	kinds *[4]int64,
) {
	t.Helper()
	id := startRequest(t, l, start)
	if err := l.Finish(t.Context(), id, finish); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if kinds == nil {
		return
	}
	if _, err := raw.ExecContext(t.Context(), `UPDATE requests SET list_input_nano_usd = ?,
		list_output_nano_usd = ?, list_cache_read_nano_usd = ?, list_cache_write_nano_usd = ? WHERE id = ?`,
		kinds[0], kinds[1], kinds[2], kinds[3], id); err != nil {
		t.Fatalf("set kind costs of request %d: %v", id, err)
	}
}

// seedKindLedger writes the ledger the kind-cost tests read beside the store at storePath, and returns its path.
// usageClaudeStepA's requests make two totals: one of usageModelOne and usageAgentOne whose four kinds are all known,
// and one of usageModelTwo and usageAgentTwo that has no kind costs. usageClaudeStepB's two requests make one total, a
// mixed one: one request has its kinds and the other has none, so the total is unknown for each kind.
func seedKindLedger(t *testing.T, storePath string) string {
	t.Helper()
	path := ledger.Path(storePath)
	l, err := ledger.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open ledger for kind costs: %v", err)
	}
	finishKindRequest(t, l, raw, usageStart(usageClaudeStepA, usageAgentOne),
		kindFinish(usageModelOne, 1000), &[4]int64{100, 200, 300, 400})
	finishKindRequest(t, l, raw, usageStart(usageClaudeStepA, usageAgentOne),
		kindFinish(usageModelOne, 10), &[4]int64{1, 2, 3, 4})
	finishKindRequest(t, l, raw, usageStart(usageClaudeStepA, usageAgentTwo),
		kindFinish(usageModelTwo, 40), nil)
	finishKindRequest(t, l, raw, usageStart(usageClaudeStepB, usageAgentOne),
		kindFinish(usageModelOne, 30), &[4]int64{7, 8, 9, 10})
	finishKindRequest(t, l, raw, usageStart(usageClaudeStepB, usageAgentOne),
		kindFinish(usageModelOne, 20), nil)
	if err = raw.Close(); err != nil {
		t.Fatalf("Close ledger handle: %v", err)
	}
	if err = l.Close(); err != nil {
		t.Fatalf("Close ledger: %v", err)
	}
	return path
}

// wantKindStepA are the totals of usageStepA. The first sums each kind of its two requests of usageModelOne and
// usageAgentOne. The second is the request of usageModelTwo and usageAgentTwo, which has no kind costs, so its kinds are
// unset.
func wantKindStepA() []*orreryv1.UsageSessionTotal {
	return []*orreryv1.UsageSessionTotal{
		{
			Model: usageModelOne, AgentType: usageAgentOne, Requests: 2,
			BilledCostNanoUsd: 1010, ListCostNanoUsd: 1010,
			ListInputNanoUsd: new(int64(101)), ListOutputNanoUsd: new(int64(202)),
			ListCacheReadNanoUsd: new(int64(303)), ListCacheWriteNanoUsd: new(int64(404)),
		},
		{Model: usageModelTwo, AgentType: usageAgentTwo, Requests: 1, BilledCostNanoUsd: 40, ListCostNanoUsd: 40},
	}
}

// wantKindStepB is the total of usageStepB. It counts both its requests, and its list cost sum counts the one with a
// cost, but its kinds are unset, since one of its requests has none.
func wantKindStepB() []*orreryv1.UsageSessionTotal {
	return []*orreryv1.UsageSessionTotal{
		{Model: usageModelOne, AgentType: usageAgentOne, Requests: 2, BilledCostNanoUsd: 50, ListCostNanoUsd: 50},
	}
}

// wantKindEpic are the sessions of usageEpicSteered that have totals. The steerer, the read-only session and the stray
// have no requests in the ledger, so they are absent.
func wantKindEpic() []*orreryv1.UsageEpicSession {
	return []*orreryv1.UsageEpicSession{
		{Session: usageStepA, Totals: wantKindStepA()},
		{Session: usageStepB, Totals: wantKindStepB()},
	}
}

// TestUsageLedgerKindCost checks that the Usage API answers each session's and each epic session's per-kind list costs
// as set fields when every request of the total has the kind, and leaves them unset for a total with an unknown kind.
func TestUsageLedgerKindCost(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "orrery.db")
	seedEpicStore(t, storePath)
	ledgerPath := seedKindLedger(t, storePath)
	client := usageClient(t, server.Config{
		Sessions: store.NewReader(storePath),
		Ledger:   ledger.NewReader(ledgerPath),
	})

	t.Run("SessionTotals answers the four summed kinds as set fields", func(t *testing.T) {
		requireTotals(t, sessionTotals(t, client, usageStepA), wantKindStepA())
	})
	t.Run("SessionTotals leaves the kinds of a mixed total unset", func(t *testing.T) {
		requireTotals(t, sessionTotals(t, client, usageStepB), wantKindStepB())
	})
	t.Run("EpicTotals answers each session's kinds, set where summed and unset where unknown", func(t *testing.T) {
		requireEpicSessions(t, epicTotals(t, client, usageEpicSteered), wantKindEpic())
	})
}
