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

// NameUnhostedPaneRows writes name into the host of each pending notice, notice history row and pushed row whose pane
// is set and whose host is "". It refuses an empty name with an error and writes nothing. An unnamed pushed row whose
// pane and session a row under name already holds is dropped first, so the named row keeps that key.
func (s *Store) NameUnhostedPaneRows(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("name unhosted pane rows: host name is empty")
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if _, err := queries.NameUnhostedPaneNotices(ctx, name); err != nil {
			return fmt.Errorf("name unhosted pane notices %s: %w", name, err)
		}
		if _, err := queries.NameUnhostedPaneHistory(ctx, name); err != nil {
			return fmt.Errorf("name unhosted pane notice history %s: %w", name, err)
		}
		if err := queries.DeleteUnhostedPaneDuplicatePushes(ctx, name); err != nil {
			return fmt.Errorf("drop unhosted duplicate pushes of %s: %w", name, err)
		}
		if _, err := queries.NameUnhostedPanePushes(ctx, name); err != nil {
			return fmt.Errorf("name unhosted pane pushes %s: %w", name, err)
		}
		return nil
	})
}
