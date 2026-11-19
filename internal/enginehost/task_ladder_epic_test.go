package enginehost

import (
	"testing"
	"time"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// The synthetic times the task's stored versions are written and settled at, in unix seconds.
const (
	versionTwoWritten   = 1700000800
	versionTwoSettled   = 1700000850
	versionThreeWritten = 1700000900
	versionThreeSettled = 1700000950
)

// The statements that write a task's stored version, review and finding into the store file.
const (
	insertVersionSQL = `INSERT INTO epic_task_versions
		(epic, task, version, source, step, reason, deliverable, files, state, refusal, created, settled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	insertReviewSQL = `INSERT INTO epic_task_reviews (epic, task, attempt, position, gate, step, verdict)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	insertFindingSQL = `INSERT INTO epic_task_findings
		(epic, task, attempt, review, position, id, claim, citation, location, evidence, verify_by, step, result, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', '', '', '', ?, ?, ?)`
)

// TestTaskLadderGetEpicListsVersionsAndFindings checks that GetEpic lists each task's stored versions in version order,
// with the fields each one changes and its state, and the reviews of its build with every finding, confirmed or not.
func TestTaskLadderGetEpicListsVersionsAndFindings(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openPlannerStore(t)
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

	// Version 3 is written first, so the read is the one that orders the versions.
	execStoreFile(t, path, insertVersionSQL, epicServiceID, 1, 3, "grant", "step-6", "the grant widens the files",
		nil, `["a.go","d.go"]`, "refused", "the files are off the target", versionThreeWritten, versionThreeSettled)
	execStoreFile(t, path, insertVersionSQL, epicServiceID, 1, 2, "orchestrator", "step-5", "narrow the deliverable",
		"Synthetic narrowed deliverable.", nil, "applied", "", versionTwoWritten, versionTwoSettled)
	execStoreFile(t, path, insertReviewSQL, epicServiceID, 1, 1, 1, "review", "step-4", "fails")
	execStoreFile(t, path, insertFindingSQL, epicServiceID, 1, 1, 1, 1, "f1", "the claim is unmet", "step-4",
		"confirmed", "")
	execStoreFile(t, path, insertFindingSQL, epicServiceID, 1, 1, 1, 2, "f2", "the naming is loose", "step-4",
		"dropped", "the naming matches the spec")

	tasks := getEpicOK(t, client).GetTasks()
	if len(tasks) != 2 || tasks[0].GetId() != "T1" {
		t.Fatalf("tasks = %v, want T1 then T2", tasks)
	}
	wantProtos(t, "task 1 versions", tasks[0].GetVersions(), []*v1.EpicTaskVersion{
		{
			Version: 2, Source: "orchestrator", Step: "step-5", Reason: "narrow the deliverable", State: "applied",
			Changed: []string{"deliverable"}, Deliverable: "Synthetic narrowed deliverable.",
			Created: versionTwoWritten, Settled: versionTwoSettled,
		},
		{
			Version: 3, Source: "grant", Step: "step-6", Reason: "the grant widens the files", State: "refused",
			Changed: []string{"files"}, Files: []string{"a.go", "d.go"}, Refusal: "the files are off the target",
			Created: versionThreeWritten, Settled: versionThreeSettled,
		},
	})
	wantProtos(t, "task 1 reviews", tasks[0].GetReviews(), []*v1.EpicTaskReview{
		{
			Attempt: 1, Gate: "review", Step: "step-4", Verdict: "fails",
			Findings: []*v1.EpicTaskFinding{
				{Id: "f1", Claim: "the claim is unmet", Step: "step-4", Result: "confirmed"},
				{
					Id: "f2", Claim: "the naming is loose", Step: "step-4", Result: "dropped",
					Reason: "the naming matches the spec",
				},
			},
		},
	})
	if got := tasks[1].GetVersions(); len(got) != 0 {
		t.Errorf("task 2 versions = %v, want none for a task never re-specified", got)
	}
}
