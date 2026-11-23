package server

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/store"
)

// endedAge is how long an ended conversation stays listed after it ended before it archives on its own, whether or not
// the owner has seen it. A final epic ends at its change time, so it ages by endedAge too (see epicAged).
const endedAge = 24 * time.Hour

// readClosureVerdicts reads the newest closure verdict of every session through reader into a map by session id. A nil
// reader has no verdicts, so no row is aged by one. A failed read answers unavailable for store.ErrUnavailable and
// internal otherwise, with fixed messages.
func readClosureVerdicts(ctx context.Context, reader *store.Reader) (map[string]store.ClosureVerdict, error) {
	verdicts := map[string]store.ClosureVerdict{}
	if reader == nil {
		return verdicts, nil
	}
	rows, err := reader.ClosureVerdicts(ctx)
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read closure verdicts")
	}
	for _, row := range rows {
		verdicts[row.Session] = row
	}
	return verdicts, nil
}

// endedRow reports whether a conversation row is ended, and the unix seconds it ended at. A row is ended when stopped
// is set, as a headless row in state stopped is, or when its newest closure verdict v is closed and was written for
// its current change time, changed in unix seconds, whether or not the owner has seen the row. ok is whether the row
// has a verdict. A row with change time 0 is never ended by a verdict. An ended row's time is changed, and an unended
// row's is 0. A row the owner unarchived at or after its change time, which mark records, is unended whatever its state
// or verdict, until it changes again (see unarchivedAtChange).
func endedRow(stopped bool, v store.ClosureVerdict, ok bool, changed int64, mark store.ArchiveMark) (bool, int64) {
	if unarchivedAtChange(mark, changed) {
		return false, 0
	}
	closedAtChange := ok && v.Closed && changed != 0 && v.Changed == changed
	if !stopped && !closedAtChange {
		return false, 0
	}
	return true, changed
}

// endedAged reports whether a row that is ended, at endedAt unix seconds, ages out at now: it ended at least endedAge
// before now. An unended row never ages by this rule.
func endedAged(ended bool, endedAt int64, now time.Time) bool {
	return ended && endedAt <= now.Add(-endedAge).Unix()
}
