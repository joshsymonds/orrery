package store

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// TestStoreReadHandleReadsWhileAWriteHoldsTheWriter holds the writer inside writeDB, after BEGIN IMMEDIATE, until the
// deferred release closes. Meanwhile each read of an epic, its open questions, its attention and the labeled steps
// returns without error, since reads run on the read pool and never wait for the writer.
func TestStoreReadHandleReadsWhileAWriteHoldsTheWriter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{labelledStepOf("s1", "E1", "T1")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var writeErr error
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		writeErr = s.writeDB(ctx, func(sqlcgen.DBTX) error {
			close(entered)
			<-release
			return nil
		})
	}()
	defer func() {
		close(release)
		<-writeDone
		if writeErr != nil {
			t.Errorf("writeDB holding the writer: %v", writeErr)
		}
	}()
	select {
	case <-entered:
	case <-writeDone:
		t.Fatalf("writeDB returned before it held the writer: %v", writeErr)
	}

	epic, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic while writeDB holds the writer: %v", err)
	}
	if epic.ID != claimEpic {
		t.Fatalf("Epic: got id %q, want %q", epic.ID, claimEpic)
	}
	t.Logf("GetEpic returned while writeDB held the writer")

	if _, err := s.OpenQuestions(ctx, claimEpic); err != nil {
		t.Fatalf("OpenQuestions while writeDB holds the writer: %v", err)
	}
	t.Logf("ListOpenEpicQuestions returned while writeDB held the writer")

	attention, err := s.Attention(ctx)
	if err != nil {
		t.Fatalf("Attention while writeDB holds the writer: %v", err)
	}
	if epics := attentionEpics(attention); !reflect.DeepEqual(epics, []string{claimEpic}) {
		t.Fatalf("Attention epics = %v, want only %s", epics, claimEpic)
	}
	t.Logf("GetEpic, ListEpicTells and ListEpicReleaseRuns returned while writeDB held the writer")

	labeled, err := s.LabelledSteps(ctx, "", "")
	if err != nil {
		t.Fatalf("LabelledSteps while writeDB holds the writer: %v", err)
	}
	if len(labeled) != 1 {
		t.Fatalf("LabelledSteps: got %d steps, want the one labeled step", len(labeled))
	}
	t.Logf("LabelledSteps (ListLabelledSteps) returned while writeDB held the writer")
}

// TestStoreReadHandleOpensEveryConnectionWithTheStore checks that Open leaves each of the read pool's connections open, so
// no read's store call opens one inside its timer.
func TestStoreReadHandleOpensEveryConnectionWithTheStore(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	if got := s.readConnector.open.Load(); got != readConns {
		t.Fatalf("read connections open after Open = %d, want %d", got, readConns)
	}
}

// TestStoreReadHandleRefusesAWrite runs an INSERT on the read pool. The pool's connections open the file read-only, so
// SQLite fails the INSERT with its read-only error.
func TestStoreReadHandleRefusesAWrite(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	_, err := s.read.ExecContext(t.Context(),
		`INSERT INTO sessions (id, server, pane) VALUES ('read-handle', 'server', '%1')`)
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code()&primaryCodeMask != sqlite3.SQLITE_READONLY {
		t.Fatalf("INSERT on the read pool: %v, want SQLite's read-only error", err)
	}
	t.Logf("INSERT on the read pool failed as read-only: %v", err)
}

// TestStoreReadHandleLogsASlowRead keeps a read's rows open while the test call clock moves past slowCallThreshold. The
// read's call runs that long on the read pool, so the store logs it as a slow store call and counts it.
func TestStoreReadHandleLogsASlowRead(t *testing.T) {
	t.Parallel()
	f := newSlowCallFixture(t)
	func() {
		rows, err := f.store.read.QueryContext(t.Context(), `SELECT count(*) FROM epics`)
		if err != nil {
			t.Fatalf("query on the read pool: %v", err)
		}
		defer func() {
			if closeErr := rows.Close(); closeErr != nil {
				t.Errorf("close the read's rows: %v", closeErr)
			}
		}()
		if err := rows.Err(); err != nil {
			t.Fatalf("rows of the read pool: %v", err)
		}
		f.clock.advance(150 * time.Millisecond)
	}()
	t.Logf("the read logged: %s", strings.TrimSpace(f.logs.String()))
	f.wantSlowCall(t, `queries="SELECT count(*) FROM epics"`, "wait=0s", "run=150ms")
}

// TestStoreReadHandleWriterReferencedOnlyByWriteFunctions scans the package's non-test files for the writer's field,
// db, and requires the functions that reference it to be writeDB, migrate and Close alone: every read runs on the read
// pool.
func TestStoreReadHandleWriterReferencedOnlyByWriteFunctions(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	fset := token.NewFileSet()
	referencing := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "db" {
					referencing[fn.Name.Name] = true
				}
				return true
			})
		}
	}
	got := make([]string, 0, len(referencing))
	for name := range referencing {
		got = append(got, name)
	}
	slices.Sort(got)
	t.Logf("non-test functions referencing the writer handle: %s", strings.Join(got, ", "))
	if want := []string{"Close", "migrate", "writeDB"}; !slices.Equal(got, want) {
		t.Fatalf("functions referencing the writer handle = %v, want %v", got, want)
	}
}
