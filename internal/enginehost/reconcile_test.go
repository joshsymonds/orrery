package enginehost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sync"
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
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
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
	t.Parallel()
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
	t.Parallel()
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

func (f failingStore) EngineTextSessions(context.Context) (map[string]bool, error) {
	return nil, f.err
}

func (f failingStore) EngineSession(context.Context, string) (store.EngineRow, error) {
	return store.EngineRow{}, f.err
}

func (f failingStore) Step(context.Context, string) (store.StepRow, error) {
	return store.StepRow{}, f.err
}

func (f failingStore) EnginePromptSent(context.Context, string, string) (bool, error) {
	return false, f.err
}

func (f failingStore) EnginePromptFailure(context.Context, string, string) (string, bool, error) {
	return "", false, f.err
}

func (f failingStore) HolderPrompts(context.Context, string) ([]store.HolderPrompt, error) {
	return nil, f.err
}

func TestReconcileReturnsReadError(t *testing.T) {
	t.Parallel()
	errRead := errors.New("read failed")
	writer := NewWriter(WriterConfig{Tick: time.Hour})
	m := newReconcileManager(t, failingStore{err: errRead}, writer, &fakeSink{})

	if err := m.Reconcile(t.Context()); !errors.Is(err, errRead) {
		t.Fatalf("Reconcile error = %v, want one wrapping %v", err, errRead)
	}
}

func TestReconcileReturnsWriteErrorAndNotifiesNothing(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestReconcileSettlesStepRowSilently(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: "step", Dir: "/work", State: "working", Kind: store.KindStep},
		store.EngineText{ID: "step", MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
		store.EngineAdd{ID: "engine", Dir: "/work", State: "working", Kind: store.KindEngine},
		store.EngineText{ID: "engine", MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
	}, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed engine rows: %v", err)
	}
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	requireEngineRows(t, s, map[string]restartRow{
		"step":   {state: "interrupted", reason: "orreryd restarted during the turn"},
		"engine": {state: "interrupted", reason: "orreryd restarted during the turn"},
	})
	if n := countRows(t, path, "engine_text"); n != 0 {
		t.Fatalf("unfinished text rows after Reconcile = %d, want 0", n)
	}
	want := Notice{Session: "engine", Dir: "/work", State: "interrupted", Body: "orreryd restarted during the turn"}
	if got := sink.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
}

// settledRestartRows returns the state, question and reason each restartRows row holds after Reconcile.
func settledRestartRows() map[string]restartRow {
	return map[string]restartRow{
		"working":     {state: "interrupted", reason: "orreryd restarted during the turn"},
		"waiting":     {state: "interrupted", reason: "orreryd restarted during the turn"},
		"done":        {state: "done"},
		"stopped":     {state: "stopped"},
		"failed":      {state: "failed", reason: "spawn failed"},
		"interrupted": {state: "interrupted", reason: "orreryd stopped during the turn"},
	}
}

// requireRestartNotices checks that sink got one notice of an interrupted turn for each of the working and waiting
// rows of restartRows, and no other.
func requireRestartNotices(t *testing.T, sink *fakeSink) {
	t.Helper()
	wantNotices := map[string]Notice{
		"working": {Session: "working", Dir: "/work", State: "interrupted", Body: "orreryd restarted during the turn"},
		"waiting": {Session: "waiting", Dir: "/work", State: "interrupted", Body: "orreryd restarted during the turn"},
	}
	got := sink.got()
	if len(got) != len(wantNotices) {
		t.Fatalf("notices = %+v, want one for each of working and waiting", got)
	}
	for _, notice := range got {
		want, ok := wantNotices[notice.Session]
		if !ok {
			t.Fatalf("unexpected notice %+v", notice)
		}
		if notice != want {
			t.Fatalf("notice = %+v, want %+v", notice, want)
		}
		delete(wantNotices, notice.Session)
	}
}

// countingApplier forwards to a real store and counts the ApplyEngine calls it receives, recording their ops.
type countingApplier struct {
	store *store.Store
	mu    sync.Mutex
	calls int
	ops   []store.EngineOp
}

func (a *countingApplier) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	a.mu.Lock()
	a.calls++
	a.ops = append(a.ops, ops...)
	a.mu.Unlock()
	return a.store.ApplyEngine(ctx, ops, now)
}

// eventCounts returns how many events each session in ids holds in the store at path, read on a separate connection. A
// session with no event is absent from the map.
func eventCounts(t *testing.T, path string, ids []string) map[string]int {
	t.Helper()
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		t.Fatalf("encode session ids: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store connection: %v", err)
		}
	}()
	rows, err := db.Query("SELECT session_id, COUNT(*) FROM events "+
		"WHERE session_id IN (SELECT value FROM json_each(?)) GROUP BY session_id", string(idsJSON))
	if err != nil {
		t.Fatalf("count events by session: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close event rows: %v", err)
		}
	}()
	counts := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			t.Fatalf("scan event count: %v", err)
		}
		counts[id] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read event counts: %v", err)
	}
	return counts
}

// reconcileMixedRows is how many rows the mixed-rows reconcile test seeds.
const reconcileMixedRows = 10_000

// reconcileGuard bounds the one Reconcile call of the mixed-rows test. It is a hang guard, never a timing assumption.
const reconcileGuard = time.Minute

// mixedRow is one row of the mixed-rows test: its id, kind, and the state, question and reason it is seeded with, and
// whether it holds one unfinished text block.
type mixedRow struct {
	id, kind, state, question, reason string
	text                              bool
}

// mixedPatterns returns the rows the mixed-rows test cycles through: settled rows with and without text, working and
// waiting engine rows, and step rows.
func mixedPatterns() []mixedRow {
	return []mixedRow{
		{kind: store.KindEngine, state: "working", text: true},
		{kind: store.KindEngine, state: "waiting", question: "Which color?", text: true},
		{kind: store.KindEngine, state: "working"},
		{kind: store.KindEngine, state: "done", text: true},
		{kind: store.KindEngine, state: "done"},
		{kind: store.KindEngine, state: "stopped"},
		{kind: store.KindEngine, state: "failed", reason: "spawn failed", text: true},
		{kind: store.KindStep, state: "working", text: true},
		{kind: store.KindStep, state: "done"},
		{kind: store.KindEngine, state: "interrupted", reason: "orreryd stopped during the turn"},
	}
}

// mixedRows returns n rows, cycling through mixedPatterns, each with its own id.
func mixedRows(n int) []mixedRow {
	patterns := mixedPatterns()
	rows := make([]mixedRow, n)
	for i := range rows {
		rows[i] = patterns[i%len(patterns)]
		rows[i].id = fmt.Sprintf("row-%05d", i)
	}
	return rows
}

// turning reports whether the turn of row is running, so a restart interrupts it.
func (r mixedRow) turning() bool {
	return r.state == "working" || r.state == "waiting"
}

// skipped reports whether a restart writes nothing for row: no turn is running and it holds no text.
func (r mixedRow) skipped() bool {
	return !r.turning() && !r.text
}

// settled returns the state, question and reason a restart leaves row with.
func (r mixedRow) settled() restartRow {
	if r.turning() {
		return restartRow{state: "interrupted", reason: "orreryd restarted during the turn"}
	}
	return restartRow{state: r.state, question: r.question, reason: r.reason}
}

// seedMixedRows adds rows to the store at path, each with the kind, state, question and reason it names and its
// unfinished text block when it has one. The rows go in through one transaction of prepared statements: ApplyEngine
// prepares every statement it runs, and seeding this many rows through it costs more than the Reconcile under test.
// The seeded rows have no event history, so a skipped row must still have none after Reconcile.
func seedMixedRows(t *testing.T, path string, rows []mixedRow) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store connection: %v", err)
		}
	}()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin seed: %v", err)
	}
	insertSession, err := tx.Prepare(`
		INSERT INTO sessions (id, server, pane, kind, dir, state, changed, question, reason)
		VALUES (?, '', '', ?, '/work', ?, '1', ?, ?)`)
	if err != nil {
		t.Fatalf("prepare session insert: %v", err)
	}
	insertText, err := tx.Prepare(`
		INSERT INTO engine_text (session_id, message_id, block_index, parent_tool_use_id, kind, text)
		VALUES (?, 'm1', 0, '', 'text', 'partial')`)
	if err != nil {
		t.Fatalf("prepare text insert: %v", err)
	}
	defer func() {
		if err := insertSession.Close(); err != nil {
			t.Errorf("close session insert: %v", err)
		}
		if err := insertText.Close(); err != nil {
			t.Errorf("close text insert: %v", err)
		}
	}()
	for _, row := range rows {
		if _, err := insertSession.Exec(row.id, row.kind, row.state, row.question, row.reason); err != nil {
			t.Fatalf("seed session %s: %v", row.id, err)
		}
		if row.text {
			if _, err := insertText.Exec(row.id); err != nil {
				t.Fatalf("seed text of session %s: %v", row.id, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
}

// requireSettledOps checks that the ops applier received are, for each row, exactly the ops a restart gives it: one
// EngineTextClear unless the row is skipped, and one EngineSet when its turn is running. A skipped row gets none.
func requireSettledOps(t *testing.T, applier *countingApplier, rows []mixedRow) {
	t.Helper()
	applier.mu.Lock()
	calls, ops := applier.calls, applier.ops
	applier.mu.Unlock()
	if calls != 1 {
		t.Fatalf("ApplyEngine calls = %d, want 1", calls)
	}
	clears, sets := map[string]int{}, map[string]int{}
	for _, op := range ops {
		switch op := op.(type) {
		case store.EngineTextClear:
			clears[op.ID]++
		case store.EngineSet:
			sets[op.ID]++
		default:
			t.Fatalf("op %T, want only EngineTextClear and EngineSet", op)
		}
	}
	wantOps := 0
	for _, row := range rows {
		wantClears, wantSets := 0, 0
		if !row.skipped() {
			wantClears = 1
		}
		if row.turning() {
			wantSets = 1
		}
		wantOps += wantClears + wantSets
		if clears[row.id] != wantClears || sets[row.id] != wantSets {
			t.Fatalf("ops of row %s = %d clears and %d sets, want %d and %d",
				row.id, clears[row.id], sets[row.id], wantClears, wantSets)
		}
	}
	if len(ops) != wantOps {
		t.Fatalf("ops = %d, want %d", len(ops), wantOps)
	}
}

func TestReconcileSettlesMixedRowsInOneWrite(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		tick time.Duration
	}{
		{name: "1 ms tick", tick: time.Millisecond},
		{name: "default tick", tick: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, path := openStore(t)
			rows := mixedRows(reconcileMixedRows)
			seedMixedRows(t, path, rows)
			applier := &countingApplier{store: s}
			writer := NewWriter(WriterConfig{Applier: applier, Tick: tc.tick})
			startRun(t, writer)
			sink := &fakeSink{}
			m := newReconcileManager(t, s, writer, sink)
			var skippedIDs []string
			for _, row := range rows {
				if row.skipped() {
					skippedIDs = append(skippedIDs, row.id)
				}
			}
			eventsBefore := eventCounts(t, path, skippedIDs)

			ctx, cancel := context.WithTimeout(t.Context(), reconcileGuard)
			defer cancel()
			if err := m.Reconcile(ctx); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}

			requireSettledOps(t, applier, rows)
			eventsAfter := eventCounts(t, path, skippedIDs)
			for _, row := range rows {
				if row.skipped() && eventsAfter[row.id] != eventsBefore[row.id] {
					t.Fatalf("events of skipped row %s = %d, want %d",
						row.id, eventsAfter[row.id], eventsBefore[row.id])
				}
			}
			wantRows := make(map[string]restartRow, len(rows))
			for _, row := range rows {
				wantRows[row.id] = row.settled()
			}
			requireEngineRows(t, s, wantRows)
			if n := countRows(t, path, "engine_text"); n != 0 {
				t.Fatalf("unfinished text rows after Reconcile = %d, want 0", n)
			}

			wantNotices := map[string]Notice{}
			for _, row := range rows {
				if row.turning() && row.kind == store.KindEngine {
					wantNotices[row.id] = Notice{Session: row.id, Dir: "/work", State: "interrupted",
						Body: "orreryd restarted during the turn"}
				}
			}
			got := sink.got()
			if len(got) != len(wantNotices) {
				t.Fatalf("notices = %d, want %d, one for each working and waiting engine row",
					len(got), len(wantNotices))
			}
			for _, notice := range got {
				wantNotice, ok := wantNotices[notice.Session]
				if !ok {
					t.Fatalf("unexpected notice %+v", notice)
				}
				if notice != wantNotice {
					t.Fatalf("notice = %+v, want %+v", notice, wantNotice)
				}
				delete(wantNotices, notice.Session)
			}
		})
	}
}

// ghostStore is a Store whose engine rows read also lists ghost, a row the store does not hold. The ops of ghost fail
// in the store, so its group is dropped.
type ghostStore struct {
	Store
	ghost store.EngineRow
}

func (g ghostStore) EngineSessions(ctx context.Context) ([]store.EngineRow, error) {
	rows, err := g.Store.EngineSessions(ctx)
	if err != nil {
		return nil, err
	}
	return append([]store.EngineRow{g.ghost}, rows...), nil
}

func TestReconcileDroppedGroupStopsNoOtherRow(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	seedRestartRows(t, s)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	ghost := ghostStore{Store: s, ghost: store.EngineRow{
		ID: "ghost", Dir: "/work", State: "working", Kind: store.KindEngine,
	}}
	m := newReconcileManager(t, ghost, writer, sink)

	if err := m.Reconcile(t.Context()); !errors.Is(err, store.ErrNoEngineSession) {
		t.Fatalf("Reconcile error = %v, want one wrapping ErrNoEngineSession", err)
	}

	requireEngineRows(t, s, settledRestartRows())
	if n := countRows(t, path, "engine_text"); n != 0 {
		t.Fatalf("unfinished text rows after Reconcile = %d, want 0", n)
	}
	requireRestartNotices(t, sink)
}

// textFailingStore is a Store whose unfinished-text read fails with err. Its other reads go to the store.
type textFailingStore struct {
	Store
	err error
}

func (f textFailingStore) EngineTextSessions(context.Context) (map[string]bool, error) {
	return nil, f.err
}

func TestReconcileTextReadErrorClearsEverySettledRow(t *testing.T) {
	t.Parallel()
	errText := errors.New("text read failed")
	s, path := openStore(t)
	seedRestartRows(t, s)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, textFailingStore{Store: s, err: errText}, writer, sink)

	if err := m.Reconcile(t.Context()); !errors.Is(err, errText) {
		t.Fatalf("Reconcile error = %v, want one wrapping %v", err, errText)
	}

	requireEngineRows(t, s, settledRestartRows())
	if n := countRows(t, path, "engine_text"); n != 0 {
		t.Fatalf("unfinished text rows after Reconcile = %d, want 0", n)
	}
	requireRestartNotices(t, sink)
}
