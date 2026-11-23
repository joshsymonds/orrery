package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

// sessionHostsOf returns the host of every sessions row of s, by row id.
func sessionHostsOf(t *testing.T, s *Store) map[string]string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), `SELECT id, host FROM sessions`)
	if err != nil {
		t.Fatalf("read session hosts: %v", err)
	}
	defer rows.Close()
	hosts := map[string]string{}
	for rows.Next() {
		var id, host string
		if err := rows.Scan(&id, &host); err != nil {
			t.Fatalf("scan session host: %v", err)
		}
		hosts[id] = host
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate session hosts: %v", err)
	}
	return hosts
}

// TestHostMigrationNamesEveryUnhostedSession stores a pane row and an engine row at migration 00094, before the session
// host column, then opens the store, which migrates it to 00095. Each row reads the empty host until NameUnhostedSessions
// names it. A row another host named keeps its host, and a second call names no row, as none has the empty host.
func TestHostMigrationNamesEveryUnhostedSession(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 94)
	seeds := []string{
		`INSERT INTO sessions (id, server, pane) VALUES ('pane-1', '', '%1')`,
		`INSERT INTO sessions (id, server, pane, kind, dir, state, changed)
			VALUES ('engine-1', '', '', 'engine', '/work/one', 'waiting', '2026-11-22T09:00:00Z')`,
	}
	for _, stmt := range seeds {
		if _, err := fixture.ExecContext(ctx, stmt); err != nil {
			fixture.Close()
			t.Fatalf("seed fixture: %v", err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 94: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	unnamed := map[string]string{"pane-1": "", "engine-1": ""}
	if got := sessionHostsOf(t, s); !reflect.DeepEqual(got, unnamed) {
		t.Fatalf("hosts after migration to 00095 = %v, want every row unnamed %v", got, unnamed)
	}

	if _, err := s.fixtureExec(ctx, `INSERT INTO sessions (id, server, pane, kind, dir, state, changed, host)
		VALUES ('remote-1', '', '', 'engine', '/work/two', 'waiting', '2026-11-22T09:01:00Z', 'desk')`); err != nil {
		t.Fatalf("insert remote row: %v", err)
	}
	named, err := s.NameUnhostedSessions(ctx, "desktop")
	if err != nil {
		t.Fatalf("NameUnhostedSessions: %v", err)
	}
	if named != 2 {
		t.Fatalf("NameUnhostedSessions named %d rows, want the 2 unhosted ones", named)
	}
	want := map[string]string{"pane-1": "desktop", "engine-1": "desktop", "remote-1": "desk"}
	if got := sessionHostsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts after naming = %v, want %v", got, want)
	}

	named, err = s.NameUnhostedSessions(ctx, "laptop")
	if err != nil {
		t.Fatalf("second NameUnhostedSessions: %v", err)
	}
	if named != 0 {
		t.Fatalf("second NameUnhostedSessions named %d rows, want none", named)
	}
	if got := sessionHostsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts after the second naming = %v, want %v", got, want)
	}
}

// TestHostMigrationRefusesEmptyName checks that NameUnhostedSessions refuses an empty name with an error and writes
// nothing: the rows that have the empty host still do.
func TestHostMigrationRefusesEmptyName(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if _, err := s.fixtureExec(ctx, `INSERT INTO sessions (id, server, pane) VALUES ('pane-1', '', '%1')`); err != nil {
		t.Fatalf("insert pane row: %v", err)
	}

	named, err := s.NameUnhostedSessions(ctx, "")
	if err == nil {
		t.Fatalf("NameUnhostedSessions with an empty name = %d rows named and no error, want an error", named)
	}
	if named != 0 {
		t.Fatalf("NameUnhostedSessions with an empty name named %d rows, want none", named)
	}
	if got, want := sessionHostsOf(t, s), map[string]string{"pane-1": ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts after the refused name = %v, want %v", got, want)
	}
}
