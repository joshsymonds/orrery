package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// reviewBuildOf is the build of an open task whose review gate is running on its revision.
func reviewBuildOf() store.TaskBuild {
	return store.TaskBuild{Stage: "review", Base: buildBase, Attempt: 1, Review: "unreviewed"}
}

// verifyBuildOf is the build of an open task whose review gate has written its verdict and findings, with the first
// finding next to verify.
func verifyBuildOf() store.TaskBuild {
	return store.TaskBuild{Stage: "verify", Base: buildBase, Attempt: 1, Review: "unreviewed"}
}

// verifiedBuildOf is the build of an open task after its first finding has its result, with the second next.
func verifiedBuildOf() store.TaskBuild {
	return store.TaskBuild{
		Stage:      "verify",
		Base:       buildBase,
		Attempt:    1,
		Review:     "unreviewed",
		Step:       "verify-1-0-0",
		FindingPos: 1,
	}
}

// failedReviewOf is the review of gate 0 in attempt 1 of task 1 that fails and raises two findings in position order.
// Its findings have no verification yet.
func failedReviewOf() store.TaskReview {
	return store.TaskReview{
		Attempt: 1, Position: 0, Gate: "check", Step: "review-1-0", Verdict: "fails",
		Findings: []store.TaskFinding{
			{
				Position: 0,
				ID:       "F-1",
				Claim:    "first claim",
				Citation: "a.go:10",
				Location: "a.go",
				Evidence: "log one",
				VerifyBy: "read a.go",
			},
			{
				Position: 1,
				ID:       "F-2",
				Claim:    "second claim",
				Citation: "b.go:20",
				Location: "b.go",
				Evidence: "log two",
				VerifyBy: "read b.go",
			},
		},
	}
}

// storeFailedReview stores failedReviewOf as the review of task 1, with the build the review gate ran under and then
// verifyBuildOf as the build.
func storeFailedReview(ctx context.Context, t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, reviewBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	if err := s.AddTaskReview(ctx, buildEpic, 1, reviewBuildOf(), verifyBuildOf(), failedReviewOf()); err != nil {
		t.Fatalf("AddTaskReview: %v", err)
	}
}

// reviewStoredState returns the rows of the build, review and finding tables, so a test can compare the store before
// and after a write that must change nothing.
func reviewStoredState(t *testing.T, raw *sql.DB) map[string][][]any {
	t.Helper()
	state := storedState(t, raw)
	state["epic_task_reviews"] = rawRows(
		t,
		raw,
		`SELECT * FROM epic_task_reviews ORDER BY epic, task, attempt, position`,
	)
	state["epic_task_findings"] = rawRows(
		t,
		raw,
		`SELECT * FROM epic_task_findings ORDER BY epic, task, attempt, review, position`,
	)
	return state
}

func TestAddTaskReviewStoresReviewFindingsAndNextBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, reviewBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	if err := s.AddTaskReview(ctx, buildEpic, 1, reviewBuildOf(), verifyBuildOf(), failedReviewOf()); err != nil {
		t.Fatalf("AddTaskReview: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if task.Build != verifyBuildOf() {
		t.Errorf("task build = %+v, want %+v", task.Build, verifyBuildOf())
	}
	if want := []store.TaskReview{failedReviewOf()}; !reflect.DeepEqual(task.Reviews, want) {
		t.Errorf("task reviews = %+v, want %+v", task.Reviews, want)
	}
}

func TestAddTaskReviewStoresSettledFindings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, reviewBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	// With verify off, the findings of a failed review are stored confirmed and unverified.
	review := failedReviewOf()
	for i := range review.Findings {
		review.Findings[i].Step = "review-1-0"
		review.Findings[i].Result = "confirmed"
		review.Findings[i].Reason = "verify is off"
		review.Findings[i].Unverified = true
	}
	if err := s.AddTaskReview(ctx, buildEpic, 1, reviewBuildOf(), verifyBuildOf(), review); err != nil {
		t.Fatalf("AddTaskReview: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if want := []store.TaskReview{review}; !reflect.DeepEqual(task.Reviews, want) {
		t.Errorf("task reviews = %+v, want %+v", task.Reviews, want)
	}

	// A settled finding has its result, so verifying it again is refused.
	result := store.TaskFindingResult{
		Attempt:  1,
		Review:   0,
		Position: 0,
		Step:     "verify-1-0-0",
		Result:   "dropped",
		Reason:   "not in a.go",
	}
	err := s.SetFindingResult(ctx, buildEpic, 1, verifyBuildOf(), verifiedBuildOf(), result)
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("SetFindingResult of a settled finding err = %v, want ErrTaskMoved", err)
	}
}

func TestAddTaskReviewOnAStaleBuildWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	before := reviewStoredState(t, raw)

	// The stored build is zero, so a review build names a different one.
	err := s.AddTaskReview(ctx, buildEpic, 1, reviewBuildOf(), verifyBuildOf(), failedReviewOf())
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("AddTaskReview with a stale build err = %v, want ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, reviewStoredState(t, raw)) {
		t.Errorf("AddTaskReview with a stale build changed the store")
	}
}

func TestAddTaskReviewWithAVerdictOtherThanHoldsOrFailsWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	if err := s.SetTaskBuild(ctx, buildEpic, 1, store.TaskBuild{}, reviewBuildOf()); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	before := reviewStoredState(t, raw)

	// The build is current, so only the table's verdict CHECK can refuse the write.
	review := failedReviewOf()
	review.Verdict = "passes"
	err := s.AddTaskReview(ctx, buildEpic, 1, reviewBuildOf(), verifyBuildOf(), review)
	if err == nil {
		t.Fatalf("AddTaskReview with verdict passes err = nil, want the verdict CHECK refusal")
	}
	if errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("AddTaskReview with verdict passes err = %v, want the verdict CHECK refusal", err)
	}
	if !reflect.DeepEqual(before, reviewStoredState(t, raw)) {
		t.Errorf("AddTaskReview with verdict passes changed the store")
	}
}

func TestSetFindingResultStoresResultReasonAndNextBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openBuildStore(ctx, t)
	storeFailedReview(ctx, t, s)

	result := store.TaskFindingResult{
		Attempt:  1,
		Review:   0,
		Position: 0,
		Step:     "verify-1-0-0",
		Result:   "confirmed",
		Reason:   "seen in a.go",
	}
	if err := s.SetFindingResult(ctx, buildEpic, 1, verifyBuildOf(), verifiedBuildOf(), result); err != nil {
		t.Fatalf("SetFindingResult: %v", err)
	}

	task := buildTaskOf(ctx, t, s)
	if task.Build != verifiedBuildOf() {
		t.Errorf("task build = %+v, want %+v", task.Build, verifiedBuildOf())
	}
	want := failedReviewOf()
	want.Findings[0].Step = "verify-1-0-0"
	want.Findings[0].Result = "confirmed"
	want.Findings[0].Reason = "seen in a.go"
	if got := []store.TaskReview{want}; !reflect.DeepEqual(task.Reviews, got) {
		t.Errorf("task reviews = %+v, want %+v", task.Reviews, got)
	}
}

func TestSetFindingResultOnAStaleBuildWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	storeFailedReview(ctx, t, s)
	before := reviewStoredState(t, raw)

	// The stored build is verifyBuildOf, so reviewBuildOf is stale.
	result := store.TaskFindingResult{
		Attempt:  1,
		Review:   0,
		Position: 0,
		Step:     "verify-1-0-0",
		Result:   "confirmed",
		Reason:   "seen",
	}
	err := s.SetFindingResult(ctx, buildEpic, 1, reviewBuildOf(), verifiedBuildOf(), result)
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("SetFindingResult with a stale build err = %v, want ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, reviewStoredState(t, raw)) {
		t.Errorf("SetFindingResult with a stale build changed the store")
	}
}

func TestSetFindingResultTwiceOnTheSameFindingReturnsErrTaskMoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	storeFailedReview(ctx, t, s)
	first := store.TaskFindingResult{
		Attempt:  1,
		Review:   0,
		Position: 0,
		Step:     "verify-1-0-0",
		Result:   "confirmed",
		Reason:   "seen",
	}
	if err := s.SetFindingResult(ctx, buildEpic, 1, verifyBuildOf(), verifiedBuildOf(), first); err != nil {
		t.Fatalf("first SetFindingResult: %v", err)
	}
	before := reviewStoredState(t, raw)

	// The second write names the build the first left, so only the finding's own result can refuse it.
	later := verifiedBuildOf()
	later.FindingPos = 2
	second := store.TaskFindingResult{
		Attempt:  1,
		Review:   0,
		Position: 0,
		Step:     "verify-1-0-1",
		Result:   "dropped",
		Reason:   "not in a.go",
	}
	err := s.SetFindingResult(ctx, buildEpic, 1, verifiedBuildOf(), later, second)
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("second SetFindingResult err = %v, want ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, reviewStoredState(t, raw)) {
		t.Errorf("second SetFindingResult changed the store")
	}
}

func TestSetFindingResultOfAFindingNotStoredWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	storeFailedReview(ctx, t, s)
	before := reviewStoredState(t, raw)

	// The review stores positions 0 and 1 only, so the build is current and only the missing finding can refuse it.
	result := store.TaskFindingResult{
		Attempt:  1,
		Review:   0,
		Position: 2,
		Step:     "verify-1-0-2",
		Result:   "confirmed",
		Reason:   "seen",
	}
	err := s.SetFindingResult(ctx, buildEpic, 1, verifyBuildOf(), verifiedBuildOf(), result)
	if !errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("SetFindingResult of a finding not stored err = %v, want ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, reviewStoredState(t, raw)) {
		t.Errorf("SetFindingResult of a finding not stored changed the store")
	}
}

func TestSetFindingResultWithoutAResultWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openBuildStore(ctx, t)
	storeFailedReview(ctx, t, s)
	before := reviewStoredState(t, raw)

	// An empty result leaves the finding unverified, so the build must not move past it.
	result := store.TaskFindingResult{Attempt: 1, Review: 0, Position: 0, Step: "verify-1-0-0", Result: "", Reason: ""}
	err := s.SetFindingResult(ctx, buildEpic, 1, verifyBuildOf(), verifiedBuildOf(), result)
	if err == nil {
		t.Fatalf("SetFindingResult with an empty result err = nil, want a refusal")
	}
	if errors.Is(err, store.ErrTaskMoved) {
		t.Fatalf("SetFindingResult with an empty result err = %v, want a refusal that is not ErrTaskMoved", err)
	}
	if !reflect.DeepEqual(before, reviewStoredState(t, raw)) {
		t.Errorf("SetFindingResult with an empty result changed the store")
	}
}
