package store

import (
	"testing"
)

// nodeCheckKeyLinesDownAndUp is the walk's check of 00120_node_check_key_lines.sql: its Down drops the key_lines column
// of epic_node_checks and keeps each check, and its Up gives a check stored before it no key lines.
func nodeCheckKeyLinesDownAndUp() migrationCheck {
	return migrationCheck{
		seed: func(t *testing.T, s *Store) {
			t.Helper()
			if _, err := s.db.DB.Exec(`INSERT INTO epic_node_checks
				(id, epic, path, revision, argv, limit_seconds, needs, state, exit, stdout, created, ended)
				VALUES ('c1', 'repo/epic', 'lint', 'rev-1', '["just","check"]', 60, '[]', 'ended', 1, 'FAIL',
				1700000000, 1700000060)`,
			); err != nil {
				t.Fatalf("insert node check: %v", err)
			}
		},
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if hasAny(columnsOf(t, s, "epic_node_checks"), "key_lines") {
				t.Errorf("epic_node_checks columns after Down = %v, want no key_lines",
					columnsOf(t, s, "epic_node_checks"))
			}
			var stdout string
			row := s.db.DB.QueryRow(`SELECT stdout FROM epic_node_checks WHERE id = 'c1'`)
			if err := row.Scan(&stdout); err != nil || stdout != "FAIL" {
				t.Errorf("node check after Down = stdout %q (err %v), want it kept", stdout, err)
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			check, err := s.NodeCheck(t.Context(), "c1")
			if err != nil {
				t.Fatalf("read node check after Up: %v", err)
			}
			if check.KeyLines != nil || check.Stdout != "FAIL" {
				t.Errorf("node check after Down and Up = %+v, want it with no key lines", check)
			}
		},
	}
}
