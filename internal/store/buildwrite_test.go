package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// buildEpic is the accepted epic, at base main, whose one task the build write tests write.
const buildEpic = "orrery-1a2b3c4/epic-write"

// buildBase is the revision the build write tests give a task's build as its base.
const buildBase = "b68d2a24e4bfc11691be94b7acdce4340bce6cac"

// writtenAt is the second the build write tests stamp their writes at.
func writtenAt() time.Time {
	return time.Unix(1700000800, 0)
}

// openBuildOf is the build of an open task whose implementer step is running on its base.
func openBuildOf() store.TaskBuild {
	return store.TaskBuild{Stage: "implement", Base: buildBase, Attempt: 1, Step: "step-1"}
}

// buildEpicRow is the accepted epic the build write tests plan a task of.
func buildEpicRow(created time.Time) store.EpicRow {
	return store.EpicRow{
		ID: buildEpic, Repository: "/work/synthetic-repo", Base: "main", Title: "Synthetic epic",
		Approved: "Approved sentence", Intent: "Store the build writes", Care: "serious",
		Approach: "Compare and set", ReleaseThen: "Then line", Checks: "just check", Phase: "accepted",
		Created: created, Changed: created,
	}
}

// buildPlan is a valid planner return of one task that the build write tests start from.
func buildPlan() store.EpicPlan {
	return store.EpicPlan{
		Decisions: []string{"Each build end is one write"},
		Tasks: []store.EpicTaskInput{{
			Title: "Write the build ends", Deliverable: "Each end is one write", Files: []string{"a.go"},
			Requirements: []string{"R5"}, Gate: "check",
			Evidence: evidence.Item{
				Capture: []string{"go", "test", "./internal/store/"}, Limit: 600,
				Artifacts: []string{"out/store.json"}, Conclusion: "the ends write", Judge: "read the output",
			},
		}},
		Outline: "Write the ends",
		Outcome: "work",
	}
}

// openBuildStore opens a migrated store file with buildEpic accepted and its task 1 planned, open with a zero build,
// and returns the store with a raw handle to the same file for reads of its tables.
func openBuildStore(ctx context.Context, t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	store.SeedMigrated(t, path)
	s, err := store.Open(ctx, path, store.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw handle: %v", err)
	}
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	})
	created := time.Unix(1700000000, 0)
	if err := s.CreateEpic(ctx, buildEpicRow(created)); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	turn, err := s.ClaimTurn(ctx, buildEpic, created)
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, buildEpic, turn.Seq, buildPlan(), writtenAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	return s, raw
}

// tellBuildEpic adds text to buildEpic as a tell written at at, through the write the ref check tells through: task 1 is
// stored with the build it already has, so only the tell is new.
func tellBuildEpic(ctx context.Context, t *testing.T, s *store.Store, text string, at time.Time) {
	t.Helper()
	build := store.TaskBuild{}
	if err := s.SetTaskBuildTelling(ctx, buildEpic, 1, build, build, []string{text}, at); err != nil {
		t.Fatalf("SetTaskBuildTelling of %q: %v", text, err)
	}
}

// buildTaskOf returns task 1 of buildEpic as the store reads it.
func buildTaskOf(ctx context.Context, t *testing.T, s *store.Store) store.EpicTask {
	t.Helper()
	row := epicOf(ctx, t, s)
	for _, task := range row.Tasks {
		if task.Seq == 1 {
			return task
		}
	}
	t.Fatalf("epic %s has no task 1", buildEpic)
	return store.EpicTask{}
}

// epicOf returns buildEpic as the store reads it.
func epicOf(ctx context.Context, t *testing.T, s *store.Store) store.EpicRow {
	t.Helper()
	row, err := s.Epic(ctx, buildEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	return row
}

// rawRows returns the rows query gives, each as its column values in column order.
func rawRows(t *testing.T, raw *sql.DB, query string, args ...any) [][]any {
	t.Helper()
	rows, err := raw.Query(query, args...)
	if err != nil {
		t.Fatalf("query %s: %v", query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %s: %v", query, err)
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("scan %s: %v", query, err)
		}
		out = append(out, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s: %v", query, err)
	}
	return out
}

// tellRows returns the epic_tells rows of buildEpic: seq, text, at and told.
func tellRows(t *testing.T, raw *sql.DB) [][]any {
	t.Helper()
	return rawRows(t, raw, `SELECT seq, text, at, told FROM epic_tells WHERE epic = ? ORDER BY seq`, buildEpic)
}

// storedState returns every row of the tables a build write changes, so a test can compare the store before and after
// a write that must change nothing.
func storedState(t *testing.T, raw *sql.DB) map[string][][]any {
	t.Helper()
	return map[string][][]any{
		"epics":         rawRows(t, raw, `SELECT * FROM epics ORDER BY id`),
		"epic_tasks":    rawRows(t, raw, `SELECT * FROM epic_tasks ORDER BY epic, seq`),
		"epic_triggers": rawRows(t, raw, `SELECT * FROM epic_triggers ORDER BY epic, id`),
		"epic_tells":    rawRows(t, raw, `SELECT * FROM epic_tells ORDER BY epic, seq`),
	}
}

func TestFailTaskStoresFailedStatusCutFailureAndPendingTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)

	// The reason's 1024th byte is the first byte of the two-byte é, so the cut stops before the é.
	cut := strings.Repeat("a", store.TurnReasonMax-1)
	reason := cut + "é" + "tail"
	if err := s.FailTask(ctx, buildEpic, 1, store.TaskBuild{}, reason, writtenAt()); err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if task.Status != "failed" {
		t.Errorf("task status = %q, want failed", task.Status)
	}
	if task.Build.Failure != cut {
		t.Errorf("task failure is %d bytes, want the %d-byte cut", len(task.Build.Failure), len(cut))
	}

	want := [][]any{{buildEpic, "T1-failed", "task T1 failed: " + cut, "pending", int64(0), writtenAt().Unix()}}
	got := rawRows(t, raw, `SELECT * FROM epic_triggers WHERE epic = ? AND id = ?`, buildEpic, "T1-failed")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("trigger rows = %v, want %v", got, want)
	}

	states, err := s.PlanStates(ctx)
	if err != nil {
		t.Fatalf("PlanStates: %v", err)
	}
	if len(states) != 1 || states[0].Epic != buildEpic || states[0].Pending != 1 {
		t.Errorf("PlanStates = %+v, want one epic with one pending trigger", states)
	}
}

func TestAcceptTaskStoresDoneAndTheReview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)

	if err := s.AcceptTask(ctx, buildEpic, 1, store.TaskBuild{}, "reviewed"); err != nil {
		t.Fatalf("AcceptTask: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if task.Status != "done" {
		t.Errorf("task status = %q, want done", task.Status)
	}
	if task.Build.Review != "reviewed" {
		t.Errorf("task review = %q, want reviewed", task.Build.Review)
	}
}

func TestStopTaskPausesEpicAndStoresBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	to := store.TaskBuild{Stage: "implement", Base: buildBase, Attempt: 1, Held: "stopped", Step: "step-1"}

	if err := s.StopTask(ctx, buildEpic, 1, store.TaskBuild{}, to); err != nil {
		t.Fatalf("StopTask: %v", err)
	}

	if !epicOf(ctx, t, s).Paused {
		t.Errorf("epic is not paused")
	}
	if got := buildTaskOf(ctx, t, s).Build; got != to {
		t.Errorf("task build = %+v, want %+v", got, to)
	}
}

func TestParkTaskParksEpicWithReasonAndAddsTell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	to := store.TaskBuild{Stage: "red", Base: buildBase, Held: "parked"}
	const reason = "the red run did not capture"

	if err := s.ParkTask(ctx, buildEpic, 1, store.TaskBuild{}, to, reason, writtenAt()); err != nil {
		t.Fatalf("ParkTask: %v", err)
	}

	if got := epicOf(ctx, t, s).Parked; got != reason {
		t.Errorf("epic park = %q, want %q", got, reason)
	}
	if got := buildTaskOf(ctx, t, s).Build; got != to {
		t.Errorf("task build = %+v, want %+v", got, to)
	}
	want := [][]any{{int64(1), reason, writtenAt().Unix(), int64(0)}}
	if got := tellRows(t, raw); !reflect.DeepEqual(got, want) {
		t.Errorf("tells = %v, want %v", got, want)
	}
}

func TestParkEpicParksWithReasonAndAddsTell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	const reason = "the branch exists"

	if err := s.ParkEpic(ctx, buildEpic, reason, writtenAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	if got := epicOf(ctx, t, s).Parked; got != reason {
		t.Errorf("epic park = %q, want %q", got, reason)
	}
	want := [][]any{{int64(1), reason, writtenAt().Unix(), int64(0)}}
	if got := tellRows(t, raw); !reflect.DeepEqual(got, want) {
		t.Errorf("tells = %v, want %v", got, want)
	}
}

func TestParkEpicOfUnknownEpicIsErrNoEpicAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	before := storedState(t, raw)

	err := s.ParkEpic(ctx, "orrery-1a2b3c4/epic-missing", "no such epic", writtenAt())
	if !errors.Is(err, store.ErrNoEpic) {
		t.Fatalf("ParkEpic of unknown epic err = %v, want ErrNoEpic", err)
	}
	if !reflect.DeepEqual(before, storedState(t, raw)) {
		t.Errorf("ParkEpic of unknown epic changed the store")
	}
}

func TestSetTaskBuildTellingNumbersTellsFromOneAndUnknownEpicIsErrTaskMoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)

	tellBuildEpic(ctx, t, s, "first", writtenAt())
	tellBuildEpic(ctx, t, s, "second", writtenAt())

	want := [][]any{
		{int64(1), "first", writtenAt().Unix(), int64(0)},
		{int64(2), "second", writtenAt().Unix(), int64(0)},
	}
	if got := tellRows(t, raw); !reflect.DeepEqual(got, want) {
		t.Errorf("tells = %v, want %v", got, want)
	}

	unknown := store.TaskBuild{}
	err := s.SetTaskBuildTelling(
		ctx, "orrery-1a2b3c4/epic-missing", 1, unknown, unknown, []string{"no such epic"}, writtenAt(),
	)
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Errorf("SetTaskBuildTelling of unknown epic err = %v, want ErrTaskMoved", err)
	}
}

func TestSetEpicBranchReadsBackOnEpicBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)

	if err := s.SetEpicBranch(ctx, buildEpic, "creating"); err != nil {
		t.Fatalf("SetEpicBranch creating: %v", err)
	}
	if got := epicOf(ctx, t, s).Branch; got != "creating" {
		t.Errorf("branch = %q, want creating", got)
	}
	if err := s.SetEpicBranch(ctx, buildEpic, "created"); err != nil {
		t.Fatalf("SetEpicBranch created: %v", err)
	}
	if got := epicOf(ctx, t, s).Branch; got != "created" {
		t.Errorf("branch = %q, want created", got)
	}

	err := s.SetEpicBranch(ctx, "orrery-1a2b3c4/epic-missing", "created")
	if !errors.Is(err, store.ErrNoEpic) {
		t.Errorf("SetEpicBranch of unknown epic err = %v, want ErrNoEpic", err)
	}
}

// movedWrite is one task write, named for the subtest, that takes from as the build it expects.
type movedWrite struct {
	name string
	run  func(s *store.Store, from store.TaskBuild) error
}

// movedWrites is every task write, each run against task 1 of buildEpic.
func movedWrites() []movedWrite {
	ctx := context.Background()
	return []movedWrite{
		{name: "FailTask", run: func(s *store.Store, from store.TaskBuild) error {
			return s.FailTask(ctx, buildEpic, 1, from, "late failure", writtenAt())
		}},
		{name: "AcceptTask", run: func(s *store.Store, from store.TaskBuild) error {
			return s.AcceptTask(ctx, buildEpic, 1, from, "reviewed")
		}},
		{name: "StopTask", run: func(s *store.Store, from store.TaskBuild) error {
			to := from
			to.Held = "stopped"
			return s.StopTask(ctx, buildEpic, 1, from, to)
		}},
		{name: "ParkTask", run: func(s *store.Store, from store.TaskBuild) error {
			to := from
			to.Held = "parked"
			return s.ParkTask(ctx, buildEpic, 1, from, to, "late park", writtenAt())
		}},
	}
}

func TestTaskWritesOnAStaleBuildReturnErrTaskMovedAndWriteNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, write := range movedWrites() {
		t.Run(write.name, func(t *testing.T) {
			t.Parallel()
			s, raw := openBuildStore(ctx, t)
			before := storedState(t, raw)

			// The stored build is zero, so an open build names a different one.
			err := write.run(s, openBuildOf())
			if !errors.Is(err, store.ErrTaskMoved) {
				t.Fatalf("%s with a stale build err = %v, want ErrTaskMoved", write.name, err)
			}
			if !reflect.DeepEqual(before, storedState(t, raw)) {
				t.Errorf("%s with a stale build changed the store", write.name)
			}
		})
	}
}

func TestTaskWritesOnADoneOrFailedTaskReturnErrTaskMovedAndWriteNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	closers := []struct {
		name  string
		close func(t *testing.T, s *store.Store)
	}{
		{name: "done", close: func(t *testing.T, s *store.Store) {
			t.Helper()
			if err := s.AcceptTask(ctx, buildEpic, 1, openBuildOf(), "reviewed"); err != nil {
				t.Fatalf("AcceptTask: %v", err)
			}
		}},
		{name: "failed", close: func(t *testing.T, s *store.Store) {
			t.Helper()
			if err := s.FailTask(ctx, buildEpic, 1, openBuildOf(), "red failed", writtenAt()); err != nil {
				t.Fatalf("FailTask: %v", err)
			}
		}},
	}
	for _, closer := range closers {
		for _, write := range movedWrites() {
			t.Run(closer.name+"/"+write.name, func(t *testing.T) {
				t.Parallel()
				s, raw := openBuildStore(ctx, t)
				if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, openBuildOf()); err != nil {
					t.Fatalf("SetTaskBuild: %v", err)
				}
				closer.close(t, s)
				// The stored build is the one the task closed with, so only its status can refuse the write.
				from := buildTaskOf(ctx, t, s).Build
				before := storedState(t, raw)

				err := write.run(s, from)
				if !errors.Is(err, store.ErrTaskMoved) {
					t.Fatalf("%s on a %s task err = %v, want ErrTaskMoved", write.name, closer.name, err)
				}
				if !reflect.DeepEqual(before, storedState(t, raw)) {
					t.Errorf("%s on a %s task changed the store", write.name, closer.name)
				}
			})
		}
	}
}
