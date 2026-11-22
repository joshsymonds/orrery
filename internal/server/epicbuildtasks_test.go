package server

import (
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// buildTasksEpic returns the stored epic of the build task tests: T1, with a red run, a green run and a review of two
// findings, and T2, with none of them.
func buildTasksEpic() *enginev1.StoredEpic {
	red := &enginev1.EvidenceRun{
		Id: "red-1", Item: "R1", Epic: "repoa/built", Phase: "red", Source: "captured", State: "ended", Exit: 1,
		Verdict: "holds", JudgeReason: "synthetic judge reason", Passed: true, Revision: "synthetic-rev-red",
		Created: epicsAgo(3 * time.Hour), Ended: epicsAgo(2 * time.Hour), LogBytes: 2048, StderrBytes: 64,
		Artifacts: []*enginev1.EvidenceArtifact{{Path: "out/result.json", State: "kept", Size: 512}},
	}
	green := &enginev1.EvidenceRun{
		Id: "green-1", Item: "R1", Epic: "repoa/built", Phase: "green", Source: "captured", State: "ended",
		Verdict: "holds", Passed: true, Revision: "synthetic-rev-green", Created: epicsAgo(time.Hour),
		Ended: epicsAgo(30 * time.Minute), LogBytes: 64,
	}
	epic := storedEpicOf("repoa/built", "building", epicsAgo(time.Hour))
	epic.Tasks = []*enginev1.EpicTask{
		{
			Id: "T1", Title: "synthetic task one", Status: "done", Stage: "review",
			Requirements: []string{"R1", "R2"}, Red: red, Green: green,
			Reviews: []*enginev1.EpicTaskReview{{
				Attempt: 1, Gate: "review-gate", Step: "step-review-1", Verdict: "fails",
				Findings: []*enginev1.EpicTaskFinding{
					{
						Id: "F1", Claim: "synthetic claim one", Location: "synthetic/one.go:10", Step: "step-verify-1",
						Result: "confirmed", Reason: "synthetic reason one",
					},
					{Id: "F2", Claim: "synthetic claim two", Location: "synthetic/two.go:20"},
				},
			}, {
				Attempt: 2, Gate: "review-gate", Step: "step-review-2", Verdict: "undetermined",
				Reason: "synthetic review step stopped",
			}},
		},
		{Id: "T2", Title: "synthetic task two", Status: "open", Requirements: []string{"R2"}},
	}
	return epic
}

func TestEpicsGetAnswersBuildTasksInOrder(t *testing.T) {
	root := t.TempDir()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{buildTasksEpic()}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/built").GetBuildTasks()
	wantRed := &orreryv1.EvidenceRun{
		Id: "red-1", Phase: "red", Source: "captured", State: "ended", Exit: 1, Verdict: "holds",
		JudgeReason: "synthetic judge reason", Passed: true, Revision: "synthetic-rev-red",
		Created: epicsAgo(3 * time.Hour), Ended: epicsAgo(2 * time.Hour), LogBytes: 2048, StderrBytes: 64,
		Artifacts: []*orreryv1.EvidenceArtifact{
			{Path: "out/result.json", State: "kept", Size: 512, MediaType: "application/json"},
		},
	}
	wantGreen := &orreryv1.EvidenceRun{
		Id: "green-1", Phase: "green", Source: "captured", State: "ended", Verdict: "holds", Passed: true,
		Revision: "synthetic-rev-green", Created: epicsAgo(time.Hour), Ended: epicsAgo(30 * time.Minute), LogBytes: 64,
	}
	want := []*orreryv1.EpicBuildTask{
		{
			Id: "T1", Title: "synthetic task one", Status: "done", Stage: "review",
			Requirements: []string{"R1", "R2"}, Red: wantRed, Green: wantGreen,
			Reviews: []*orreryv1.EpicTaskReview{{
				Attempt: 1, Gate: "review-gate", Step: "step-review-1", Verdict: "fails",
				Findings: []*orreryv1.EpicTaskFinding{
					{
						Id: "F1", Claim: "synthetic claim one", Location: "synthetic/one.go:10", Step: "step-verify-1",
						Result: "confirmed", Reason: "synthetic reason one",
					},
					{Id: "F2", Claim: "synthetic claim two", Location: "synthetic/two.go:20"},
				},
			}, {
				Attempt: 2, Gate: "review-gate", Step: "step-review-2", Verdict: "undetermined",
				Reason: "synthetic review step stopped",
			}},
			Place: "integrate", Tone: "acc",
		},
		{
			Id:           "T2",
			Title:        "synthetic task two",
			Status:       "open",
			Requirements: []string{"R2"},
			Place:        "planned",
			Tone:         "plan",
		},
	}
	if len(got) != len(want) {
		t.Fatalf("build_tasks = %d tasks, want %d", len(got), len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("build_tasks[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEpicsGetRedactsBuildTaskText(t *testing.T) {
	root := t.TempDir()
	epic := storedEpicOf("repoa/leaky", "building", epicsAgo(time.Hour))
	epic.Tasks = []*enginev1.EpicTask{{
		Id: "T1", Title: "ghp_EXAMPLE0123456789abcdefABCDEF0123", Status: "open", Stage: "red",
		Red: &enginev1.EvidenceRun{
			Id:          "red-1",
			Phase:       "red",
			Source:      "captured",
			State:       "ended",
			Verdict:     "fails",
			JudgeReason: "sk-ant-api03-EXAMPLEKEY0123456789ab",
			NotCaptured: "SLACK_TOKEN=xoxb-EXAMPLE-0123456789-abcdef",
		},
		Reviews: []*enginev1.EpicTaskReview{{
			Attempt: 1, Gate: "review-gate", Step: "step-review-1", Verdict: "fails",
			Findings: []*enginev1.EpicTaskFinding{{
				Id: "ghp_EXAMPLE0123456789abcdefABCDEF0123", Claim: "Authorization: Bearer EXAMPLEBEARERTOKEN",
				Location: "OPENAI_KEY=sk-EXAMPLEKEY0123456789abcd", Step: "step-verify-1", Result: "dropped",
				Reason: "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
			}},
		}},
	}}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{epic}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/leaky").GetBuildTasks()
	if len(got) != 1 {
		t.Fatalf("build_tasks = %d tasks, want 1", len(got))
	}
	task := got[0]
	finding := task.GetReviews()[0].GetFindings()[0]
	check := func(name, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	check("title", task.GetTitle(), "[redacted]")
	check("red judge_reason", task.GetRed().GetJudgeReason(), "[redacted]")
	check("red not_captured", task.GetRed().GetNotCaptured(), "SLACK_TOKEN=[redacted]")
	check("finding id", finding.GetId(), "[redacted]")
	check("finding claim", finding.GetClaim(), "Authorization: [redacted]")
	check("finding location", finding.GetLocation(), "OPENAI_KEY=[redacted]")
	check("finding reason", finding.GetReason(), "token [redacted] ok")
	check("finding step", finding.GetStep(), "step-verify-1")
	check("finding result", finding.GetResult(), "dropped")
}

func TestEpicsGetRecordOnlyEpicHasNoBuildTasks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repoa", "record")
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow())
	writeEffortState(t, dir, "1", `{"tasks":[{"slug":"one-a","subject":"One A","status":"gated"}]}`)
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/other", "accepted", epicsAgo(time.Hour)),
	}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/record")
	if got.GetEpic().GetSource() != "record" {
		t.Fatalf("epic source = %q, want record", got.GetEpic().GetSource())
	}
	if len(got.GetTasks()) != 1 {
		t.Fatalf("tasks = %v, want the record's one-a", got.GetTasks())
	}
	if n := len(got.GetBuildTasks()); n != 0 {
		t.Errorf("build_tasks = %d, want none for a record epic", n)
	}
}
