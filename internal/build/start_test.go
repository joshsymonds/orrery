package build

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// startSteps is a Steps that records each start it is given and the error its context had when the start arrived. It
// returns the start's id, or err when it is set.
type startSteps struct {
	starts []enginehost.StepStart
	ctxErr error
	err    error
}

func (f *startSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	f.starts = append(f.starts, start)
	f.ctxErr = ctx.Err()
	if f.err != nil {
		return "", f.err
	}
	return start.ID, nil
}

func (f *startSteps) Status(_ context.Context, _ string) (store.StepRow, error) {
	return store.StepRow{}, errors.New("start tests read no step")
}

// startEvidence is an Evidence whose stdout log is log, or err when it is set, and whose stderr log is stderr, or
// stderrErr when it is set. It records the run each log is read from.
type startEvidence struct {
	log        []byte
	err        error
	runs       []string
	stderr     []byte
	stderrErr  error
	stderrRuns []string
}

func (f *startEvidence) RunTask(
	_ context.Context, _, _ string, _ int64, _, _ string,
) (store.EvidenceRun, error) {
	return store.EvidenceRun{}, errors.New("start tests run no evidence")
}

func (f *startEvidence) Artifact(_ context.Context, run, _ string) ([]byte, string, error) {
	f.runs = append(f.runs, run)
	return f.log, "", f.err
}

func (f *startEvidence) StderrLog(_ context.Context, run string) ([]byte, string, error) {
	f.stderrRuns = append(f.stderrRuns, run)
	return f.stderr, "", f.stderrErr
}

// startSetup is the epic, its base commit, the state directory and the environment the start tests run under. The role
// file at rolesPath names claude-impl as the implementer and claude-verify as the verifier.
type startSetup struct {
	epic      store.EpicRow
	base      string
	stateDir  string
	rolesPath string
	getenv    func(string) string
}

// newStartSetup returns a setup whose epic repository has one commit, with epicGates as its gates.
func newStartSetup(t *testing.T, epicGates ...store.EpicGate) startSetup {
	t.Helper()
	epic, homeEnv := readoptEpic(t, epicGates...)
	buildRun(t, epic.Repository, "commit", "-q", "--allow-empty", "-m", "base")
	config := t.TempDir()
	rolesPath := filepath.Join(config, "orrery", "roles.toml")
	writeReadoptFile(t, rolesPath, "implementer = \"claude-impl\"\nverifier = \"claude-verify\"\n")
	getenv := func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return config
		}
		return homeEnv(name)
	}
	return startSetup{
		epic:      epic,
		base:      buildHead(t, epic.Repository),
		stateDir:  buildWorktreeRoot(t),
		rolesPath: rolesPath,
		getenv:    getenv,
	}
}

// startTask is task 3 of an epic at attempt one, built at base, with a red run whose log is stored and one failure
// from the previous attempt.
func startTask(base string) store.EpicTask {
	return store.EpicTask{
		Seq:          3,
		Title:        "Start the steps",
		Deliverable:  "Steps start under derived ids",
		Files:        []string{"internal/build/start.go", "internal/build/start_test.go"},
		Requirements: []string{"R3"},
		Gate:         "check",
		Build:        store.TaskBuild{Stage: StageImplement, Base: base, Attempt: 1, Failure: "the red test passed"},
		Red:          &store.EvidenceRun{ID: "redrun", LogHash: "loghash"},
	}
}

// startRunner returns a Runner that starts steps on steps, reads red logs from evidence, and reads setup's roles.
func startRunner(setup startSetup, steps *startSteps, evidence *startEvidence) *Runner {
	return New(Config{Steps: steps, Evidence: evidence, Getenv: setup.getenv, StateDir: setup.stateDir})
}

// onlyStart returns the one start steps recorded, and fails the test when it recorded any other count.
func onlyStart(t *testing.T, steps *startSteps) enginehost.StepStart {
	t.Helper()
	if len(steps.starts) != 1 {
		t.Fatalf("Start calls = %d, want 1", len(steps.starts))
	}
	return steps.starts[0]
}

func TestStartImplementerCarriesDerivedIDRoleAgentFilesAndGate(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	id := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	got, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: id},
	)
	if err != nil {
		t.Fatalf("startStep(implement) = %v, want nil", err)
	}
	if got != id {
		t.Errorf("startStep(implement) id = %s, want %s", got, id)
	}
	start := onlyStart(t, steps)
	if start.ID != id {
		t.Errorf("start ID = %s, want the derived id %s", start.ID, id)
	}
	if start.Agent != "claude-impl" {
		t.Errorf("start Agent = %q, want the roles file's implementer agent claude-impl", start.Agent)
	}
	if start.ReadOnly {
		t.Error("start ReadOnly = true, want a writing step")
	}
	if !slices.Equal(start.Owned, task.Files) {
		t.Errorf("start Owned = %v, want the task's files %v", start.Owned, task.Files)
	}
	if start.CommandGate == nil || start.CommandGate.Name != "check" ||
		!slices.Equal(start.CommandGate.Argv, []string{"/opt/tools/run", "-c", "check"}) {
		t.Errorf("start CommandGate = %+v, want the epic's check command gate", start.CommandGate)
	}
	if start.Schema != ImplementerSchema {
		t.Error("start Schema is not ImplementerSchema")
	}
	if start.Revision != setup.base {
		t.Errorf("start Revision = %s, want the base %s", start.Revision, setup.base)
	}
	if start.DoneValue != "done" {
		t.Errorf("start DoneValue = %q, want done", start.DoneValue)
	}
	if start.Epic != setup.epic.ID || start.Task != "T3" {
		t.Errorf("start Epic, Task = %s, %s, want %s, T3", start.Epic, start.Task, setup.epic.ID)
	}
}

func TestStartImplementerWorktreeIsDetachedAtBaseUnderTaskDir(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	id := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	if _, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: id},
	); err != nil {
		t.Fatalf("startStep(implement) = %v, want nil", err)
	}
	want := ImplementWorktree(setup.stateDir, setup.epic.ID, task.Seq)
	start := onlyStart(t, steps)
	if start.Workspace != want {
		t.Fatalf("start Workspace = %s, want %s", start.Workspace, want)
	}
	if got := buildRun(t, want, "rev-parse", "HEAD"); got != setup.base {
		t.Errorf("worktree HEAD = %s, want the base %s", got, setup.base)
	}
	if got := buildRun(t, want, "rev-parse", "--symbolic-full-name", "HEAD"); got != "HEAD" {
		t.Errorf("worktree HEAD is attached to %s, want detached", got)
	}
	if !buildListsWorktree(t, setup.epic.Repository, want) {
		t.Errorf("git worktree list does not name %s", want)
	}
}

func TestStartImplementerPromptHoldsRedLogAndFailure(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	id := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	evidence := &startEvidence{log: []byte("FAIL TestStartNamesRedLog")}
	if _, err := startRunner(setup, steps, evidence).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: id},
	); err != nil {
		t.Fatalf("startStep(implement) = %v, want nil", err)
	}
	prompt := onlyStart(t, steps).Prompt
	if !strings.Contains(prompt, "FAIL TestStartNamesRedLog") {
		t.Errorf("prompt lacks the red log:\n%s", prompt)
	}
	if !strings.Contains(prompt, "From your previous try on this task: the red test passed") {
		t.Errorf("prompt lacks the previous attempt's failure:\n%s", prompt)
	}
	if !slices.Equal(evidence.runs, []string{"redrun"}) {
		t.Errorf("Artifact runs = %v, want the red run [redrun]", evidence.runs)
	}
}

func TestStartImplementerRedLogErrorLeavesLogEmpty(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	id := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	evidence := &startEvidence{err: errors.New("log is gone")}
	if _, err := startRunner(setup, steps, evidence).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: id},
	); err != nil {
		t.Fatalf("startStep(implement) with an unreadable red log = %v, want nil", err)
	}
	if prompt := onlyStart(t, steps).Prompt; !strings.Contains(prompt, "Red log (last 8 KiB):\n```\n\n```") {
		t.Errorf("prompt does not carry an empty red log:\n%s", prompt)
	}
}

func TestStartImplementerPromptHoldsRedStderrAfterRedLog(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	task.Red.StderrHash = "errhash"
	id := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	evidence := &startEvidence{
		log:    []byte("FAIL TestStartNamesRedLog"),
		stderr: []byte("panic: TestStartNamesRedStderr"),
	}
	if _, err := startRunner(setup, steps, evidence).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: id},
	); err != nil {
		t.Fatalf("startStep(implement) = %v, want nil", err)
	}
	prompt := onlyStart(t, steps).Prompt
	want := "Red log (last 8 KiB):\n```\nFAIL TestStartNamesRedLog\n```\n" +
		"Red stderr (last 8 KiB):\n```\npanic: TestStartNamesRedStderr\n```\n"
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt lacks the red stderr after the red log:\n%s", prompt)
	}
	if !slices.Equal(evidence.runs, []string{"redrun"}) {
		t.Errorf("Artifact runs = %v, want the red run [redrun]", evidence.runs)
	}
	if !slices.Equal(evidence.stderrRuns, []string{"redrun"}) {
		t.Errorf("StderrLog runs = %v, want the red run [redrun]", evidence.stderrRuns)
	}
}

func TestStartImplementerRedStderrErrorLeavesStderrBlockOut(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	task.Red.StderrHash = "errhash"
	id := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	evidence := &startEvidence{log: []byte("FAIL TestStartNamesRedLog"), stderrErr: errors.New("stderr log is gone")}
	var logs bytes.Buffer
	runner := New(Config{
		Steps: steps, Evidence: evidence, Getenv: setup.getenv, StateDir: setup.stateDir,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if _, err := runner.startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: id},
	); err != nil {
		t.Fatalf("startStep(implement) with an unreadable red stderr log = %v, want nil", err)
	}
	prompt := onlyStart(t, steps).Prompt
	if strings.Contains(prompt, "Red stderr") {
		t.Errorf("prompt carries a red stderr block after its read failed:\n%s", prompt)
	}
	if !strings.Contains(prompt, "FAIL TestStartNamesRedLog") {
		t.Errorf("prompt lacks the red log its stdout read returned:\n%s", prompt)
	}
	if !strings.Contains(logs.String(), "stderr log is gone") {
		t.Errorf("failed red stderr read was not logged:\n%s", logs.String())
	}
}

func TestStartImplementerRedRunWithoutStderrReadsNoStderr(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	id := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	evidence := &startEvidence{log: []byte("FAIL TestStartNamesRedLog")}
	if _, err := startRunner(setup, steps, evidence).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: id},
	); err != nil {
		t.Fatalf("startStep(implement) = %v, want nil", err)
	}
	if len(evidence.stderrRuns) != 0 {
		t.Errorf("StderrLog runs = %v for a red run with no stderr log, want none", evidence.stderrRuns)
	}
	if prompt := onlyStart(t, steps).Prompt; strings.Contains(prompt, "Red stderr") {
		t.Errorf("prompt carries a red stderr block for a red run with no stderr log:\n%s", prompt)
	}
}

func TestStartImplementerMissingRoleFileIsRoleError(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	if err := os.Remove(setup.rolesPath); err != nil {
		t.Fatal(err)
	}
	task := startTask(setup.base)
	steps := &startSteps{}
	_, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: "id"},
	)
	var roleErr RoleError
	if !errors.As(err, &roleErr) {
		t.Fatalf("startStep with no role file = %v, want a RoleError", err)
	}
	if len(steps.starts) != 0 {
		t.Errorf("Start calls = %d after a role file error, want none", len(steps.starts))
	}
}

func TestStartImplementerUnknownGateErrors(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	task.Gate = "missing"
	steps := &startSteps{}
	_, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: "id"},
	)
	if !errors.Is(err, gates.ErrUnknown) {
		t.Fatalf("startStep with an unknown gate = %v, want an error wrapping gates.ErrUnknown", err)
	}
	if len(steps.starts) != 0 {
		t.Errorf("Start calls = %d for an unknown gate, want none", len(steps.starts))
	}
}

func TestStartImplementerAgentGateErrors(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"), readoptReviewGate())
	task := startTask(setup.base)
	task.Gate = "review"
	steps := &startSteps{}
	_, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: "id"},
	)
	if err == nil || !strings.Contains(err.Error(), "is an agent gate, not a command gate") {
		t.Fatalf("startStep with an agent gate = %v, want an agent gate error", err)
	}
	if len(steps.starts) != 0 {
		t.Errorf("Start calls = %d for an agent gate, want none", len(steps.starts))
	}
}

func TestStartReturnsStartErrorAsIsSoCapReachedMatches(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	steps := &startSteps{err: enginehost.ErrCapReached}
	_, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: "id"},
	)
	if !errors.Is(err, enginehost.ErrCapReached) {
		t.Fatalf("startStep when Start is at the cap = %v, want an error matching ErrCapReached", err)
	}
}

// reviewTask is task 3 at the review of gate position gatePos of attempt one, built from base to revision.
func reviewTask(base, revision string, gatePos int64) store.EpicTask {
	task := startTask(base)
	task.Build = store.TaskBuild{Stage: StageReview, Base: base, Revision: revision, Attempt: 1, GatePos: gatePos}
	return task
}

// agentReviewer is an enabled task-stage reviewer named name that runs the reviewer agent and must hold the diff.
func agentReviewer(name string) reviewers.Reviewer {
	return reviewers.Reviewer{
		Name: name, Enabled: true, Stage: []string{reviewers.StageTask}, Agent: "reviewer", Limit: 60,
		Must: []string{"the diff is correct"}, Verify: true,
	}
}

func TestStartReviewIsReadOnlyAtRevisionWithReviewAgentGate(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	setup.epic.ReviewGates = []string{"review"}
	setup.epic.Reviewers = []reviewers.Reviewer{agentReviewer("review")}
	revision := buildCommit(t, setup.epic.Repository)
	task := reviewTask(setup.base, revision, 0)
	id := StepID(setup.epic.ID, task.Seq, StageReview, task.Build.Attempt, task.Build.Rerun, 0, 0)
	steps := &startSteps{}
	got, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageReview, ID: id},
	)
	if err != nil {
		t.Fatalf("startStep(review) = %v, want nil", err)
	}
	if got != id {
		t.Errorf("startStep(review) id = %s, want %s", got, id)
	}
	start := onlyStart(t, steps)
	if start.ID != id {
		t.Errorf("start ID = %s, want the derived id %s", start.ID, id)
	}
	if !start.ReadOnly {
		t.Error("review start ReadOnly = false, want a read-only step")
	}
	if start.Workspace != setup.epic.Repository {
		t.Errorf("review start Workspace = %s, want the repository %s", start.Workspace, setup.epic.Repository)
	}
	if start.Revision != revision {
		t.Errorf("review start Revision = %s, want %s", start.Revision, revision)
	}
	if start.AgentGate == nil || start.AgentGate.Name != "review" || start.AgentGate.Agent != "reviewer" {
		t.Errorf("review start AgentGate = %+v, want the epic's review agent gate", start.AgentGate)
	}
	if start.Agents != "" {
		t.Errorf("review start Agents = %s, want none for an agent reviewer", start.Agents)
	}
	if want := ReviewPrompt(setup.epic, task, "review", setup.base, revision, store.StepCheck{}); start.Prompt != want {
		t.Errorf("review start Prompt = %q, want %q", start.Prompt, want)
	}
	if start.Epic != setup.epic.ID || start.Task != "T3" {
		t.Errorf("review start Epic, Task = %s, %s, want %s, T3", start.Epic, start.Task, setup.epic.ID)
	}
}

// TestStartReviewWithoutTaskReviewerErrors covers R3: a review gate that no enabled task-stage reviewer of the epic
// is named by is an error, and no start reaches the engine. An epic command gate of the same name is not a reviewer.
func TestStartReviewWithoutTaskReviewerErrors(t *testing.T) {
	t.Parallel()
	epicStage := agentReviewer("review")
	epicStage.Stage = []string{reviewers.StageEpic}
	disabled := agentReviewer("review")
	disabled.Enabled = false
	cases := []struct {
		name      string
		gates     []store.EpicGate
		reviewers []reviewers.Reviewer
	}{
		{name: "missing", reviewers: nil},
		{name: "command gate", gates: []store.EpicGate{readoptCommandGate("review")}, reviewers: nil},
		{name: "epic stage", reviewers: []reviewers.Reviewer{epicStage}},
		{name: "disabled", reviewers: []reviewers.Reviewer{disabled}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			setup := newStartSetup(t, tc.gates...)
			setup.epic.ReviewGates = []string{"review"}
			setup.epic.Reviewers = tc.reviewers
			task := reviewTask(setup.base, setup.base, 0)
			steps := &startSteps{}
			_, err := startRunner(setup, steps, &startEvidence{}).startStep(
				context.Background(), setup.epic, View{Task: task},
				Action{Kind: ActStartStep, Unit: StageReview, ID: "id"},
			)
			if err == nil || !strings.Contains(err.Error(), "is not a task-stage reviewer") {
				t.Fatalf("startStep without a task-stage reviewer = %v, want a task-stage reviewer error", err)
			}
			if len(steps.starts) != 0 {
				t.Errorf("Start calls = %d without a task-stage reviewer, want none", len(steps.starts))
			}
		})
	}
}

func TestStartReviewGatePositionOutOfRangeErrors(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	setup.epic.ReviewGates = []string{"review"}
	setup.epic.Reviewers = []reviewers.Reviewer{agentReviewer("review")}
	task := reviewTask(setup.base, setup.base, 1)
	steps := &startSteps{}
	_, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageReview, ID: "id"},
	)
	if err == nil {
		t.Fatal("startStep with a review position past the epic's review gates = nil, want an error")
	}
	if len(steps.starts) != 0 {
		t.Errorf("Start calls = %d for a missing review gate, want none", len(steps.starts))
	}
}

// startFindingTask is task 3 at the verification of finding position 1 of the failed review of gate position 0, built
// from base to revision. The review raised two findings.
func startFindingTask(base, revision string) store.EpicTask {
	task := startTask(base)
	task.Build = store.TaskBuild{
		Stage: StageVerify, Base: base, Revision: revision, Attempt: 1, GatePos: 0, FindingPos: 1,
	}
	task.Reviews = []store.TaskReview{{
		Attempt: 1, Position: 0, Gate: "review", Step: "reviewstep", Verdict: verdictFails,
		Findings: []store.TaskFinding{
			{Position: 0, ID: "F1", Claim: "the first claim", Citation: "assertion 1", Location: "start.go:1",
				Evidence: "one", VerifyBy: "read line one"},
			{Position: 1, ID: "F2", Claim: "the second claim", Citation: "assertion 2", Location: "start.go:2",
				Evidence: "two", VerifyBy: "read line two"},
		},
	}}
	return task
}

func TestStartVerifierCarriesAgentSchemaCheckAndFinding(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	revision := buildCommit(t, setup.epic.Repository)
	task := startFindingTask(setup.base, revision)
	id := StepID(setup.epic.ID, task.Seq, StageVerify, 1, 0, 0, 1)
	steps := &startSteps{}
	got, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageVerify, ID: id},
	)
	if err != nil {
		t.Fatalf("startStep(verify) = %v, want nil", err)
	}
	if got != id {
		t.Errorf("startStep(verify) id = %s, want %s", got, id)
	}
	start := onlyStart(t, steps)
	if start.ID != id {
		t.Errorf("start ID = %s, want the derived id %s", start.ID, id)
	}
	if start.Agent != "claude-verify" {
		t.Errorf("verifier start Agent = %q, want the roles file's verifier agent claude-verify", start.Agent)
	}
	if !start.ReadOnly {
		t.Error("verifier start ReadOnly = false, want a read-only step")
	}
	if start.Workspace != setup.epic.Repository || start.Revision != revision {
		t.Errorf("verifier start Workspace, Revision = %s, %s, want %s, %s",
			start.Workspace, start.Revision, setup.epic.Repository, revision)
	}
	if start.Schema != VerifierSchema {
		t.Error("verifier start Schema is not VerifierSchema")
	}
	finding := task.Reviews[0].Findings[1]
	if want := VerifierPrompt(setup.epic, task, setup.base, revision, finding); start.Prompt != want {
		t.Errorf("verifier start Prompt = %q, want the prompt of finding F2", start.Prompt)
	}
	if start.DoneValue != "done" {
		t.Errorf("verifier start DoneValue = %q, want done", start.DoneValue)
	}
	if start.Epic != setup.epic.ID || start.Task != "T3" {
		t.Errorf("verifier start Epic, Task = %s, %s, want %s, T3", start.Epic, start.Task, setup.epic.ID)
	}
	if start.ReturnCheck == nil {
		t.Fatal("verifier start ReturnCheck = nil, want VerifierCheck")
	}
	one := json.RawMessage(`{"status":"done","results":[{"id":"F2","evidence":"e","result":"confirmed"}]}`)
	if msgs := start.ReturnCheck(one); len(msgs) != 0 {
		t.Errorf("ReturnCheck on one result = %v, want none", msgs)
	}
	none := json.RawMessage(`{"status":"done","results":[]}`)
	if msgs := start.ReturnCheck(none); len(msgs) == 0 {
		t.Error("ReturnCheck on no results = none, want a failure line")
	}
}

func TestStartHandsEngineUncanceledContextWhenRunnerContextIsCanceled(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	task := startFindingTask(setup.base, setup.base)
	id := StepID(setup.epic.ID, task.Seq, StageVerify, 1, 0, 0, 1)
	steps := &startSteps{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := startRunner(setup, steps, &startEvidence{}).startStep(
		ctx, setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageVerify, ID: id},
	)
	if err != nil {
		t.Fatalf("startStep with a canceled context = %v, want nil", err)
	}
	if got != id {
		t.Errorf("startStep with a canceled context id = %s, want %s", got, id)
	}
	onlyStart(t, steps)
	if steps.ctxErr != nil {
		t.Errorf("Start received a canceled context (%v), want one that runs the start to its end", steps.ctxErr)
	}
}

func TestStartVerifierMissingRoleFileIsRoleError(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	if err := os.Remove(setup.rolesPath); err != nil {
		t.Fatal(err)
	}
	task := startFindingTask(setup.base, setup.base)
	steps := &startSteps{}
	_, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageVerify, ID: "id"},
	)
	var roleErr RoleError
	if !errors.As(err, &roleErr) {
		t.Fatalf("verifier startStep with no role file = %v, want a RoleError", err)
	}
	if len(steps.starts) != 0 {
		t.Errorf("Start calls = %d after a role file error, want none", len(steps.starts))
	}
}

func TestStartVerifierFindingPositionOutOfRangeErrors(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	task := startFindingTask(setup.base, setup.base)
	task.Build.FindingPos = 2
	steps := &startSteps{}
	_, err := startRunner(setup, steps, &startEvidence{}).startStep(
		context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageVerify, ID: "id"},
	)
	if err == nil {
		t.Fatal("verifier startStep with a finding position past the review's findings = nil, want an error")
	}
	if len(steps.starts) != 0 {
		t.Errorf("Start calls = %d for a missing finding, want none", len(steps.starts))
	}
}

func TestStartImplementerLaterAttemptKeepsTheEarlierWork(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t, readoptCommandGate("check"))
	task := startTask(setup.base)
	steps := &startSteps{}
	runner := startRunner(setup, steps, &startEvidence{})
	first := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	if _, err := runner.startStep(context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: first}); err != nil {
		t.Fatalf("startStep(first attempt) = %v, want nil", err)
	}
	worktree := ImplementWorktree(setup.stateDir, setup.epic.ID, task.Seq)
	earlier := buildCommit(t, worktree)
	task.Build.Attempt = lastAttempt
	second := StepID(setup.epic.ID, task.Seq, StageImplement, task.Build.Attempt, task.Build.Rerun, 0, 0)
	if _, err := runner.startStep(context.Background(), setup.epic, View{Task: task},
		Action{Kind: ActStartStep, Unit: StageImplement, ID: second}); err != nil {
		t.Fatalf("startStep(second attempt) = %v, want nil", err)
	}
	start := steps.starts[len(steps.starts)-1]
	if start.Workspace != worktree || start.Revision != setup.base {
		t.Fatalf("second start in %s from %s, want %s checked from the base %s",
			start.Workspace, start.Revision, worktree, setup.base)
	}
	if got := buildRun(t, worktree, "rev-parse", "HEAD"); got != earlier {
		t.Errorf("worktree HEAD = %s, want the earlier attempt's commit %s", got, earlier)
	}
}
