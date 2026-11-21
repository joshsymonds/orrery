package build

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// featureReadFixture is the repository the feature read tests start steps in. stored is the epic's stored base B,
// candidate is commit C on B, and taskOnStored and taskOnCandidate are task commits on B and on C. unrelated is commit
// U, which shares no history with them and is the commit the checkout has checked out.
type featureReadFixture struct {
	repo            string
	stored          string
	candidate       string
	taskOnStored    string
	taskOnCandidate string
	unrelated       string
}

// newFeatureReadFixture makes the repository of a feature read test: a root commit B on main, commit C on B with main
// moved to it, a task commit on each of B and C, and the unrelated commit U, checked out detached.
func newFeatureReadFixture(t *testing.T) featureReadFixture {
	t.Helper()
	repo := buildRepo(t)
	fx := featureReadFixture{repo: repo, stored: buildHead(t, repo)}
	fx.candidate = buildCommit(t, repo)
	fx.taskOnStored = buildObject(t, repo, fx.stored)
	fx.taskOnCandidate = buildObject(t, repo, fx.candidate)
	fx.unrelated = buildRun(t, repo, "commit-tree", "HEAD^{tree}", "-m", "unrelated")
	buildRun(t, repo, "checkout", "-q", "--detach", fx.unrelated)
	return fx
}

// epic returns the epic whose repository is fx's, with StoredBase the stored base and Candidate the candidate, which
// is empty when candidate is "". Its one task-stage review gate runs the agent reviewer readoptReviewer names, not the
// floor reviewer, so a review start runs no guard check.
func (fx featureReadFixture) epic(candidate string) store.EpicRow {
	return store.EpicRow{
		ID:          "acme/feature",
		Repository:  fx.repo,
		StoredBase:  fx.stored,
		Candidate:   candidate,
		Title:       "Read the feature",
		ReviewGates: []string{"review"},
		Reviewers:   []reviewers.Reviewer{readoptReviewer()},
	}
}

// featureReadTask returns task 3 of epic at stage, built on the epic's candidate when it has one and otherwise on its
// stored base, with revision as the revision its build reached.
func featureReadTask(epic store.EpicRow, stage, revision string) store.EpicTask {
	base := epic.StoredBase
	if epic.Candidate != "" {
		base = epic.Candidate
	}
	return store.EpicTask{
		Seq:         3,
		Title:       "Read the feature",
		Deliverable: "The read-only steps start at the epic's code",
		Files:       []string{"internal/build/feature.go"},
		Gate:        "check",
		Build:       store.TaskBuild{Stage: stage, Base: base, Revision: revision, Attempt: 1},
	}
}

// featureReadFinding is a finding a review of task 3 raised, which a verifier step rules on.
func featureReadFinding() store.TaskFinding {
	return store.TaskFinding{
		ID:       "F1",
		Claim:    "the step starts at the checkout",
		Citation: "start.go",
		Location: "internal/build/start.go:1",
		Evidence: "the revision is the checkout's HEAD",
		VerifyBy: "rev-parse the start's revision",
	}
}

// featureReadStore is a Store with no runs and no steps, which are the two reads the orchestrator's start makes of it.
// Any other method panics on the nil Store it embeds.
type featureReadStore struct{ Store }

// NodeRuns returns no runs.
func (featureReadStore) NodeRuns(context.Context, string) ([]store.NodeRun, error) { return nil, nil }

// LabelledSteps returns no steps.
func (featureReadStore) LabelledSteps(context.Context, string, string) ([]store.StepRow, error) {
	return nil, nil
}

// featureReadRunner returns a Runner whose role file names the orchestrator and verifier agents.
func featureReadRunner(t *testing.T) *Runner {
	t.Helper()
	config := t.TempDir()
	writeReadoptFile(t, filepath.Join(config, "orrery", "roles.toml"),
		"orchestrator = \"claude-orch\"\nverifier = \"claude-verify\"\n")
	getenv := func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
	return New(Config{Store: featureReadStore{}, Getenv: getenv, StateDir: buildWorktreeRoot(t)})
}

// featureReadCheckStart fails t unless start is read-only and its revision resolves in fx's repository, with git
// rev-parse, to want. A revision that resolves to the checkout's HEAD is named as such.
func featureReadCheckStart(t *testing.T, fx featureReadFixture, start enginehost.StepStart, want string) {
	t.Helper()
	if !start.ReadOnly {
		t.Error("start ReadOnly = false, want a read-only step")
	}
	got := buildRun(t, fx.repo, "rev-parse", start.Revision)
	if got == fx.unrelated {
		t.Fatalf("start Revision %s resolves to %s, the checkout's HEAD, want %s", start.Revision, got, want)
	}
	if got != want {
		t.Fatalf("start Revision %s resolves to %s, want %s", start.Revision, got, want)
	}
}

// TestFeatureReadOrchestratorStartsAtTheCandidate: with a candidate, the orchestrator starts at the base of the task,
// which is the candidate, and not at the checkout's HEAD.
func TestFeatureReadOrchestratorStartsAtTheCandidate(t *testing.T) {
	t.Parallel()
	fx := newFeatureReadFixture(t)
	epic := fx.epic(fx.candidate)
	view := View{Epic: epic.ID, Task: featureReadTask(epic, StageOrchestrate, fx.taskOnCandidate)}
	start, err := featureReadRunner(t).orchestratorStart(context.Background(), epic, view, "orchestrate-3")
	if err != nil {
		t.Fatalf("orchestratorStart = %v, want nil", err)
	}
	featureReadCheckStart(t, fx, start, fx.candidate)
}

// TestFeatureReadOrchestratorStartsAtTheStoredBase: with no candidate, the orchestrator starts at the task's base,
// which is the stored base, and not at the checkout's HEAD.
func TestFeatureReadOrchestratorStartsAtTheStoredBase(t *testing.T) {
	t.Parallel()
	fx := newFeatureReadFixture(t)
	epic := fx.epic("")
	view := View{Epic: epic.ID, Task: featureReadTask(epic, StageOrchestrate, fx.taskOnStored)}
	start, err := featureReadRunner(t).orchestratorStart(context.Background(), epic, view, "orchestrate-3")
	if err != nil {
		t.Fatalf("orchestratorStart = %v, want nil", err)
	}
	featureReadCheckStart(t, fx, start, fx.stored)
}

// TestFeatureReadTaskReviewerStartsAtTheTaskCommitOnTheCandidate: with a candidate, the task reviewer starts at the
// task's revision, the task commit whose first parent is the candidate, and not at the checkout's HEAD.
func TestFeatureReadTaskReviewerStartsAtTheTaskCommitOnTheCandidate(t *testing.T) {
	t.Parallel()
	fx := newFeatureReadFixture(t)
	epic := fx.epic(fx.candidate)
	task := featureReadTask(epic, StageReview, fx.taskOnCandidate)
	start, err := featureReadRunner(t).reviewStart(context.Background(), epic, task, "review-3")
	if err != nil {
		t.Fatalf("reviewStart = %v, want nil", err)
	}
	featureReadCheckStart(t, fx, start, fx.taskOnCandidate)
}

// TestFeatureReadTaskReviewerStartsAtTheTaskCommitOnTheStoredBase: with no candidate, the task reviewer starts at the
// task's revision, the task commit whose first parent is the stored base, and not at the checkout's HEAD.
func TestFeatureReadTaskReviewerStartsAtTheTaskCommitOnTheStoredBase(t *testing.T) {
	t.Parallel()
	fx := newFeatureReadFixture(t)
	epic := fx.epic("")
	task := featureReadTask(epic, StageReview, fx.taskOnStored)
	start, err := featureReadRunner(t).reviewStart(context.Background(), epic, task, "review-3")
	if err != nil {
		t.Fatalf("reviewStart = %v, want nil", err)
	}
	featureReadCheckStart(t, fx, start, fx.taskOnStored)
}

// TestFeatureReadTaskVerifierStartsAtTheTaskCommitOnTheCandidate: with a candidate, the task verifier starts at the
// task's revision, the task commit on the candidate, and not at the checkout's HEAD.
func TestFeatureReadTaskVerifierStartsAtTheTaskCommitOnTheCandidate(t *testing.T) {
	t.Parallel()
	fx := newFeatureReadFixture(t)
	epic := fx.epic(fx.candidate)
	task := featureReadTask(epic, StageVerify, fx.taskOnCandidate)
	task.Reviews = []store.TaskReview{{Attempt: 1, Position: 0, Findings: []store.TaskFinding{featureReadFinding()}}}
	start, err := featureReadRunner(t).verifierStart(epic, View{Epic: epic.ID, Task: task}, "verify-3")
	if err != nil {
		t.Fatalf("verifierStart = %v, want nil", err)
	}
	featureReadCheckStart(t, fx, start, fx.taskOnCandidate)
}

// TestFeatureReadEpicVerifierStartsAtTheCandidate: the epic-stage verifier, which has no task, starts at the candidate
// it checks, and not at the checkout's HEAD.
func TestFeatureReadEpicVerifierStartsAtTheCandidate(t *testing.T) {
	t.Parallel()
	fx := newFeatureReadFixture(t)
	epic := fx.epic(fx.candidate)
	subject := Subject{Epic: epic, Base: fx.stored, Revision: fx.candidate}
	start := VerifierStart(subject, featureReadFinding(), "verify-epic", "claude-verify")
	featureReadCheckStart(t, fx, start, fx.candidate)
}
