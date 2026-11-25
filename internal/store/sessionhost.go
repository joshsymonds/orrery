package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// SessionHost returns the host column of the sessions row with id, and true. It reads on the read handle. It returns
// "", false, and no error, when no row has the id.
func (s *Store) SessionHost(ctx context.Context, id string) (string, bool, error) {
	row, err := sqlcgen.New(s.read).GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read host of session %s: %w", id, err)
	}
	return row.Host, true, nil
}
