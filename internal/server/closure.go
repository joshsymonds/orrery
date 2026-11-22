package server

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/store"
)

// closureUnseenAge is how long a closed conversation the owner has not seen stays listed before it archives on its own.
const closureUnseenAge = time.Hour

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

// closureAged reports whether a row that a verdict v is read for ages out at now. ok is whether the row has a verdict,
// changed is the row's change time and seen its seen time, both in unix seconds. The row ages out when its verdict is
// closed and was written for its current change time, and either the owner has seen the row since it changed or it has
// gone unchanged for closureUnseenAge. A verdict for an earlier change time never ages a row, so a row that has changed
// since its closed turn stays listed.
func closureAged(v store.ClosureVerdict, ok bool, changed, seen int64, now time.Time) bool {
	if !ok || !v.Closed || v.Changed != changed {
		return false
	}
	return seen >= changed || changed <= now.Add(-closureUnseenAge).Unix()
}

// endedRow reports whether a conversation row is ended, and the unix seconds it ended at. A row is ended when stopped
// is set, as a headless row in state stopped is, or when its newest closure verdict v is closed and was written for
// its current change time, changed in unix seconds, whether or not the owner has seen the row. ok is whether the row
// has a verdict. A row with change time 0 is never ended by a verdict. An ended row's time is changed, and an unended
// row's is 0.
func endedRow(stopped bool, v store.ClosureVerdict, ok bool, changed int64) (bool, int64) {
	closedAtChange := ok && v.Closed && changed != 0 && v.Changed == changed
	if !stopped && !closedAtChange {
		return false, 0
	}
	return true, changed
}
