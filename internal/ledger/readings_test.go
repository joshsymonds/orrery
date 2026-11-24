package ledger

import (
	"database/sql"
	"testing"
)

// storedReading is one limit_readings row as it reads back from ledger.db.
type storedReading struct {
	Utilization sql.NullFloat64
	ResetAt     sql.NullInt64
	Status      string
	ObservedAt  int64
	RequestID   int64
	Dropped     int64
}

// Invented account, models, times and resets for the readings checks. Times are unix seconds, in increasing order.
const (
	readingsAccount       = "acct-readings-invented"
	carrierModel          = "model-carrier-invented"
	otherModel            = "model-other-invented"
	fableModel            = "model-fable-invented"
	readingsFirst   int64 = 1_800_000_100
	readingsSecond  int64 = 1_800_000_200
	readingsThird   int64 = 1_800_000_300
	fiveHourReset   int64 = 1_800_003_600
	sevenDayReset   int64 = 1_800_600_000
)

// claimOf returns a claim named name that reports a utilization, a reset and a status.
func claimOf(name string, utilization float64, reset int64, status string) Claim {
	return Claim{Name: name, Utilization: new(utilization), Reset: new(reset), Status: status}
}

// finishAt starts a request for readingsAccount and finishes it at completed, with model as its resolved model and
// claims as the answer it reported. It returns the request's id.
func finishAt(t *testing.T, l *Ledger, model string, completed int64, claims ...Claim) int64 {
	t.Helper()
	start := inventedStart()
	start.Account = readingsAccount
	id, err := l.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	finish := Finish{CompletedAt: completed, ResolvedModel: model, Claims: claims}
	if err := l.Finish(t.Context(), id, finish); err != nil {
		t.Fatalf("Finish of request %d at %d: %v", id, completed, err)
	}
	return id
}

// readReading reads the limit_readings row of readingsAccount and claim from db.
func readReading(t *testing.T, db *sql.DB, claim string) storedReading {
	t.Helper()
	var r storedReading
	err := db.QueryRowContext(t.Context(), `SELECT utilization, reset_at, status, observed_at, request_id, dropped
		FROM limit_readings WHERE account = ? AND claim = ?`, readingsAccount, claim).Scan(
		&r.Utilization, &r.ResetAt, &r.Status, &r.ObservedAt, &r.RequestID, &r.Dropped)
	if err != nil {
		t.Fatalf("read reading %q of account %q: %v", claim, readingsAccount, err)
	}
	return r
}

func TestLedgerReadings(t *testing.T) {
	t.Run("each finalized request upserts its account's latest reading per claim", func(t *testing.T) {
		l, db := openWriteLedger(t)
		first := finishAt(t, l, carrierModel, readingsFirst,
			claimOf("five_hour", 0.1, fiveHourReset, "allowed"),
			claimOf("seven_day", 0.2, sevenDayReset, "allowed"))

		wantFiveFirst := storedReading{Utilization: nullFloat(0.1), ResetAt: nullInt(fiveHourReset),
			Status: "allowed", ObservedAt: readingsFirst, RequestID: first}
		if got := readReading(t, db, "five_hour"); got != wantFiveFirst {
			t.Fatalf("five_hour after first request = %+v, want %+v", got, wantFiveFirst)
		}

		second := finishAt(t, l, carrierModel, readingsSecond,
			claimOf("five_hour", 0.5, fiveHourReset, "allowed"),
			claimOf("seven_day", 0.6, sevenDayReset, "rejected"))

		wantFiveSecond := storedReading{Utilization: nullFloat(0.5), ResetAt: nullInt(fiveHourReset),
			Status: "allowed", ObservedAt: readingsSecond, RequestID: second}
		if got := readReading(t, db, "five_hour"); got != wantFiveSecond {
			t.Fatalf("five_hour after second request = %+v, want %+v", got, wantFiveSecond)
		}
		wantWeekSecond := storedReading{Utilization: nullFloat(0.6), ResetAt: nullInt(sevenDayReset),
			Status: "rejected", ObservedAt: readingsSecond, RequestID: second}
		if got := readReading(t, db, "seven_day"); got != wantWeekSecond {
			t.Fatalf("seven_day after second request = %+v, want %+v", got, wantWeekSecond)
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM limit_readings`); n != 2 {
			t.Fatalf("limit_readings rows = %d, want one per claim, 2", n)
		}
	})

	t.Run("a claim its model carried is marked dropped when a later answer omits it, and clears when carried again",
		func(t *testing.T) {
			l, db := openWriteLedger(t)
			first := finishAt(t, l, carrierModel, readingsFirst,
				claimOf("five_hour", 0.1, fiveHourReset, "allowed"),
				claimOf("seven_day", 0.2, sevenDayReset, "allowed"))

			second := finishAt(t, l, carrierModel, readingsSecond,
				claimOf("seven_day", 0.3, sevenDayReset, "allowed"))

			wantFiveDropped := storedReading{Utilization: nullFloat(0.1), ResetAt: nullInt(fiveHourReset),
				Status: "allowed", ObservedAt: readingsFirst, RequestID: first, Dropped: 1}
			if got := readReading(t, db, "five_hour"); got != wantFiveDropped {
				t.Fatalf("five_hour after an answer omitting it = %+v, want %+v", got, wantFiveDropped)
			}
			wantWeekSecond := storedReading{Utilization: nullFloat(0.3), ResetAt: nullInt(sevenDayReset),
				Status: "allowed", ObservedAt: readingsSecond, RequestID: second}
			if got := readReading(t, db, "seven_day"); got != wantWeekSecond {
				t.Fatalf("seven_day after the second answer = %+v, want %+v", got, wantWeekSecond)
			}

			third := finishAt(t, l, carrierModel, readingsThird,
				claimOf("five_hour", 0.6, fiveHourReset, "allowed"),
				claimOf("seven_day", 0.4, sevenDayReset, "allowed"))

			wantFiveCarried := storedReading{Utilization: nullFloat(0.6), ResetAt: nullInt(fiveHourReset),
				Status: "allowed", ObservedAt: readingsThird, RequestID: third}
			if got := readReading(t, db, "five_hour"); got != wantFiveCarried {
				t.Fatalf("five_hour after carrying it again = %+v, want %+v", got, wantFiveCarried)
			}
		})

	t.Run("an answer from another model that omits 7d_oi leaves it unmarked", func(t *testing.T) {
		l, db := openWriteLedger(t)
		fable := finishAt(t, l, fableModel, readingsFirst,
			claimOf("five_hour", 0.1, fiveHourReset, "allowed"),
			claimOf("7d_oi", 0.2, sevenDayReset, "allowed"))

		finishAt(t, l, otherModel, readingsSecond,
			claimOf("five_hour", 0.3, fiveHourReset, "allowed"))

		wantOi := storedReading{Utilization: nullFloat(0.2), ResetAt: nullInt(sevenDayReset),
			Status: "allowed", ObservedAt: readingsFirst, RequestID: fable}
		if got := readReading(t, db, "7d_oi"); got != wantOi {
			t.Fatalf("7d_oi after another model omits it = %+v, want %+v", got, wantOi)
		}

		finishAt(t, l, fableModel, readingsThird,
			claimOf("five_hour", 0.4, fiveHourReset, "allowed"))

		wantOiDropped := wantOi
		wantOiDropped.Dropped = 1
		if got := readReading(t, db, "7d_oi"); got != wantOiDropped {
			t.Fatalf("7d_oi after the Fable model omits it = %+v, want %+v", got, wantOiDropped)
		}
	})

	t.Run("an older answer does not overwrite a newer reading", func(t *testing.T) {
		l, db := openWriteLedger(t)
		newer := finishAt(t, l, carrierModel, readingsSecond,
			claimOf("five_hour", 0.7, fiveHourReset, "allowed"),
			claimOf("seven_day", 0.3, sevenDayReset, "allowed"))

		finishAt(t, l, carrierModel, readingsFirst,
			claimOf("five_hour", 0.1, fiveHourReset, "rejected"))

		wantFive := storedReading{Utilization: nullFloat(0.7), ResetAt: nullInt(fiveHourReset),
			Status: "allowed", ObservedAt: readingsSecond, RequestID: newer}
		if got := readReading(t, db, "five_hour"); got != wantFive {
			t.Fatalf("five_hour after an older answer = %+v, want %+v", got, wantFive)
		}
		wantWeek := storedReading{Utilization: nullFloat(0.3), ResetAt: nullInt(sevenDayReset),
			Status: "allowed", ObservedAt: readingsSecond, RequestID: newer}
		if got := readReading(t, db, "seven_day"); got != wantWeek {
			t.Fatalf("seven_day after an older answer omits it = %+v, want %+v", got, wantWeek)
		}
	})
}
