package enginehost

import (
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestTaskVersionGetEpicServesTheCurrentVersionsSource checks that GetEpic serves the source of each task's current spec
// version: planner for a task at version 1, and the source of its last applied version once a grant stores one.
func TestTaskVersionGetEpicServesTheCurrentVersionsSource(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	first, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = st.SetTurnStep(ctx, epicServiceID, first.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	if err = st.CompleteTurn(ctx, epicServiceID, first.Seq, plannedReturn(), time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	tasks := getEpicOK(t, client).GetTasks()
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(tasks))
	}
	for _, task := range tasks {
		if task.GetSource() != "planner" {
			t.Errorf("%s source = %q at version %d, want planner", task.GetId(), task.GetSource(), task.GetVersion())
		}
	}

	grant := store.TaskVersionInput{
		Source: store.SourceGrant, Step: "step-2", Reason: "the implementer needs e.go", Files: []string{"e.go"},
	}
	err = st.GrantTaskFiles(ctx, epicServiceID, 1, store.TaskBuild{}, store.TaskBuild{}, grant,
		time.Unix(versionGrantedAt, 0))
	if err != nil {
		t.Fatalf("GrantTaskFiles: %v", err)
	}

	tasks = getEpicOK(t, client).GetTasks()
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d after the grant, want 2", len(tasks))
	}
	if tasks[0].GetSource() != "grant" || tasks[0].GetVersion() != 2 {
		t.Errorf("T1 source = %q at version %d, want the grant's at version 2",
			tasks[0].GetSource(), tasks[0].GetVersion())
	}
	if tasks[1].GetSource() != "planner" {
		t.Errorf("T2 source = %q, want planner: the grant is T1's", tasks[1].GetSource())
	}
}
