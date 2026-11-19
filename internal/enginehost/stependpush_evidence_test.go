package enginehost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidencePushNow returns the clock every Evidence in these tests reads.
func evidencePushNow() time.Time {
	return time.Unix(1700000500, 0)
}

// evidencePushSteps is an EvidenceSteps for the one judge of a run. The judge ends with startEnd when startEnd has an end, at
// its start; otherwise the test ends it with publish. Each Subscribe is counted, and fails with the next error of subErrs while
// one is queued. A subscription made once the judge has ended is offered its end, and each row publish is given goes to every
// subscription made.
type evidencePushSteps struct {
	mu         sync.Mutex
	startEnd   store.StepRow
	subErrs    []error
	judge      string
	end        store.StepRow
	subs       []*StepSubscription
	subscribes int
}

// Start starts the judge under the id its start carries. A second judge is an error, since these tests start one.
func (f *evidencePushSteps) Start(_ context.Context, start StepStart) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.judge != "" {
		return "", errors.New("evidence push test: a second judge started")
	}
	f.judge = start.ID
	if f.startEnd.End != "" {
		f.end = f.startEnd
		f.end.ID = start.ID
	}
	return start.ID, nil
}

// Subscribe counts the subscribe, and fails it with the next queued error. Otherwise it returns a subscription to ids, offered
// the judge's end when the judge has ended.
func (f *evidencePushSteps) Subscribe(_ context.Context, ids []string) (*StepSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribes++
	if len(f.subErrs) > 0 {
		err := f.subErrs[0]
		f.subErrs = f.subErrs[1:]
		return nil, err
	}
	sub := NewStepSubscription(ids)
	if f.end.End != "" {
		sub.Offer(f.end)
	}
	f.subs = append(f.subs, sub)
	return sub, nil
}

// publish gives row to every subscription made so far. A row that ends the judge is kept as its end, so a subscription made later
// is offered it too.
func (f *evidencePushSteps) publish(row store.StepRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row.End != "" {
		f.end = row
	}
	for _, sub := range f.subs {
		sub.Offer(row)
	}
}

// judgeID returns the id the judge started under.
func (f *evidencePushSteps) judgeID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.judge
}

// subscribeCount returns how many Subscribe calls have been made.
func (f *evidencePushSteps) subscribeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subscribes
}

// evidencePushSetup stores an epic whose Requirement R1 is a supplied item that reads notes.txt at HEAD, and returns the store
// and HEAD. A supplied run reads its file without launching a gate.
func evidencePushSetup(t *testing.T) (*store.Store, string) {
	t.Helper()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"notes.txt": "the build passes\n"})
	st := epicTestStore(t)
	item := evidence.Item{Supplied: []string{"notes.txt"}, Conclusion: "the build passes", Judge: "reviewer"}
	evidenceRunSeed(t, st, repo, item, item)
	return st, head
}

// evidencePushNew returns the Evidence of evidenceRunNew over steps, whose subscribe retries arm clock and whose verdicts are
// dated by evidencePushNow.
func evidencePushNew(t *testing.T, st *store.Store, steps EvidenceSteps, clock *retryClock) *Evidence {
	t.Helper()
	ev := evidenceRunNew(t, st, steps)
	ev.newTimer = clock.newTimer
	ev.now = evidencePushNow
	return ev
}

// evidencePushStart starts a green run of R1 at head through ev, and returns it as Run stores it.
func evidencePushStart(t *testing.T, ev *Evidence, head string) store.EvidenceRun {
	t.Helper()
	started, err := ev.Run(t.Context(), evidenceRunEpic, "R1", head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("Run(R1, %s, green): %v", head, err)
	}
	return started
}

// evidencePushVerdict returns the verdict stored for run id, which is empty until one is recorded.
func evidencePushVerdict(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	run, err := st.EvidenceRun(t.Context(), id)
	if err != nil {
		t.Fatalf("read evidence run %s: %v", id, err)
	}
	return run.Verdict
}

// TestStepEndPushEvidenceVerdictWaitsForTheJudgeEnd records a run's verdict once its judge's end is offered, and not before.
func TestStepEndPushEvidenceVerdictWaitsForTheJudgeEnd(t *testing.T) {
	t.Parallel()
	st, head := evidencePushSetup(t)
	steps := &evidencePushSteps{}
	ev := evidencePushNew(t, st, steps, &retryClock{armed: make(chan armedRetry, 8)})

	started := evidencePushStart(t, ev, head)
	evidenceRunEventually(t, engineOf(ev), "the judge subscribes", func() bool { return steps.subscribeCount() == 1 })
	if verdict := evidencePushVerdict(t, st, started.ID); verdict != "" {
		t.Fatalf("verdict %q recorded before the judge's end was offered", verdict)
	}
	steps.publish(store.StepRow{ID: steps.judgeID(), End: stepLoopEndDone})
	got := evidenceRunSettled(t, engineOf(ev), st, started.ID)
	if got.Verdict != evidence.VerdictHolds {
		t.Fatalf("verdict = %q, want holds", got.Verdict)
	}
}

// TestStepEndPushEvidenceStoredEndIsRecordedAtSubscribe records the verdict of a judge whose end was stored before the judge
// was subscribed to, which the subscription is offered when it is made.
func TestStepEndPushEvidenceStoredEndIsRecordedAtSubscribe(t *testing.T) {
	t.Parallel()
	st, head := evidencePushSetup(t)
	steps := &evidencePushSteps{startEnd: store.StepRow{End: stepLoopEndDone}}
	ev := evidencePushNew(t, st, steps, &retryClock{armed: make(chan armedRetry, 8)})

	started := evidencePushStart(t, ev, head)
	got := evidenceRunSettled(t, engineOf(ev), st, started.ID)
	if got.Verdict != evidence.VerdictHolds {
		t.Fatalf("verdict = %q, want holds", got.Verdict)
	}
}

// TestStepEndPushEvidenceHeldJudgeRecordsNoVerdict offers a judge held by a usage limit alone, which records no verdict. The
// judge's later end then gives the one verdict, from the one judge the run started.
func TestStepEndPushEvidenceHeldJudgeRecordsNoVerdict(t *testing.T) {
	t.Parallel()
	st, head := evidencePushSetup(t)
	steps := &evidencePushSteps{}
	ev := evidencePushNew(t, st, steps, &retryClock{armed: make(chan armedRetry, 8)})

	started := evidencePushStart(t, ev, head)
	evidenceRunEventually(t, engineOf(ev), "the judge subscribes", func() bool { return steps.subscribeCount() == 1 })
	steps.publish(store.StepRow{ID: steps.judgeID(), LoopLimited: true})
	if verdict := evidencePushVerdict(t, st, started.ID); verdict != "" {
		t.Fatalf("verdict %q recorded for a judge only held", verdict)
	}
	steps.publish(store.StepRow{ID: steps.judgeID(), End: stepLoopEndDone})
	got := evidenceRunSettled(t, engineOf(ev), st, started.ID)
	if got.Verdict != evidence.VerdictHolds || got.Judges != 1 {
		t.Fatalf("run = verdict %q judges %d, want holds from one judge", got.Verdict, got.Judges)
	}
}

// TestStepEndPushEvidenceFailedSubscribeRetriesOnTheTimer fails the first subscribe to a judge that has ended. Nothing is recorded
// until the armed timer fires, and then the judge is subscribed to again and its verdict is recorded.
func TestStepEndPushEvidenceFailedSubscribeRetriesOnTheTimer(t *testing.T) {
	t.Parallel()
	st, head := evidencePushSetup(t)
	steps := &evidencePushSteps{
		startEnd: store.StepRow{End: stepLoopEndDone},
		subErrs:  []error{errors.New("subscribe to steps: transient")},
	}
	clock := &retryClock{armed: make(chan armedRetry, 8)}
	ev := evidencePushNew(t, st, steps, clock)

	started := evidencePushStart(t, ev, head)
	evidenceRunEventually(t, engineOf(ev), "the retry is armed", func() bool { return len(clock.armed) == 1 })
	retry := <-clock.armed
	if retry.d != evidenceSubscribeRetry {
		t.Fatalf("retry armed for %v, want %v", retry.d, evidenceSubscribeRetry)
	}
	if n := steps.subscribeCount(); n != 1 {
		t.Fatalf("subscribes = %d before the timer fires, want 1", n)
	}
	if verdict := evidencePushVerdict(t, st, started.ID); verdict != "" {
		t.Fatalf("verdict %q recorded before the timer fired", verdict)
	}
	retry.fire <- evidencePushNow()
	got := evidenceRunSettled(t, engineOf(ev), st, started.ID)
	if got.Verdict != evidence.VerdictHolds {
		t.Fatalf("verdict = %q, want holds", got.Verdict)
	}
	if n := steps.subscribeCount(); n != 2 {
		t.Fatalf("subscribes = %d, want 2", n)
	}
	clock.noRetryArmed(t)
}

// TestStepEndPushEvidenceNoStepIsNotRetried fails the subscribe to a judge with an error matching store.ErrNoStep. Nothing will
// end the judge, so the judge records no verdict, is not subscribed to again, and arms no timer.
func TestStepEndPushEvidenceNoStepIsNotRetried(t *testing.T) {
	t.Parallel()
	st, head := evidencePushSetup(t)
	steps := &evidencePushSteps{
		subErrs: []error{fmt.Errorf("subscribe to steps: %w", store.ErrNoStep)},
	}
	clock := &retryClock{armed: make(chan armedRetry, 8)}
	ev := evidencePushNew(t, st, steps, clock)

	started := evidencePushStart(t, ev, head)
	evidenceRunEventually(t, engineOf(ev), "the judge subscribes", func() bool { return steps.subscribeCount() == 1 })
	ev.Close()
	clock.noRetryArmed(t)
	if n := steps.subscribeCount(); n != 1 {
		t.Fatalf("subscribes = %d, want 1", n)
	}
	if verdict := evidencePushVerdict(t, st, started.ID); verdict != "" {
		t.Fatalf("verdict %q recorded for a judge no step will end", verdict)
	}
}
