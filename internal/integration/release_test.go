package integration_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/build"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// The tests in this file run a release of a clean epic through the real orreryd, with the fake claude in place of claude
// and a bare local origin for the epic's repository. Each release step and the judge run as steps, and the tests wait on
// the rows those steps and the release write. They live in internal/integration because they start the orreryd binary
// and read the store over its socket, which crosses the packages the release runs in.

// releaseAgent and judgeAgent are the agents the roles file names for a release step and for the judge. Their files are
// written under the claude config directory, as the agent-node tests write theirs.
const (
	releaseAgent = "orrery-releaser"
	judgeAgent   = "orrery-judge"
)

// releasedPhase is the phase a release whose judge passed ends the epic in.
const releasedPhase = "released"

// releaseStepText is the step the tests release. Its text holds RELEASEDONE, which the fake claude answers with one
// commit and a done return. releaseStepFails is a step whose text holds no keyword, so the fake answers it with no
// structured return and the step does not end done.
const (
	releaseStepText  = "RELEASEDONE tag the release"
	releaseStepFails = "tag the release"
)

// releaseThen is the promise the judge checks. It holds JUDGERELEASED, which the fake claude answers as a released judge.
const releaseThen = "JUDGERELEASED the release is tagged on main"

// releaseEvidence is the evidence the fake claude returns for RELEASEDONE.
const releaseEvidence = "fake release evidence"

// releaseWorktrees and releaseWorktreeName are the directory under the state directory that holds one directory per
// release step, and the worktree under it a writing release step runs in.
const (
	releaseWorktrees    = "release-worktrees"
	releaseWorktreeName = "wt"
)

// releaseNodesText is the workflow the release tests pin: one release node at the top level.
const releaseNodesText = "name: " + nodesName + "\nnodes:\n  - id: release\n    kind: release\n"

// releaseDeadline bounds each wait of the release tests. A release that never reaches the state a wait names fails its
// test there, naming that state, in place of a hang until the test binary's timeout.
const releaseDeadline = 2 * time.Minute

// newReleaseRig returns the nodes rig with the roles file and agents the release names, a bare origin of the repository,
// and the epic branch at the base. The fake claude writes its files into the worktrees, so the repository's info/exclude
// ignores them.
func newReleaseRig(t *testing.T) *buildRig {
	t.Helper()
	r := newNodesRig(t)
	stepReadoptWrite(t, filepath.Join(r.f.root, "config", "orrery", "roles.toml"),
		fmt.Sprintf("release = %q\njudge = %q\n", releaseAgent, judgeAgent))
	claude := filepath.Join(r.f.root, "home", ".claude")
	for _, agent := range []string{releaseAgent, judgeAgent} {
		stepReadoptWrite(t, filepath.Join(claude, "agents", agent+".md"),
			"---\nname: "+agent+"\ndescription: Releases a base\n---\n")
	}
	r.f.env = append(r.f.env, "CLAUDE_CONFIG_DIR="+claude)
	stepReadoptWrite(t, filepath.Join(r.repo, ".git", "info", "exclude"), "fakeclaude.*\n")
	origin := filepath.Join(r.f.root, "origin.git")
	stepReadoptGit(t, r.f.root, "clone", "-q", "--bare", r.repo, origin)
	stepReadoptGit(t, r.repo, "remote", "add", "origin", origin)
	stepReadoptGit(t, r.repo, "update-ref", build.BranchRef(buildRigEpic), r.base)
	return r
}

// seedReleaseEpic stores the rig's epic at phase clean, pinned to the release workflow, with steps as its release
// steps, and a frozen review at its base, as an epic that ended clean has. The epic plans nothing, so its release is
// the only work it runs.
func seedReleaseEpic(t *testing.T, r *buildRig, steps ...store.EpicReleaseStep) {
	t.Helper()
	ctx := t.Context()
	st, err := store.Open(ctx, r.f.dbPath, storetest.NoSync())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	created := time.Unix(1700000000, 0)
	if err = st.CreateEpic(ctx, store.EpicRow{
		ID:           buildRigEpic,
		Repository:   r.repo,
		Base:         r.base,
		Title:        "Ship the release",
		Approved:     "approved text",
		Intent:       "run a release through real steps",
		Care:         "severe",
		Approach:     "release steps",
		ReleaseThen:  releaseThen,
		Checks:       "just check",
		Phase:        store.PhaseClean,
		Created:      created,
		Changed:      created,
		Workflow:     nodesPin(t, releaseNodesText),
		ReleaseSteps: steps,
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "the release is tagged", Evidence: "release test", Kind: "test"},
		},
		Exclusions: []string{buildRigExclusion},
		Decisions:  []store.EpicDecision{{Text: buildRigDecision}},
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if err = st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	freezeReviewRow(t, r.f.dbPath, buildRigEpic, r.base)
}

// freezeReviewRow stores the review of epic id as frozen at revision in the store file at path, the row a review
// leaves on an epic that ended clean. It writes the row directly: a review run's freeze also sets the epic's phase to
// reviewing, which the release epic is not in.
func freezeReviewRow(t *testing.T, path, id, revision string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	_, err = db.ExecContext(t.Context(), "INSERT INTO epic_reviews (epic, frozen) VALUES (?, ?)", id, revision)
	if err != nil {
		t.Fatalf("insert review of epic %s: %v", id, err)
	}
}

// releaseEpic returns the rig's epic as the Epics service reports it, with its release runs and its tells.
func (r *buildRig) releaseEpic(t *testing.T) *v1.StoredEpic {
	t.Helper()
	resp, err := r.epics().GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{
		Id:      buildRigEpic,
		History: true,
	}))
	if err != nil {
		t.Fatalf("get epic %s: %v", buildRigEpic, err)
	}
	return resp.Msg.GetEpic()
}

// releaseRunOf returns the release run of part at position in epic, and whether epic holds one.
func releaseRunOf(epic *v1.StoredEpic, part string, position int64) (*v1.EpicReleaseRun, bool) {
	for _, run := range epic.GetReleases() {
		if run.GetPart() == part && run.GetPosition() == position {
			return run, true
		}
	}
	return nil, false
}

// awaitRelease waits until ok reports true. It fails the test, naming what it waited for, once releaseDeadline passes.
func awaitRelease(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(releaseDeadline)
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for !ok() {
		stopIfFailed(t)
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", releaseDeadline, what)
		}
		<-ticker.C
	}
}

// passReleaseUntil signals daemon, which runs with --flow-ticks at ack, to run its flow one pass at a time until done
// reports true, as passUntil does. It fails the test, naming what it waited for, once releaseDeadline passes.
func passReleaseUntil(t *testing.T, daemon *orreryProcess, ack, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(releaseDeadline)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", releaseDeadline, what)
		}
		signalPass(t, daemon, ack)
	}
}

// TestReleaseRunsStepsThroughRealOrreryd proves that orreryd runs a clean epic's release through real steps. Its release
// step is an unowned writing step in a worktree under the state directory. The store holds the step's end and its one
// commit before the judge starts, the judge's end is done, and the epic then ends released with one tell and frees its
// slot.
func TestReleaseRunsStepsThroughRealOrreryd(t *testing.T) {
	t.Parallel()
	r := newReleaseRig(t)
	seedReleaseEpic(t, r, store.EpicReleaseStep{Step: releaseStepText, Target: "main", Undo: "none"})
	daemon, ack := r.startPassDaemon(t, "first")
	t.Cleanup(func() { clearHolders(t, r.stateDir) })

	passReleaseUntil(t, daemon, ack, "the release step to start", func() bool { return len(r.epicSteps(t)) == 1 })
	step := r.epicSteps(t)[0]
	wt := filepath.Join(r.stateDir, releaseWorktrees, step.GetId(), releaseWorktreeName)
	if step.GetAgent() != releaseAgent || step.GetReadOnly() || step.GetTask() != "" ||
		step.GetEpic() != buildRigEpic || step.GetRevision() != r.base || step.GetWorkspace() != wt {
		t.Fatalf("release step %+v, want an unowned writing step under agent %s at base %s in worktree %s",
			step, releaseAgent, r.base, wt)
	}
	stdin := filepath.Join(wt, "fakeclaude.stdin")
	awaitRelease(t, "the release step's prompt to reach its fake claude", func() bool {
		return fileContains(t, stdin, releaseStepText)
	})
	if !fileContains(t, filepath.Join(wt, "fakeclaude.args"), "--agent "+releaseAgent) {
		t.Fatalf("fake claude was not started with --agent %s", releaseAgent)
	}
	stepReadoptTouch(t, filepath.Join(wt, "fakeclaude.release"))
	awaitRelease(t, "release step "+step.GetId()+" to end", func() bool {
		return r.stepRow(t, step.GetId()).GetEnd() != ""
	})
	if end := r.stepRow(t, step.GetId()).GetEnd(); end != "done" {
		t.Fatalf("release step %s ended %q, want done", step.GetId(), end)
	}

	passReleaseUntil(t, daemon, ack, "the judge to start", func() bool { return len(r.epicSteps(t)) == 2 })
	run, ok := releaseRunOf(r.releaseEpic(t), store.ReleasePartStep, 1)
	if !ok || !run.GetPassed() || run.GetEnd() != "done" || run.GetEvidence() != releaseEvidence ||
		len(run.GetCommits()) != 1 || run.GetHead() != run.GetCommits()[0] || run.GetHead() == r.base {
		t.Fatalf("release step 1 %+v when the judge started, want passed at one new commit with its evidence", run)
	}

	// The judge is the epic's step that is not the release step. It is read-only at the candidate, which is the base
	// here, and its own worktree holds the fake claude's files. ListSteps does not order steps that started in the same
	// second, so the judge is found by its id.
	var judge *v1.Step
	for _, s := range r.epicSteps(t) {
		if s.GetId() != step.GetId() {
			judge = s
		}
	}
	if judge == nil {
		t.Fatalf("epic %s holds no step besides release step %s", buildRigEpic, step.GetId())
	}
	if judge.GetAgent() != judgeAgent || !judge.GetReadOnly() || judge.GetRevision() != r.base ||
		judge.GetEpic() != buildRigEpic {
		t.Fatalf("judge step %+v, want a read-only step under agent %s at base %s", judge, judgeAgent, r.base)
	}
	stepReadoptTouch(t, filepath.Join(judge.GetWorktree(), "fakeclaude.release"))
	awaitRelease(t, "judge step "+judge.GetId()+" to end", func() bool {
		return r.stepRow(t, judge.GetId()).GetEnd() != ""
	})
	passReleaseUntil(t, daemon, ack, "the judge's run to be recorded", func() bool {
		_, recorded := releaseRunOf(r.releaseEpic(t), store.ReleasePartJudge, 0)
		return recorded
	})
	judged, _ := releaseRunOf(r.releaseEpic(t), store.ReleasePartJudge, 0)
	if !judged.GetPassed() || judged.GetEnd() != "done" {
		t.Fatalf("judge run %+v, want ended done and passed", judged)
	}

	// orreryd's Ending is the store's EndEpic: the passed judge ends the epic released with one tell that names no gap,
	// and the next pass frees the slot it held.
	passReleaseUntil(t, daemon, ack, "the released epic to free its slot", func() bool {
		return r.releaseEpic(t).GetReleaseSlot() == ""
	})
	released := r.releaseEpic(t)
	if released.GetPhase() != releasedPhase {
		t.Fatalf("epic phase %q after the judge passed, want %q", released.GetPhase(), releasedPhase)
	}
	endings := 0
	for _, tell := range released.GetTells() {
		if tell.GetText() == "Released. No gaps." {
			endings++
		}
	}
	if endings != 1 {
		t.Fatalf("tells %+v hold %d endings, want one", released.GetTells(), endings)
	}
	wantExit(t, daemon.stop(t), 0)
}

// TestReleaseStepNotDoneStopsRelease proves that a release step whose end is not done stops the release: the epic is
// stopped and parked, and one tell names the step.
func TestReleaseStepNotDoneStopsRelease(t *testing.T) {
	t.Parallel()
	r := newReleaseRig(t)
	seedReleaseEpic(t, r, store.EpicReleaseStep{Step: releaseStepFails, Target: "main", Undo: "none"})
	daemon, ack := r.startPassDaemon(t, "first")
	t.Cleanup(func() { clearHolders(t, r.stateDir) })

	passReleaseUntil(t, daemon, ack, "the release step to start", func() bool { return len(r.epicSteps(t)) == 1 })
	step := r.epicSteps(t)[0]
	awaitRelease(t, "release step "+step.GetId()+" to end", func() bool {
		return r.stepRow(t, step.GetId()).GetEnd() != ""
	})
	if end := r.stepRow(t, step.GetId()).GetEnd(); end == "done" {
		t.Fatalf("release step %s ended done, want a step that does not end done", step.GetId())
	}

	passReleaseUntil(t, daemon, ack, "the release to stop", func() bool {
		return r.releaseEpic(t).GetPhase() == store.PhaseStopped
	})
	epic := r.releaseEpic(t)
	run, ok := releaseRunOf(epic, store.ReleasePartStep, 1)
	if !ok || run.GetPassed() || run.GetEnd() == "done" {
		t.Fatalf("release step 1 %+v, want recorded failed", run)
	}
	if !strings.Contains(epic.GetParked(), releaseStepFails) {
		t.Fatalf("park reason %q does not name the step %q", epic.GetParked(), releaseStepFails)
	}
	if tells := epic.GetTells(); len(tells) != 1 || !strings.Contains(tells[0].GetText(), releaseStepFails) {
		t.Fatalf("tells %+v, want one that names the step %q", tells, releaseStepFails)
	}
	wantExit(t, daemon.stop(t), 0)
}
