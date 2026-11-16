package integration_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// The test in this file SIGKILLs orreryd while an epic's end-of-epic review runs, then restarts it over the same
// store. The epic is seeded at phase built with its stored base, one enabled epic-stage reviewer, and a pinned
// workflow of a review node and a conclude node. The reviewer's step is an agent gate step, and its fake claude holds
// its turn until fakeclaude.release exists in the step's worktree. The test releases the turn after the restart. The
// review resumes from the store: its frozen revision stays the stored base, the review holds one reviewer step, the
// review ends passed with one verdict, and the epic ends clean with one ending tell.

// reviewKillReviewer is the epic-stage reviewer the epic enables, and reviewKillAssertion is its one must. The fake
// claude's JUDGEHOLDS return holds that assertion, and the reviewer's prompt names it.
const (
	reviewKillReviewer  = "checker"
	reviewKillAssertion = "JUDGEHOLDS"
)

// reviewKillReviewNode and reviewKillConcludeNode are the ids of the epic's review node and conclude node. The review
// stores its first reviewer's step under reviewKillReviewerPart, and a rerun of that reviewer as its next try under
// reviewKillRerunPart.
const (
	reviewKillReviewNode   = "examine"
	reviewKillConcludeNode = "finish"
	reviewKillReviewerPart = "reviewer 1"
	reviewKillRerunPart    = "reviewer 1 try 2"
)

// reviewKillVerifierRole and reviewKillVerifierAgent are the role and agent the roles file names. The reviewer has
// verify off, so no verifier runs; the file names one as a machine's roles file does.
const (
	reviewKillVerifierRole  = "verifier"
	reviewKillVerifierAgent = "orrery-verifier"
)

// reviewKillPassLimit bounds the passes a test runs to reach a state the flow reaches by its passes. A state the flow
// never reaches fails the test within that many passes, rather than waiting forever.
const reviewKillPassLimit = 20

// reviewKillText returns the pinned workflow: a review node at the top level, then a conclude node.
func reviewKillText() string {
	return "name: " + nodesName + "\nnodes:\n" +
		"  - id: " + reviewKillReviewNode + "\n    kind: review\n" +
		"  - id: " + reviewKillConcludeNode + "\n    kind: conclude\n"
}

// reviewKillSeed stores the rig's epic at phase built, with the reviewer enabled and the epic pinned to the review
// workflow. The epic has no planner turn and no task, as the nodes tests' epic has.
func reviewKillSeed(t *testing.T, r *buildRig) {
	t.Helper()
	ctx := t.Context()
	st, err := store.Open(ctx, r.f.dbPath, storetest.NoSync())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	created := time.Unix(1700000000, 0)
	if err = st.CreateEpic(ctx, store.EpicRow{
		ID:          buildRigEpic,
		Repository:  r.repo,
		Base:        r.base,
		Title:       "Review a built epic",
		Approved:    "approved text",
		Intent:      "the end-of-epic review resumes after a SIGKILL",
		Care:        "severe",
		Approach:    "one epic-stage reviewer",
		ReleaseThen: "release it",
		Checks:      "just check",
		Phase:       "built",
		Created:     created,
		Changed:     created,
		Workflow:    nodesPin(t, reviewKillText()),
		Reviewers: []reviewers.Reviewer{{
			Name: reviewKillReviewer, Enabled: true, Stage: []string{reviewers.StageEpic},
			Must: []string{reviewKillAssertion}, Agent: agentNodeAgent, Limit: 1800,
		}},
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: buildRigRequirement, Evidence: "build test", Kind: "test"},
		},
		Exclusions: []string{buildRigExclusion},
		Decisions:  []store.EpicDecision{{Text: buildRigDecision}},
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if err = st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

// reviewKillRoles writes the roles file of the rig's config directory, which names the verifier.
func reviewKillRoles(t *testing.T, r *buildRig) {
	t.Helper()
	stepReadoptWrite(t, filepath.Join(r.f.root, "config", "orrery", "roles.toml"),
		fmt.Sprintf("%s = %q\n", reviewKillVerifierRole, reviewKillVerifierAgent))
}

// reviewKillPass signals daemon to run its flow one pass at a time, each pass awaited in ack, until done reports true.
// A state the flow does not reach within reviewKillPassLimit passes fails the test.
func reviewKillPass(t *testing.T, daemon *orreryProcess, ack string, done func() bool) {
	t.Helper()
	for range reviewKillPassLimit {
		if done() {
			return
		}
		signalPass(t, daemon, ack)
	}
	if !done() {
		t.Fatalf("the flow reached no such state of epic %s in %d passes", buildRigEpic, reviewKillPassLimit)
	}
}

// reviewKillLog links the fake claude's stdin log in dir to a directory of the test's own named for step id, and
// returns that directory. A read-only step's worktree and the log in it go when its end is stored; the link keeps
// every line the fake appended, so the prompts can be counted once the step has ended.
func reviewKillLog(t *testing.T, r *buildRig, dir, id string) string {
	t.Helper()
	logDir := filepath.Join(r.f.root, "reviewer-"+id)
	if err := os.Mkdir(logDir, 0o700); err != nil {
		t.Fatalf("make reviewer log directory: %v", err)
	}
	if err := os.Link(filepath.Join(dir, "fakeclaude.stdin"), filepath.Join(logDir, "fakeclaude.stdin")); err != nil {
		t.Fatalf("link the reviewer's stdin log: %v", err)
	}
	return logDir
}

// reviewKillNode returns the node at path of the rig's epic as the Epics service reports it, or nil when the epic
// holds no node there.
func (r *buildRig) reviewKillNode(t *testing.T, path string) *v1.EpicNode {
	t.Helper()
	for _, node := range r.epic(t).GetNodes() {
		if node.GetPath() == path {
			return node
		}
	}
	return nil
}

// reviewKillStepEnds returns the end of each labeled step of the rig's epic, by id. An open step has the end "".
func (r *buildRig) reviewKillStepEnds(t *testing.T) map[string]string {
	t.Helper()
	ends := make(map[string]string)
	for _, step := range r.epicSteps(t) {
		ends[step.GetId()] = step.GetEnd()
	}
	return ends
}

// reviewKillRequireStep fails unless step is the reviewer's read-only step at id: it runs at the stored base in its own
// worktree, labeled with the epic and the review node.
func (r *buildRig) reviewKillRequireStep(t *testing.T, step *v1.Step, id string) {
	t.Helper()
	if step.GetId() != id || !step.GetReadOnly() || step.GetRevision() != r.base ||
		step.GetEpic() != buildRigEpic || step.GetTask() != reviewKillReviewNode || step.GetWorktree() == "" {
		t.Fatalf("step %+v, want the read-only step %s at base %s labeled with node %s",
			step, id, r.base, reviewKillReviewNode)
	}
}

// TestReviewKillSigkillResumesReviewFromStore proves that orreryd killed while an epic's reviewer step runs resumes the
// review from the store. The frozen revision stays the stored base. The restart ends the reviewer's turn
// engine_restarted and the review reruns the reviewer under its next try id, which is free, so no step id starts twice. Once the turn is released, the review ends passed with the reviewer's one verdict, and the epic ends
// clean with one ending tell.
func TestReviewKillSigkillResumesReviewFromStore(t *testing.T) {
	t.Parallel()
	r := newAgentNodesRig(t)
	reviewKillRoles(t, r)
	reviewKillSeed(t, r)
	daemon, ack := r.startPassDaemon(t, "first")
	t.Cleanup(func() { clearHolders(t, r.stateDir) })

	reviewKillPass(t, daemon, ack, func() bool { return len(r.epicSteps(t)) > 0 })
	steps := r.epicSteps(t)
	if len(steps) != 1 {
		t.Fatalf("labeled steps of epic %s = %d, want the reviewer's one step", buildRigEpic, len(steps))
	}
	run := r.nodeRun(t, reviewKillReviewNode)
	id := store.NodeWorkID(run.ID, run.Attempt, reviewKillReviewerPart)
	r.reviewKillRequireStep(t, steps[0], id)
	dir := steps[0].GetWorktree()
	stdin := filepath.Join(dir, "fakeclaude.stdin")
	waitUntil(t, func() bool { return fileContains(t, stdin, reviewKillAssertion) })
	pid := fakeClaudePID(t, dir)
	logDir := reviewKillLog(t, r, dir, id)
	if frozen := r.epic(t).GetFrozen(); frozen != r.base {
		t.Fatalf("frozen revision before the kill = %q, want the stored base %s", frozen, r.base)
	}

	killOrreryd(t, daemon)
	restarted, restartedAck := r.startPassDaemon(t, "restarted")
	if frozen := r.epic(t).GetFrozen(); frozen != r.base {
		t.Fatalf("frozen revision after the restart = %q, want the stored base %s", frozen, r.base)
	}
	if node := r.reviewKillNode(t, reviewKillReviewNode); node == nil || node.GetState() != store.NodeRunning ||
		node.GetRevision() != r.base {
		t.Fatalf("review node after the restart = %+v, want it running at base %s", node, r.base)
	}
	reviewKillPass(t, restarted, restartedAck, func() bool {
		for _, end := range r.reviewKillStepEnds(t) {
			if end == "" {
				return true
			}
		}
		return false
	})

	// An epic-stage reviewer's agent-gate step is not re-adopted at a restart (cmd/orreryd readoptGate), so the
	// restart ends it engine_restarted and the review reruns the reviewer under its next try id, which is free.
	ends := r.reviewKillStepEnds(t)
	resumed := store.NodeWorkID(run.ID, run.Attempt, reviewKillRerunPart)
	if len(ends) != 2 || ends[id] != "engine_restarted" || ends[resumed] != "" {
		t.Fatalf("labeled steps of epic %s after the restart = %v, want step %s ended engine_restarted and its "+
			"rerun %s open", buildRigEpic, ends, id, resumed)
	}
	waitGone(t, pid)
	resumedDir := r.stepRow(t, resumed).GetWorktree()
	waitUntil(t, func() bool {
		return fileContains(t, filepath.Join(resumedDir, "fakeclaude.stdin"), reviewKillAssertion)
	})
	resumedPid := fakeClaudePID(t, resumedDir)
	logs := []string{logDir, reviewKillLog(t, r, resumedDir, resumed)}

	stepReadoptTouch(t, filepath.Join(resumedDir, "fakeclaude.release"))
	if end := r.awaitEnd(t, resumed).GetEnd(); end != "done" {
		t.Fatalf("reviewer step %s ended %q, want done", resumed, end)
	}
	reviewKillPass(t, restarted, restartedAck, func() bool {
		node := r.reviewKillNode(t, reviewKillConcludeNode)
		return node != nil && node.GetState() == store.NodeEnded
	})

	if node := r.reviewKillNode(t, reviewKillReviewNode); node.GetState() != store.NodeEnded ||
		node.GetOutcome() != "passed" {
		t.Fatalf("review node %q with outcome %q, want ended passed", node.GetState(), node.GetOutcome())
	}
	if node := r.reviewKillNode(t, reviewKillConcludeNode); node.GetState() != store.NodeEnded ||
		node.GetOutcome() != "passed" {
		t.Fatalf("conclude node %q with outcome %q, want ended passed", node.GetState(), node.GetOutcome())
	}
	epic := r.epic(t)
	if frozen := epic.GetFrozen(); frozen != r.base {
		t.Fatalf("frozen revision at the end = %q, want the stored base %s", frozen, r.base)
	}
	verdicts := epic.GetLedger().GetVerdicts()
	if len(verdicts) != 1 || verdicts[0].GetReviewer() != reviewKillReviewer || verdicts[0].GetVerdict() != "holds" ||
		verdicts[0].GetStep() != resumed {
		t.Fatalf("review verdicts = %+v, want the one verdict holds from reviewer %s at step %s",
			verdicts, reviewKillReviewer, resumed)
	}
	if epic.GetPhase() != "clean" || epic.GetClean() != r.base || epic.GetParked() != "" {
		t.Fatalf("epic phase %q clean %q parked %q, want clean at %s unparked",
			epic.GetPhase(), epic.GetClean(), epic.GetParked(), r.base)
	}
	endings := 0
	for _, text := range r.tells(t) {
		if strings.Contains(text, "ended clean") {
			endings++
		}
	}
	if endings != 1 {
		t.Fatalf("ending tells of epic %s = %d, want one", buildRigEpic, endings)
	}

	want := map[string]string{resumed: "done", id: "engine_restarted"}
	if got := r.reviewKillStepEnds(t); !maps.Equal(got, want) {
		t.Fatalf("labeled steps of epic %s = %v, want %v", buildRigEpic, got, want)
	}
	for _, log := range logs {
		if n := promptsWritten(t, log, reviewKillAssertion); n != 1 {
			t.Fatalf("fake claude read the reviewer's prompt %d times in %s, want 1", n, log)
		}
	}
	waitGone(t, resumedPid)
	wantExit(t, restarted.stop(t), 0)
}
