package enginehost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// restartRow is one engine row the reconcile tests seed, with the state, question and reason it starts with. As an
// expectation it names the state, question and reason a row must hold after Reconcile.
type restartRow struct {
	id, state, question, reason string
}

// restartRows returns one row for each state Reconcile handles. Each row starts with one unfinished text block.
func restartRows() []restartRow {
	return []restartRow{
		{id: "working", state: "working"},
		{id: "waiting", state: "waiting", question: "Which color?"},
		{id: "done", state: "done"},
		{id: "stopped", state: "stopped"},
		{id: "failed", state: "failed", reason: "spawn failed"},
		{id: "interrupted", state: "interrupted", reason: "orreryd stopped during the turn"},
	}
}

// seedRestartRows adds every restartRows row to s, each with one unfinished text block.
func seedRestartRows(t *testing.T, s *store.Store) {
	t.Helper()
	var ops []store.EngineOp
	for _, row := range restartRows() {
		ops = append(ops, store.EngineAdd{ID: row.id, Dir: "/work", State: row.state},
			store.EngineText{ID: row.id, MessageID: "m1", Index: 0, Kind: "text", Text: "partial"})
		if row.question != "" {
			question := row.question
			ops = append(ops, store.EngineSet{ID: row.id, Changes: store.EngineChanges{Question: &question}})
		}
		if row.reason != "" {
			reason := row.reason
			ops = append(ops, store.EngineSet{ID: row.id, Changes: store.EngineChanges{Reason: &reason}})
		}
	}
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed engine rows: %v", err)
	}
}

// newReconcileManager returns a manager that reads rows from s, writes through writer and sends its notices to sink.
// Close runs at the end of the test.
func newReconcileManager(t *testing.T, s Store, writer *Writer, sink NoticeSink) *Manager {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	m := NewManager(Config{
		Shell:    sh,
		Claude:   fakeClaudePath(t),
		Environ:  []string{"PATH=" + os.Getenv("PATH")},
		Cap:      1,
		Writer:   writer,
		Now:      fixedClock,
		Notifier: sink,
		Store:    s,
	})
	t.Cleanup(m.Close)
	return m
}

// requireEngineRows checks that s holds exactly the rows want names, each with the state, question and reason want
// gives it. The id field of want is unused.
func requireEngineRows(t *testing.T, s *store.Store, want map[string]restartRow) {
	t.Helper()
	rows, err := s.EngineSessions(t.Context())
	if err != nil {
		t.Fatalf("EngineSessions: %v", err)
	}
	if len(rows) != len(want) {
		t.Fatalf("engine rows = %d, want %d", len(rows), len(want))
	}
	for _, row := range rows {
		exp, ok := want[row.ID]
		if !ok {
			t.Fatalf("unexpected engine row %s", row.ID)
		}
		got := restartRow{state: row.State, question: row.Question, reason: row.Reason}
		if got != exp {
			t.Fatalf("row %s = %+v, want %+v", row.ID, got, exp)
		}
	}
}

func TestReconcileInterruptsTurnsAndClearsTextOfRowsWithoutProcess(t *testing.T) {
	s, path := openStore(t)
	seedRestartRows(t, s)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	requireEngineRows(t, s, map[string]restartRow{
		"working":     {state: "interrupted", reason: "orreryd restarted during the turn"},
		"waiting":     {state: "interrupted", reason: "orreryd restarted during the turn"},
		"done":        {state: "done"},
		"stopped":     {state: "stopped"},
		"failed":      {state: "failed", reason: "spawn failed"},
		"interrupted": {state: "interrupted", reason: "orreryd stopped during the turn"},
	})
	if n := countRows(t, path, "engine_text"); n != 0 {
		t.Fatalf("unfinished text rows after Reconcile = %d, want 0", n)
	}

	wantNotices := map[string]Notice{
		"working": {Session: "working", Dir: "/work", State: "interrupted", Body: "orreryd restarted during the turn"},
		"waiting": {Session: "waiting", Dir: "/work", State: "interrupted", Body: "orreryd restarted during the turn"},
	}
	got := sink.got()
	if len(got) != len(wantNotices) {
		t.Fatalf("notices = %+v, want one for each of working and waiting", got)
	}
	seen := map[string]bool{}
	for _, notice := range got {
		want, ok := wantNotices[notice.Session]
		if !ok || seen[notice.Session] || notice != want {
			t.Fatalf("notices = %+v, want exactly [%+v, %+v]", got, wantNotices["waiting"], wantNotices["working"])
		}
		seen[notice.Session] = true
	}
}

func TestReconcileSecondRunChangesNothingAndNotifiesNothing(t *testing.T) {
	s, path := openStore(t)
	seedRestartRows(t, s)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)
	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	rowsBefore, err := s.EngineSessions(t.Context())
	if err != nil {
		t.Fatalf("EngineSessions: %v", err)
	}
	eventsBefore := countRows(t, path, "events")
	noticesBefore := len(sink.got())

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	rowsAfter, err := s.EngineSessions(t.Context())
	if err != nil {
		t.Fatalf("EngineSessions after second Reconcile: %v", err)
	}
	if !reflect.DeepEqual(rowsAfter, rowsBefore) {
		t.Fatalf("rows after second Reconcile = %+v, want %+v", rowsAfter, rowsBefore)
	}
	if n := countRows(t, path, "events"); n != eventsBefore {
		t.Fatalf("events after second Reconcile = %d, want %d", n, eventsBefore)
	}
	if n := len(sink.got()); n != noticesBefore {
		t.Fatalf("notices after second Reconcile = %d, want %d", n, noticesBefore)
	}
}

// failingStore is a Store whose reads all return err.
type failingStore struct {
	err error
}

func (f failingStore) EngineSessions(context.Context) ([]store.EngineRow, error) {
	return nil, f.err
}

func (f failingStore) EngineSession(context.Context, string) (store.EngineRow, error) {
	return store.EngineRow{}, f.err
}

func (f failingStore) EnginePromptSent(context.Context, string, string) (bool, error) {
	return false, f.err
}

func TestReconcileReturnsReadError(t *testing.T) {
	errRead := errors.New("read failed")
	writer := NewWriter(WriterConfig{Tick: time.Hour})
	m := newReconcileManager(t, failingStore{err: errRead}, writer, &fakeSink{})

	if err := m.Reconcile(t.Context()); !errors.Is(err, errRead) {
		t.Fatalf("Reconcile error = %v, want one wrapping %v", err, errRead)
	}
}

func TestReconcileReturnsWriteErrorAndNotifiesNothing(t *testing.T) {
	s, _ := openStore(t)
	seedRestartRows(t, s)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	stop := startRun(t, writer)
	stop()
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)

	if err := m.Reconcile(t.Context()); !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("Reconcile error = %v, want one wrapping ErrWriterStopped", err)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after a failed write = %+v, want none", got)
	}
}

func TestReconcileLeavesLiveSessionAlone(t *testing.T) {
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the hanging block's unfinished text", func() bool { return countRows(t, path, "engine_text") == 1 })

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if n := countRows(t, path, "engine_text"); n != 1 {
		t.Fatalf("unfinished text rows of the live session after Reconcile = %d, want 1", n)
	}
	row, err := s.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "working" || row.Reason != "" {
		t.Fatalf("live session after Reconcile = state %q reason %q, want working with no reason",
			row.State, row.Reason)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after Reconcile of a live session = %+v, want none", got)
	}
}
