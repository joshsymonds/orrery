package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// NameUnhostedSessions writes name into the host of each session row whose host is "" and returns how many rows it
// named. It refuses an empty name with an error and writes nothing.
func (s *Store) NameUnhostedSessions(ctx context.Context, name string) (int64, error) {
	if name == "" {
		return 0, errors.New("name unhosted sessions: host name is empty")
	}
	var named int64
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.NameUnhostedSessions(ctx, name)
		if err != nil {
			return fmt.Errorf("name unhosted sessions %s: %w", name, err)
		}
		named = rows
		return nil
	})
	if err != nil {
		return 0, err
	}
	return named, nil
}
