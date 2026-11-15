package integration_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The tests in this file run a plan's integrate node through the real orreryd. The epic is seeded in the store with one
// planner turn that completed with outcome complete and one accepted task built on the base, so the integrate node starts
// at once and no planner or implementer step runs. The epic's one Done gate blocks on a release file, and each run of it
// appends a line to a file outside the repository, which the tests count. The tests SIGKILL orreryd and restart it over
// the same store. A gate whose holder is cleared while orreryd is down cannot be re-adopted, so it ends lost.

// integrateKillWorkflow is the workflow the epic pins: a plan whose body holds the build and the integrate node.
const integrateKillWorkflow = "plan"

// integrateKillText is the text of that workflow as the repository writes it.
const integrateKillText = "name: plan\nnodes:\n  - id: plan\n    kind: plan\n    body:\n" +
	"      - id: build\n        kind: build\n      - id: integrate\n        kind: integrate\n"

// integrateKillGate is the name of the epic's Done gate, and integrateKillKind the kind of the integrate node run.
const (
	integrateKillGate = "check"
	integrateKillKind = "integrate"
)

// integrateKillFile is the file the accepted task changes, and integrateKillDonePart and integrateKillRerunPart are the
// parts the Done gate's check and its rerun are stored under.
const (
	integrateKillFile       = "feature.txt"
	integrateKillDonePart   = "done 1"
	integrateKillRerunPart  = "done 1 rerun"
	integrateKillLostReason = "Done gate " + integrateKillGate + " lost at a restart twice"
)

// integrateKillPin returns the integrate test workflow as the epic pins it: the canonical text Load gives, its sha256 and
// its nodes in preorder.
func integrateKillPin(t *testing.T) store.EpicWorkflow {
	t.Helper()
	path := ".orrery/workflows/" + integrateKillWorkflow + ".yaml"
	pinned, err := workflow.Load(integrateKillWorkflow, func(file string) ([]byte, bool, error) {
		if file != path {
			return nil, false, nil
		}
		return []byte(integrateKillText), true, nil
	})
	if err != nil {
		t.Fatalf("load workflow %s: %v", integrateKillWorkflow, err)
	}
	return store.EpicWorkflow{
		Name:   pinned.Name,
		Text:   pinned.Text,
		SHA256: pinned.SHA256,
		Nodes:  flowNodeRows(pinned.Workflow.Nodes, ""),
	}
}

// integrateKillTaskCommit commits integrateKillFile on a branch from the base in repo, and returns the commit. The
// repository's checkout is left on main, at the base.
func integrateKillTaskCommit(t *testing.T, repo string) string {
	t.Helper()
	stepReadoptGit(t, repo, "checkout", "-q", "-b", "task")
	stepReadoptWrite(t, filepath.Join(repo, integrateKillFile), "the accepted task\n")
	stepReadoptGit(t, repo, "add", "-A")
	stepReadoptGit(t, repo, "commit", "-q", "-m", "accepted task")
	revision := stepReadoptGit(t, repo, "rev-parse", "HEAD")
	stepReadoptGit(t, repo, "checkout", "-q", "main")
	return revision
}

// integrateKillSeed stores the rig's epic pinned to the integrate workflow, with gate as its one Done gate. The epic has
// one completed planner turn and one accepted task, whose build is at a commit of its own on the base. The epic branch
// starts at the base.
func integrateKillSeed(t *testing.T, r *buildRig, gate store.EpicGate) {
	t.Helper()
	ctx := t.Context()
	revision := integrateKillTaskCommit(t, r.repo)
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
		Title:       "Integrate an accepted task",
		Approved:    "approved text",
		Intent:      "compose and integrate one accepted task",
		Care:        "severe",
		Approach:    "one integrate node",
		ReleaseThen: "release it",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     created,
		Changed:     created,
		Workflow:    integrateKillPin(t),
		Gates:       []store.EpicGate{gate},
		DoneGates:   []string{gate.Name},
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
	if err = st.CompleteTurn(ctx, buildRigEpic, 1, store.EpicPlan{
		Outline: "one accepted task",
		Outcome: "complete",
		Tasks: []store.EpicTaskInput{{
			Title:        buildRigTitle,
			Deliverable:  buildRigDeliverable,
			Files:        []string{integrateKillFile},
			Requirements: []string{"R1"},
			Gate:         gate.Name,
		}},
	}, created); err != nil {
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
	accepted := store.TaskBuild{Stage: build.StageVerify, Base: r.base, Attempt: 1, Revision: revision}
	if err = st.SetTaskBuild(ctx, buildRigEpic, task.Seq, task.Build, accepted); err != nil {
		t.Fatalf("set task build: %v", err)
	}
	if err = st.AcceptTask(ctx, buildRigEpic, task.Seq, accepted, "reviewed"); err != nil {
		t.Fatalf("accept task: %v", err)
	}
	if err = build.CreateBranch(ctx, r.repo, buildRigEpic, r.base); err != nil {
		t.Fatalf("create epic branch: %v", err)
	}
	if err = st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

// integrateKillGateOf returns the epic's Done gate: a command that appends a line to lines each time it runs, then holds
// until release exists.
func integrateKillGateOf(shell, lines, release string) store.EpicGate {
	script := fmt.Sprintf("echo run >> '%s'; while [ ! -e '%s' ]; do sleep 0.05; done", lines, release)
	return store.EpicGate{Name: integrateKillGate, Argv: []string{shell, "-c", script}, Limit: 60}
}

// integrateRun returns the integrate node run of the rig's epic, failing the test when the epic holds none.
func (r *buildRig) integrateRun(t *testing.T) sqlcgen.EpicNodeRun {
	t.Helper()
	for _, run := range r.nodeRuns(t) {
		if run.Kind == integrateKillKind {
			return run
		}
	}
	t.Fatalf("epic %s holds no integrate node run", buildRigEpic)
	return sqlcgen.EpicNodeRun{}
}

// integrateKillCheckID returns the id of the Done gate's check of run, and integrateKillRerunID the id of its rerun.
func integrateKillCheckID(run sqlcgen.EpicNodeRun) string {
	return store.NodeWorkID(run.ID, run.Attempt, integrateKillDonePart)
}

func integrateKillRerunID(run sqlcgen.EpicNodeRun) string {
	return store.NodeWorkID(run.ID, run.Attempt, integrateKillRerunPart)
}

// checkRows returns the node checks the store holds for the rig's epic.
func (r *buildRig) checkRows(t *testing.T) []sqlcgen.EpicNodeCheck {
	t.Helper()
	var checks []sqlcgen.EpicNodeCheck
	r.readStore(t, func(q *sqlcgen.Queries) error {
		var err error
		checks, err = q.ListEpicNodeChecks(t.Context(), buildRigEpic)
		return err
	})
	return checks
}

// storedEpic returns the rig's epic row as the store holds it.
func (r *buildRig) storedEpic(t *testing.T) sqlcgen.Epic {
	t.Helper()
	var epic sqlcgen.Epic
	r.readStore(t, func(q *sqlcgen.Queries) error {
		var err error
		epic, err = q.GetEpic(t.Context(), buildRigEpic)
		return err
	})
	return epic
}

// tells returns the texts of the tells the rig's epic holds.
func (r *buildRig) tells(t *testing.T) []string {
	t.Helper()
	var texts []string
	r.readStore(t, func(q *sqlcgen.Queries) error {
		tells, err := q.ListEpicTells(t.Context(), buildRigEpic)
		for _, tell := range tells {
			texts = append(texts, tell.Text)
		}
		return err
	})
	return texts
}

// readStore runs read against a read-only handle on the rig's store. The handle is closed at once, so orreryd keeps the
// file it writes.
func (r *buildRig) readStore(t *testing.T, read func(q *sqlcgen.Queries) error) {
	t.Helper()
	db := r.openReadOnlyStore(t)
	readErr := read(sqlcgen.New(db))
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close store read-only: %v", closeErr)
	}
	if readErr != nil {
		t.Fatalf("read store: %v", readErr)
	}
}

// pinnedCandidates returns the commits the candidate refs of the rig's epic point at, one per ref.
func (r *buildRig) pinnedCandidates(t *testing.T) []string {
	t.Helper()
	out := stepReadoptGit(t, r.repo, "for-each-ref", "--format=%(objectname)",
		"refs/orrery/"+build.Slug(buildRigEpic)+"/candidate/")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// branchTip returns the commit the rig's epic branch is at.
func (r *buildRig) branchTip(t *testing.T) string {
	t.Helper()
	return stepReadoptGit(t, r.repo, "rev-parse", "--verify", build.BranchRef(buildRigEpic))
}

// TestIntegrateKillDoneGateReadoptedOnce proves that orreryd killed while the epic's Done gate runs re-adopts the gate
// under its holder after the restart. The gate is not run again, its check stays one row, and once the gate is released
// the integrate node passes and moves the epic to the same candidate commit the first orreryd pinned.
func TestIntegrateKillDoneGateReadoptedOnce(t *testing.T) {
	t.Parallel()
	r := newNodesRig(t)
	lines := filepath.Join(r.f.root, "gate.lines")
	release := filepath.Join(r.f.root, "gate.release")
	integrateKillSeed(t, r, integrateKillGateOf(r.f.shell, lines, release))
	daemon, _ := r.startPassDaemon(t, "first")
	t.Cleanup(func() { clearHolders(t, r.stateDir) })
	// A gate still held at the end of a failed test exits at once, so its holder is not left waiting.
	t.Cleanup(func() { stepReadoptTouch(t, release) })
	waitUntil(t, func() bool { return gateRunCount(t, lines) == 1 })
	pinned := r.pinnedCandidates(t)
	if len(pinned) != 1 {
		t.Fatalf("candidate refs before the kill = %d, want 1", len(pinned))
	}
	doneID := integrateKillCheckID(r.integrateRun(t))

	killOrreryd(t, daemon)
	restarted, restartedAck := r.startPassDaemon(t, "restarted")
	if n := gateRunCount(t, lines); n != 1 {
		t.Fatalf("gate lines after the restart = %d, want 1", n)
	}
	if checks := r.checkRows(t); len(checks) != 1 || checks[0].ID != doneID || checks[0].State != store.NodeRunning {
		t.Fatalf("node checks after the restart = %+v, want the one running Done gate check", checks)
	}

	stepReadoptTouch(t, release)
	waitUntil(t, func() bool { return r.checkEnded(t, doneID) })
	signalPass(t, restarted, restartedAck)
	if run := r.integrateRun(t); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Fatalf("integrate run %q with outcome %q, want ended passed; output %q", run.State, run.Outcome, run.Output)
	}
	if n := gateRunCount(t, lines); n != 1 {
		t.Fatalf("gate lines after the gate ended = %d, want 1", n)
	}
	checks := r.checkRows(t)
	if len(checks) != 1 || checks[0].Revision != pinned[0] || checks[0].NotRun != "" || checks[0].Exit != 0 {
		t.Fatalf("node checks after the gate ended = %+v, want the one passed check at candidate %s", checks, pinned[0])
	}
	if got := r.pinnedCandidates(t); len(got) != 1 || got[0] != pinned[0] {
		t.Fatalf("candidate refs after the batch = %v, want only %s", got, pinned[0])
	}
	if epic := r.storedEpic(t); epic.StoredBase != pinned[0] {
		t.Fatalf("stored base = %s, want the candidate %s", epic.StoredBase, pinned[0])
	}
	if tip := r.branchTip(t); tip != pinned[0] {
		t.Fatalf("epic branch = %s, want the candidate %s", tip, pinned[0])
	}
	wantExit(t, restarted.stop(t), 0)
}

// TestIntegrateKillLostDoneGateRerunsOnceThenParks proves that a Done gate whose holder is cleared while orreryd is down
// ends lost at the restart and runs once more as its rerun. When the rerun is lost too, the integrate node parks, and the
// epic parks with a tell that names the gate. The gate runs twice in all: once for the check and once for the rerun.
func TestIntegrateKillLostDoneGateRerunsOnceThenParks(t *testing.T) {
	t.Parallel()
	r := newNodesRig(t)
	lines := filepath.Join(r.f.root, "gate.lines")
	release := filepath.Join(r.f.root, "gate.release")
	integrateKillSeed(t, r, integrateKillGateOf(r.f.shell, lines, release))
	daemon, _ := r.startPassDaemon(t, "first")
	t.Cleanup(func() { clearHolders(t, r.stateDir) })
	t.Cleanup(func() { stepReadoptTouch(t, release) })
	waitUntil(t, func() bool { return gateRunCount(t, lines) == 1 })
	run := r.integrateRun(t)
	doneID, rerunID := integrateKillCheckID(run), integrateKillRerunID(run)

	killOrreryd(t, daemon)
	clearHolders(t, r.stateDir)
	restarted, restartedAck := r.startPassDaemon(t, "restarted")
	waitUntil(t, func() bool { return r.checkEnded(t, doneID) })
	if lost := r.checkNotRun(t, doneID); lost != store.LostAtRestart {
		t.Fatalf("Done gate check not run = %q after the restart, want %q", lost, store.LostAtRestart)
	}
	signalPass(t, restarted, restartedAck)
	if !slices.ContainsFunc(r.checkRows(t), func(check sqlcgen.EpicNodeCheck) bool { return check.ID == rerunID }) {
		t.Fatalf("no Done gate rerun stored by the pass after the check was lost")
	}
	waitUntil(t, func() bool { return gateRunCount(t, lines) == 2 })
	if run := r.integrateRun(t); run.State != store.NodeRunning {
		t.Fatalf("integrate run after the rerun starts is %q, want running", run.State)
	}

	killOrreryd(t, restarted)
	clearHolders(t, r.stateDir)
	again, againAck := r.startPassDaemon(t, "again")
	waitUntil(t, func() bool { return r.checkEnded(t, rerunID) })
	if lost := r.checkNotRun(t, rerunID); lost != store.LostAtRestart {
		t.Fatalf("Done gate rerun not run = %q after the second restart, want %q", lost, store.LostAtRestart)
	}
	signalPass(t, again, againAck)
	if run := r.integrateRun(t); run.State != store.NodeParked || run.Outcome != "failed" {
		t.Fatalf("integrate run %q with outcome %q, want parked failed; output %q", run.State, run.Outcome, run.Output)
	}
	if parked := r.storedEpic(t).Parked; !strings.Contains(parked, integrateKillLostReason) {
		t.Fatalf("epic parked with %q, want the reason to name %q", parked, integrateKillLostReason)
	}
	tells := r.tells(t)
	if !slices.ContainsFunc(tells, func(text string) bool { return strings.Contains(text, integrateKillLostReason) }) {
		t.Fatalf("tells %q do not name %q", tells, integrateKillLostReason)
	}
	if n := gateRunCount(t, lines); n != 2 {
		t.Fatalf("gate lines after the rerun was lost = %d, want 2", n)
	}
	wantExit(t, again.stop(t), 0)
}

// checkNotRun returns the NotRun of the node check id, or "" when the store holds no such check.
func (r *buildRig) checkNotRun(t *testing.T, id string) string {
	t.Helper()
	for _, check := range r.checkRows(t) {
		if check.ID == id {
			return check.NotRun
		}
	}
	return ""
}
