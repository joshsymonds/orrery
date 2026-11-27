package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// preEpicDesignID is the id of the design row the test writes with no host, as a design stored before the host column
// reads.
const preEpicDesignID = "synthetic-design-pre-epic"

// seedPreEpicDesign writes the store at path with one design row that has no host: the row a design stored before the
// host column holds, which reads the empty host.
func seedPreEpicDesign(t *testing.T, path string) {
	t.Helper()
	storetest.Seed(t, path)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open store to seed a design: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close seeded store: %v", err)
		}
	}()
	if _, err := db.Exec(
		`INSERT INTO designs (id, title, conversation, created) `+
			`VALUES (?, 'Synthetic design', 'synthetic-session', 1700000000)`,
		preEpicDesignID,
	); err != nil {
		t.Fatalf("seed design row: %v", err)
	}
}

// TestDesignHostPreEpicDesignReadsController runs a --sessions orreryd with --host over a store holding a design row
// from before the host column, and reads that design over the Designs service once orreryd has started: the row reads
// the controller's host name.
func TestDesignHostPreEpicDesignReadsController(t *testing.T) {
	getenv, root := hostMigrationEnv(t, "")
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	seedPreEpicDesign(t, path)
	socket := filepath.Join(root, "orreryd.sock")
	opts, err := parseFlags(hostMigrationSessionArgs(t, socket), getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	stop := startHostMigrationServe(t, getenv, opts)

	designs := enginev1connect.NewDesignsClient(
		cronWiringHTTP(t, protocol.SessionSocketPath(socket)), "http://designs.invalid")
	reply, err := designs.GetDesign(t.Context(), connect.NewRequest(&v1.GetDesignRequest{Id: preEpicDesignID}))
	if err != nil {
		t.Fatalf("GetDesign %s: %v", preEpicDesignID, err)
	}
	if host := reply.Msg.GetDesign().GetHost(); host != hostMigrationName {
		t.Errorf("host of the pre-epic design = %q, want the controller's %q", host, hostMigrationName)
	}
	if err := stop(); err != nil {
		t.Fatalf("serve: %v", err)
	}
}
