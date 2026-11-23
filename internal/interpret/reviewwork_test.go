package interpret

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// The revisions the review tests run at: the revision a review freezes at, which is not the epic's base, and the stored
// base the review and closure prepares take. The agent is the one the verifier role names in the role file the tests write.
const (
	reviewWorkFrozen        = "frozen-rev"
	reviewWorkStored        = "stored-rev"
	reviewWorkVerifierAgent = "claude-verify"
)

// reviewWorkCall is one Start a reviewWorkReviewers received: the reviewer's name, the base, the revision, the Done
// gate checks and the base cost checks.
type reviewWorkCall struct {
	name, base, revision string
	doneGates, baseCost  []store.NodeCheck
}

// reviewWorkReviewers stands in for the reviewers service. It records each Start and returns the read-only step a
// reviewer runs at the revision it was given.
type reviewWorkReviewers struct {
	calls []reviewWorkCall
}

// Epic returns no reviewers: the review and closure starts do not read them.
func (*reviewWorkReviewers) Epic(store.EpicRow) []kinds.Reviewer {
	return nil
}

// Start records the call and returns the reviewer's step, which carries no id or labels.
func (r *reviewWorkReviewers) Start(
	_ context.Context, epic store.EpicRow, name, base, revision string, doneGates, baseCost []store.NodeCheck,
) (enginehost.StepStart, error) {
	r.calls = append(r.calls, reviewWorkCall{
		name: name, base: base, revision: revision, doneGates: doneGates, baseCost: baseCost,
	})
	return enginehost.StepStart{
		Agent: "reviewer-" + name, Prompt: "review as " + name, Schema: `{"type":"object"}`,
		Workspace: epic.Repository, Revision: revision, ReadOnly: true,
	}, nil
}

// reviewWorkSteps stands in for the step service. It starts each step as fakeSteps does, except that the start at
// refuseFrom (1-based) and every start after it is refused with err. A refused start is recorded too. A refuseFrom of 0
// refuses none.
type reviewWorkSteps struct {
	fakeSteps
	refuseFrom int
	err        error
}

// Start refuses the start with err when it is one the fake refuses, and starts it as fakeSteps does otherwise.
func (f *reviewWorkSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	if f.refuseFrom > 0 && len(f.calls)+1 >= f.refuseFrom {
		f.calls = append(f.calls, start)
		return "", f.err
	}
	return f.fakeSteps.Start(ctx, start)
}

// reviewWorkGetenv returns an environment whose role file, in a config directory of its own, names the verifier role's
// agent, so the tests read no machine's roles.
func reviewWorkGetenv(t *testing.T) func(string) string {
	t.Helper()
	config := t.TempDir()
	path := filepath.Join(config, "orrery", "roles.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("make role directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("verifier = \""+reviewWorkVerifierAgent+"\"\n"), 0o600); err != nil {
		t.Fatalf("write role file: %v", err)
	}
	return func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
}

// reviewWorkRunner returns a runner over s with the steps, reviewers and environment given, and the clock at runClock.
func reviewWorkRunner(s Store, steps Steps, reviewers Reviewers, getenv func(string) string) *Runner {
	return New(Config{
		Store: s, Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Steps: steps, Reviewers: reviewers,
		Ending: &recordingEnding{}, Getenv: getenv, Now: runClock,
	})
}

// reviewWorkStartRun starts the node run of kind nodeKind on the test epic and returns it as the store holds it. Its kind
// decides nothing, so the run stays running with no revision.
func reviewWorkStartRun(t *testing.T, s *store.Store, nodeKind string) store.NodeRun {
	t.Helper()
	runner := runnerReviewRunner(s, runnerReviewers{}, &recordingEnding{})
	runner.kinds[nodeKind] = runnerEnds(kinds.Action{Kind: kinds.ActNone})
	runner.passFlow(context.Background(), store.EpicRow{ID: runEpic}, runnerFlow(nodeKind))
	return runAt(t, s, nodeKind)
}

// reviewWorkFrozenRun starts the review run of the test epic and freezes its review at reviewWorkFrozen, and returns the
// run as the store holds it, at that revision.
func reviewWorkFrozenRun(t *testing.T, s *store.Store) store.NodeRun {
	t.Helper()
	run := reviewWorkStartRun(t, s, reviewNodeKind)
	if err := s.FreezeReview(
		context.Background(),
		runEpic,
		run.Path,
		run.Attempt,
		reviewWorkFrozen,
		runClock(),
	); err != nil {
		t.Fatalf("FreezeReview: %v", err)
	}
	return runAt(t, s, reviewNodeKind)
}

// reviewWorkComparable returns steps with each return check cleared. A deep comparison cannot compare the functions, so
// a test checks the check of a step itself and compares the rest.
func reviewWorkComparable(steps []enginehost.StepStart) []enginehost.StepStart {
	out := make([]enginehost.StepStart, 0, len(steps))
	for _, step := range steps {
		step.ReturnCheck = nil
		out = append(out, step)
	}
	return out
}

// TestReviewPrepareFreezesAtStoredBaseAndSetsReviewing: the prepare of a review run freezes the review at the epic's
// stored base, which is the revision the run records, and sets the epic reviewing.
func TestReviewPrepareFreezesAtStoredBaseAndSetsReviewing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	run := reviewWorkStartRun(t, s, reviewNodeKind)
	runner := reviewWorkRunner(s, nil, &reviewWorkReviewers{}, nil)
	epic := store.EpicRow{ID: runEpic, Repository: runRepo, Base: runBase, StoredBase: reviewWorkStored}
	act := Action{Kind: ActWork, Work: kinds.ActPrepare, Path: run.Path, NodeKind: reviewNodeKind, Run: run}
	if err := runner.work(ctx, epic, act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	if got := runAt(t, s, run.Path).Revision; got != reviewWorkStored {
		t.Errorf("review run revision = %q, want the stored base %q", got, reviewWorkStored)
	}
	review, err := s.EpicReview(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	if review.Frozen != reviewWorkStored {
		t.Errorf("frozen review = %q, want the stored base %q", review.Frozen, reviewWorkStored)
	}
	if phase := epicOf(t, s).Phase; phase != "reviewing" {
		t.Errorf("epic phase = %q, want reviewing", phase)
	}
}

// TestClosurePrepareRecordsStoredBaseWithNoRefs: the prepare of a closure run records the stored base as its revision,
// with no refs, so the closure run takes no ref compare.
func TestClosurePrepareRecordsStoredBaseWithNoRefs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, _ := nodeRepo(t)
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	run := reviewWorkStartRun(t, s, closureNodeKind)
	runner := reviewWorkRunner(s, nil, &reviewWorkReviewers{}, nil)
	// The repository is a real one, so refs computed for the run would not be empty.
	epic := store.EpicRow{ID: runEpic, Repository: repo, Base: runBase, StoredBase: reviewWorkStored}
	act := Action{Kind: ActWork, Work: kinds.ActPrepare, Path: run.Path, NodeKind: closureNodeKind, Run: run}
	if err := runner.work(ctx, epic, act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	got := runAt(t, s, run.Path)
	if got.Revision != reviewWorkStored {
		t.Errorf("closure run revision = %q, want the stored base %q", got.Revision, reviewWorkStored)
	}
	if got.Refs != "" {
		t.Errorf("closure run refs = %q, want none", got.Refs)
	}
}

// TestPrepareWithoutStoredBaseFailsAndWritesNothing: a review or closure prepare of an epic with no stored base has no
// revision to run at, so it returns an error and records no revision for its run.
func TestPrepareWithoutStoredBaseFailsAndWritesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, nodeKind string
	}{
		{name: "review", nodeKind: reviewNodeKind},
		{name: "closure", nodeKind: closureNodeKind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openRunStore(t)
			createRunEpic(t, s, store.EpicWorkflow{})
			run := reviewWorkStartRun(t, s, tc.nodeKind)
			runner := reviewWorkRunner(s, nil, &reviewWorkReviewers{}, nil)
			epic := store.EpicRow{ID: runEpic, Repository: runRepo, Base: runBase}
			act := Action{Kind: ActWork, Work: kinds.ActPrepare, Path: run.Path, NodeKind: tc.nodeKind, Run: run}
			if err := runner.work(ctx, epic, act, runClock()); err == nil {
				t.Error("work = nil, want an error for an epic with no stored base")
			}
			if got := runAt(t, s, run.Path).Revision; got != "" {
				t.Errorf("run revision = %q, want none", got)
			}
		})
	}
}

// TestReviewStartStartsReviewerAndVerifierUnits: the start of a review run starts its reviewer's step through the
// reviewers, at the base and the frozen revision, and its verifier's step over the base to the frozen revision. Each is
// started under its unit's id, labeled with the epic and the run's path, and the verifier's runs under the verifier role's
// agent.
func TestReviewStartStartsReviewerAndVerifierUnits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	run := reviewWorkFrozenRun(t, s)
	epic := epicOf(t, s)
	reviewers := &reviewWorkReviewers{}
	steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}}
	runner := reviewWorkRunner(s, steps, reviewers, reviewWorkGetenv(t))
	finding := runnerLedger()[0]
	reviewerID := workID(t, s, "review", "reviewer 1")
	verifyID := workID(t, s, "review", "verify 1 1")
	act := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: "review", NodeKind: reviewNodeKind, Run: run,
		IDs: []string{reviewerID, verifyID},
		Units: []kinds.Unit{
			{ID: reviewerID, Part: "reviewer 1", Reviewer: "security"},
			{ID: verifyID, Part: "verify 1 1", Reviewer: "security", Finding: finding},
		},
	}
	if err := runner.work(ctx, epic, act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	wantCalls := []reviewWorkCall{{name: "security", base: runBase, revision: reviewWorkFrozen}}
	if !reflect.DeepEqual(reviewers.calls, wantCalls) {
		t.Errorf("reviewer starts = %+v, want %+v", reviewers.calls, wantCalls)
	}
	if len(steps.calls) != 2 {
		t.Fatalf("step starts = %d, want 2", len(steps.calls))
	}
	if steps.calls[1].ReturnCheck == nil {
		t.Error("verifier step has no return check")
	}
	verify := epicVerifierStart(epic, runBase, reviewWorkFrozen, finding, verifyID, reviewWorkVerifierAgent)
	verify.Task = "review"
	want := []enginehost.StepStart{
		{
			ID: reviewerID, Agent: "reviewer-security", Prompt: "review as security", Schema: `{"type":"object"}`,
			Workspace: runRepo, Revision: reviewWorkFrozen, ReadOnly: true, Epic: runEpic, Task: "review",
		},
		verify,
	}
	if got := reviewWorkComparable(steps.calls); !reflect.DeepEqual(got, reviewWorkComparable(want)) {
		t.Errorf("step starts = %+v, want %+v", got, reviewWorkComparable(want))
	}
}

// TestReviewStartGivesTestParsimonyTheDoneGateChecksAtTheFrozenRevision: the test_parsimony reviewer's start carries the
// checks of the Done gates the integrate run ran at the frozen revision, with their kept output, so its prompt can carry
// their budget lines: the rerun that decided a gate, not the attempt that exited 75, a lost check or a check at another
// revision. Another reviewer's start carries none.
func TestReviewStartGivesTestParsimonyTheDoneGateChecksAtTheFrozenRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	if err := s.CreateEpic(ctx, store.EpicRow{
		ID: runEpic, Repository: runRepo, Base: runBase, Title: "Run the workflow", Phase: runPhase,
		Created: runClock(), Changed: runClock(), DoneGates: []string{"check"},
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	integrate := reviewWorkStartRun(t, s, "integrate")
	if err := s.PrepareNode(ctx, runEpic, integrate.Path, integrate.Attempt, reviewWorkFrozen, ""); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	// The gate's first attempt exited 75 and its rerun passed; a check lost at a restart, and the reproduction the run
	// ran at the epic's base, are not Done gate runs at the frozen revision.
	stdout := "test-budget: internal/store 212 cpu-s > 160 (+33%)\n"
	gateID := workID(t, s, integrate.Path, "done 1 rerun")
	for _, ran := range []struct {
		part, revision string
		end            store.NodeCheckEnd
	}{
		{"done 1", reviewWorkFrozen, store.NodeCheckEnd{Exit: store.EnvironmentExit, Stdout: "test-budget: environment\n"}},
		{"done 1 rerun", reviewWorkFrozen, store.NodeCheckEnd{Stdout: stdout}},
		{"done 2", reviewWorkFrozen, store.NodeCheckEnd{NotRun: store.LostAtRestart, Stdout: "test-budget: lost\n"}},
		{"repro", runBase, store.NodeCheckEnd{Exit: 1, Stdout: "test-budget: at the base\n"}},
	} {
		id := workID(t, s, integrate.Path, ran.part)
		if err := s.AddNodeCheck(ctx, store.NodeCheck{
			ID: id, Epic: runEpic, Path: integrate.Path, Revision: ran.revision, Argv: []string{"just", "check"},
		}, runClock()); err != nil {
			t.Fatalf("AddNodeCheck %s: %v", ran.part, err)
		}
		if err := s.EndNodeCheck(ctx, id, ran.end, runClock()); err != nil {
			t.Fatalf("EndNodeCheck %s: %v", ran.part, err)
		}
	}
	run := reviewWorkFrozenRun(t, s)
	reviewers := &reviewWorkReviewers{}
	steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}}
	runner := reviewWorkRunner(s, steps, reviewers, reviewWorkGetenv(t))
	parsimonyID := workID(t, s, "review", "reviewer 1")
	securityID := workID(t, s, "review", "reviewer 2")
	act := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: "review", NodeKind: reviewNodeKind, Run: run,
		IDs: []string{parsimonyID, securityID},
		Units: []kinds.Unit{
			{ID: parsimonyID, Part: "reviewer 1", Reviewer: "test_parsimony"},
			{ID: securityID, Part: "reviewer 2", Reviewer: "security"},
		},
	}
	if err := runner.work(ctx, epicOf(t, s), act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	if len(reviewers.calls) != 2 {
		t.Fatalf("reviewer starts = %+v, want 2", reviewers.calls)
	}
	gates := reviewers.calls[0].doneGates
	if len(gates) != 1 || gates[0].ID != gateID || gates[0].Stdout != stdout {
		t.Errorf("test_parsimony Done gate checks = %+v, want the check %s with stdout %q", gates, gateID, stdout)
	}
	if gates := reviewers.calls[1].doneGates; gates != nil {
		t.Errorf("security Done gate checks = %+v, want none", gates)
	}
}

// TestReviewStartReturnsStartErrors: a start error is returned, and the units after it are not started.
func TestReviewStartReturnsStartErrors(t *testing.T) {
	t.Parallel()
	other := errors.New("spawn claude: no such file")
	cases := []struct {
		name       string
		refuseFrom int
		err        error
		wantCalls  int
		wantErr    error
	}{
		{name: "error on the first unit", refuseFrom: 1, err: other, wantCalls: 1, wantErr: other},
		{name: "error on the second unit", refuseFrom: 2, err: other, wantCalls: 2, wantErr: other},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openRunStore(t)
			createRunEpic(t, s, store.EpicWorkflow{})
			run := reviewWorkFrozenRun(t, s)
			steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}, refuseFrom: tc.refuseFrom, err: tc.err}
			runner := reviewWorkRunner(s, steps, &reviewWorkReviewers{}, reviewWorkGetenv(t))
			finding := runnerLedger()[0]
			reviewerID := workID(t, s, "review", "reviewer 1")
			verifyOne := workID(t, s, "review", "verify 1 1")
			verifyTwo := workID(t, s, "review", "verify 1 2")
			act := Action{
				Kind: ActWork, Work: kinds.ActStart, Path: "review", NodeKind: reviewNodeKind, Run: run,
				IDs: []string{reviewerID, verifyOne, verifyTwo},
				Units: []kinds.Unit{
					{ID: reviewerID, Part: "reviewer 1", Reviewer: "security"},
					{ID: verifyOne, Part: "verify 1 1", Reviewer: "security", Finding: finding},
					{ID: verifyTwo, Part: "verify 1 2", Reviewer: "security", Finding: finding},
				},
			}
			err := runner.work(ctx, epicOf(t, s), act, runClock())
			switch {
			case tc.wantErr == nil && err != nil:
				t.Errorf("work = %v, want no error", err)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Errorf("work = %v, want an error matching %v", err, tc.wantErr)
			}
			if got := len(steps.calls); got != tc.wantCalls {
				t.Errorf("step starts = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

// TestClosureStartStartsCloseUnitAtStoredBaseWithFrozenReview: the start of a closure run starts its close unit through
// the closure verifier, over the frozen revision of the review to the closure run's revision, which is the stored base. It
// is started under its id, labeled with the epic and the run's path, under the verifier role's agent.
func TestClosureStartStartsCloseUnitAtStoredBaseWithFrozenReview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	reviewWorkFrozenRun(t, s)
	epic := epicOf(t, s)
	steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}}
	runner := reviewWorkRunner(s, steps, &reviewWorkReviewers{}, reviewWorkGetenv(t))
	finding := runnerLedger()[0]
	closeID := store.NodeWorkID("closure-run", 1, "close security-1")
	run := store.NodeRun{Path: "closure", Attempt: 1, Revision: runBase}
	act := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: "closure", NodeKind: closureNodeKind, Run: run,
		IDs:   []string{closeID},
		Units: []kinds.Unit{{ID: closeID, Part: "close security-1", Reviewer: "security", Finding: finding}},
	}
	if err := runner.work(ctx, epic, act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	if len(steps.calls) != 1 {
		t.Fatalf("step starts = %d, want 1", len(steps.calls))
	}
	if steps.calls[0].ReturnCheck == nil {
		t.Error("closure verifier step has no return check")
	}
	closure := closureStart(epic, reviewWorkFrozen, runBase, finding, closeID, reviewWorkVerifierAgent)
	closure.Task = "closure"
	want := []enginehost.StepStart{closure}
	if got := reviewWorkComparable(steps.calls); !reflect.DeepEqual(got, reviewWorkComparable(want)) {
		t.Errorf("step starts = %+v, want %+v", got, reviewWorkComparable(want))
	}
}

// TestClosureStartWithoutFrozenReviewStartsNothing: a closure start whose epic has no frozen review has no revision its
// closure verifier checks the change from, so it returns an error and starts no step.
func TestClosureStartWithoutFrozenReviewStartsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}}
	runner := reviewWorkRunner(s, steps, &reviewWorkReviewers{}, reviewWorkGetenv(t))
	finding := runnerLedger()[0]
	closeID := store.NodeWorkID("closure-run", 1, "close security-1")
	act := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: "closure", NodeKind: closureNodeKind,
		Run:   store.NodeRun{Path: "closure", Attempt: 1, Revision: runBase},
		IDs:   []string{closeID},
		Units: []kinds.Unit{{ID: closeID, Part: "close security-1", Reviewer: "security", Finding: finding}},
	}
	if err := runner.work(ctx, epicOf(t, s), act, runClock()); err == nil {
		t.Error("work = nil, want an error for an epic with no frozen review")
	}
	if got := len(steps.calls); got != 0 {
		t.Errorf("step starts = %d, want none", got)
	}
}

// TestReviewStartReadsTheChangeFromTheEpicsBase: the reviewers and verifiers of a review run start over the change from
// the epic's base at the frozen revision, so they read the epic's own change.
func TestReviewStartReadsTheChangeFromTheEpicsBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	run := reviewWorkFrozenRun(t, s)
	epic := epicOf(t, s)
	reviewers := &reviewWorkReviewers{}
	steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}}
	runner := reviewWorkRunner(s, steps, reviewers, reviewWorkGetenv(t))
	finding := runnerLedger()[0]
	reviewerID := workID(t, s, "review", "reviewer 1")
	verifyID := workID(t, s, "review", "verify 1 1")
	act := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: "review", NodeKind: reviewNodeKind, Run: run,
		IDs: []string{reviewerID, verifyID},
		Units: []kinds.Unit{
			{ID: reviewerID, Part: "reviewer 1", Reviewer: "security"},
			{ID: verifyID, Part: "verify 1 1", Reviewer: "security", Finding: finding},
		},
	}
	if err := runner.work(ctx, epic, act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	wantCalls := []reviewWorkCall{{name: "security", base: runBase, revision: reviewWorkFrozen}}
	if !reflect.DeepEqual(reviewers.calls, wantCalls) {
		t.Errorf("reviewer starts = %+v, want %+v", reviewers.calls, wantCalls)
	}
	if len(steps.calls) != 2 {
		t.Fatalf("step starts = %d, want 2", len(steps.calls))
	}
	verify := epicVerifierStart(epic, runBase, reviewWorkFrozen, finding, verifyID, reviewWorkVerifierAgent)
	verify.Task = "review"
	if got := reviewWorkComparable(steps.calls[1:]); !reflect.DeepEqual(got, reviewWorkComparable(
		[]enginehost.StepStart{verify},
	)) {
		t.Errorf("verifier start = %+v, want it over the change from %s", got, runBase)
	}
	if !strings.Contains(steps.calls[1].Prompt, runBase) {
		t.Errorf("verifier prompt = %q, want it to name %s", steps.calls[1].Prompt, runBase)
	}
}
