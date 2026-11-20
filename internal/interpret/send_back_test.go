package interpret

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// sendBackReopenedAt is the second a send_back reopened the task of the tests, between the runs that composed it.
func sendBackReopenedAt() time.Time {
	return time.Unix(1700004100, 0)
}

// sendBackRepo returns an empty repository for the commits the tests make.
func sendBackRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	nodeGit(t, dir, "init", "-q")
	return dir
}

// sendBackCommit returns a commit of repo with message on top of parent, or a root commit when parent is empty.
func sendBackCommit(t *testing.T, repo, parent, message string) string {
	t.Helper()
	if parent != "" {
		nodeGit(t, repo, "checkout", "-q", parent)
	}
	nodeGit(t, repo, "commit", "-q", "--allow-empty", "-m", message)
	return nodeGit(t, repo, "rev-parse", "HEAD")
}

// sendBackRun returns an integrate run at path that ended with outcome, started at started, whose result composed each
// task of composed into the commit it names.
func sendBackRun(t *testing.T, path, outcome string, started time.Time, composed map[int64]string) store.NodeRun {
	t.Helper()
	type commit struct {
		Task   int64  `json:"task"`
		Commit string `json:"commit"`
	}
	list := make([]commit, 0, len(composed))
	for seq, sha := range composed {
		list = append(list, commit{Task: seq, Commit: sha})
	}
	result, err := json.Marshal(map[string]any{"composed": list, "leftOut": []string{}})
	if err != nil {
		t.Fatalf("encode result of run %s: %v", path, err)
	}
	return store.NodeRun{
		Path: path, Kind: kindIntegrate, State: store.NodeEnded, Outcome: outcome, Started: started,
		Result: string(result),
	}
}

// TestStrandedTasksReopenedTaskCorrectionIsStranded checks that a task a send_back reopened, whose correction a failed
// batch composed, is stranded when the stored base lacks the correction, even though the base holds the commit the task
// was built as before the reopen: that commit was composed by a run that started before the send_back.
func TestStrandedTasksReopenedTaskCorrectionIsStranded(t *testing.T) {
	t.Parallel()
	repo := sendBackRepo(t)
	base := sendBackCommit(t, repo, "", "base")
	built := sendBackCommit(t, repo, base, "task 1 as built")
	correction := sendBackCommit(t, repo, built, "task 1 corrected")
	epic := store.EpicRow{Repository: repo, Tasks: []store.EpicTask{{
		Seq: 1, Status: taskDone, Batch: "plan/integrate-2",
		Versions: []store.TaskVersion{{Version: 2, Source: store.SourceSendBack, Created: sendBackReopenedAt()}},
	}}}
	runs := []store.NodeRun{
		sendBackRun(t, "plan/integrate", outcomePassed, sendBackReopenedAt().Add(-time.Minute),
			map[int64]string{1: built}),
		sendBackRun(t, "plan/integrate-2", outcomeFailed, sendBackReopenedAt().Add(time.Minute),
			map[int64]string{1: correction}),
	}

	got, err := strandedTasks(context.Background(), epic, runs, "", built)
	if err != nil {
		t.Fatalf("strandedTasks: %v", err)
	}
	if want := []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("stranded tasks = %v, want %v: the correction is not in the stored base", got, want)
	}
}

// TestStrandedTasksSkipsAnOpenTaskAndKeepsADoneOneOfAFailedBatch checks that a task a send_back reopened is not stranded
// though a failed batch composed it, and that a done task whose failed batch composed a commit the stored base lacks
// stays stranded, so the next batch composes it again from the stored base.
func TestStrandedTasksSkipsAnOpenTaskAndKeepsADoneOneOfAFailedBatch(t *testing.T) {
	t.Parallel()
	repo := sendBackRepo(t)
	base := sendBackCommit(t, repo, "", "base")
	composed := sendBackCommit(t, repo, base, "composed by the failed batch")
	failed := sendBackRun(t, "plan/integrate", outcomeFailed, sendBackReopenedAt(),
		map[int64]string{1: composed, 2: composed})
	runs := []store.NodeRun{failed}

	t.Run("an open task is not stranded", func(t *testing.T) {
		t.Parallel()
		epic := store.EpicRow{Repository: repo, Tasks: []store.EpicTask{{Seq: 1, Status: "open"}}}
		got, err := strandedTasks(context.Background(), epic, runs, "", base)
		if err != nil {
			t.Fatalf("strandedTasks: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("stranded tasks = %v, want none for an open task", got)
		}
	})
	t.Run("a done task of a failed batch is stranded", func(t *testing.T) {
		t.Parallel()
		epic := store.EpicRow{Repository: repo, Tasks: []store.EpicTask{{
			Seq: 2, Status: taskDone, Batch: "plan/integrate",
		}}}
		got, err := strandedTasks(context.Background(), epic, runs, "", base)
		if err != nil {
			t.Fatalf("strandedTasks: %v", err)
		}
		if want := []int64{2}; !reflect.DeepEqual(got, want) {
			t.Errorf("stranded tasks = %v, want %v", got, want)
		}
	})
}
