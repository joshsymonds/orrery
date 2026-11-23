package build

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/testrunner"
)

// passRequestsReview is the kept return of a review gate that failed with one finding and requested two test runs: the
// first at revision rev-a, and the second at none, so it runs at the task's revision.
const passRequestsReview = `{"status":"done","findings":[{"id":"F1","claim":"the thing is wrong","citation":"R1",` +
	`"location":"thing.go:3","evidence":"line 3","verify_by":"run it"}],` +
	`"test_runner_requests":[{"command":"go test ./internal/thing","revision":"rev-a"},` +
	`{"command":"go vet ./internal/thing","revision":""}],"results":[{"result":"fails"}]}`

// passNoRequestsReview is the kept return of a review gate that failed with one finding and requested no test runs.
const passNoRequestsReview = `{"status":"done","findings":[{"id":"F1","claim":"the thing is wrong","citation":"R1",` +
	`"location":"thing.go:3","evidence":"line 3","verify_by":"run it"}],"test_runner_requests":[],` +
	`"results":[{"result":"fails"}]}`

// passTestRun returns the derived id of the test run step at position of the review of attempt 1 at the first gate,
// the review the fixture stores.
func passTestRun(position int64) string {
	return StepID(passEpic, 1, unitTestRun, 1, 0, 0, position)
}

// requestsFixture returns a fixture whose task's review gate, with verify on or off, has ended failed with kept return
// ret, and whose build is at that review, so the next pass stores the review. The test runner role runs under
// claude-tests.
func requestsFixture(t *testing.T, verify bool, ret string) *passFixture {
	t.Helper()
	review := agentReviewer("review")
	review.Verify = verify
	f := newPassFixtureWith(t, []reviewers.Reviewer{review})
	f.writeRoles("test_runner = \"claude-tests\"\n")
	f.pass()
	f.setGreen(1, evidence.VerdictHolds)
	f.setBuild(store.TaskBuild{
		Stage: StageReview, Base: f.base, Attempt: 1, Revision: f.base, GreenRun: passRunID(StageGreen, 1),
	})
	f.setStep(passStep(StageReview, 1, 0), endGateFailed, ret)
	return f
}

// expectTestRunStart fails the test unless start is the step of test run position: the test runner's agent, read-only
// and scratch in the epic's repository at revision, running command under its derived id.
func (f *passFixture) expectTestRunStart(start enginehost.StepStart, position int64, command, revision string) {
	f.t.Helper()
	want := testrunner.Prompt(testrunner.Request{Command: command, Revision: revision})
	if start.ID != passTestRun(position) || start.Agent != "claude-tests" || !start.ReadOnly || !start.Scratch ||
		start.Workspace != f.repo || start.Revision != revision || start.Prompt != want ||
		start.Epic != passEpic || start.Task != taskLabel(1) {
		f.t.Errorf("test run start %+v, want run %d of %q at %s read-only and scratch in %s under %s",
			start, position, command, revision, f.repo, passTestRun(position))
	}
}

// refusingTestRuns is a Steps that refuses the start of the step refused and starts every other step on steps.
type refusingTestRuns struct {
	refused string
	steps   *passSteps
}

func (f refusingTestRuns) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	if start.ID == f.refused {
		return "", errors.New("engine refused the run")
	}
	return f.steps.Start(ctx, start)
}

func (f refusingTestRuns) Status(ctx context.Context, id string) (store.StepRow, error) {
	return f.steps.Status(ctx, id)
}

// TestTestRunnerRequestsRunBeforeVerifier covers R3: a failed review's test runner requests each start one read-only
// scratch step on the test runner's agent, at its request's revision, in position order, and the verifier starts only
// once every run has its step.
func TestTestRunnerRequestsRunBeforeVerifier(t *testing.T) {
	t.Parallel()
	f := requestsFixture(t, true, passRequestsReview)
	f.pass()
	if len(f.steps.starts) != 1 {
		t.Fatalf("Start calls = %d after the review, want the first test run only", len(f.steps.starts))
	}
	f.expectTestRunStart(f.steps.starts[0], 0, "go test ./internal/thing", "rev-a")

	f.setStep(passTestRun(0), endDone, `{"exit_code":0,"summary":"passed","output":"ok"}`)
	f.pass()
	if len(f.steps.starts) != 2 {
		t.Fatalf("Start calls = %d, want the second test run after the first ended", len(f.steps.starts))
	}
	f.expectTestRunStart(f.steps.starts[1], 1, "go vet ./internal/thing", f.base)

	f.setStep(passTestRun(1), endDone, `{"exit_code":0,"summary":"clean","output":""}`)
	f.pass()
	if len(f.steps.starts) != 3 {
		t.Fatalf("Start calls = %d, want the verifier after both test runs", len(f.steps.starts))
	}
	if start := f.lastStart(); start.ID != passStep(StageVerify, 1, 0) || start.Agent != "claude-verify" {
		t.Errorf("verifier start %s on %s, want finding 0's verifier under its derived id", start.ID, start.Agent)
	}
	for i, run := range f.task().Reviews[0].TestRuns {
		if run.Step != passTestRun(int64(i)) {
			t.Errorf("test run %d step = %s, want %s", i, run.Step, passTestRun(int64(i)))
		}
	}
}

// TestTestRunnerRequestsOutputReachesVerifier covers R3: a test run's output is stored with the finding it checks, and
// the finding's verifier prompt carries it.
func TestTestRunnerRequestsOutputReachesVerifier(t *testing.T) {
	t.Parallel()
	f := requestsFixture(t, true, passRequestsReview)
	f.pass()
	f.setStep(passTestRun(0), endDone, `{"exit_code":1,"summary":"one failure","output":"--- FAIL: TestThing"}`)
	f.pass()
	f.setStep(passTestRun(1), endDone, `{"exit_code":0,"summary":"clean","output":"ok"}`)
	f.pass()

	const first = "exit 1: one failure\n--- FAIL: TestThing"
	task := f.task()
	if got := task.Reviews[0].TestRuns[0].Output; got != first {
		t.Errorf("stored output of test run 0 = %q, want %q", got, first)
	}
	if got := task.Reviews[0].Findings[0].TestRuns[0].Output; got != first {
		t.Errorf("output of test run 0 on the finding = %q, want %q", got, first)
	}
	verifier := f.lastStart()
	if verifier.ID != passStep(StageVerify, 1, 0) {
		t.Fatalf("last start %s, want the verifier of finding 0", verifier.ID)
	}
	block := "Command: go test ./internal/thing\nRevision: rev-a\nOutput: " + first
	if !strings.Contains(verifier.Prompt, testRunsHeading) || !strings.Contains(verifier.Prompt, block) {
		t.Errorf("verifier prompt does not carry test run 0's output %q:\n%s", block, verifier.Prompt)
	}
}

// TestTestRunnerRequestsNoneGoesStraightToVerifier: a failed review that requests no test runs starts its verifier
// with no test runner step before it.
func TestTestRunnerRequestsNoneGoesStraightToVerifier(t *testing.T) {
	t.Parallel()
	f := requestsFixture(t, true, passNoRequestsReview)
	f.pass()
	if len(f.steps.starts) != 1 {
		t.Fatalf("Start calls = %d, want the verifier alone", len(f.steps.starts))
	}
	if start := f.lastStart(); start.ID != passStep(StageVerify, 1, 0) {
		t.Errorf("start %s, want the verifier %s", start.ID, passStep(StageVerify, 1, 0))
	}
	if runs := f.task().Reviews[0].TestRuns; len(runs) != 0 {
		t.Errorf("test runs = %+v, want none", runs)
	}
}

// TestTestRunnerRequestsBlockedRunStoresReason: a test runner step that returned stores blocked with its reason as the
// run's output, and the build goes on to the next run.
func TestTestRunnerRequestsBlockedRunStoresReason(t *testing.T) {
	t.Parallel()
	f := requestsFixture(t, true, passRequestsReview)
	f.pass()
	f.steps.rows[passTestRun(0)] = store.StepRow{
		ID: passTestRun(0), End: endReturned, Reason: "the command needs a database",
	}
	f.pass()

	const blocked = "blocked: the command needs a database"
	if got := f.task().Reviews[0].TestRuns[0].Output; got != blocked {
		t.Errorf("stored output of the blocked run = %q, want %q", got, blocked)
	}
	if len(f.steps.starts) != 2 || f.lastStart().ID != passTestRun(1) {
		t.Errorf("Start calls = %d, want the second test run to start after the blocked one", len(f.steps.starts))
	}
}

// TestTestRunnerRequestsStartRefusedStoresNotRun: a test run the engine refuses to start stores why it did not run as
// its output, under its derived id, and the build goes on to the next run.
func TestTestRunnerRequestsStartRefusedStoresNotRun(t *testing.T) {
	t.Parallel()
	f := requestsFixture(t, true, passRequestsReview)
	f.runner = New(Config{
		Store: f.store, Steps: refusingTestRuns{refused: passTestRun(0), steps: f.steps}, Evidence: f.evidence,
		Getenv: f.getenv, StateDir: f.stateDir, Now: passClock,
	})
	f.pass()
	run := f.task().Reviews[0].TestRuns[0]
	if !strings.HasPrefix(run.Output, "not run: ") || !strings.Contains(run.Output, "engine refused the run") ||
		run.Step != passTestRun(0) {
		t.Fatalf("refused test run = %+v, want its derived step and the output not run: engine refused the run", run)
	}
	if len(f.steps.starts) != 1 || f.lastStart().ID != passTestRun(1) {
		t.Fatalf("Start calls = %d, want the second test run to start after the refused one", len(f.steps.starts))
	}
	f.setStep(passTestRun(1), endDone, `{"exit_code":0,"summary":"clean","output":""}`)
	f.pass()
	if start := f.lastStart(); start.ID != passStep(StageVerify, 1, 0) {
		t.Errorf("last start %s, want the verifier once every run has a step", start.ID)
	}
}

// TestTestRunnerRequestsVerifyOffStoresNone: with verify off the review's requests are not run and not stored.
func TestTestRunnerRequestsVerifyOffStoresNone(t *testing.T) {
	t.Parallel()
	f := requestsFixture(t, false, passRequestsReview)
	f.pass()
	if runs := f.task().Reviews[0].TestRuns; len(runs) != 0 {
		t.Errorf("test runs = %+v, want none with verify off", runs)
	}
	if f.startCount(passTestRun(0)) != 0 || f.startCount(passTestRun(1)) != 0 {
		t.Errorf("a test run started with verify off: starts = %+v", f.steps.starts)
	}
}
