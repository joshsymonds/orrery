package planner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// blockWant is the block a notice opens on: its name and the ref that names its target.
type blockWant struct {
	block, ref string
}

// releaseStopReason is the reason of the park a release stop tells, the text the tell's body starts with.
const releaseStopReason = "release step failed"

// checkBlock fails the test unless the desk notice and the push of n open on want.
func checkBlock(t *testing.T, n route.Notice, want blockWant) {
	t.Helper()
	got := shownOf(t, n)
	if desk := (blockWant{got.desk.Block, got.desk.BlockRef}); desk != want {
		t.Errorf("desk notice opens on %+v, want %+v", desk, want)
	}
	if push := (blockWant{got.push.Block, got.push.BlockRef}); push != want {
		t.Errorf("push opens on %+v, want %+v", push, want)
	}
}

// noticeEndingWith returns the notice of router whose desk body ends with last, and fails the test when none does. A
// park's body ends with its reason line. The tells of an epic are routed ahead of its turn's notice, so the first
// notice that ends with a park's reason is the notice of its tell.
func noticeEndingWith(t *testing.T, router *fakeAttentionRouter, last string) route.Notice {
	t.Helper()
	for _, n := range router.notices {
		if strings.HasSuffix(shownOf(t, n).desk.Body, "\n"+last) {
			return n
		}
	}
	t.Fatalf("no notice body ends with %q among %d notices", last, len(router.notices))
	return route.Notice{}
}

// stopRunnerRelease ends the planned work of the runner epic, stops it as a release stop does, and records a failed
// release step at position when position is above zero.
func stopRunnerRelease(t *testing.T, s *store.Store, position int64) {
	t.Helper()
	ctx := context.Background()
	endWorkTurn(t, s, claimedAt())
	if err := s.EndEpic(
		ctx, runnerEpicID, store.PhaseStopped, "the release stopped", nil, claimedAt().Add(time.Second),
	); err != nil {
		t.Fatalf("EndEpic stopped: %v", err)
	}
	if err := s.ParkEpic(ctx, runnerEpicID, releaseStopReason, claimedAt().Add(2*time.Second)); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	if position == 0 {
		return
	}
	if err := s.RecordRelease(ctx, runnerEpicID, store.ReleaseRun{
		Part: store.ReleasePartStep, Position: position, Attempt: 1, Step: "push",
		Reason: "tests failed", At: claimedAt(),
	}); err != nil {
		t.Fatalf("RecordRelease: %v", err)
	}
}

func TestAttentionBlockQuestionsTurnOpensOnItsFirstOpenQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, askingTurn(), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if _, err := s.AnswerQuestion(
		ctx, runnerEpicID, "answer-1", "use the store", 1, store.ChoiceOther, claimedAt(),
	); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	checkBlock(t, router.notices[0], blockWant{block: "question", ref: "Q2"})
}

func TestAttentionBlockParkOpensOnItsParkQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.FreeTurn(ctx, runnerEpicID, turn, store.TurnFree{
		Reason: "credit ran out", Park: "credit ran out",
	}, claimedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	checkBlock(t, router.notices[0], blockWant{block: "question", ref: "Q1"})
}

func TestAttentionBlockUsageLimitOpensOnNoBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.FreeTurn(ctx, runnerEpicID, turn, store.TurnFree{
		Reason: "usage limit", Pause: true, Resets: time.Unix(limitResetsUnix, 0),
	}, claimedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	checkBlock(t, router.notices[0], blockWant{})
}

func TestAttentionBlockQuestionTellOnARunningEpicOpensOnItsQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	// The turn stays running, so the park's tell is the only notice and its question is Q1.
	claimAttentionTurn(t, s)
	if err := s.ParkEpic(ctx, runnerEpicID, "credit ran out", claimedAt().Add(time.Second)); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	checkBlock(t, router.notices[0], blockWant{block: "question", ref: "Q1"})
}

func TestAttentionBlockReleaseStopTellOpensOnTheFailedReleaseStep(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	stopRunnerRelease(t, s, 2)
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(context.Background())

	checkBlock(t, noticeEndingWith(t, router, "parked: "+releaseStopReason), blockWant{block: "release_step", ref: "2"})
}

func TestAttentionBlockQuestionTellOnAStoppedEpicWithNoFailedStepOpensOnItsQuestion(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	stopRunnerRelease(t, s, 0)
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(context.Background())

	checkBlock(t, noticeEndingWith(t, router, "parked: "+releaseStopReason), blockWant{block: "question", ref: "Q1"})
}

func TestAttentionBlockRestoreNoticeKeepsTheBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, askingTurn(), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if _, err := s.AnswerQuestion(
		ctx, runnerEpicID, "answer-1", "use the store", 1, store.ChoiceOther, claimedAt(),
	); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	router := &fakeAttentionRouter{}
	attentionOver(s, router).pass(ctx)
	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}

	restored, err := RestoreNotice(ctx, router.notices[0].Source.Data, claimedAt().Unix())
	if err != nil {
		t.Fatalf("RestoreNotice: %v", err)
	}

	checkBlock(t, restored, blockWant{block: "question", ref: "Q2"})
}

func TestRestoreNoticeReadsAnOlderRowAsNoBlock(t *testing.T) {
	t.Parallel()
	data := []byte(`{"id":"orrery-1a2b3c4/planner-runner:turn:1:questions","epic":"orrery-1a2b3c4/planner-runner",` +
		`"title":"Plan the runner","body":"Q1 which-store: which store?"}`)

	restored, err := RestoreNotice(context.Background(), data, claimedAt().Unix())
	if err != nil {
		t.Fatalf("RestoreNotice: %v", err)
	}

	checkBlock(t, restored, blockWant{})
}
