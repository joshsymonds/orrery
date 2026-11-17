package store_test

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// preparedRevision and preparedResult are the revision and result startPreparedRelease stores, as a release prepare of a
// candidate that is not behind stores them.
const (
	preparedRevision = "rev-1"
	preparedResult   = `{"behind":false,"candidate":"rev-1","tip":"tip-1"}`
)

// startPreparedRelease starts the release node run of releaseEpic at attempt 1, with preparedRevision and preparedResult
// stored as a release prepare stores them. releaseEpic must be stored releasing.
func startPreparedRelease(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	run := store.NodeRun{Path: "release", ID: "id-release", Node: "release", Kind: "release", Attempt: 1}
	if err := s.StartNodes(ctx, releaseEpic, []store.NodeRun{run}, nil, releaseRunAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.PrepareNode(ctx, releaseEpic, "release", 1, preparedRevision, ""); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if err := s.RecordNodeResult(ctx, releaseEpic, "release", 1, preparedResult); err != nil {
		t.Fatalf("RecordNodeResult: %v", err)
	}
}

// releaseNodeRun returns the release node run of releaseEpic as the store reads it.
func releaseNodeRun(t *testing.T, s *store.Store) store.NodeRun {
	t.Helper()
	runs, err := s.NodeRuns(context.Background(), releaseEpic)
	if err != nil {
		t.Fatalf("NodeRuns of epic %s: %v", releaseEpic, err)
	}
	for _, run := range runs {
		if run.Path == "release" {
			return run
		}
	}
	t.Fatalf("epic %s has no release node run", releaseEpic)
	return store.NodeRun{}
}

// verifyRunsOf returns the verify runs of releaseEpic as the store reads them.
func verifyRunsOf(t *testing.T, s *store.Store) []store.ReleaseRun {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), releaseEpic)
	if err != nil {
		t.Fatalf("EpicFlow %s: %v", releaseEpic, err)
	}
	var out []store.ReleaseRun
	for _, run := range flow.Releases {
		if run.Part == store.ReleasePartVerify {
			out = append(out, run)
		}
	}
	return out
}

// TestReleaseRepreparesMarkStaleClearsRunAndStoresVerifyRun checks that MarkCandidateStale stores a verify run of the
// attempt that did not pass, with the head and reason it names, and clears the running release run's revision and result
// in the same write, so the run can be prepared again at its attempt.
func TestReleaseRepreparesMarkStaleClearsRunAndStoresVerifyRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseReleasing))
	startPreparedRelease(t, s)

	const reason = "origin's main is not in candidate rev-1"
	if err := s.MarkCandidateStale(ctx, releaseEpic, "release", 1, "rev-1", reason, releaseRunAt()); err != nil {
		t.Fatalf("MarkCandidateStale: %v", err)
	}

	run := releaseNodeRun(t, s)
	if run.Revision != "" || run.Result != "" || run.State != store.NodeRunning || run.Attempt != 1 {
		t.Errorf("release run = %+v, want running at attempt 1 with no revision and no result", run)
	}
	verify := verifyRunsOf(t, s)
	if len(verify) != 1 || verify[0].Head != "rev-1" || verify[0].Attempt != 1 || verify[0].Position != 0 ||
		verify[0].Passed || verify[0].Reason != reason || !verify[0].At.Equal(releaseRunAt()) {
		t.Errorf("verify runs = %+v, want the one run of rev-1 at attempt 1 that did not pass", verify)
	}

	if err := s.PrepareNode(ctx, releaseEpic, "release", 1, "rev-2", ""); err != nil {
		t.Fatalf("PrepareNode after the mark: %v", err)
	}
	if got := releaseNodeRun(t, s).Revision; got != "rev-2" {
		t.Errorf("revision after the new prepare = %q, want rev-2", got)
	}
}

// TestReleaseRepreparesMarkStaleNumbersASecondMark checks that a second mark of the same attempt is stored after the
// first rather than dropped as a repeat of its key, so both marks are kept.
func TestReleaseRepreparesMarkStaleNumbersASecondMark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseReleasing))
	startPreparedRelease(t, s)

	if err := s.MarkCandidateStale(ctx, releaseEpic, "release", 1, "rev-1", "first", releaseRunAt()); err != nil {
		t.Fatalf("MarkCandidateStale rev-1: %v", err)
	}
	if err := s.PrepareNode(ctx, releaseEpic, "release", 1, "rev-2", ""); err != nil {
		t.Fatalf("PrepareNode rev-2: %v", err)
	}
	const second = `{"behind":false,"candidate":"rev-2","tip":"tip-1"}`
	if err := s.RecordNodeResult(ctx, releaseEpic, "release", 1, second); err != nil {
		t.Fatalf("RecordNodeResult rev-2: %v", err)
	}
	if err := s.MarkCandidateStale(ctx, releaseEpic, "release", 1, "rev-2", "second", releaseRunAt()); err != nil {
		t.Fatalf("MarkCandidateStale rev-2: %v", err)
	}

	verify := verifyRunsOf(t, s)
	if len(verify) != 2 || verify[0].Head != "rev-1" || verify[0].Position != 0 ||
		verify[1].Head != "rev-2" || verify[1].Position != 1 {
		t.Errorf("verify runs = %+v, want rev-1 at position 0 and rev-2 at position 1", verify)
	}
}

// TestReleaseRepreparesMarkStaleChangesNothingOutsideTheRunningRun checks that a mark names a running release run at its
// attempt. A run at another attempt keeps its revision and stores nothing, and so does a run that is parked.
func TestReleaseRepreparesMarkStaleChangesNothingOutsideTheRunningRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseReleasing))
	startPreparedRelease(t, s)

	if err := s.MarkCandidateStale(ctx, releaseEpic, "release", 2, "rev-1", "later", releaseRunAt()); err != nil {
		t.Fatalf("MarkCandidateStale at attempt 2: %v", err)
	}
	if got := releaseNodeRun(t, s); got.Revision != "rev-1" || got.Result == "" {
		t.Errorf("release run at attempt 1 = %+v after a mark at attempt 2, want its revision and result kept", got)
	}
	if verify := verifyRunsOf(t, s); len(verify) != 0 {
		t.Errorf("verify runs = %+v after a mark at another attempt, want none", verify)
	}

	if err := s.StopRelease(ctx, releaseEpic, "release", 1, "publish rejected", releaseRunAt()); err != nil {
		t.Fatalf("StopRelease: %v", err)
	}
	if err := s.MarkCandidateStale(ctx, releaseEpic, "release", 1, "rev-1", "parked", releaseRunAt()); err != nil {
		t.Fatalf("MarkCandidateStale of a parked run: %v", err)
	}
	if got := releaseNodeRun(t, s); got.Revision != "rev-1" || got.State != store.NodeParked {
		t.Errorf("parked release run = %+v, want it parked with its revision kept", got)
	}
	if verify := verifyRunsOf(t, s); len(verify) != 0 {
		t.Errorf("verify runs = %+v after a mark of a parked run, want none", verify)
	}
}

// candidateRunsOf returns the candidate runs of releaseEpic as the store reads them.
func candidateRunsOf(t *testing.T, s *store.Store) []store.ReleaseRun {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), releaseEpic)
	if err != nil {
		t.Fatalf("EpicFlow %s: %v", releaseEpic, err)
	}
	var out []store.ReleaseRun
	for _, run := range flow.Releases {
		if run.Part == store.ReleasePartCandidate {
			out = append(out, run)
		}
	}
	return out
}

// TestReleaseRepreparesRecordCandidateNumbersASecondCandidate checks that a candidate prepared again at the attempt of the
// candidate it replaces is stored after it, at the next position, and that both candidates and both marks of the attempt
// are kept, as the store numbers them.
func TestReleaseRepreparesRecordCandidateNumbersASecondCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseReleasing))
	startPreparedRelease(t, s)

	first := store.ReleaseRun{Attempt: 1, Start: "tip-1", Head: "rev-1", Passed: true, At: releaseRunAt()}
	if err := s.RecordCandidate(ctx, releaseEpic, first); err != nil {
		t.Fatalf("RecordCandidate rev-1: %v", err)
	}
	if err := s.MarkCandidateStale(ctx, releaseEpic, "release", 1, "rev-1", "moved", releaseRunAt()); err != nil {
		t.Fatalf("MarkCandidateStale rev-1: %v", err)
	}
	if err := s.PrepareNode(ctx, releaseEpic, "release", 1, "rev-2", ""); err != nil {
		t.Fatalf("PrepareNode rev-2: %v", err)
	}
	if err := s.RecordNodeResult(ctx, releaseEpic, "release", 1,
		`{"behind":false,"candidate":"rev-2","tip":"tip-2"}`); err != nil {
		t.Fatalf("RecordNodeResult rev-2: %v", err)
	}
	second := store.ReleaseRun{Attempt: 1, Start: "tip-2", Head: "rev-2", Passed: true, At: releaseRunAt()}
	if err := s.RecordCandidate(ctx, releaseEpic, second); err != nil {
		t.Fatalf("RecordCandidate rev-2: %v", err)
	}

	candidates := candidateRunsOf(t, s)
	if len(candidates) != 2 || candidates[0].Head != "rev-1" || candidates[0].Position != 0 ||
		candidates[1].Head != "rev-2" || candidates[1].Position != 1 || !candidates[1].Passed {
		t.Errorf("candidate runs = %+v, want rev-1 at position 0 and rev-2 at position 1, both passed", candidates)
	}
	if verify := verifyRunsOf(t, s); len(verify) != 1 || verify[0].Head != "rev-1" || verify[0].Position != 0 {
		t.Errorf("verify runs = %+v, want the one mark of rev-1 at position 0", verify)
	}
}
