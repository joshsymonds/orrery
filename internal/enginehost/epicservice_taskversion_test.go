package enginehost

import (
	"slices"
	"testing"
	"time"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The synthetic times the two versions after the planner's are stored at, in unix seconds.
const (
	versionRerunAt   = 1700000500
	versionGrantedAt = 1700000600
)

// TestTaskVersionGetEpicServesEachTasksCurrentVersionAndHistory checks that GetEpic serves a planned task at its current
// version, with the deliverable and files the stored versions set, and lists every version after the planner's in order.
func TestTaskVersionGetEpicServesEachTasksCurrentVersionAndHistory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

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

	reworded := "Synthetic deliverable one, reworded by the orchestrator."
	rerun := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "T1 rerun: the spec moved", Deliverable: &reworded,
	}
	decision := store.TaskDecision{ID: "T1-request-1", Text: "T1 rerun: the spec moved"}
	err = st.RerunTask(ctx, epicServiceID, 1, store.TaskBuild{}, store.TaskBuild{}, rerun, nil, decision,
		time.Unix(versionRerunAt, 0))
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	grant := store.TaskVersionInput{
		Source: store.SourceGrant, Step: "step-3", Reason: "the implementer needs e.go", Files: []string{"e.go"},
	}
	err = st.GrantTaskFiles(ctx, epicServiceID, 1, store.TaskBuild{}, store.TaskBuild{}, grant,
		time.Unix(versionGrantedAt, 0))
	if err != nil {
		t.Fatalf("GrantTaskFiles: %v", err)
	}

	got := getEpicOK(t, client)
	tasks := got.GetTasks()
	if len(tasks) != 2 || tasks[0].GetId() != "T1" {
		t.Fatalf("tasks = %v, want T1 then T2", tasks)
	}
	task := tasks[0]
	if task.GetVersion() != 3 {
		t.Errorf("version = %d, want 3: the grant is the task's last applied version", task.GetVersion())
	}
	if task.GetDeliverable() != reworded {
		t.Errorf("deliverable = %q, want the orchestrator's %q", task.GetDeliverable(), reworded)
	}
	if want := []string{"a.go", "b.go", "e.go"}; !slices.Equal(task.GetFiles(), want) {
		t.Errorf("files = %v, want the grown list %v", task.GetFiles(), want)
	}
	wantProtos(t, "versions", task.GetVersions(), []*v1.EpicTaskVersion{
		{
			Version: 2, Source: store.SourceOrchestrator, Decision: decision.ID, Step: "step-2", Reason: rerun.Reason,
			State: "applied", Changed: []string{"deliverable"}, Deliverable: reworded,
			Created: versionRerunAt, Settled: versionRerunAt,
		},
		{
			Version: 3, Source: store.SourceGrant, Step: "step-3", Reason: grant.Reason, State: "applied",
			Changed: []string{"files"}, Files: []string{"a.go", "b.go", "e.go"},
			Created: versionGrantedAt, Settled: versionGrantedAt,
		},
	})
}
