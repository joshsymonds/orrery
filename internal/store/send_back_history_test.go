package store

import (
	"context"
	"reflect"
	"testing"
)

// sendBackHistoryRow is one stored row of a task's history: its generation, then the columns that tell it apart.
type sendBackHistoryRow struct {
	Generation int64
	Key        string
}

// sendBackHistoryRows returns the rows query reads for task 1 of sendBackEpic, each as its generation and a key the
// query joins from the row's own columns, in the order the query gives.
func sendBackHistoryRows(t *testing.T, s *Store, query string) []sendBackHistoryRow {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), query, sendBackEpic)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	var got []sendBackHistoryRow
	for rows.Next() {
		var row sendBackHistoryRow
		if err = rows.Scan(&row.Generation, &row.Key); err != nil {
			t.Fatalf("scan history: %v", err)
		}
		got = append(got, row)
	}
	if err = rowsDone(rows); err != nil {
		t.Fatalf("read history: %v", err)
	}
	return got
}

// sendBackHistoryReview is the review of gate review at attempt 1, position 0, run by step, failing with one finding
// whose id and claim are id and claim.
func sendBackHistoryReview(step, id, claim string) TaskReview {
	return TaskReview{
		Attempt: 1, Position: 0, Gate: "review", Step: step, Verdict: "fails",
		Findings: []TaskFinding{{
			Position: 0, ID: id, Claim: claim, Citation: "R5", Location: "a.go:3", Evidence: "line 3",
			VerifyBy: "run it",
		}},
	}
}

// sendBackHistoryVerify is the build of task 1 at verify on the first finding of its attempt-1 review at gate 0, built
// on sendBackBase.
func sendBackHistoryVerify() TaskBuild {
	return TaskBuild{Stage: "verify", Base: sendBackBase, Attempt: 1, Revision: sendBackBase}
}

// TestSendBackHistoryKeepsEarlierRowsAndStoresTheReopenedReview checks that a send_back deletes nothing from a task's
// history. Task 1 is reviewed at attempt 1 position 0 with finding F1, climbs to rung 2 on a fail_task, and is sent
// back. Its reopened build is reviewed again at attempt 1 position 0, with finding F2, and F2 is verified. Every review,
// finding, rung and rung outcome stays stored, the earlier ones at generation 0 and the reopen's at generation 1; the
// result lands on F2 alone; and the epic read gives the task only its generation 1 review and ladder.
func TestSendBackHistoryKeepsEarlierRowsAndStoresTheReopenedReview(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const first = "the first build failed"
	s := sendBackStore(t)
	sendBackOpen(t, s, 1, TaskBuild{Stage: "review", Base: sendBackBase, Attempt: 1, Revision: sendBackBase})
	earlier := sendBackHistoryReview("step-review-0", "F1", "the earlier claim")
	if err := s.AddTaskReview(ctx, sendBackEpic, 1, sendBackTask(t, s, 1).Build, sendBackHistoryVerify(),
		earlier); err != nil {
		t.Fatalf("AddTaskReview before the send_back: %v", err)
	}
	sendBackFail(t, s, 1, first)
	sendBackCarry(t, s, sendBackAction(t, s, 2, sendBackInput()))

	reopened := sendBackHistoryReview("step-review-1", "F2", "the reopened claim")
	if err := s.AddTaskReview(ctx, sendBackEpic, 1, sendBackTask(t, s, 1).Build, sendBackHistoryVerify(),
		reopened); err != nil {
		t.Fatalf("AddTaskReview at attempt 1 position 0 after the send_back: %v", err)
	}
	verified := sendBackHistoryVerify()
	verified.FindingPos = 1
	if err := s.SetFindingResult(ctx, sendBackEpic, 1, sendBackHistoryVerify(), verified, TaskFindingResult{
		Attempt: 1, Review: 0, Position: 0, Step: "step-verify-1", Result: "confirmed", Reason: "it holds",
	}); err != nil {
		t.Fatalf("SetFindingResult of F2: %v", err)
	}

	reviews := sendBackHistoryRows(t, s, `SELECT generation, attempt || '/' || position || '/' || step
		FROM epic_task_reviews WHERE epic = ? AND task = 1 ORDER BY generation, attempt, position`)
	wantReviews := []sendBackHistoryRow{{0, "1/0/step-review-0"}, {1, "1/0/step-review-1"}}
	if !reflect.DeepEqual(reviews, wantReviews) {
		t.Errorf("stored reviews = %+v, want %+v", reviews, wantReviews)
	}
	findings := sendBackHistoryRows(t, s, `SELECT generation, id || '/' || result
		FROM epic_task_findings WHERE epic = ? AND task = 1 ORDER BY generation, attempt, review, position`)
	wantFindings := []sendBackHistoryRow{{0, "F1/"}, {1, "F2/confirmed"}}
	if !reflect.DeepEqual(findings, wantFindings) {
		t.Errorf("stored findings = %+v, want %+v", findings, wantFindings)
	}
	rungs := sendBackHistoryRows(t, s, `SELECT generation, rung || '/' || moved_by
		FROM epic_task_rungs WHERE epic = ? AND task = 1 ORDER BY generation, rung`)
	wantRungs := []sendBackHistoryRow{{0, "1/"}, {0, "2/fail_task"}, {1, "1/send_back"}}
	if !reflect.DeepEqual(rungs, wantRungs) {
		t.Errorf("stored rungs = %+v, want %+v", rungs, wantRungs)
	}
	outcomes := sendBackHistoryRows(t, s, `SELECT generation, rung || '/' || outcome || '/' || reason
		FROM epic_task_rung_outcomes WHERE epic = ? AND task = 1 ORDER BY generation, rung`)
	wantOutcomes := []sendBackHistoryRow{{0, "1/failed/" + first}}
	if !reflect.DeepEqual(outcomes, wantOutcomes) {
		t.Errorf("stored rung outcomes = %+v, want %+v", outcomes, wantOutcomes)
	}

	task := sendBackTask(t, s, 1)
	if task.Generation != 1 {
		t.Errorf("generation = %d, want 1 after one send_back", task.Generation)
	}
	reopened.Findings[0].Step, reopened.Findings[0].Result, reopened.Findings[0].Reason =
		"step-verify-1", "confirmed", "it holds"
	if want := []TaskReview{reopened}; !reflect.DeepEqual(task.Reviews, want) {
		t.Errorf("reviews read:\n got %+v\nwant %+v", task.Reviews, want)
	}
	if !reflect.DeepEqual(task.Rungs, sendBackRungs()) {
		t.Errorf("rungs read:\n got %+v\nwant %+v", task.Rungs, sendBackRungs())
	}
}
