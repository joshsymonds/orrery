package build

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// The units a task's build runs, in order: red at its base, one implementer, green at its commit, then the review
// gates and the verification of their findings. StageOrchestrate is the unit an implementer's request runs between
// implement and the decision on it: one orchestrator step.
const (
	StageRed         = "red"
	StageImplement   = "implement"
	StageOrchestrate = "orchestrate"
	StageGreen       = "green"
	StageReview      = "review"
	StageVerify      = "verify"
)

// unitRepro is the unit a test item's green stage runs first: its reproduction at the commit, under the derived id of
// the unit.
const unitRepro = "repro"

// HeldStopped is a build the owner stopped, and HeldParked one that parked for the owner to answer. HeldWaiting is a
// build an orchestrator's decision holds until an integrate batch has taken each task it waits for, and HeldAsked one
// that waits on the owner's answer to the orchestrator's question.
const (
	HeldStopped = "stopped"
	HeldParked  = "parked"
	HeldWaiting = "waiting"
	HeldAsked   = "asked"
)

// ReviewUnreviewed is an accepted task whose review gates did not run, and ReviewReviewed one whose gates passed.
const (
	ReviewUnreviewed = "unreviewed"
	ReviewReviewed   = "reviewed"
)

// lastAttempt is the attempt a failed unit of the first attempt moves the build to, and the one whose failure fails
// the task.
const lastAttempt = 2

// noVerdictLimit is the count of units in a row that gave no verdict at which the task parks.
const noVerdictLimit = 2

// View is what Decide reads of one open task: the epic and whether it is paused or parked, its review gates, whether
// the reviewer at its gate position has verify off, the task, its branch, the epic's candidate, the step and evidence
// run of its current unit when they exist, the reproduction check of a test item's green unit when it exists, the
// primary checkout's HEAD, the ref moves the implementer's step made, and EnvironmentBefore, how many runs of the
// current unit just before its current rerun exited store.EnvironmentExit in a row, up to store.EnvironmentReruns.
// Returned is the implementer step whose end an orchestrate unit decides. Peers are the epic's tasks as a request reads
// them, read for an orchestrate unit whose step ended and for a waiting build. NewBase is the commit the next integrate
// batch composes on, which a waiting build moves onto, and Answered is whether the question a build asked is no longer
// open.
type View struct {
	Epic              string
	Paused            bool
	Parked            bool
	ReviewGates       []string
	NoVerify          bool
	Task              store.EpicTask
	Branch            string
	Candidate         string
	Step              *store.StepRow
	Run               *store.EvidenceRun
	Repro             *store.NodeCheck
	Head              string
	RefMoves          []RefMove
	EnvironmentBefore int64
	Returned          *store.StepRow
	Peers             []Peer
	NewBase           string
	Answered          bool
}

// ActionKind is what a pass does for a task.
type ActionKind string

// The actions a pass takes for a task. ActNone waits. ActMove stores Next as the build. ActGuard guards the
// implementer's commit before Next, the green build, is stored. ActSnapshot takes the refs snapshot the implementer's
// step is checked against, and ActRefs checks the step's ref moves. ActStartRun starts the evidence run of Phase, and
// ActStartStep the step of Unit. ActRepro starts the reproduction check of a test item's green unit under ID at
// Revision. ActFail, ActAccept, ActPause and ActPark end the task's build. ActReview stores a review and ActFinding a
// finding's verification. ActGrant grants an implementer the files it asked for. ActDecide carries out an
// orchestrator's rerun or grant, and ActAsk its question to the owner. ActResume moves a waiting build's worktree onto
// Revision, the new base, before Next is stored.
const (
	ActNone      ActionKind = "none"
	ActMove      ActionKind = "move"
	ActGuard     ActionKind = "guard"
	ActSnapshot  ActionKind = "snapshot"
	ActRefs      ActionKind = "refs"
	ActStartRun  ActionKind = "start_run"
	ActStartStep ActionKind = "start_step"
	ActRepro     ActionKind = "repro"
	ActFail      ActionKind = "fail"
	ActAccept    ActionKind = "accept"
	ActPause     ActionKind = "pause"
	ActPark      ActionKind = "park"
	ActReview    ActionKind = "review"
	ActFinding   ActionKind = "finding"
	ActGrant     ActionKind = "grant"
	ActDecide    ActionKind = "decide"
	ActAsk       ActionKind = "ask"
	ActResume    ActionKind = "resume"
)

// Action is one thing a pass does for a task. Next is the build the task moves to. Reason is what a fail or a park
// carries, the text of a parking move, or the review an accept stores. NeedsFiles is what a fail carries as the files
// a later task may need: the paths an implementer that returned needs_context named. Phase and ID name the evidence
// run or step to start, Revision the revision it runs at, and Unit the step's unit. Review and Finding are what a
// review or finding action stores, and Tells are the texts the owner is told. Decision is the epic decision that
// records an orchestrator's decision, written with the action. Behind is the tasks a decided build waits for, whose
// ownership of a granted file does not refuse it. Question is the owner question an ask opens, with Reason as the tell
// that carries it. Fallback is what a decided grant does instead when the store refuses one of its files.
type Action struct {
	Kind       ActionKind
	Next       store.TaskBuild
	Reason     string
	NeedsFiles []string
	Phase      string
	ID         string
	Revision   string
	Unit       string
	Review     store.TaskReview
	Finding    store.TaskFindingResult
	Tells      []string
	Decision   *store.TaskDecision
	Behind     []int64
	Question   *store.EpicQuestionInput
	Fallback   *Action
}

// spend returns the action for a unit of the build that failed with failure. The first attempt moves the build to the
// second, at implement, with the failure and no step, revision, refs or green run. The second attempt fails the task.
func spend(view View, failure string) Action {
	if view.Task.Build.Attempt < lastAttempt {
		next := view.Task.Build
		next.Stage = StageImplement
		next.Attempt = lastAttempt
		next.Rerun = 0
		next.NoVerdict = 0
		next.Held = ""
		next.Step = ""
		next.Revision = ""
		next.Refs = ""
		next.GreenRun = ""
		next.Failure = failure
		return Action{Kind: ActMove, Next: next}
	}
	return Action{Kind: ActFail, Reason: failure}
}

// noVerdict returns the action for unit, a judge, review or verifier that gave no verdict for reason. Its first no
// verdict reruns the unit. Its second parks the task held parked, with the reason the owner is told.
func noVerdict(view View, unit, reason string) Action {
	next := view.Task.Build
	next.NoVerdict++
	if next.NoVerdict < noVerdictLimit {
		next.Rerun++
		return Action{Kind: ActMove, Next: next}
	}
	next.Held = HeldParked
	return Action{
		Kind:   ActPark,
		Next:   next,
		Reason: fmt.Sprintf("%s %s gave no verdict twice: %s", taskLabel(view.Task.Seq), unit, reason),
	}
}

// environmentRerun returns the action for a run of unit, a red capture, a green capture or a reproduction, that exited
// store.EnvironmentExit without timing out. A run that is the first or second of its environment exits in a row reruns
// the unit under its next rerun. The third parks the task held parked, as noVerdict does, with a reason that names the
// task's capture and says the exit was an environment result.
func environmentRerun(view View, unit string) Action {
	next := view.Task.Build
	if view.EnvironmentBefore < store.EnvironmentReruns {
		next.Rerun++
		return Action{Kind: ActMove, Next: next}
	}
	next.Held = HeldParked
	return Action{
		Kind: ActPark,
		Next: next,
		Reason: fmt.Sprintf(
			"%s %s %s exited %d in %d runs in a row: an environment result, not a verdict",
			taskLabel(view.Task.Seq), unit, strings.Join(view.Task.Evidence.Capture, " "),
			store.EnvironmentExit, store.EnvironmentReruns+1,
		),
	}
}

// Store is the store a Runner reads and moves tasks through.
type Store interface {
	BuildEpics(ctx context.Context) ([]string, error)
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	Epics(ctx context.Context) ([]store.EpicRow, error)
	EvidenceRun(ctx context.Context, id string) (store.EvidenceRun, error)
	NodeCheck(ctx context.Context, id string) (store.NodeCheck, error)
	SetTaskBuild(ctx context.Context, epic string, seq int64, from, to store.TaskBuild) error
	SetTaskBuildTelling(
		ctx context.Context, epic string, seq int64, from, to store.TaskBuild, tells []string, at time.Time,
	) error
	FailTask(
		ctx context.Context, epic string, seq int64, from store.TaskBuild, reason string, needsFiles []string,
		at time.Time,
	) error
	FailTaskDecided(
		ctx context.Context, epic string, seq int64, from store.TaskBuild, reason string, needsFiles []string,
		decision store.TaskDecision, at time.Time,
	) error
	GrantTaskFiles(ctx context.Context, epic string, seq int64, from, to store.TaskBuild, files []string) error
	RerunTask(
		ctx context.Context, epic string, seq int64, from, to store.TaskBuild, files []string, behind []int64,
		decision store.TaskDecision, at time.Time,
	) error
	AskTask(
		ctx context.Context, epic string, seq int64, from, to store.TaskBuild, question store.EpicQuestionInput,
		tell string, decision store.TaskDecision, at time.Time,
	) error
	NodeRuns(ctx context.Context, epic string) ([]store.NodeRun, error)
	LabelledSteps(ctx context.Context, epic, task string) ([]store.StepRow, error)
	AcceptTask(ctx context.Context, epic string, seq int64, from store.TaskBuild, review string) error
	StopTask(ctx context.Context, epic string, seq int64, from, to store.TaskBuild) error
	ParkTask(
		ctx context.Context, epic string, seq int64, from, to store.TaskBuild, class store.ParkClass, reason string,
		at time.Time, tells ...string,
	) error
	ParkEpic(ctx context.Context, epic string, class store.ParkClass, reason string, at time.Time) error
	SetEpicBranch(ctx context.Context, epic, state string) error
	AddTaskReview(ctx context.Context, epic string, seq int64, from, to store.TaskBuild, review store.TaskReview) error
	SetFindingResult(
		ctx context.Context, epic string, seq int64, from, to store.TaskBuild, result store.TaskFindingResult,
	) error
}

// Steps starts the steps of a task's units and reads their rows.
type Steps interface {
	Start(ctx context.Context, start enginehost.StepStart) (string, error)
	Status(ctx context.Context, id string) (store.StepRow, error)
}

// Evidence starts a task's evidence runs and reads their artifacts and stderr logs.
type Evidence interface {
	RunTask(ctx context.Context, id, epic string, task int64, revision, phase string) (store.EvidenceRun, error)
	Artifact(ctx context.Context, run, name string) ([]byte, string, error)
	StderrLog(ctx context.Context, run string) ([]byte, string, error)
}

// Checks runs the reproduction check of a test item's green unit, once per id. *enginehost.NodeChecks satisfies it.
type Checks interface {
	Run(ctx context.Context, check store.NodeCheck) (store.NodeCheck, error)
}

// Git makes the writes a Runner makes to a repository: the epic's branch create, a pin of a ref, a task worktree add,
// the move of a task's worktree onto a new base, and the removal of a task's worktrees.
type Git interface {
	CreateBranch(ctx context.Context, repo, epic, base string) error
	Pin(ctx context.Context, repo, ref, commit string) error
	AddWorktree(ctx context.Context, repo, path, base string) error
	MoveWorktree(ctx context.Context, path, wip, to string) error
	RemoveWorktrees(ctx context.Context, repo, dir string) error
}

// packageGit makes the writes of Git through the package's own functions.
type packageGit struct{}

func (packageGit) CreateBranch(ctx context.Context, repo, epic, base string) error {
	return CreateBranch(ctx, repo, epic, base)
}

func (packageGit) Pin(ctx context.Context, repo, ref, commit string) error {
	return Pin(ctx, repo, ref, commit)
}

func (packageGit) AddWorktree(ctx context.Context, repo, path, base string) error {
	return AddWorktree(ctx, repo, path, base)
}

func (packageGit) MoveWorktree(ctx context.Context, path, wip, to string) error {
	return MoveWorktree(ctx, path, wip, to)
}

func (packageGit) RemoveWorktrees(ctx context.Context, repo, dir string) error {
	return RemoveWorktrees(ctx, repo, dir)
}

// Config configures a Runner. A Tick that is not positive means five seconds, and a nil Now means time.Now. A nil
// Logger discards, and a nil Getenv means os.Getenv. StateDir is the directory the task worktrees live under. A nil Git
// makes the repository writes through the package's own functions. Checks runs the reproductions of test items, and a
// test item whose green unit needs one has no reproduction without it.
type Config struct {
	Store    Store
	Steps    Steps
	Evidence Evidence
	Checks   Checks
	Getenv   func(string) string
	StateDir string
	Git      Git
	Tick     time.Duration
	Now      func() time.Time
	Logger   *slog.Logger
}

// Runner moves the builds of the tasks of accepted epics, one pass per tick.
type Runner struct {
	store    Store
	steps    Steps
	evidence Evidence
	checks   Checks
	getenv   func(string) string
	stateDir string
	git      Git
	tick     time.Duration
	now      func() time.Time
	logger   *slog.Logger
}

// defaultTick is how often a Runner passes when its Config names no positive tick.
const defaultTick = 5 * time.Second

// New returns a Runner for cfg, with the defaults Config documents filled in.
func New(cfg Config) *Runner {
	runner := &Runner{
		store: cfg.Store, steps: cfg.Steps, evidence: cfg.Evidence, checks: cfg.Checks, getenv: cfg.Getenv,
		stateDir: cfg.StateDir, git: cfg.Git, tick: cfg.Tick, now: cfg.Now, logger: cfg.Logger,
	}
	if runner.git == nil {
		runner.git = packageGit{}
	}
	if runner.tick <= 0 {
		runner.tick = defaultTick
	}
	if runner.getenv == nil {
		runner.getenv = os.Getenv
	}
	if runner.now == nil {
		runner.now = time.Now
	}
	if runner.logger == nil {
		runner.logger = slog.New(slog.DiscardHandler)
	}
	return runner
}
