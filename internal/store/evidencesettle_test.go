package store

import (
	"context"
	"testing"
	"time"
)

// settleGatherer and settleNextGatherer are the gatherer steps the settle tests start, one after the other.
const (
	settleGatherer     = "settle-gatherer"
	settleNextGatherer = "settle-next-gatherer"
	settleCapture      = "settle-capture"
)

// settleAt returns the time the settle tests end gatherers, captures and briefs at.
func settleAt() time.Time {
	return time.Unix(1700000100, 0)
}

// settleStartGatherer starts step as the gatherer of runEpic, which takes each waiting brief, and fails the test unless the
// gatherer is recorded under step.
func settleStartGatherer(t *testing.T, s *Store, step string) {
	t.Helper()
	gatherer, err := s.StartEvidenceGatherer(context.Background(), runEpic, step, runCreatedAt())
	if err != nil {
		t.Fatalf("StartEvidenceGatherer %s: %v", step, err)
	}
	if gatherer.Step != step || gatherer.Epic != runEpic {
		t.Fatalf("StartEvidenceGatherer %s = %+v, want step %s of epic %s", step, gatherer, step, runEpic)
	}
}

// settleSeed stores a waiting brief for each id in s and starts settleGatherer, which takes them all.
func settleSeed(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		brief := newRun(id, runCreatedAt())
		brief.State = RunWaiting
		if err := s.AddEvidenceRun(ctx, brief); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", id, err)
		}
	}
	settleStartGatherer(t, s, settleGatherer)
}

// settleAddCapture stores id as a running capture by settleGatherer, at the briefs' commit, started for briefs.
func settleAddCapture(t *testing.T, s *Store, id string, briefs ...string) {
	t.Helper()
	if err := s.AddCapture(context.Background(), Capture{
		ID: id, Epic: runEpic, Repository: "/repo", Step: settleGatherer, Role: "evidence gatherer", Runner: "local",
		Commit: "abc1234", Argv: []string{"go", "test"}, Briefs: briefs, Limit: 60, Created: runCreatedAt(),
	}); err != nil {
		t.Fatalf("AddCapture %s: %v", id, err)
	}
}

// settleEndGatherer ends step, the running gatherer of runEpic, and returns the briefs its end sent back to waiting.
func settleEndGatherer(t *testing.T, s *Store, step string) []EvidenceRun {
	t.Helper()
	released, err := s.EndEvidenceGatherer(context.Background(), runEpic, step, "returned", settleAt())
	if err != nil {
		t.Fatalf("EndEvidenceGatherer %s: %v", step, err)
	}
	return released
}

// settleEndCapture ends settleCapture as end says, then settles its briefs, and returns the runs the settle moved.
func settleEndCapture(t *testing.T, s *Store, end EvidenceCapture) []EvidenceRun {
	t.Helper()
	end.At = settleAt()
	if err := s.RecordCaptureEnd(context.Background(), settleCapture, end); err != nil {
		t.Fatalf("RecordCaptureEnd: %v", err)
	}
	moved, err := s.SettleCaptureBriefs(context.Background(), settleCapture, settleAt())
	if err != nil {
		t.Fatalf("SettleCaptureBriefs: %v", err)
	}
	return moved
}

// settleWant fails the test unless the run with id is stored in state, held by gatherer, with gatherers counted and
// capture attached.
func settleWant(t *testing.T, s *Store, id, state, gatherer string, gatherers int, capture string) EvidenceRun {
	t.Helper()
	run, err := s.EvidenceRun(context.Background(), id)
	if err != nil {
		t.Fatalf("EvidenceRun %s: %v", id, err)
	}
	if run.State != state || run.Gatherer != gatherer || run.Gatherers != gatherers || run.Capture != capture {
		t.Fatalf("run %s = state %q gatherer %q gatherers %d capture %q, want %q %q %d %q",
			id, run.State, run.Gatherer, run.Gatherers, run.Capture, state, gatherer, gatherers, capture)
	}
	return run
}

// settleIDs returns the ids of runs, in order.
func settleIDs(runs []EvidenceRun) []string {
	ids := make([]string, len(runs))
	for i, run := range runs {
		ids[i] = run.ID
	}
	return ids
}

// TestEndEvidenceGathererKeepsABriefARunningCaptureAnswers ends a gatherer that holds two briefs while a capture it
// started for the first still runs. The first stays gathering, uncounted, for its capture; only the second goes back to
// waiting and counts the gatherer.
func TestEndEvidenceGathererKeepsABriefARunningCaptureAnswers(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	settleSeed(t, s, "brief-answered", "brief-unanswered")
	settleAddCapture(t, s, settleCapture, "brief-answered")

	released := settleEndGatherer(t, s, settleGatherer)

	if ids := settleIDs(released); len(ids) != 1 || ids[0] != "brief-unanswered" {
		t.Fatalf("EndEvidenceGatherer released %v, want only brief-unanswered", ids)
	}
	settleWant(t, s, "brief-answered", RunGathering, settleGatherer, 0, "")
	settleWant(t, s, "brief-unanswered", RunWaiting, "", 1, "")
}

// TestSettleCaptureBriefsAttachesACaptureWhoseGathererEnded ends the gatherer while its capture runs, then ends the capture
// with a failing exit, which a red run captures. The settle attaches the capture to the brief, which moves to judging
// with the capture's exit and log, and a second settle moves nothing.
func TestSettleCaptureBriefsAttachesACaptureWhoseGathererEnded(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	settleSeed(t, s, "brief-1")
	settleAddCapture(t, s, settleCapture, "brief-1")
	settleEndGatherer(t, s, settleGatherer)

	moved := settleEndCapture(t, s, EvidenceCapture{Exit: 1, LogHash: "sha256-log", LogBytes: 10})

	if ids := settleIDs(moved); len(ids) != 1 || ids[0] != "brief-1" {
		t.Fatalf("SettleCaptureBriefs moved %v, want brief-1", ids)
	}
	run := settleWant(t, s, "brief-1", "judging", settleGatherer, 0, settleCapture)
	if run.Exit != 1 || run.LogHash != "sha256-log" || run.NotCaptured != "" {
		t.Fatalf("run brief-1 = exit %d log %q not captured %q, want the capture's exit 1 and log, captured",
			run.Exit, run.LogHash, run.NotCaptured)
	}
	again, err := s.SettleCaptureBriefs(context.Background(), settleCapture, settleAt())
	if err != nil || len(again) != 0 {
		t.Fatalf("second SettleCaptureBriefs = %v, %v, want nothing moved", settleIDs(again), err)
	}
}

// TestSettleCaptureBriefsAttachesWhileItsGathererRuns ends a capture while the gatherer that started it still runs. The
// brief takes the capture all the same, so the gatherer need not attach it.
func TestSettleCaptureBriefsAttachesWhileItsGathererRuns(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	settleSeed(t, s, "brief-1")
	settleAddCapture(t, s, settleCapture, "brief-1")

	settleEndCapture(t, s, EvidenceCapture{})

	settleWant(t, s, "brief-1", "judging", settleGatherer, 0, settleCapture)
}

// TestSettleCaptureBriefsReleasesABriefItsTimedOutCaptureLeft ends the gatherer while its capture runs, then times the
// capture out. The brief, which no one answers now, goes back to waiting and counts the gatherer, so the next gatherer
// takes it.
func TestSettleCaptureBriefsReleasesABriefItsTimedOutCaptureLeft(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	settleSeed(t, s, "brief-1")
	settleAddCapture(t, s, settleCapture, "brief-1")
	settleEndGatherer(t, s, settleGatherer)

	moved := settleEndCapture(t, s, EvidenceCapture{TimedOut: true})

	if ids := settleIDs(moved); len(ids) != 1 || ids[0] != "brief-1" {
		t.Fatalf("SettleCaptureBriefs moved %v, want brief-1", ids)
	}
	settleWant(t, s, "brief-1", RunWaiting, "", 1, "")
}

// TestSettleCaptureBriefsLeavesAFailedCaptureToItsRunningGatherer times out a capture while the gatherer that started it
// still runs. The brief stays with that gatherer, which is told the capture ended and chooses what comes next.
func TestSettleCaptureBriefsLeavesAFailedCaptureToItsRunningGatherer(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	settleSeed(t, s, "brief-1")
	settleAddCapture(t, s, settleCapture, "brief-1")

	moved := settleEndCapture(t, s, EvidenceCapture{TimedOut: true})

	if len(moved) != 0 {
		t.Fatalf("SettleCaptureBriefs moved %v, want nothing", settleIDs(moved))
	}
	settleWant(t, s, "brief-1", RunGathering, settleGatherer, 0, "")
}

// TestSettleCaptureBriefsLeavesAFailedCaptureToAnotherRunningCapture starts two captures for one brief and ends the
// gatherer, then times the first out. The brief stays gathering for the second capture, uncounted.
func TestSettleCaptureBriefsLeavesAFailedCaptureToAnotherRunningCapture(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	settleSeed(t, s, "brief-1")
	settleAddCapture(t, s, settleCapture, "brief-1")
	settleAddCapture(t, s, "settle-capture-2", "brief-1")
	settleEndGatherer(t, s, settleGatherer)

	moved := settleEndCapture(t, s, EvidenceCapture{TimedOut: true})

	if len(moved) != 0 {
		t.Fatalf("SettleCaptureBriefs moved %v, want nothing", settleIDs(moved))
	}
	settleWant(t, s, "brief-1", RunGathering, settleGatherer, 0, "")
}

// TestSettleCaptureBriefsEndsABriefAtTheGathererLimit leaves a brief unattached by two gatherers, then has a third start a
// capture for it and end while it runs. The capture times out, which is the brief's third gatherer gone with nothing
// attached: the brief takes the capture and ends not captured with its reason, as killed at its limit.
func TestSettleCaptureBriefsEndsABriefAtTheGathererLimit(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	settleSeed(t, s, "brief-1")
	settleEndGatherer(t, s, settleGatherer)
	settleStartGatherer(t, s, settleNextGatherer)
	settleEndGatherer(t, s, settleNextGatherer)
	settleStartGatherer(t, s, settleGatherer+"-3")
	settleAddCapture(t, s, settleCapture, "brief-1")
	settleEndGatherer(t, s, settleGatherer+"-3")
	settleWant(t, s, "brief-1", RunGathering, settleGatherer+"-3", 2, "")

	settleEndCapture(t, s, EvidenceCapture{TimedOut: true})

	run := settleWant(t, s, "brief-1", runEnded, settleGatherer+"-3", 2, settleCapture)
	if run.NotCaptured != notCapturedKilled {
		t.Fatalf("run brief-1 not captured = %q, want %q", run.NotCaptured, notCapturedKilled)
	}
}
