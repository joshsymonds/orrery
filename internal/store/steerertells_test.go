package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Invented epics of the steerer tell tests. Both share one family, so alpha sorts before beta.
const (
	steerAlpha = "orrery-5a6b7c8/steer-alpha"
	steerBeta  = "orrery-5a6b7c8/steer-beta"
)

// createSteerEpic stores an invented epic with id and title, in the accepted phase fullEpic gives it.
func createSteerEpic(t *testing.T, s *Store, id, title string) {
	t.Helper()
	row := fullEpic(id)
	row.Title = title
	createEpic(t, s, row)
}

// steerTellAt is the second the steerer tell tests stamp on a tell.
func steerTellAt() time.Time {
	return time.Unix(1700001000, 0)
}

// steererDeliveredOf reads the steerer_delivered column of tell seq of epic, freshly.
func steererDeliveredOf(ctx context.Context, t *testing.T, s *Store, epic string, seq int64) int64 {
	t.Helper()
	var delivered int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT steerer_delivered FROM epic_tells WHERE epic = ? AND seq = ?`, epic, seq).Scan(&delivered); err != nil {
		t.Fatalf("read steerer_delivered of tell %d of %s: %v", seq, epic, err)
	}
	return delivered
}

// toldOf reads the told column of tell seq of epic, freshly.
func toldOf(ctx context.Context, t *testing.T, s *Store, epic string, seq int64) int64 {
	t.Helper()
	var told int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT told FROM epic_tells WHERE epic = ? AND seq = ?`, epic, seq).Scan(&told); err != nil {
		t.Fatalf("read told of tell %d of %s: %v", seq, epic, err)
	}
	return told
}

func TestTellReachesSteererHookFiresOnceForTellWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createSteerEpic(t, s, steerAlpha, "Steer alpha")
	record := recordCallerEvents(t, s, func() ([]SteererTell, error) { return s.UndeliveredTells(ctx) })

	if err := s.TellEpic(ctx, steerAlpha, "check the gate", steerTellAt()); err != nil {
		t.Fatalf("TellEpic: %v", err)
	}
	if record.calls != 1 || len(record.seen) != 1 || len(record.seen[0]) != 1 {
		t.Errorf("hook calls = %d, fresh reads = %v; want one call that reads one undelivered tell",
			record.calls, record.seen)
	}
}

func TestTellReachesSteererUndeliveredListsPlainTellsInOrderUntilMarked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createSteerEpic(t, s, steerAlpha, "Steer alpha")
	createSteerEpic(t, s, steerBeta, "Steer beta")
	// Beta's tell is written first, so the listing follows epic then seq rather than the order of the writes.
	for _, tell := range []struct{ epic, text string }{
		{steerBeta, "beta first"}, {steerAlpha, "alpha first"}, {steerAlpha, "alpha second"},
	} {
		if err := s.TellEpic(ctx, tell.epic, tell.text, steerTellAt()); err != nil {
			t.Fatalf("TellEpic %q: %v", tell.text, err)
		}
	}
	// A tell that is about a question carries the question's seq, here 4.
	if _, err := s.fixtureExec(ctx,
		`INSERT INTO epic_tells (epic, seq, text, at, question) VALUES (?, 2, 'beta answer', ?, 4)`,
		steerBeta, steerTellAt().Unix()); err != nil {
		t.Fatalf("insert question tell: %v", err)
	}

	want := []SteererTell{
		{Epic: steerAlpha, Title: "Steer alpha", Seq: 1, Text: "alpha first", At: steerTellAt()},
		{Epic: steerAlpha, Title: "Steer alpha", Seq: 2, Text: "alpha second", At: steerTellAt()},
		{Epic: steerBeta, Title: "Steer beta", Seq: 1, Text: "beta first", At: steerTellAt()},
		{Epic: steerBeta, Title: "Steer beta", Seq: 2, Text: "beta answer", At: steerTellAt(), Question: 4},
	}
	got, err := s.UndeliveredTells(ctx)
	if err != nil {
		t.Fatalf("UndeliveredTells: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("undelivered tells = %v, want %v", got, want)
	}

	if err := s.MarkTellDelivered(ctx, steerAlpha, 1); err != nil {
		t.Fatalf("MarkTellDelivered: %v", err)
	}
	if delivered := steererDeliveredOf(ctx, t, s, steerAlpha, 1); delivered != 1 {
		t.Errorf("steerer_delivered of marked tell = %d, want 1", delivered)
	}
	if told := toldOf(ctx, t, s, steerAlpha, 1); told != 0 {
		t.Errorf("told of marked tell = %d, want 0: marking delivered must not touch told", told)
	}
	got, err = s.UndeliveredTells(ctx)
	if err != nil {
		t.Fatalf("UndeliveredTells after mark: %v", err)
	}
	if !reflect.DeepEqual(got, want[1:]) {
		t.Fatalf("undelivered tells after marking alpha 1 = %v, want %v", got, want[1:])
	}
}

func TestTellReachesSteererEndEpicTellStoredDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createSteerEpic(t, s, steerAlpha, "Steer alpha")
	createSteerEpic(t, s, steerBeta, "Steer beta")

	// done sends its steerer a phase event, so its ending tell is delivered with it. built sends none, so its tell is
	// left undelivered.
	if err := s.EndEpic(ctx, steerAlpha, phaseDone, "alpha ended", nil, steerTellAt()); err != nil {
		t.Fatalf("EndEpic done: %v", err)
	}
	if err := s.EndEpic(ctx, steerBeta, phaseBuilt, "beta built", nil, steerTellAt()); err != nil {
		t.Fatalf("EndEpic built: %v", err)
	}
	if delivered := steererDeliveredOf(ctx, t, s, steerAlpha, 1); delivered != 1 {
		t.Errorf("steerer_delivered of the done ending tell = %d, want 1", delivered)
	}

	want := []SteererTell{{Epic: steerBeta, Title: "Steer beta", Seq: 1, Text: "beta built", At: steerTellAt()}}
	got, err := s.UndeliveredTells(ctx)
	if err != nil {
		t.Fatalf("UndeliveredTells: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("undelivered tells = %v, want only the built tell %v", got, want)
	}
}

func TestTellDeliveredOnceUnknownTellMatchesErrNoTell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createSteerEpic(t, s, steerAlpha, "Steer alpha")
	if err := s.TellEpic(ctx, steerAlpha, "only tell", steerTellAt()); err != nil {
		t.Fatalf("TellEpic: %v", err)
	}

	err := s.MarkTellDelivered(ctx, steerAlpha, 2)
	if !errors.Is(err, ErrNoTell) {
		t.Fatalf("MarkTellDelivered of unknown tell 2: err = %v, want one matching ErrNoTell", err)
	}
	// The refused mark wrote nothing, so the epic's one tell is still undelivered.
	if delivered := steererDeliveredOf(ctx, t, s, steerAlpha, 1); delivered != 0 {
		t.Errorf("steerer_delivered of tell 1 = %d after a refused mark of tell 2, want 0", delivered)
	}
}

func TestTellDeliveredOnceMigrationMarksStoredTellsDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 109)
	seedEpic(t, fixture)
	if _, err := fixture.ExecContext(ctx,
		`INSERT INTO epic_tells (epic, seq, text, at, told, question) VALUES
		(?, 1, 'stored plain', 1700000100, 0, 0), (?, 2, 'stored answer', 1700000200, 1, 3)`,
		seededEpic, seededEpic); err != nil {
		fixture.Close()
		t.Fatalf("seed tells at version 109: %v", err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 109: %v", err)
	}
	defer s.Close()

	// The migration marks each stored tell delivered, so seq 1 (untold) and seq 2 (told with a question) both read 1.
	for _, seq := range []int64{1, 2} {
		if delivered := steererDeliveredOf(ctx, t, s, seededEpic, seq); delivered != 1 {
			t.Errorf("steerer_delivered of tell %d = %d after migration, want 1", seq, delivered)
		}
	}

	// An empty listing means the old tells send nothing: the steerer pass (planner.EpicCallerEvents.Pass,
	// internal/planner/callerevents.go:86) sends only what UndeliveredTells lists.
	tells, err := s.UndeliveredTells(ctx)
	if err != nil {
		t.Fatalf("UndeliveredTells: %v", err)
	}
	if len(tells) != 0 {
		t.Fatalf("undelivered tells after migrating from version 109 = %v, want none", tells)
	}

	// A tell stored after the migration starts undelivered, so the empty listing above is not for want of a row.
	if err := s.TellEpic(ctx, seededEpic, "stored after", steerTellAt()); err != nil {
		t.Fatalf("TellEpic after migration: %v", err)
	}
	tells, err = s.UndeliveredTells(ctx)
	if err != nil {
		t.Fatalf("UndeliveredTells after new tell: %v", err)
	}
	want := []SteererTell{{Epic: seededEpic, Title: "Epic one", Seq: 3, Text: "stored after", At: steerTellAt()}}
	if !reflect.DeepEqual(tells, want) {
		t.Fatalf("undelivered tells after a new tell = %v, want %v", tells, want)
	}
}
