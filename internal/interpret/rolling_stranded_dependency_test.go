package interpret

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/store"
)

// strandedCommit writes file on top of parent and returns the commit, leaving the repository detached at it.
func strandedCommit(t *testing.T, repo, parent, file string) string {
	t.Helper()
	nodeGit(t, repo, "checkout", "-q", "--detach", parent)
	nodeWrite(t, repo, file, "change\n")
	nodeGit(t, repo, "add", file)
	nodeGit(t, repo, "commit", "-q", "-m", file)
	return nodeGit(t, repo, "rev-parse", "HEAD")
}

// strandedRepo makes a scratch repository whose epic branch is at base. It returns the repository, base, and composed,
// a child of base the branch does not hold, at which a failed batch composed task 1.
func strandedRepo(t *testing.T) (string, string, string) {
	t.Helper()
	repo, base := nodeRepo(t)
	composed := strandedCommit(t, repo, base, "composed.txt")
	nodeGit(t, repo, "update-ref", build.BranchRef(runEpic), base)
	return repo, base, composed
}

// strandedRead reads the epic of repo with candidate. Task 1 is done and claimed by a failed integrate run that composed
// it at composed, and task 2 is open, depends on task 1 and has its build running. It returns the stranded tasks that
// startStranded reads from the repository, and the actions Decide gives those rows with them set as Stranded.
func strandedRead(t *testing.T, repo, base, candidate, composed string) ([]int64, []Action) {
	t.Helper()
	dependent := openTask(2, 1)
	dependent.DependsOn = []int64{1}
	tasks := []store.EpicTask{claimed(1, rollingBatchPath), dependent}
	epic := store.EpicRow{ID: runEpic, Repository: repo, Candidate: candidate, Tasks: tasks}
	result := fmt.Sprintf(
		`{"base":"%s","candidate":"%s","composed":[{"task":1,"commit":"%s"}],"leftOut":[]}`, base, composed, composed,
	)
	batch := store.NodeRun{
		ID: "batch-1", Path: rollingBatchPath, Kind: kindIntegrate, State: store.NodeEnded, Outcome: outcomeFailed,
		Result: result, Started: runClock(),
	}
	stranded, err := startStranded(context.Background(), epic, []store.NodeRun{batch})
	if err != nil {
		t.Fatalf("startStranded: %v", err)
	}
	rows := rowsOf(
		[]store.EpicTurn{turnAt(1, "work")}, epic.Tasks, runningPlan(0), running(childOf("plan/build/T2", 0, 2, 1, 2)),
		batch,
	)
	rows.Stranded = stranded
	return stranded, Decide(rollingFlow(), rows, integrateKinds(scripted{}))
}

// TestRollingIntegrateStrandedDependencyRead: T1 is done and claimed by a failed integrate run that composed it at a
// commit the repository holds, and T2 is open and depends on it. When neither the epic's candidate nor its branch tip
// holds that commit, startStranded reads T1 as stranded, and T2 is given a gap naming it. When the candidate holds it,
// nothing is stranded, and T2 runs.
func TestRollingIntegrateStrandedDependencyRead(t *testing.T) {
	t.Parallel()
	const reason = "dependency T1 was composed by a failed batch whose work the base does not hold"
	t.Run("neither the candidate nor the branch tip holds the work", func(t *testing.T) {
		t.Parallel()
		repo, base, composed := strandedRepo(t)
		candidate := strandedCommit(t, repo, base, "candidate.txt")
		stranded, acts := strandedRead(t, repo, base, candidate, composed)
		if !reflect.DeepEqual(stranded, []int64{1}) {
			t.Fatalf("startStranded = %v, want [1]", stranded)
		}
		want := Action{Kind: ActGapTask, Path: "plan/build/T2", Task: 2, Reason: reason}
		var gaps []Action
		for _, act := range acts {
			if act.Kind == ActGapTask {
				gaps = append(gaps, act)
			}
		}
		if !reflect.DeepEqual(gaps, []Action{want}) {
			t.Errorf("gap tasks = %+v, want %+v", gaps, want)
		}
		if hasRunAt(acts, "plan/build/T2") {
			t.Errorf("Decide = %+v, want no run for T2, whose dependency is stranded", acts)
		}
	})
	t.Run("the candidate holds the work", func(t *testing.T) {
		t.Parallel()
		repo, base, composed := strandedRepo(t)
		candidate := strandedCommit(t, repo, composed, "candidate.txt")
		stranded, acts := strandedRead(t, repo, base, candidate, composed)
		if len(stranded) != 0 {
			t.Fatalf("startStranded = %v, want none", stranded)
		}
		if !hasRunAt(acts, "plan/build/T2") {
			t.Errorf("Decide = %+v, want T2, whose dependency the candidate holds, run", acts)
		}
	})
}
