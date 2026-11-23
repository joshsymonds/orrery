package interpret

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The commits of an epic after the migration that removed the sync of origin's main. legacySynced is the commit of
// origin's main its last sync merged, which the migration made the epic's base. legacyStored is the stored base that
// sync moved the epic to, which the migration left as it was.
const (
	legacySynced = "5e1f0a2b3c4d5e6f708192a3b4c5d6e7f8091a2b"
	legacyStored = "9b8a7f6e5d4c3b2a1908f7e6d5c4b3a291807f6e"
	// legacySkipOutput is the output the migration gives a sync run it ends skipped.
	legacySkipOutput = "the sync was removed at deploy"
)

// legacySyncRun returns a sync run of origin's main at attempt 1 at path, with revision as its revision. Its state is
// set by the caller, as the store holds it.
func legacySyncRun(path, revision string) store.NodeRun {
	run := attempted(startOf(path, "sync", "sync", 0), 1)
	run.Revision = revision
	return run
}

// legacyEpicRows returns the rows of an epic shaped as the migration leaves one whose sync passed: its base is the
// commit its sync merged, its stored base is the revision the sync moved to, its one Requirement R1 holds, and its plan
// run started after turn 1 and its turn 2 completed with the batch that integrated task 2. Task 1 is an earlier plan's.
// The node runs are the plan's and those given.
func legacyEpicRows(runs ...store.NodeRun) kinds.Rows {
	turns := []store.EpicTurn{turnAt(1, "work"), turnAt(2, "complete")}
	integrated := doneTask(2, 2)
	integrated.Batch = batchPath
	tasks := []store.EpicTask{doneTask(1, 1), integrated}
	epic := store.EpicRow{
		ID: decideEpic, Base: legacySynced, StoredBase: legacyStored, Phase: "accepted", Turns: turns, Tasks: tasks,
		Requirements: []store.EpicRequirement{itemRequirement("R1")},
	}
	return kinds.Rows{Epic: epic, Runs: append([]store.NodeRun{runningPlan(1)}, runs...)}
}

// TestEpicBaseLegacyPlanEndsBuiltWhenNoSyncHoldsIt: an epic shaped as the migration leaves it, whose sync run ended
// skipped, has its plan end. The passed sync run that moved its stored base counts as the capture that makes R1 green
// at that base, so the plan ends built. The plan does not end while a sync run still runs under it, and it parks
// unbuilt when that sync's capture of R1 is missing.
func TestEpicBaseLegacyPlanEndsBuiltWhenNoSyncHoldsIt(t *testing.T) {
	t.Parallel()
	flow := append([]workflow.Node{baseEvidence("red")}, planWithChecks(batchNode())...)
	red := ended(startOf("red", "red", "evidence", 0), "passed")
	reg := integrateKinds(scripted{})
	skipped := ended(attempted(startOf("plan/M0", "sync", "sync", 0), 1), "skipped")
	skipped.Output = legacySkipOutput
	passed := ended(legacySyncRun("plan/M1", legacyStored), "passed")
	passed.Output = "merged"
	redOfR1 := redRun(red, "R1", evidence.VerdictFails, true)
	captured := captureRun(passed, "R1")
	unbuiltR1 := "epic not built: R1 is not green at the stored base " + legacyStored
	requirements := []store.EpicRequirement{itemRequirement("R1")}
	decideCases(t, []decideCase{
		{
			name: "skipped and passed syncs end the plan built, with R1 green at the stored base",
			flow: flow,
			rows: withEvidence(legacyEpicRows(red, skipped, passed), requirements, redOfR1, captured),
			reg:  reg,
			want: []Action{{Kind: ActEnd, Ends: []store.NodeEnd{{Path: "plan", Outcome: "complete"}}, Built: true}},
		},
		{
			name: "a sync run still running holds the plan",
			flow: flow,
			rows: withEvidence(legacyEpicRows(red, running(legacySyncRun("plan/M1", legacyStored))),
				requirements, redOfR1, captured),
			reg:  reg,
			want: []Action{{Kind: ActRun, Path: "plan", NodeKind: "plan"}},
		},
		{
			name: "a passed sync without R1's capture at the stored base leaves the epic unbuilt",
			flow: flow,
			rows: withEvidence(legacyEpicRows(red, skipped, passed), requirements, redOfR1),
			reg:  reg,
			want: []Action{{
				Kind: ActParkEpic, Reason: unbuiltR1, Trigger: unbuiltTrigger(unbuiltR1),
			}},
		},
	})
}

// TestEpicBaseLegacyReviewersStartFromTheSyncedBase: the reviewers of an epic after the migration start over the change
// from its base, which is the commit its sync merged, so the change they read leaves origin's main out.
func TestEpicBaseLegacyReviewersStartFromTheSyncedBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	if err := s.CreateEpic(ctx, store.EpicRow{
		ID: runEpic, Repository: runRepo, Base: legacySynced, Title: "Run the workflow", Phase: runPhase,
		Created: runClock(), Changed: runClock(),
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	run := reviewWorkFrozenRun(t, s)
	epic := epicOf(t, s)
	reviewers := &reviewWorkReviewers{}
	steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}}
	runner := reviewWorkRunner(s, steps, reviewers, reviewWorkGetenv(t))
	reviewerID := workID(t, s, "review", "reviewer 1")
	act := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: "review", NodeKind: reviewNodeKind, Run: run,
		IDs:   []string{reviewerID},
		Units: []kinds.Unit{{ID: reviewerID, Part: "reviewer 1", Reviewer: "security"}},
	}
	if err := runner.work(ctx, epic, act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	want := []reviewWorkCall{{name: "security", base: legacySynced, revision: reviewWorkFrozen}}
	if !reflect.DeepEqual(reviewers.calls, want) {
		t.Errorf("reviewer starts = %+v, want %+v", reviewers.calls, want)
	}
}

// TestEpicBaseLegacyOwnedStartsFromTheSyncedBase: the owned node of an epic after the migration records the paths its
// branch changes over its base, the commit its sync merged. origin's own change, which that commit holds, is not among
// them, so only the epic's change is recorded.
func TestEpicBaseLegacyOwnedStartsFromTheSyncedBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, _ := nodeRepo(t)
	nodeWrite(t, repo, "origin.txt", "origin's main\n")
	nodeGit(t, repo, "add", "origin.txt")
	nodeGit(t, repo, "commit", "-q", "-m", "origin's main")
	synced := nodeGit(t, repo, "rev-parse", "HEAD")
	nodeEpicBranch(t, repo, "b.go")
	s := openRunStore(t)
	createNodeEpic(t, s, repo, synced, nodeOwnedFlow)
	runNodes(s, newNodeFakes(s)).Pass(ctx)

	if got, want := runAt(t, s, "owned").Result, `["b.go"]`; got != want {
		t.Errorf("owned result = %q, want %q", got, want)
	}
}
