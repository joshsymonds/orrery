package enginehost

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// The synthetic ids and revisions the build tests use. Run and step ids are 32 lowercase hex digits, as the engine
// derives them.
const (
	buildRedRunID     = "0123456789abcdef0123456789abcdef"
	buildGreenRunID   = "fedcba9876543210fedcba9876543210"
	buildJudgeStepID  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	buildImplementID  = "dddddddddddddddddddddddddddddddd"
	buildReviewStepID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	buildVerifyStepID = "cccccccccccccccccccccccccccccccc"
	buildBaseRevision = "1111111111111111111111111111111111111111"
	buildHeadRevision = "2222222222222222222222222222222222222222"
)

// plannedBuildEpic stores epicServiceID with its two planned tasks, T1 and T2, and returns the store and an Epics
// client that serves it.
func plannedBuildEpic(t *testing.T) (*store.Store, enginev1connect.EpicsClient) {
	t.Helper()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = st.CompleteTurn(ctx, epicServiceID, turn.Seq, plannedReturn(), time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	return st, client
}

// taskWithID returns the task of epic with id, failing the test when the epic holds no such task.
func taskWithID(t *testing.T, epic *v1.StoredEpic, id string) *v1.EpicTask {
	t.Helper()
	for _, task := range epic.GetTasks() {
		if task.GetId() == id {
			return task
		}
	}
	t.Fatalf("epic has no task %s", id)
	return nil
}

// buildOfMessage returns the build fields the EpicTask message carries.
func buildOfMessage(task *v1.EpicTask) store.TaskBuild {
	return store.TaskBuild{
		Stage: task.GetStage(), Base: task.GetBase(), Attempt: task.GetAttempt(), Held: task.GetHeld(),
		Step: task.GetStep(), Revision: task.GetRevision(), Failure: task.GetFailure(),
	}
}

// recordBuildRun stores an evidence run of task T1 with id for phase at buildHeadRevision, captured with exit, and
// judged with verdict and passed. Its spec is the evidence plannedReturn stored for task 1, and its one artifact is
// missing.
func recordBuildRun(t *testing.T, st *store.Store, id, phase string, exit int, verdict string, passed bool) {
	t.Helper()
	ctx := t.Context()
	if err := st.AddEvidenceRun(ctx, store.EvidenceRun{
		ID: id, Epic: epicServiceID, Item: "T1", Revision: buildHeadRevision, Phase: phase, Source: "captured",
		Spec: plannedReturn().Tasks[0].Evidence, Created: time.Unix(turnStarted, 0),
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", phase, err)
	}
	if err := st.RecordEvidenceCapture(ctx, id, store.EvidenceCapture{
		Exit: exit, LogBytes: 12, Artifacts: []store.EvidenceArtifact{{Path: "out/one.txt", State: "missing"}},
		At: time.Unix(turnCompleted, 0),
	}); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", phase, err)
	}
	if err := st.SetEvidenceJudge(ctx, id, buildJudgeStepID); err != nil {
		t.Fatalf("SetEvidenceJudge %s: %v", phase, err)
	}
	if err := st.RecordEvidenceVerdict(ctx, id, store.EvidenceVerdict{
		Verdict: verdict, Reason: "Synthetic reason.", Passed: passed, At: time.Unix(turnFreed, 0),
	}); err != nil {
		t.Fatalf("RecordEvidenceVerdict %s: %v", phase, err)
	}
}

func TestEpicGetShowsTaskBuild(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, client := plannedBuildEpic(t)
	build := store.TaskBuild{
		Stage: "implement", Base: buildBaseRevision, Attempt: 2, Held: "stopped", Step: buildImplementID,
		Revision: buildHeadRevision, Failure: "red: the conclusion holds at the base",
	}
	if err := st.SetTaskBuild(ctx, epicServiceID, 1, store.TaskBuild{}, build); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	epic := getEpicOK(t, client)
	if got := buildOfMessage(taskWithID(t, epic, "T1")); got != build {
		t.Fatalf("T1 build = %+v, want %+v", got, build)
	}
	if got := buildOfMessage(taskWithID(t, epic, "T2")); got != (store.TaskBuild{}) {
		t.Fatalf("T2 build = %+v, want the zero build of an unbuilt task", got)
	}
}

func TestEpicGetShowsTaskRedAndGreenRuns(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, client := plannedBuildEpic(t)
	recordBuildRun(t, st, buildRedRunID, "red", 1, "fails", true)
	recordBuildRun(t, st, buildGreenRunID, "green", 0, "holds", true)
	build := store.TaskBuild{
		Stage: "review", Base: buildBaseRevision, Attempt: 1, RedRun: buildRedRunID, GreenRun: buildGreenRunID,
	}
	if err := st.SetTaskBuild(ctx, epicServiceID, 1, store.TaskBuild{}, build); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}

	epic := getEpicOK(t, client)
	red := taskWithID(t, epic, "T1").GetRed()
	if red.GetId() != buildRedRunID || red.GetPhase() != "red" || red.GetExit() != 1 ||
		red.GetVerdict() != "fails" || !red.GetPassed() {
		t.Fatalf("T1 red = %v, want run %s in phase red, exit 1, verdict fails, passed", red, buildRedRunID)
	}
	green := taskWithID(t, epic, "T1").GetGreen()
	if green.GetId() != buildGreenRunID || green.GetPhase() != "green" || green.GetExit() != 0 ||
		green.GetVerdict() != "holds" || !green.GetPassed() {
		t.Fatalf("T1 green = %v, want run %s in phase green, exit 0, verdict holds, passed", green, buildGreenRunID)
	}
	other := taskWithID(t, epic, "T2")
	if other.GetRed() != nil || other.GetGreen() != nil {
		t.Fatalf("T2 red and green = %v and %v, want none", other.GetRed(), other.GetGreen())
	}
}

func TestEpicGetShowsTaskReviewsAndFindings(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, client := plannedBuildEpic(t)
	review := store.TaskBuild{Stage: "review", Base: buildBaseRevision, Attempt: 1, Revision: buildHeadRevision}
	if err := st.SetTaskBuild(ctx, epicServiceID, 1, store.TaskBuild{}, review); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	if err := st.AddTaskReview(ctx, epicServiceID, 1, review, review, store.TaskReview{
		Attempt: 1, Position: 0, Gate: "review", Step: buildReviewStepID, Verdict: "fails",
		Findings: []store.TaskFinding{
			{Position: 0, ID: "F1", Claim: "Synthetic claim one.", Citation: "R1", Location: "a.go:10",
				Evidence: "Synthetic evidence one.", VerifyBy: "Synthetic check one."},
			{Position: 1, ID: "F2", Claim: "Synthetic claim two.", Citation: "R2", Location: "b.go:20",
				Evidence: "Synthetic evidence two.", VerifyBy: "Synthetic check two."},
		},
	}); err != nil {
		t.Fatalf("AddTaskReview: %v", err)
	}
	for _, result := range []store.TaskFindingResult{
		{Attempt: 1, Review: 0, Position: 0, Step: buildVerifyStepID, Result: "confirmed", Reason: "Synthetic reason one."},
		{Attempt: 1, Review: 0, Position: 1, Step: buildVerifyStepID, Result: "dropped", Reason: "Synthetic reason two."},
	} {
		if err := st.SetFindingResult(ctx, epicServiceID, 1, review, review, result); err != nil {
			t.Fatalf("SetFindingResult %d: %v", result.Position, err)
		}
	}
	if err := st.AddTaskReview(ctx, epicServiceID, 1, review, review, store.TaskReview{
		Attempt: 2, Position: 0, Gate: "review", Step: buildReviewStepID, Verdict: "holds",
	}); err != nil {
		t.Fatalf("AddTaskReview of attempt 2: %v", err)
	}
	if err := st.AddTaskReview(ctx, epicServiceID, 1, review, review, store.TaskReview{
		Attempt: 2, Position: 1, Gate: "review", Step: buildReviewStepID, Verdict: "undetermined",
		Reason: "Synthetic review step stopped.",
	}); err != nil {
		t.Fatalf("AddTaskReview of attempt 2 undetermined: %v", err)
	}
	if err := st.AcceptTask(ctx, epicServiceID, 1, review, "reviewed"); err != nil {
		t.Fatalf("AcceptTask: %v", err)
	}

	got := taskWithID(t, getEpicOK(t, client), "T1")
	if got.GetReview() != "reviewed" {
		t.Fatalf("T1 review = %q, want reviewed", got.GetReview())
	}
	wantProtos(t, "T1 reviews", got.GetReviews(), []*v1.EpicTaskReview{
		{
			Attempt: 1, Gate: "review", Step: buildReviewStepID, Verdict: "fails",
			Findings: []*v1.EpicTaskFinding{
				{
					Id: "F1", Claim: "Synthetic claim one.", Location: "a.go:10", Step: buildVerifyStepID,
					Result: "confirmed", Reason: "Synthetic reason one.",
				},
				{
					Id: "F2", Claim: "Synthetic claim two.", Location: "b.go:20", Step: buildVerifyStepID,
					Result: "dropped", Reason: "Synthetic reason two.",
				},
			},
		},
		{Attempt: 2, Gate: "review", Step: buildReviewStepID, Verdict: "holds"},
		{
			Attempt: 2, Gate: "review", Step: buildReviewStepID, Verdict: "undetermined",
			Reason: "Synthetic review step stopped.",
		},
	})
}

func TestEpicGetShowsAcceptedTaskWithoutReviewsAsUnreviewed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, client := plannedBuildEpic(t)
	build := store.TaskBuild{Stage: "green", Base: buildBaseRevision, Attempt: 1, Revision: buildHeadRevision}
	if err := st.SetTaskBuild(ctx, epicServiceID, 1, store.TaskBuild{}, build); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	if err := st.AcceptTask(ctx, epicServiceID, 1, build, "unreviewed"); err != nil {
		t.Fatalf("AcceptTask: %v", err)
	}

	got := taskWithID(t, getEpicOK(t, client), "T1")
	if got.GetStatus() != "done" || got.GetReview() != "unreviewed" || len(got.GetReviews()) != 0 {
		t.Fatalf("T1 status %q, review %q, %d reviews; want done, unreviewed, no reviews",
			got.GetStatus(), got.GetReview(), len(got.GetReviews()))
	}
}

func TestEpicGetReadsStoredBranchState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	if got := getEpicOK(t, client).GetBranch(); got != "" {
		t.Fatalf("branch = %q before any branch write, want empty", got)
	}
	for _, state := range []string{"creating", "created"} {
		if err := st.SetEpicBranch(ctx, epicServiceID, state); err != nil {
			t.Fatalf("SetEpicBranch %s: %v", state, err)
		}
		if got := getEpicOK(t, client).GetBranch(); got != state {
			t.Fatalf("branch = %q, want %q", got, state)
		}
	}
}

func TestEpicReadsRefuseRunExitOutOfRange(t *testing.T) {
	t.Parallel()
	const want = "task 1 red run: exit 1099511627776 is outside the range of int32"
	reads := []struct {
		name string
		call func(ctx context.Context, client enginev1connect.EpicsClient) error
	}{
		{name: "GetEpic", call: func(ctx context.Context, client enginev1connect.EpicsClient) error {
			_, err := client.GetEpic(ctx, connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID}))
			return err
		}},
		{name: "PauseEpic", call: func(ctx context.Context, client enginev1connect.EpicsClient) error {
			_, err := client.PauseEpic(ctx, connect.NewRequest(&v1.PauseEpicRequest{Id: epicServiceID}))
			return err
		}},
	}
	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			t.Parallel()
			st, client := plannedBuildEpic(t)
			recordBuildRun(t, st, buildRedRunID, "red", 1<<40, "fails", true)
			build := store.TaskBuild{Stage: "green", Base: buildBaseRevision, Attempt: 1, RedRun: buildRedRunID}
			if err := st.SetTaskBuild(t.Context(), epicServiceID, 1, store.TaskBuild{}, build); err != nil {
				t.Fatalf("SetTaskBuild: %v", err)
			}
			wantConnectError(t, read.call(t.Context(), client), connect.CodeInternal, want)
		})
	}
}
