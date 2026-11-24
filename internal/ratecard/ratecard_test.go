package ratecard

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/ledger"
)

// cardUsage is a request's tokens with every kind present, the 5-minute and 1-hour cache writes at different counts.
// Input is 1,001,234 tokens, so its cost includes a part that is not a whole million.
func cardUsage() Usage {
	return Usage{Input: 1_001_234, Output: 500_000, CacheRead: 2_000_000, CacheWrite5m: 300_000, CacheWrite1h: 200_000}
}

// openLedgerPair opens a ledger at a new path in a temp dir. It returns the Ledger, which writes, and a second handle
// on the same ledger.db, which reads the stored columns back.
func openLedgerPair(t *testing.T) (*ledger.Ledger, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := ledger.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open ledger.db for reading: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close ledger.db reader: %v", err)
		}
	})
	return l, db
}

// startRequest starts an invented request for model in l and returns its id.
func startRequest(t *testing.T, l *ledger.Ledger, model string) int64 {
	t.Helper()
	id, err := l.Start(t.Context(), ledger.Start{
		Account:        "acct-invented",
		Seat:           "seat-invented",
		Context:        "ctx-invented",
		RequestedModel: model,
		ClaudeSession:  "sess-invented",
		StartedAt:      1_700_000_000,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return id
}

// listColumns is the four per-kind list-cost columns and the list cost of one requests row, as they read back.
type listColumns struct {
	Input, Output, CacheRead, CacheWrite, Total sql.NullInt64
}

// readListColumns reads the list-cost columns of request id from db.
func readListColumns(t *testing.T, db *sql.DB, id int64) listColumns {
	t.Helper()
	var got listColumns
	err := db.QueryRowContext(t.Context(), `SELECT list_input_nano_usd, list_output_nano_usd,
		list_cache_read_nano_usd, list_cache_write_nano_usd, list_cost_nano_usd FROM requests WHERE id = ?`, id).
		Scan(&got.Input, &got.Output, &got.CacheRead, &got.CacheWrite, &got.Total)
	if err != nil {
		t.Fatalf("read list-cost columns of request %d: %v", id, err)
	}
	return got
}

// TestGatewayListPriceCardModel prices a card model's five token kinds into its four list costs. Each rate is the
// published one for claude-opus-5-5: $4 input, $20 output, $0.20 cache read, $5 5-minute and $8 1-hour cache write.
// The expected costs are worked by hand from those rates.
func TestGatewayListPriceCardModel(t *testing.T) {
	cost, ok := Price("claude-opus-5-5", cardUsage())
	if !ok {
		t.Fatal("Price(claude-opus-5-5): unknown, but the card names it")
	}
	// Input: 1,001,234 at $4/MTok is $4.004936. Output: 500,000 at $20/MTok is $10.
	// Cache read: 2,000,000 at $0.20/MTok is $0.40. Cache write: 300,000 at $5/MTok is $1.50 and 200,000 at $8/MTok is
	// $1.60, so $3.10.
	want := Cost{Input: 4_004_936_000, Output: 10_000_000_000, CacheRead: 400_000_000, CacheWrite: 3_100_000_000}
	if cost != want {
		t.Errorf("Price(claude-opus-5-5) = %+v, want %+v", cost, want)
	}
	if got := cost.Total(); got != 17_504_936_000 {
		t.Errorf("Total() = %d, want 17504936000", got)
	}
}

// TestGatewayListPriceStoredKinds finishes a card model's request in a temp ledger with its four kinds and reads the
// four columns back. The four stored kinds sum to the stored list_cost_nano_usd.
func TestGatewayListPriceStoredKinds(t *testing.T) {
	cost, ok := Price("claude-opus-5-5", cardUsage())
	if !ok {
		t.Fatal("Price(claude-opus-5-5): unknown, but the card names it")
	}
	total := cost.Total()
	l, db := openLedgerPair(t)
	id := startRequest(t, l, "claude-opus-5-5")
	usage := cardUsage()
	err := l.Finish(t.Context(), id, ledger.Finish{
		CompletedAt:               1_700_000_060,
		ResolvedModel:             "claude-opus-5-5",
		InputTokens:               usage.Input,
		OutputTokens:              usage.Output,
		CacheReadTokens:           usage.CacheRead,
		CacheWriteTokens:          usage.CacheWrite5m + usage.CacheWrite1h,
		ListCostNanoUSD:           &total,
		ListInputCostNanoUSD:      &cost.Input,
		ListOutputCostNanoUSD:     &cost.Output,
		ListCacheReadCostNanoUSD:  &cost.CacheRead,
		ListCacheWriteCostNanoUSD: &cost.CacheWrite,
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	got := readListColumns(t, db, id)
	wants := []struct {
		name string
		got  sql.NullInt64
		want int64
	}{
		{"list_input_nano_usd", got.Input, 4_004_936_000},
		{"list_output_nano_usd", got.Output, 10_000_000_000},
		{"list_cache_read_nano_usd", got.CacheRead, 400_000_000},
		{"list_cache_write_nano_usd", got.CacheWrite, 3_100_000_000},
		{"list_cost_nano_usd", got.Total, 17_504_936_000},
	}
	for _, w := range wants {
		if !w.got.Valid || w.got.Int64 != w.want {
			t.Errorf("%s = %+v, want %d", w.name, w.got, w.want)
		}
	}
	sum := got.Input.Int64 + got.Output.Int64 + got.CacheRead.Int64 + got.CacheWrite.Int64
	if sum != got.Total.Int64 {
		t.Errorf("the four kinds sum to %d, list_cost_nano_usd is %d; want equal", sum, got.Total.Int64)
	}
}

// TestGatewayListPriceUnknownModel prices a model the card does not name and finishes its request in a temp ledger.
// Both leave the request's list cost unknown.
func TestGatewayListPriceUnknownModel(t *testing.T) {
	const model = "claude-invented-9-9"
	cost, ok := Price(model, cardUsage())
	if ok {
		t.Errorf("Price(%s) reports a price, but the card does not name it", model)
	}
	if cost != (Cost{}) {
		t.Errorf("Price(%s) = %+v, want a zero Cost", model, cost)
	}
	l, db := openLedgerPair(t)
	id := startRequest(t, l, model)
	usage := cardUsage()
	err := l.Finish(t.Context(), id, ledger.Finish{
		CompletedAt:      1_700_000_060,
		ResolvedModel:    model,
		InputTokens:      usage.Input,
		OutputTokens:     usage.Output,
		CacheReadTokens:  usage.CacheRead,
		CacheWriteTokens: usage.CacheWrite5m + usage.CacheWrite1h,
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	got := readListColumns(t, db, id)
	for _, c := range []struct {
		name string
		got  sql.NullInt64
	}{
		{"list_input_nano_usd", got.Input},
		{"list_output_nano_usd", got.Output},
		{"list_cache_read_nano_usd", got.CacheRead},
		{"list_cache_write_nano_usd", got.CacheWrite},
		{"list_cost_nano_usd", got.Total},
	} {
		if c.got.Valid {
			t.Errorf("%s = %d, want NULL for a model the card does not name", c.name, c.got.Int64)
		}
	}
}
