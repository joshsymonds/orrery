package kinds

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The revisions of the failed-batch tests: the epic's base, the candidate of the first batch, which passed and became
// the stored base, the candidate of the second, which failed, and the candidate of the third, which composed on the
// failed candidate and passed.
const (
	failedEpicBase  = "base-1"
	firstPassed     = "s-1"
	secondFailed    = "f-2"
	thirdPassed     = "s-3"
	failedBatchPath = "plan/S2/integrate"
)

// failedBatchEpic returns an epic whose Requirements R1, R2 and R3 each name an evidence item. T1 claims R1, T2 claims
// R2 and contributes to R1, and T3 claims R3, each claimed by the batch of its own integrate run. Its stored base is
// stored.
func failedBatchEpic(stored string) store.EpicRow {
	return store.EpicRow{
		Base:       failedEpicBase,
		StoredBase: stored,
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "items are tracked", Item: evidenceItem("items are tracked")},
			{ID: "R2", Text: "the listener settles items", Item: evidenceItem("the listener settles items")},
			{ID: "R3", Text: "facts carry citations", Item: evidenceItem("facts carry citations")},
		},
		Tasks: []store.EpicTask{
			{Seq: 1, Status: taskDone, Batch: "plan/S1/integrate", Requirements: []string{"R1"}},
			{
				Seq: 2, Status: taskDone, Batch: failedBatchPath, Requirements: []string{"R2"},
				Contributes: []string{"R1"},
			},
			{Seq: 3, Status: taskDone, Batch: "plan/S3/integrate", Requirements: []string{"R3"}},
		},
	}
}

// batchResultOf returns the result of an integrate run that composed task on base into candidate.
func batchResultOf(base, candidate string, task int64) string {
	seq := strconv.FormatInt(task, 10)
	return asBefore(`{"base":"` + base + `","candidate":"` + candidate + `","composed":[{"task":` + seq +
		`,"commit":"c` + seq + `"}],"leftOut":[]}`)
}

// endedBatch returns the integrate run id at attempt 1 at path, ended with outcome at revision, whose batch composed
// task on base.
func endedBatch(id, path, outcome, base, revision string, task int64) store.NodeRun {
	return store.NodeRun{
		ID: id, Path: path, Kind: kindIntegrate, Attempt: 1, State: store.NodeEnded, Outcome: outcome,
		Revision: revision, Result: batchResultOf(base, revision, task),
	}
}

// batchCapture returns the ended capture of Requirement id by the integrate run run at attempt 1, judged as passed
// says.
func batchCapture(run, id string, passed bool) store.EvidenceRun {
	verdict := evidence.VerdictFails
	if passed {
		verdict = evidence.VerdictHolds
	}
	return store.EvidenceRun{
		ID: store.NodeWorkID(run, 1, "capture "+id), Item: id, Phase: evidence.PhaseGreen, State: runEnded,
		Verdict: verdict, Passed: passed,
	}
}

// unattachedCapture returns the ended capture of Requirement id by the integrate run run at attempt 1 that its
// gatherers left without a capture.
func unattachedCapture(run, id string) store.EvidenceRun {
	return store.EvidenceRun{
		ID: store.NodeWorkID(run, 1, "capture "+id), Item: id, Phase: evidence.PhaseGreen, State: runEnded,
		NotCaptured: "left unattached by 3 gatherers",
	}
}

// failedBatchRows returns the rows of the failed-batch epic stored at stored: the first batch passed at s-1 with R1
// green; the second, composing T2 on s-1, failed at f-2 because R2 was left unattached, though its capture of R1
// passed; and extra runs and captures after them.
func failedBatchRows(stored string, runs []store.NodeRun, captures ...store.EvidenceRun) Rows {
	return Rows{
		Epic: failedBatchEpic(stored),
		Runs: append([]store.NodeRun{
			endedBatch("int-1", "plan/S1/integrate", outcomePassed, failedEpicBase, firstPassed, 1),
			endedBatch("int-2", failedBatchPath, outcomeFailed, firstPassed, secondFailed, 2),
		}, runs...),
		Evidence: append([]store.EvidenceRun{
			batchCapture("int-1", "R1", true),
			batchCapture("int-2", "R1", true),
			unattachedCapture("int-2", "R2"),
		}, captures...),
	}
}

// TestGreenAtWalksThroughAFailedBatch: a batch that failed for one Requirement's capture still ran the others, and the
// next batch composed on its candidate. So the Requirements green at the stored base the next batch moved to are its
// own captures', and below them the failed batch's own passed captures, and below those what the failed batch did not
// touch, as it was at its base. A Requirement the failed batch touched and did not capture shows nothing there, so it
// is the only one Unbuilt names.
func TestGreenAtWalksThroughAFailedBatch(t *testing.T) {
	t.Parallel()
	third := endedBatch("int-3", "plan/S3/integrate", outcomePassed, secondFailed, thirdPassed, 3)
	rows := failedBatchRows(thirdPassed, []store.NodeRun{third}, batchCapture("int-3", "R3", true))
	if got, want := GreenAt(rows, secondFailed), map[string]bool{"R1": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("GreenAt(f-2) = %v, want %v: the failed batch's passed capture of R1 holds there", got, want)
	}
	if got, want := GreenAt(rows, thirdPassed), map[string]bool{"R1": true, "R3": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("GreenAt(s-3) = %v, want %v", got, want)
	}
	want := []string{"R2 is not green at the stored base " + thirdPassed}
	if got := Unbuilt(rows); !reflect.DeepEqual(got, want) {
		t.Errorf("Unbuilt() = %q, want %q", got, want)
	}
}

// TestGreenAtFailedBatchInheritsWhatItDidNotTouch: a Requirement no task of a failed batch claims or contributes to
// keeps, at the failed candidate, the green it had at the batch's base, as it would below a passed batch.
func TestGreenAtFailedBatchInheritsWhatItDidNotTouch(t *testing.T) {
	t.Parallel()
	epic := failedBatchEpic(firstPassed)
	epic.Tasks[1].Contributes = nil
	rows := Rows{
		Epic: epic,
		Runs: []store.NodeRun{
			endedBatch("int-1", "plan/S1/integrate", outcomePassed, failedEpicBase, firstPassed, 1),
			endedBatch("int-2", failedBatchPath, outcomeFailed, firstPassed, secondFailed, 2),
		},
		Evidence: []store.EvidenceRun{batchCapture("int-1", "R1", true), unattachedCapture("int-2", "R2")},
	}
	if got, want := GreenAt(rows, secondFailed), map[string]bool{"R1": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("GreenAt(f-2) = %v, want %v", got, want)
	}
}

// TestGreenAtFailedBatchKeepsAFailedVerdict: a capture a failed batch's judge ruled fails is not green there, and a
// failed batch's judgeless capture is green only when it was carried, its Requirement green at the batch's base.
func TestGreenAtFailedBatchKeepsAFailedVerdict(t *testing.T) {
	t.Parallel()
	judgeless := store.EvidenceRun{
		ID: store.NodeWorkID("int-2", 1, "capture R1"), Item: "R1", Phase: evidence.PhaseGreen, State: runEnded,
		JudgeReason: judgeGaveNone,
	}
	cases := []struct {
		name    string
		capture store.EvidenceRun
		base    bool
		want    map[string]bool
	}{
		{"judged fails", batchCapture("int-2", "R1", false), true, map[string]bool{}},
		{"judgeless and carried", judgeless, true, map[string]bool{"R1": true}},
		{"judgeless and not green at its base", judgeless, false, map[string]bool{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			evidenceRuns := []store.EvidenceRun{tc.capture, unattachedCapture("int-2", "R2")}
			if tc.base {
				evidenceRuns = append(evidenceRuns, batchCapture("int-1", "R1", true))
			}
			rows := Rows{
				Epic: failedBatchEpic(firstPassed),
				Runs: []store.NodeRun{
					endedBatch("int-1", "plan/S1/integrate", outcomePassed, failedEpicBase, firstPassed, 1),
					endedBatch("int-2", failedBatchPath, outcomeFailed, firstPassed, secondFailed, 2),
				},
				Evidence: evidenceRuns,
			}
			if got := GreenAt(rows, secondFailed); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("GreenAt(f-2) = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBatchOnAFailedCandidateCapturesItsWork: a batch that composes on the candidate of a failed batch captures, with
// its own Requirements, those the failed batch's tasks claim or contribute to, since nothing else vouches for that
// work at the candidate it moves to.
func TestBatchOnAFailedCandidateCapturesItsWork(t *testing.T) {
	t.Parallel()
	rows := failedBatchRows(firstPassed, nil)
	node := Node{Run: store.NodeRun{
		ID: testRunID, Path: "plan/S3/integrate", Kind: kindIntegrate, Attempt: 1,
		Revision: thirdPassed, Result: batchResultOf(secondFailed, thirdPassed, 3),
	}}
	checkDecisions(t, Integrate{}, []decideCase{{
		"captures the failed batch's Requirements with its own", node, rows,
		Action{
			Kind:  ActCapture,
			IDs:   []string{workID("capture R1"), workID("capture R2"), workID("capture R3")},
			Items: []string{"R1", "R2", "R3"},
		},
	}})
}

// TestBatchOnAFailedCandidateNamesTheBatchBase: a Requirement a batch on a failed candidate did not capture is green
// at the batch base it composed on, and its line names that base rather than the stored base.
func TestBatchOnAFailedCandidateNamesTheBatchBase(t *testing.T) {
	t.Parallel()
	epic := failedBatchEpic(firstPassed)
	epic.Tasks[1].Contributes = nil
	epic.Tasks[1].Requirements = nil
	rows := Rows{
		Epic: epic,
		Runs: []store.NodeRun{
			endedBatch("int-1", "plan/S1/integrate", outcomePassed, failedEpicBase, firstPassed, 1),
			endedBatch("int-2", failedBatchPath, outcomeFailed, firstPassed, secondFailed, 2),
		},
		Evidence: []store.EvidenceRun{
			batchCapture("int-1", "R1", true),
			{ID: workID("capture R3"), Item: "R3", Phase: evidence.PhaseGreen, State: runEnded,
				Verdict: evidence.VerdictHolds, Passed: true},
		},
	}
	node := Node{Run: store.NodeRun{
		ID: testRunID, Path: "plan/S3/integrate", Kind: kindIntegrate, Attempt: 1,
		Revision: thirdPassed, Result: batchResultOf(secondFailed, thirdPassed, 3), RefsChecked: true,
	}}
	checkDecisions(t, Integrate{}, []decideCase{{
		"untouched Requirement green at the batch base", node, rows,
		Action{Kind: ActAdvance, Output: "R1 green at the batch base " + secondFailed +
			"; no task of this batch changes it, so it was not captured again\nR2 not yet green\nR3 green"},
	}})
}

// TestBatchOnAnUnknownBaseCapturesEverything: a batch whose base is neither the stored base nor a revision an
// integrate run ended at, such as the merge that joins a candidate to a stored base that moved, captures every
// Requirement that names an item, since nothing is known of what that base changed.
func TestBatchOnAnUnknownBaseCapturesEverything(t *testing.T) {
	t.Parallel()
	rows := Rows{
		Epic: failedBatchEpic(firstPassed),
		Runs: []store.NodeRun{
			endedBatch("int-1", "plan/S1/integrate", outcomePassed, failedEpicBase, firstPassed, 1),
		},
		Evidence: []store.EvidenceRun{batchCapture("int-1", "R1", true)},
	}
	node := Node{Run: store.NodeRun{
		ID: testRunID, Path: "plan/S3/integrate", Kind: kindIntegrate, Attempt: 1,
		Revision: thirdPassed, Result: batchResultOf("joined-1", thirdPassed, 3),
	}}
	checkDecisions(t, Integrate{}, []decideCase{{
		"captures every item", node, rows,
		Action{
			Kind:  ActCapture,
			IDs:   []string{workID("capture R1"), workID("capture R2"), workID("capture R3")},
			Items: []string{"R1", "R2", "R3"},
		},
	}})
}

// recaptureAt returns orreryd's recapture of Requirement id of the failed-batch epic at stored, in state.
func recaptureAt(id, stored, state string) store.EvidenceAmendment {
	item := evidenceItem(map[string]string{
		"R1": "items are tracked", "R2": "the listener settles items", "R3": "facts carry citations",
	}[id])
	return store.EvidenceAmendment{
		Requirement: id, Amender: store.AmenderOrreryd, Prior: item, Item: item, State: state, StoredBase: stored,
	}
}

// TestRecaptureNamesWhatNoRunCapturedAtTheStoredBase: before a plan parks its epic as not built, it recaptures each
// Requirement not green at the stored base, once there: not one that is green, held by a gap, dropped as met at the
// base, or recaptured at that stored base already, and nothing while the stored base is the epic's base.
func TestRecaptureNamesWhatNoRunCapturedAtTheStoredBase(t *testing.T) {
	t.Parallel()
	third := endedBatch("int-3", "plan/S3/integrate", outcomePassed, secondFailed, thirdPassed, 3)
	walked := func(edit func(*Rows)) Rows {
		rows := failedBatchRows(thirdPassed, []store.NodeRun{third})
		edit(&rows)
		return rows
	}
	cases := []struct {
		name string
		rows Rows
		want []string
	}{
		{"not green and never recaptured", walked(func(*Rows) {}), []string{"R2", "R3"}},
		{"green is left as it is", walked(func(rows *Rows) {
			rows.Evidence = append(rows.Evidence, batchCapture("int-3", "R3", true))
		}), []string{"R2"}},
		{"held by a gap", walked(func(rows *Rows) {
			rows.Epic.Tasks[1].Status = taskGap
		}), []string{"R3"}},
		{"dropped as met at the base", walked(func(rows *Rows) {
			rows.Epic.Requirements[1].DroppedBy = 4
		}), []string{"R3"}},
		{"recaptured at the stored base, capturing or applied", walked(func(rows *Rows) {
			rows.Epic.Amendments = []store.EvidenceAmendment{
				recaptureAt("R2", thirdPassed, store.AmendmentCapturing),
				recaptureAt("R3", thirdPassed, store.AmendmentApplied),
			}
		}), nil},
		{"recaptured only at an earlier stored base", walked(func(rows *Rows) {
			rows.Epic.Amendments = []store.EvidenceAmendment{recaptureAt("R2", firstPassed, store.AmendmentApplied)}
		}), []string{"R2", "R3"}},
		{"stored base still the epic's base", Rows{Epic: failedBatchEpic(failedEpicBase)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Recapture(tc.rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Recapture() = %q, want %q", got, tc.want)
			}
		})
	}
}
