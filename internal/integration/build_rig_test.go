package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// The build tests run a planned task's implementer step through the real orreryd, with the fake claude in place of
// claude. The epic is seeded in the store before orreryd starts. Its one task is at the implement stage at its base,
// so the first step the build starts is the implementer's. The roles file names the implementer's agent. The agent is
// a user agent under HOME, because the task worktree sits under the state directory, and no project folder above that
// worktree is under HOME.

// buildRigEpic is the epic the build tests run. Its repository id and slug are synthetic.
const buildRigEpic = "build-0123456/implement-e2e"

// buildRigRole is the role the roles file names the implementer under, and buildRigAgent is the agent the file names
// for it. The agent is not the role's name, so a start that ignored the roles file would run an agent the file does
// not name.
const (
	buildRigRole  = "implementer"
	buildRigAgent = "orrery-implementer"
)

// buildRigMarker is the word the fake claude answers with a done return. The task's deliverable carries it, so the
// implementer's brief is the prompt the fake answers.
const buildRigMarker = "STEPDONE"

// The synthetic texts the epic and its task carry. The brief test checks each of them in the prompt the fake reads.
const (
	buildRigTitle       = "Wire the build"
	buildRigDeliverable = "the build runs when the fake claude reads " + buildRigMarker
	buildRigFile        = "wiring.go"
	buildRigGate        = "check"
	buildRigRequirement = "the build runs"
	buildRigExclusion   = "Do not change the store schema"
	buildRigDecision    = "Use one implementer per task"
)

// buildRig is a repository under HOME with one commit, and a store seeded with one planned task whose build is on
// implement at that commit. stateDir is the directory orreryd keeps its task worktrees and holders under.
type buildRig struct {
	f        *engineFixture
	scope    string
	stateDir string
	repo     string
	base     string
}

// newBuildRig builds the rig under the fixture's root: the roles file and the agent, the repository, and the seeded
// store.
func newBuildRig(t *testing.T) *buildRig {
	t.Helper()
	f, scope, _ := scopedFixture(t)
	home := filepath.Join(f.root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatalf("make home: %v", err)
	}
	config := filepath.Join(f.root, "config")
	f.env = append(f.env,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"XDG_CONFIG_HOME="+config, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"),
	)
	stepReadoptWrite(t, filepath.Join(config, "orrery", "roles.toml"),
		fmt.Sprintf("%s = %q\n", buildRigRole, buildRigAgent))
	stepReadoptWrite(t, filepath.Join(home, ".claude", "agents", buildRigAgent+".md"),
		"---\nname: "+buildRigAgent+"\ndescription: Implements a task\n---\n")

	repo := filepath.Join(home, "repo")
	stepReadoptGit(t, home, "init", "-q", "-b", "main", repo)
	// The fake claude writes its files into the worktree, so they are ignored and the step's clean check passes.
	stepReadoptWrite(t, filepath.Join(repo, ".gitignore"), "fakeclaude.*\n")
	stepReadoptWrite(t, filepath.Join(repo, "README"), "base\n")
	// The gate takes its one member of the default pool, which the project file declares for this host.
	host := testHost
	stepReadoptWrite(t, filepath.Join(repo, ".orrery", "project.toml"), fmt.Sprintf(
		"[pools.%s]\nhost = %q\ncap = 1\nmembers = [\"m1\"]\n"+
			"wake = [%q, \"-c\", \"exit 0\"]\nwake_limit = 60\n"+
			"status = [%q, \"-c\", \"exit 0\"]\nstatus_limit = 60\n"+
			"admit = [%q, \"-c\", \"exit 0\"]\nadmit_limit = 60\n",
		gates.DefaultPool, host, f.shell, f.shell, f.shell))
	stepReadoptGit(t, repo, "add", "-A")
	stepReadoptGit(t, repo, "commit", "-q", "-m", "base")
	r := &buildRig{
		f:        f,
		scope:    scope,
		stateDir: filepath.Dir(f.dbPath),
		repo:     repo,
		base:     stepReadoptGit(t, repo, "rev-parse", "HEAD"),
	}
	r.seed(t)
	return r
}

// seed stores the epic over the rig's repository with one planned task, then sets the task's build to implement at the
// base. The planner's turn is claimed and completed here, so no planner trigger is pending when orreryd starts.
func (r *buildRig) seed(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	storetest.Seed(t, r.f.dbPath)
	st, err := store.Open(ctx, r.f.dbPath, storetest.NoSync())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	created := time.Unix(1700000000, 0)
	if err = st.CreateEpic(ctx, store.EpicRow{
		ID:          buildRigEpic,
		Repository:  r.repo,
		Base:        r.base,
		Title:       "Build end to end",
		Approved:    "approved text",
		Intent:      "run one task's implementer step",
		Care:        "severe",
		Approach:    "one implementer",
		ReleaseThen: "release it",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     created,
		Changed:     created,
		Gates: []store.EpicGate{{
			Name: buildRigGate, Argv: []string{r.f.shell, "-c", "exit 0"}, Limit: 60,
		}},
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: buildRigRequirement, Evidence: "build test", Kind: "test"},
		},
		Exclusions: []string{buildRigExclusion},
		Decisions:  []store.EpicDecision{{Text: buildRigDecision}},
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if _, err = st.ClaimTurn(ctx, buildRigEpic, created); err != nil {
		t.Fatalf("claim turn: %v", err)
	}
	if err = st.CompleteTurn(ctx, buildRigEpic, 1, buildRigPlan(), created); err != nil {
		t.Fatalf("complete turn: %v", err)
	}
	epic, err := st.Epic(ctx, buildRigEpic)
	if err != nil {
		t.Fatalf("read epic: %v", err)
	}
	if len(epic.Tasks) != 1 {
		t.Fatalf("epic %s has %d tasks, want 1", buildRigEpic, len(epic.Tasks))
	}
	task := epic.Tasks[0]
	if err = st.SetTaskBuild(ctx, buildRigEpic, task.Seq, task.Build, store.TaskBuild{
		Stage: build.StageImplement, Base: r.base, Attempt: 1,
	}); err != nil {
		t.Fatalf("set task build: %v", err)
	}
	if err = st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

// buildRigPlan is the planner's return for the one task, task 1. It touches one file, covers R1, and builds at
// buildRigGate.
func buildRigPlan() store.EpicPlan {
	return store.EpicPlan{
		Tasks: []store.EpicTaskInput{{
			Title:        buildRigTitle,
			Deliverable:  buildRigDeliverable,
			Files:        []string{buildRigFile},
			Requirements: []string{"R1"},
			Gate:         buildRigGate,
			Evidence: evidence.Item{
				Supplied: []string{"docs/build.md"}, Conclusion: "the build runs", Judge: "the judge",
			},
		}},
		Outline: "One task",
		Outcome: "the build runs",
	}
}

// stepID returns the id of task 1's implementer step at attempt 1, the id orreryd derives for it.
func (r *buildRig) stepID() string {
	return build.StepID(buildRigEpic, 1, build.StageImplement, 1, 0, 0, 0)
}

// stepDir returns the worktree step id runs in: its directory under the task worktrees of the state directory.
func (r *buildRig) stepDir(id string) string {
	return filepath.Join(build.TaskDir(r.stateDir, buildRigEpic, 1), id)
}

// epics returns the Epics client of the session socket.
func (r *buildRig) epics() enginev1connect.EpicsClient {
	return enginev1connect.NewEpicsClient(cli.SessionHTTPClient(r.f.sessions), cli.SessionBaseURL)
}

// steps returns the Steps client of the session socket.
func (r *buildRig) steps() enginev1connect.StepsClient {
	return enginev1connect.NewStepsClient(cli.SessionHTTPClient(r.f.sessions), cli.SessionBaseURL)
}

// epic returns the epic as the Epics service reports it.
func (r *buildRig) epic(t *testing.T) *v1.StoredEpic {
	t.Helper()
	resp, err := r.epics().GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{Id: buildRigEpic}))
	if err != nil {
		t.Fatalf("get epic %s: %v", buildRigEpic, err)
	}
	return resp.Msg.GetEpic()
}

// task returns task 1 of the epic as the Epics service reports it, with its build.
func (r *buildRig) task(t *testing.T) *v1.EpicTask {
	t.Helper()
	tasks := r.epic(t).GetTasks()
	if len(tasks) != 1 {
		t.Fatalf("epic %s has %d tasks, want 1", buildRigEpic, len(tasks))
	}
	return tasks[0]
}

// awaitStep runs daemon's flow, a pass at a time, until task 1 holds step id as its build's current step.
func (r *buildRig) awaitStep(t *testing.T, daemon *orreryProcess, ack string, id string) {
	t.Helper()
	passUntil(t, daemon, ack, func() bool { return r.taskAlive(t).GetStep() == id })
}

// awaitGreen runs daemon's flow, a pass at a time, until task 1 is at green.
func (r *buildRig) awaitGreen(t *testing.T, daemon *orreryProcess, ack string) {
	t.Helper()
	passUntil(t, daemon, ack, func() bool { return r.taskAlive(t).GetStage() == build.StageGreen })
}

// taskAlive returns task 1, failing the test at once when the task has failed: a failed task starts no step and
// reaches no stage, so a wait on either would never end.
func (r *buildRig) taskAlive(t *testing.T) *v1.EpicTask {
	t.Helper()
	task := r.task(t)
	if task.GetStatus() == "failed" {
		t.Fatalf("task 1 failed while the test waited on it: %q", task.GetFailure())
	}
	return task
}

// passUntil signals daemon, which runs with --flow-ticks at ack, to run its flow one pass at a time until done reports
// true. Each pass is awaited in ack, so done reads the store after the pass's writes. A pass that has nothing to do
// changes nothing, so the loop also waits for work outside the flow, such as a fake claude's turn. It has no deadline
// of its own.
func passUntil(t *testing.T, daemon *orreryProcess, ack string, done func() bool) {
	t.Helper()
	for !done() {
		signalPass(t, daemon, ack)
	}
}

// taskSteps returns the labeled steps of task 1, oldest first, as ListSteps reports them.
func (r *buildRig) taskSteps(t *testing.T) []*v1.Step {
	t.Helper()
	resp, err := r.steps().ListSteps(t.Context(), connect.NewRequest(&v1.ListStepsRequest{
		Epic: buildRigEpic,
		Task: "T1",
	}))
	if err != nil {
		t.Fatalf("list steps of task 1: %v", err)
	}
	return resp.Msg.GetSteps()
}

// stepRow returns the row of step id as the Steps service reports it.
func (r *buildRig) stepRow(t *testing.T, id string) *v1.Step {
	t.Helper()
	resp, err := r.steps().StepStatus(t.Context(), connect.NewRequest(&v1.StepStatusRequest{Id: id}))
	if err != nil {
		t.Fatalf("read step %s: %v", id, err)
	}
	return resp.Msg.GetStep()
}

// awaitEnd waits until step id has ended, and returns its row.
func (r *buildRig) awaitEnd(t *testing.T, id string) *v1.Step {
	t.Helper()
	var step *v1.Step
	waitUntil(t, func() bool {
		step = r.stepRow(t, id)
		return step.GetEnd() != ""
	})

	return step
}

// pause pauses the epic. A paused epic starts no evidence run, so a task whose build reaches green stays at green.
func (r *buildRig) pause(t *testing.T) {
	t.Helper()
	if _, err := r.epics().
		PauseEpic(t.Context(), connect.NewRequest(&v1.PauseEpicRequest{Id: buildRigEpic})); err != nil {
		t.Fatalf("pause epic %s: %v", buildRigEpic, err)
	}
}
