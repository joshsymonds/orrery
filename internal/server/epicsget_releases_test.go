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
