package server_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/ledger"
)

// Invented accounts, claims, model and times for TestUsageReadings. Times are unix seconds, in increasing order.
const (
	readingsAccountA           = "synthetic-account-a"
	readingsAccountB           = "synthetic-account-b"
	readingsModel              = "synthetic-model-readings"
	readingsClaimFive          = "five_hour"
	readingsClaimWeek          = "seven_day"
	readingsClaimSonnet        = "seven_day_sonnet"
	readingsClaimOverage       = "overage"
	readingsFirst        int64 = 1_800_000_100
	readingsSecond       int64 = 1_800_000_200
	readingsFiveReset    int64 = 1_800_003_600
	readingsWeekReset    int64 = 1_800_600_000
)

// readingRequest is one invented request the readings ledger holds: the account it is made for, the time it completes
// and the claims it reports.
type readingRequest struct {
	account   string
	completed int64
	claims    []ledger.Claim
}

// readingClaim returns a claim named name that reports a utilization and a reset, with status allowed.
func readingClaim(name string, utilization float64, reset int64) ledger.Claim {
	return ledger.Claim{Name: name, Utilization: &utilization, Reset: &reset, Status: "allowed"}
}

// statusClaim returns a claim named name that reports only a status: no utilization and no reset.
func statusClaim(name, status string) ledger.Claim {
	return ledger.Claim{Name: name, Status: status}
}

// readingsRequests returns the invented requests in completion order. Account A's first request reports three claims,
// including seven_day_sonnet; its second reports five_hour, seven_day and a status-only overage claim, and omits
// seven_day_sonnet, which its model has carried, so that reading is dropped. Account B's one request reports five_hour.
func readingsRequests() []readingRequest {
	return []readingRequest{
		{account: readingsAccountA, completed: readingsFirst, claims: []ledger.Claim{
			readingClaim(readingsClaimFive, 0.1, readingsFiveReset),
			readingClaim(readingsClaimWeek, 0.2, readingsWeekReset),
			readingClaim(readingsClaimSonnet, 0.3, readingsWeekReset),
		}},
		{account: readingsAccountA, completed: readingsSecond, claims: []ledger.Claim{
			readingClaim(readingsClaimFive, 0.5, readingsFiveReset),
			readingClaim(readingsClaimWeek, 0.6, readingsWeekReset),
			statusClaim(readingsClaimOverage, "rejected"),
		}},
		{account: readingsAccountB, completed: readingsFirst, claims: []ledger.Claim{
			readingClaim(readingsClaimFive, 0.4, readingsFiveReset),
		}},
	}
}

// wantReadings returns the readings TestUsageReadings expects, in account then claim order. Each account's five_hour,
// seven_day and overage readings are from its second request; seven_day_sonnet is from account A's first request and
// is dropped.
func wantReadings() []*orreryv1.UsageReading {
	return []*orreryv1.UsageReading{
		{Account: readingsAccountA, Claim: readingsClaimFive, Utilization: new(0.5),
			Reset_: new(readingsFiveReset), Status: "allowed", Observed: readingsSecond},
		{Account: readingsAccountA, Claim: readingsClaimOverage, Status: "rejected", Observed: readingsSecond},
		{Account: readingsAccountA, Claim: readingsClaimWeek, Utilization: new(0.6),
			Reset_: new(readingsWeekReset), Status: "allowed", Observed: readingsSecond},
		{Account: readingsAccountA, Claim: readingsClaimSonnet, Utilization: new(0.3),
			Reset_: new(readingsWeekReset), Status: "allowed", Observed: readingsFirst, Dropped: true},
		{Account: readingsAccountB, Claim: readingsClaimFive, Utilization: new(0.4),
			Reset_: new(readingsFiveReset), Status: "allowed", Observed: readingsFirst},
	}
}

// seedReadingsLedger writes readingsRequests into a new ledger at path through its writer, and closes the writer.
func seedReadingsLedger(t *testing.T, path string) {
	t.Helper()
	ctx := t.Context()
	l, err := ledger.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, r := range readingsRequests() {
		id, err := l.Start(ctx, ledger.Start{
			Account:        r.account,
			Seat:           "synthetic-seat",
			Context:        "synthetic-context",
			RequestedModel: readingsModel,
			ClaudeSession:  "synthetic-session",
			AgentID:        "synthetic-agent",
			AgentType:      "synthetic-type",
			StartedAt:      r.completed - 5,
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		finish := ledger.Finish{CompletedAt: r.completed, ResolvedModel: readingsModel, Claims: r.claims}
		if err := l.Finish(ctx, id, finish); err != nil {
			t.Fatalf("Finish: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestUsageReadings(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "ledger.db")
	seedReadingsLedger(t, path)
	usage := usageSpendClient(t, newSpendHandler(t, ledger.NewReader(path)))

	t.Run("latest reading per account and claim", func(t *testing.T) {
		got, err := usage.Readings(ctx, connect.NewRequest(&orreryv1.UsageReadingsRequest{}))
		if err != nil {
			t.Fatalf("Readings: %v", err)
		}
		want := wantReadings()
		readings := got.Msg.GetReadings()
		if len(readings) != len(want) {
			t.Fatalf("Readings returned %d readings, want %d: %v", len(readings), len(want), readings)
		}
		for i, w := range want {
			if !proto.Equal(readings[i], w) {
				t.Errorf("Readings[%d] = %v, want %v", i, readings[i], w)
			}
		}
	})

	t.Run("query plan", func(t *testing.T) {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open ledger for EXPLAIN QUERY PLAN: %v", err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Errorf("close ledger: %v", err)
			}
		})
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+ledger.ReadingsQuery)
		if err != nil {
			t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
		}
		defer func() {
			if err := rows.Close(); err != nil {
				t.Errorf("close EXPLAIN QUERY PLAN rows: %v", err)
			}
		}()
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatalf("scan EXPLAIN QUERY PLAN: %v", err)
			}
			t.Logf("plan: %s", detail)
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read EXPLAIN QUERY PLAN: %v", err)
		}
		if len(plan) == 0 {
			t.Fatal("EXPLAIN QUERY PLAN returned no rows")
		}
		var readsReadings bool
		for _, detail := range plan {
			if fullScanOfRequests(detail) {
				t.Errorf("query plan scans requests in full: %q", detail)
			}
			if strings.Contains(detail, "limit_readings") {
				readsReadings = true
			}
		}
		if !readsReadings {
			t.Errorf("query plan does not read limit_readings: %v", plan)
		}
	})

	t.Run("no ledger", func(t *testing.T) {
		unavailable := usageSpendClient(t, newSpendHandler(t, nil))
		_, err := unavailable.Readings(ctx, connect.NewRequest(&orreryv1.UsageReadingsRequest{}))
		if code := connect.CodeOf(err); code != connect.CodeUnavailable {
			t.Errorf("Readings with no ledger: code %v, want %v (err %v)", code, connect.CodeUnavailable, err)
		}
	})

	t.Run("unreadable ledger", func(t *testing.T) {
		corrupt := filepath.Join(t.TempDir(), "ledger.db")
		if err := os.WriteFile(corrupt, []byte("not a ledger database"), 0o600); err != nil {
			t.Fatalf("write corrupt ledger: %v", err)
		}
		unreadable := usageSpendClient(t, newSpendHandler(t, ledger.NewReader(corrupt)))
		_, err := unreadable.Readings(ctx, connect.NewRequest(&orreryv1.UsageReadingsRequest{}))
		if code := connect.CodeOf(err); code != connect.CodeUnavailable {
			t.Errorf("Readings of an unreadable ledger: code %v, want %v (err %v)", code, connect.CodeUnavailable, err)
		}
	})
}
