package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// assertPutOffMarks fails unless the reader at path lists exactly want, in order.
func assertPutOffMarks(t *testing.T, path string, want []PutOffMark) {
	t.Helper()
	got, err := NewReader(path).PutOffQuestions(context.Background())
	if err != nil {
		t.Fatalf("PutOffQuestions: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PutOffQuestions:\n got %+v\nwant %+v", got, want)
	}
}

// putOffTableExists reports whether the store table put_off_questions is in its schema.
func putOffTableExists(t *testing.T, s *Store) bool {
	t.Helper()
	const query = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'put_off_questions'`
	var count int
	if err := s.db.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		t.Fatalf("read schema: %v", err)
	}
	return count == 1
}

func TestNotNowMigrationStepsDownAndUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 66)
	at := time.Unix(1700000000, 0)
	// The block is a subagent's reply, which writes no session column: the store is at version 66, before the columns.
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e-1", Dir: "/d", State: "idle"},
		EngineBlock{ID: "e-1", Kind: "text", Role: "assistant", MessageID: "m1",
			ParentToolUseID: "toolu_sub", Text: "reply"},
	}, at); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if err := s.ArchiveRow(ctx, "session", "orrery-1", at); err != nil {
		t.Fatalf("ArchiveRow: %v", err)
	}
	if err := s.PutOffQuestion(ctx, PutOffPane, "orrery-1", PaneQuestionKey("abc", 1700000000), at); err != nil {
		t.Fatalf("PutOffQuestion: %v", err)
	}

	existing := []string{"sessions", "events", "archived_rows"}
	before := map[string][][]any{}
	for _, table := range existing {
		rows := rowsOf(t, s.db.DB, table)
		if len(rows) == 0 {
			t.Fatalf("no rows seeded in %s", table)
		}
		before[table] = rows
	}
	if !putOffTableExists(t, s) {
		t.Fatal("put_off_questions is missing at version 66")
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if putOffTableExists(t, s) {
		t.Fatalf("put_off_questions exists after the Down")
	}
	for _, table := range existing {
		if got := rowsOf(t, s.db.DB, table); !reflect.DeepEqual(got, before[table]) {
			t.Errorf("%s after the Down = %v, want %v", table, got, before[table])
		}
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("UpByOne: %v", err)
	}
	if !putOffTableExists(t, s) {
		t.Fatal("put_off_questions is missing after the Up")
	}
	if got := rowsOf(t, s.db.DB, "put_off_questions"); len(got) != 0 {
		t.Errorf("put_off_questions after the Up = %v, want no rows", got)
	}
	for _, table := range existing {
		if got := rowsOf(t, s.db.DB, table); !reflect.DeepEqual(got, before[table]) {
			t.Errorf("%s after the Up = %v, want %v", table, got, before[table])
		}
	}
}

func TestNotNowStorePutOffAndBringBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	headlessAt := time.Unix(1700000100, 0)
	paneAt := time.Unix(1700000200, 0)
	paneKey := PaneQuestionKey("abc", 1700000000)
	if err := s.PutOffQuestion(ctx, PutOffHeadless, "e-1", "r-7", headlessAt); err != nil {
		t.Fatalf("PutOffQuestion headless: %v", err)
	}
	if err := s.PutOffQuestion(ctx, PutOffPane, "orrery-1", paneKey, paneAt); err != nil {
		t.Fatalf("PutOffQuestion pane: %v", err)
	}
	headless := PutOffMark{Kind: PutOffHeadless, Session: "e-1", Question: "r-7", At: headlessAt}
	pane := PutOffMark{Kind: PutOffPane, Session: "orrery-1", Question: "abc@1700000000", At: paneAt}
	assertPutOffMarks(t, path, []PutOffMark{headless, pane})

	if err := s.BringBackQuestion(ctx, PutOffHeadless, "e-1", "r-7"); err != nil {
		t.Fatalf("BringBackQuestion: %v", err)
	}
	if err := s.BringBackQuestion(ctx, PutOffHeadless, "e-1", "r-7"); err != nil {
		t.Fatalf("second BringBackQuestion of an absent mark: %v", err)
	}
	assertPutOffMarks(t, path, []PutOffMark{pane})
}

func TestNotNowStoreRefusesBadKindOrEmptyID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	at := time.Unix(1700000000, 0)
	for _, tc := range []struct {
		name, kind, session, question string
		want                          error
	}{
		{"bad kind", "session", "orrery-1", "r-1", ErrBadPutOffKind},
		{"empty session", PutOffHeadless, "", "r-1", ErrEmptyPutOffID},
		{"empty question", PutOffPane, "orrery-1", "", ErrEmptyPutOffID},
	} {
		if err := s.PutOffQuestion(ctx, tc.kind, tc.session, tc.question, at); !errors.Is(err, tc.want) {
			t.Errorf("PutOffQuestion with %s: got %v, want %v", tc.name, err, tc.want)
		}
		if err := s.BringBackQuestion(ctx, tc.kind, tc.session, tc.question); !errors.Is(err, tc.want) {
			t.Errorf("BringBackQuestion with %s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	assertPutOffMarks(t, path, []PutOffMark{})
}

func TestNotNowHeadlessRequestID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	at := time.Unix(1700000000, 0)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e-open", Dir: "/o", State: "waiting"},
		EngineRequest{ID: "e-open", Kind: "permission", RequestID: "r-open", ToolName: "Bash"},
		EngineAdd{ID: "e-later", Dir: "/l", State: "waiting"},
		EngineRequest{ID: "e-later", Kind: "permission", RequestID: "r-first", ToolName: "Bash"},
		EngineRequest{ID: "e-later", Kind: "permission", RequestID: "r-refused", ToolName: "Bash", Refused: true},
		EngineAdd{ID: "e-none", Dir: "/n", State: "idle"},
	}, at); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, err := NewReader(path).HeadlessSessions(ctx, at.Unix()-60)
	if err != nil {
		t.Fatalf("HeadlessSessions: %v", err)
	}
	byID := map[string]HeadlessRow{}
	for _, row := range got {
		byID[row.ID] = row
	}
	for _, tc := range []struct{ id, want string }{
		{"e-open", "r-open"},
		{"e-later", "r-first"},
		{"e-none", ""},
	} {
		row, ok := byID[tc.id]
		if !ok {
			t.Fatalf("HeadlessSessions lists no %s: %+v", tc.id, got)
		}
		if row.RequestID != tc.want {
			t.Errorf("RequestID of %s = %q, want %q", tc.id, row.RequestID, tc.want)
		}
	}
}

func TestNotNowPaneQuestionKey(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		hash    string
		changed int64
		want    string
	}{
		{"abc", 1700000000, "abc@1700000000"},
		{"", 1700000000, "@1700000000"},
	} {
		if got := PaneQuestionKey(tc.hash, tc.changed); got != tc.want {
			t.Errorf("PaneQuestionKey(%q, %d) = %q, want %q", tc.hash, tc.changed, got, tc.want)
		}
	}
}
