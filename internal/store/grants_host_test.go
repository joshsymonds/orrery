package store

import (
	"path/filepath"
	"testing"
)

// TestGrantHost checks the host column of grants. A grant added for host b reads back as b. A grant added without a host
// reads back with the empty host, which is the controller's own. A grant row written with the column set from before
// the host column, at migration 00121, reads back with the empty host once the store migrates to 00122.
func TestGrantHost(t *testing.T) {
	t.Parallel()

	t.Run("grant for host b reads back as b", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s := openSyncStore(t)
		row := grantRowOf("g1", engineNowAt(), GrantMember{Pool: "build", Member: "m1"})
		row.Host = "b"
		if err := s.AddGrant(ctx, row); err != nil {
			t.Fatalf("AddGrant: %v", err)
		}
		got, err := s.Grants(ctx)
		if err != nil {
			t.Fatalf("Grants: %v", err)
		}
		if len(got) != 1 || got[0].Host != "b" {
			t.Fatalf("Grants = %+v, want one grant reading back host b", got)
		}
	})

	t.Run("grant stored without a host reads back with an empty host", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s := openSyncStore(t)
		row := grantRowOf("g1", engineNowAt(), GrantMember{Pool: "build", Member: "m1"})
		if err := s.AddGrant(ctx, row); err != nil {
			t.Fatalf("AddGrant: %v", err)
		}
		got, err := s.Grants(ctx)
		if err != nil {
			t.Fatalf("Grants: %v", err)
		}
		if len(got) != 1 || got[0].Host != "" {
			t.Fatalf("Grants = %+v, want one grant reading back the empty host", got)
		}
	})

	t.Run("grant row inserted with the pre-migration column set reads back with an empty host", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "orrery.db")

		fixture := openStoreAtVersion(t, path, 121)
		if _, err := fixture.ExecContext(ctx, `INSERT INTO grants (id, project, client_pid, client_start, pools, command, granted, holder)
			VALUES ('old', '/proj/old', 4242, 987654, '{"build":["a"]}', '["make"]', 1700000000, '')`); err != nil {
			fixture.Close()
			t.Fatalf("insert pre-migration grant: %v", err)
		}
		if err := fixture.Close(); err != nil {
			t.Fatalf("close fixture: %v", err)
		}

		s, err := Open(ctx, path, noSync())
		if err != nil {
			t.Fatalf("Open at version 121: %v", err)
		}
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})
		got, err := s.Grants(ctx)
		if err != nil {
			t.Fatalf("Grants: %v", err)
		}
		if len(got) != 1 || got[0].ID != "old" || got[0].Host != "" {
			t.Fatalf("Grants = %+v, want the pre-migration grant reading back the empty host", got)
		}
	})
}
