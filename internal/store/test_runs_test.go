package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// testRunsReviewOf is the failed review of failedReviewOf with two test runs, in position order, that have no result.
func testRunsReviewOf() store.TaskReview {
	review := failedReviewOf()
	review.TestRuns = []store.TaskTestRun{
		{Position: 0, Command: "go test ./internal/store", Revision: "abc1234"},
		{Position: 1, Command: "go vet ./...", Revision: "abc1234"},
	}
	return review
}

// storeTestRunsReview stores testRunsReviewOf as the review of task 1, with the build the review gate ran under and
// then verifyBuildOf as the build.
func storeTestRunsReview(ctx context.Context, t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, reviewBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	if err := s.AddTaskReview(ctx, buildEpic, 1, reviewBuildOf(), verifyBuildOf(), testRunsReviewOf()); err != nil {
		t.Fatalf("AddTaskReview: %v", err)
	}
}

// testRunStoredState returns the rows of the build and test run tables, so a test can compare the store before and
// after a write that must change nothing.
func testRunStoredState(t *testing.T, raw *sql.DB) map[string][][]any {
	t.Helper()
	state := storedState(t, raw)
	state["epic_task_test_runs"] = rawRows(
		t,
		raw,
		`SELECT * FROM epic_task_test_runs ORDER BY epic, task, generation, attempt, review, position`,
	)
	return state
}

func TestTestRunnerRequestsStoredWithReview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)

	storeTestRunsReview(ctx, t, s)

	// Each run is stored under the task's generation, the attempt and review it belongs to, and its position, with
	// an empty step and output.
	want := [][]any{
		{int64(0), int64(1), int64(0), int64(0), "go test ./internal/store", "abc1234", "", ""},
		{int64(0), int64(1), int64(0), int64(1), "go vet ./...", "abc1234", "", ""},
	}
	got := rawRows(
		t,
		raw,
		`SELECT generation, attempt, review, position, command, revision, step, output
		 FROM epic_task_test_runs ORDER BY attempt, review, position`,
	)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored test runs = %v, want %v", got, want)
	}
}

func TestTestRunnerRequestsResultStoredOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	storeTestRunsReview(ctx, t, s)

	// The first result stores the step and output on the run and moves the build.
	resultBuild := store.TaskBuild{
		Stage:   "verify",
		Base:    buildBase,
		Attempt: 1,
		Review:  "unreviewed",
		Step:    "test-1-0-0",
	}
	first := store.TaskTestRunResult{Attempt: 1, Review: 0, Position: 0, Step: "test-1-0-0", Output: "ok"}
	if err := s.SetTestRunResult(ctx, buildEpic, 1, verifyBuildOf(), resultBuild, first); err != nil {
		t.Fatalf("SetTestRunResult: %v", err)
	}
	wantRun := [][]any{
		{int64(0), int64(1), int64(0), int64(0), "go test ./internal/store", "abc1234", "test-1-0-0", "ok"},
	}
	storedRun := rawRows(
		t,
		raw,
		`SELECT generation, attempt, review, position, command, revision, step, output
		 FROM epic_task_test_runs WHERE position = 0`,
	)
	if !reflect.DeepEqual(storedRun, wantRun) {
		t.Fatalf("stored run after the first result = %v, want %v", storedRun, wantRun)
	}
	if task := buildTaskOf(ctx, t, s); task.Build != resultBuild {
		t.Fatalf("task build after the first result = %+v, want %+v", task.Build, resultBuild)
	}
	before := testRunStoredState(t, raw)

	// The run already has a step, so a second result is refused, and the build the write moves from is the stored
	// one, so only the run can refuse it: nothing is written, not even the build.
	second := store.TaskTestRunResult{Attempt: 1, Review: 0, Position: 0, Step: "test-1-0-1", Output: "changed"}
	err := s.SetTestRunResult(ctx, buildEpic, 1, resultBuild, verifyBuildOf(), second)
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("SetTestRunResult of a run with a step err = %v, want ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, testRunStoredState(t, raw)) {
		t.Errorf("SetTestRunResult of a run with a step changed the store")
	}
}

func TestTestRunnerRequestsReadWithFindings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	storeTestRunsReview(ctx, t, s)

	// The first run has a result and the second has none, so the read carries each run's own state. The review and
	// each of its findings carry the runs in position order.
	resultBuild := store.TaskBuild{
		Stage:   "verify",
		Base:    buildBase,
		Attempt: 1,
		Review:  "unreviewed",
		Step:    "test-1-0-0",
	}
	result := store.TaskTestRunResult{Attempt: 1, Review: 0, Position: 0, Step: "test-1-0-0", Output: "ok"}
	if err := s.SetTestRunResult(ctx, buildEpic, 1, verifyBuildOf(), resultBuild, result); err != nil {
		t.Fatalf("SetTestRunResult: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if len(task.Reviews) != 1 {
		t.Fatalf("task reviews = %+v, want one review", task.Reviews)
	}
	review := task.Reviews[0]
	wantRuns := []store.TaskTestRun{
		{Position: 0, Command: "go test ./internal/store", Revision: "abc1234", Step: "test-1-0-0", Output: "ok"},
		{Position: 1, Command: "go vet ./...", Revision: "abc1234"},
	}
	if !reflect.DeepEqual(review.TestRuns, wantRuns) {
		t.Errorf("review test runs = %+v, want %+v", review.TestRuns, wantRuns)
	}
	if len(review.Findings) != 2 {
		t.Fatalf("review findings = %+v, want two findings", review.Findings)
	}
	for i, finding := range review.Findings {
		if !reflect.DeepEqual(finding.TestRuns, wantRuns) {
			t.Errorf("finding %d test runs = %+v, want %+v", i, finding.TestRuns, wantRuns)
		}
	}
}
