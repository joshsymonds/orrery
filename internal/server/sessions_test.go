package server

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// sessionsClient serves newSessionsHandler's handler on an httptest server and returns a client for it.
func sessionsClient(t *testing.T, reader *store.Reader) orreryv1connect.SessionsClient {
	t.Helper()
	path, handler := newSessionsHandler(reader)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewSessionsClient(srv.Client(), srv.URL)
}

// expectListCode calls List and fails the test unless the call fails with want.
func expectListCode(t *testing.T, client orreryv1connect.SessionsClient, want connect.Code) {
	t.Helper()
	_, err := client.List(t.Context(), connect.NewRequest(&orreryv1.ListRequest{}))
	if err == nil {
		t.Fatalf("List succeeded, want code %v", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("List code: got %v (%v), want %v", got, err, want)
	}
}

// rowFields returns a row's ten mirrored options. Each value names its field, so a swapped field fails the test.
func rowFields(id string) store.Fields {
	return store.Fields{
		State:         id + " state",
		Changed:       id + " changed",
		Seen:          id + " seen",
		Question:      id + " question",
		Summary:       id + " summary",
		Label:         id + " label",
		Context:       id + " context",
		Tool:          id + " tool",
		ClaudeSession: id + " claude_session",
		Transcript:    id + " transcript",
	}
}

// wantSession is the proto row List must answer for a pane written with rowFields(id).
func wantSession(id, server, pane string) *orreryv1.Session {
	return &orreryv1.Session{
		Id:            id,
		Server:        server,
		Pane:          pane,
		State:         id + " state",
		Changed:       id + " changed",
		Seen:          id + " seen",
		Question:      id + " question",
		Summary:       id + " summary",
		Label:         id + " label",
		Context:       id + " context",
		Tool:          id + " tool",
		ClaudeSession: id + " claude_session",
		Transcript:    id + " transcript",
	}
}

// writeStore writes a store at path with the daemon's code, closes the writer, and returns the newest event seq.
// Two rows share the pane pair ("-L a", "%1"), as they can for ten seconds after a tmux restart.
func writeStore(t *testing.T, path string) int64 {
	t.Helper()
	ctx := t.Context()
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	if err = s.SyncServer(ctx, "-L a", []store.Pane{
		{ID: "orrery-1", Pane: "%1", Fields: rowFields("orrery-1")},
		{ID: "orrery-2", Pane: "%1", Fields: rowFields("orrery-2")},
	}, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if err = s.SyncPane(
		ctx,
		"-L b",
		store.Pane{ID: "orrery-3", Pane: "%7", Fields: rowFields("orrery-3")},
		now,
	); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store for seq: %v", err)
	}
	var seq int64
	err = db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&seq)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close store for seq: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read newest seq: %v", err)
	}
	if seq == 0 {
		t.Fatal("store wrote no events")
	}
	return seq
}

func TestListAnswersEveryRowAndSeqFromStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	seq := writeStore(t, path)

	got, err := sessionsClient(t, store.NewReader(path)).List(
		t.Context(),
		connect.NewRequest(&orreryv1.ListRequest{}),
	)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := &orreryv1.ListResponse{
		Sessions: []*orreryv1.Session{
			wantSession("orrery-1", "-L a", "%1"),
			wantSession("orrery-2", "-L a", "%1"),
			wantSession("orrery-3", "-L b", "%7"),
		},
		Seq: seq,
	}
	if !proto.Equal(got.Msg, want) {
		t.Fatalf("List: got %v, want %v", got.Msg, want)
	}
}

func TestListReturnsUnavailableWhenStoreIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "orrery.db")
	expectListCode(t, sessionsClient(t, store.NewReader(path)), connect.CodeUnavailable)
}

func TestListReturnsUnavailableWhileStoreIsWriteLocked(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	writeStore(t, path)

	// An exclusive-locking connection holds the write lock, so the reader cannot read the file.
	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open lock holder: %v", err)
	}
	t.Cleanup(func() {
		if err := holder.Close(); err != nil {
			t.Errorf("close lock holder: %v", err)
		}
	})
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("lock holder connection: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA locking_mode=EXCLUSIVE"); err != nil {
		t.Fatalf("set exclusive locking: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	if _, err = conn.ExecContext(
		ctx,
		"INSERT INTO events (session_id, kind, at, payload) VALUES ('x', 'k', 0, '{}')",
	); err != nil {
		t.Fatalf("write while holding lock: %v", err)
	}

	expectListCode(t, sessionsClient(t, store.NewReader(path)), connect.CodeUnavailable)
}

func TestListReturnsUnavailableWhenReaderIsNil(t *testing.T) {
	expectListCode(t, sessionsClient(t, nil), connect.CodeUnavailable)
}

func TestListReturnsInternalWithoutStoreDetail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	if err := os.WriteFile(path, []byte("this file is not a sqlite database and has no header"), 0o600); err != nil {
		t.Fatalf("write garbage store: %v", err)
	}

	_, err := sessionsClient(t, store.NewReader(path)).List(
		t.Context(),
		connect.NewRequest(&orreryv1.ListRequest{}),
	)
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("List code: got %v (%v), want %v", got, err, connect.CodeInternal)
	}
	if strings.Contains(err.Error(), "database") {
		t.Fatalf("List echoed store detail: %v", err)
	}
}
