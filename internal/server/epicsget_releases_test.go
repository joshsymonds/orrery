package server

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// releaseEpic returns the stored epic of the release tests: a candidate run, a release step run that failed, a release
// step run of the next attempt that passed, and a judge run, in the order the stored epic lists them.
func releaseEpic() *enginev1.StoredEpic {
	epic := storedEpicOf("repoa/released", "released", epicsAgo(time.Hour))
	epic.Releases = []*enginev1.EpicReleaseRun{
		{
			Part: "candidate", Position: 1, Attempt: 1, Step: "step-candidate", Start: "synthetic-start",
			Head: "synthetic-head-candidate", End: "done", Commits: []string{"synthetic-commit-1"},
			Evidence: "synthetic candidate evidence", Passed: true, At: 1700000001,
		},
		{
			Part: "release", Position: 1, Attempt: 1, Step: "step-release", Start: "synthetic-head-candidate",
			Head: "synthetic-head-failed", End: "failed", Commits: []string{"synthetic-commit-2"},
			Evidence: "synthetic failed evidence", Passed: false, Reason: "synthetic failed reason", At: 1700000002,
		},
		{
			Part: "release", Position: 1, Attempt: 2, Step: "step-release", Start: "synthetic-head-failed",
			Head: "synthetic-head-passed", End: "done", Commits: []string{"synthetic-commit-3", "synthetic-commit-4"},
			Evidence: "synthetic passed evidence", Passed: true, At: 1700000003,
		},
		{
			Part: "judge", Position: 1, Attempt: 2, Step: "step-judge", Start: "synthetic-head-passed",
			Head: "synthetic-head-passed", End: "done", Evidence: "synthetic judge evidence", Passed: true,
			Reason: "synthetic judge reason", At: 1700000004,
		},
	}
	return epic
}

func TestEpicsGetAnswersReleaseRunsInOrder(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{releaseEpic()}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/released")

	wantReleases := []*orreryv1.EpicReleaseRun{
		{
			Part: "candidate", Position: 1, Attempt: 1, Step: "step-candidate", Start: "synthetic-start",
			Head: "synthetic-head-candidate", End: "done", Commits: []string{"synthetic-commit-1"},
			Evidence: "synthetic candidate evidence", Passed: true, At: 1700000001,
		},
		{
			Part: "release", Position: 1, Attempt: 1, Step: "step-release", Start: "synthetic-head-candidate",
			Head: "synthetic-head-failed", End: "failed", Commits: []string{"synthetic-commit-2"},
			Evidence: "synthetic failed evidence", Passed: false, Reason: "synthetic failed reason", At: 1700000002,
		},
		{
			Part: "release", Position: 1, Attempt: 2, Step: "step-release", Start: "synthetic-head-failed",
			Head: "synthetic-head-passed", End: "done", Commits: []string{"synthetic-commit-3", "synthetic-commit-4"},
			Evidence: "synthetic passed evidence", Passed: true, At: 1700000003,
		},
		{
			Part: "judge", Position: 1, Attempt: 2, Step: "step-judge", Start: "synthetic-head-passed",
			Head: "synthetic-head-passed", End: "done", Evidence: "synthetic judge evidence", Passed: true,
			Reason: "synthetic judge reason", At: 1700000004,
		},
	}

	if len(got.GetReleases()) != len(wantReleases) {
		t.Fatalf("releases = %d runs, want %d", len(got.GetReleases()), len(wantReleases))
	}
	for i := range wantReleases {
		if !proto.Equal(got.GetReleases()[i], wantReleases[i]) {
			t.Errorf("releases[%d] = %v, want %v", i, got.GetReleases()[i], wantReleases[i])
		}
	}
}

func TestEpicsGetReleasesEmptyWithoutRun(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow())
	unreleased := storedEpicOf("repoa/unreleased", "accepted", epicsAgo(time.Hour))
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{unreleased}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	for _, id := range []string{"repoa/unreleased", "repoa/record"} {
		got := epicsGet(t, client, id)
		if len(got.GetReleases()) != 0 {
			t.Errorf("%s releases = %v, want empty", id, got.GetReleases())
		}
	}
}

func TestEpicsGetRedactsReleaseText(t *testing.T) {
	t.Parallel()
	epic := storedEpicOf("repoa/leaky-release", "released", epicsAgo(time.Hour))
	epic.Releases = []*enginev1.EpicReleaseRun{{
		Part: "release", Position: 1, Attempt: 1, Step: "step-release", End: "failed", Passed: false,
		Evidence: "sk-ant-api03-EXAMPLEKEY0123456789ab",
		Reason:   "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
	}}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{epic}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/leaky-release")
	if len(got.GetReleases()) != 1 {
		t.Fatalf("releases = %d runs, want 1", len(got.GetReleases()))
	}
	run := got.GetReleases()[0]
	check := func(name, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	check("run evidence", run.GetEvidence(), "[redacted]")
	check("run reason", run.GetReason(), "token [redacted] ok")
	check("run step", run.GetStep(), "step-release")
}

// proofEpic returns the stored epic of the proof tests: an epic that ended with gaps, a report of it, and four release
// proofs in contract order, a passed step, a failed one, a running one and a waiting one.
func proofEpic() *enginev1.StoredEpic {
	epic := storedEpicOf("repoa/proved", "ended_with_gaps", epicsAgo(time.Hour))
	epic.ReleaseProof = []*enginev1.EpicReleaseProof{
		{
			Position: 1, Step: "synthetic step one", Target: "synthetic target one", Undo: "synthetic undo one",
			State: "passed", Evidence: "synthetic passed evidence", Reason: "synthetic passed reason", Attempt: 1,
			At: 1700000011,
		},
		{
			Position: 2, Step: "synthetic step two", Target: "synthetic target two", Undo: "synthetic undo two",
			State: "failed", Evidence: "synthetic failed evidence", Reason: "synthetic failed reason", Attempt: 2,
			At: 1700000012,
		},
		{
			Position: 3, Step: "synthetic step three", Target: "synthetic target three", Undo: "synthetic undo three",
			State: "running",
		},
		{
			Position: 4, Step: "synthetic step four", Target: "synthetic target four", Undo: "synthetic undo four",
			State: "waiting",
		},
	}
	epic.Report = &enginev1.EpicReport{
		Outcome: "ended_with_gaps", Shipped: []string{"R1"}, NotShipped: []string{"R2", "R3"}, Why: "synthetic why",
	}
	return epic
}

func TestEpicsGetAnswersReleaseStepsInOrder(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{proofEpic()}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/proved").GetReleaseSteps()

	want := []*orreryv1.EpicReleaseProof{
		{
			Position: 1, Step: "synthetic step one", Target: "synthetic target one", Undo: "synthetic undo one",
			State: "passed", Evidence: "synthetic passed evidence", Reason: "synthetic passed reason", Attempt: 1,
			At: 1700000011,
		},
		{
			Position: 2, Step: "synthetic step two", Target: "synthetic target two", Undo: "synthetic undo two",
			State: "failed", Evidence: "synthetic failed evidence", Reason: "synthetic failed reason", Attempt: 2,
			At: 1700000012,
		},
		{
			Position: 3, Step: "synthetic step three", Target: "synthetic target three", Undo: "synthetic undo three",
			State: "running",
		},
		{
			Position: 4, Step: "synthetic step four", Target: "synthetic target four", Undo: "synthetic undo four",
			State: "waiting",
		},
	}
	if len(got) != len(want) {
		t.Fatalf("release_steps = %d steps, want %d", len(got), len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("release_steps[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEpicsGetAnswersStoredReport(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{proofEpic()}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/proved").GetReport()

	want := &orreryv1.EpicReport{
		Outcome: "ended_with_gaps", Shipped: []string{"R1"}, NotShipped: []string{"R2", "R3"}, Why: "synthetic why",
	}
	if !proto.Equal(got, want) {
		t.Errorf("report = %v, want %v", got, want)
	}
}

// TestEpicsGetWithoutProofOrReportAnswersNone answers no release steps and no report for a stored epic that has
// neither, and for a record-only epic.
func TestEpicsGetWithoutProofOrReportAnswersNone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow())
	unproved := storedEpicOf("repoa/unproved", "accepted", epicsAgo(time.Hour))
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{unproved}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	for _, id := range []string{"repoa/unproved", "repoa/record"} {
		got := epicsGet(t, client, id)
		if len(got.GetReleaseSteps()) != 0 {
			t.Errorf("%s release_steps = %v, want none", id, got.GetReleaseSteps())
		}
		if got.GetReport() != nil {
			t.Errorf("%s report = %v, want none", id, got.GetReport())
		}
	}
}

func TestEpicsGetRedactsReleaseProofAndReportWhy(t *testing.T) {
	t.Parallel()
	epic := storedEpicOf("repoa/leaky-proof", "ended_with_gaps", epicsAgo(time.Hour))
	epic.ReleaseProof = []*enginev1.EpicReleaseProof{{
		Position: 1, Step: "step-release", State: "failed",
		Evidence: "sk-ant-api03-EXAMPLEKEY0123456789ab",
		Reason:   "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
	}}
	epic.Report = &enginev1.EpicReport{
		Outcome: "ended_with_gaps", Shipped: []string{"R1"}, NotShipped: []string{"R2"},
		Why: "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{epic}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/leaky-proof")
	if len(got.GetReleaseSteps()) != 1 {
		t.Fatalf("release_steps = %d steps, want 1", len(got.GetReleaseSteps()))
	}
	step := got.GetReleaseSteps()[0]
	check := func(name, value, want string) {
		t.Helper()
		if value != want {
			t.Errorf("%s = %q, want %q", name, value, want)
		}
	}
	check("proof evidence", step.GetEvidence(), "[redacted]")
	check("proof reason", step.GetReason(), "token [redacted] ok")
	check("proof step", step.GetStep(), "step-release")
	check("report outcome", got.GetReport().GetOutcome(), "ended_with_gaps")
	check("report why", got.GetReport().GetWhy(), "token [redacted] ok")
}
