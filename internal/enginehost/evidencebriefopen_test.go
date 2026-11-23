package enginehost

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// briefOpenRunID is the id of the brief the brief tests file.
const briefOpenRunID = "brief-1"

// briefOpenEnded is the state of a run that has ended, as the store names it.
const briefOpenEnded = "ended"

// briefOpenUnattached is the reason a brief ends not captured once three gatherers have left it unattached.
const briefOpenUnattached = "left unattached by 3 gatherers"

// briefOpenNow returns the time the brief tests' gatherers are recorded at.
func briefOpenNow() time.Time {
	return time.Unix(1700000005, 0)
}

// briefOpenSteps is the EvidenceSteps of the brief tests. Start records each start, and the briefs the store holds open
// as the start begins, so a test sees what the store held when the next gatherer started. Subscribe keeps each step's
// subscription, so a test ends a step by offering its end to it.
type briefOpenSteps struct {
	st *store.Store

	mu     sync.Mutex
	starts []StepStart
	open   [][]store.EvidenceRun
	subs   map[string]*StepSubscription
}

// Start records start, with the briefs the store holds open, and returns its id.
func (s *briefOpenSteps) Start(ctx context.Context, start StepStart) (string, error) {
	open, err := s.st.OpenBriefs(ctx, start.Epic)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, start)
	s.open = append(s.open, open)
	return start.ID, nil
}

// Subscribe keeps the subscription to ids, so a test can offer their ends to it.
func (s *briefOpenSteps) Subscribe(_ context.Context, ids []string) (*StepSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := NewStepSubscription(ids)
	for _, id := range ids {
		s.subs[id] = sub
	}
	return sub, nil
}

// startedSteps returns the starts recorded so far, in order.
func (s *briefOpenSteps) startedSteps() []StepStart {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.starts)
}

// openAt returns the briefs the store held open when the start numbered n, from 1, began.
func (s *briefOpenSteps) openAt(n int) []store.EvidenceRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.open[n-1])
}

// subscription returns the subscription to step, or nil when the loop has not watched step yet.
func (s *briefOpenSteps) subscription(step string) *StepSubscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subs[step]
}

// briefOpenSetup stores gathererTestEpic over a repository, with one waiting captured brief per id in briefs. It
// returns the store, the steps, and a Gatherers over them. Its clock is fixed and its timers never fire, so a gatherer
// moves on only when a test offers its end.
func briefOpenSetup(t *testing.T, briefs ...string) (*store.Store, *briefOpenSteps, *Gatherers) {
	t.Helper()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	gathererTestEpicIn(t, st, repo, base)
	for _, id := range briefs {
		gathererTestAddBrief(t, st, id, base, []string{"/bin/sh", "-c", "cat marker"})
	}
	steps := &briefOpenSteps{st: st, subs: map[string]*StepSubscription{}}
	g := NewGatherers(GatherersConfig{
		Store: st, Steps: steps, Deliver: (&captureTestDeliveries{}).deliver,
		Getenv: gathererTestGetenv(t, gathererTestRoles),
		Now:    briefOpenNow,
		After:  func(time.Duration) <-chan time.Time { return nil },
		NewTimer: func(time.Duration) (<-chan time.Time, func() bool) {
			return nil, func() bool { return true }
		},
	})
	t.Cleanup(g.Close)
	return st, steps, g
}

// briefOpenAwaitStart waits until n gatherer steps have started, and returns the n-th, numbered from 1.
func briefOpenAwaitStart(t *testing.T, steps *briefOpenSteps, n int) StepStart {
	t.Helper()
	waittest.Until(t, nil, fmt.Sprintf("gatherer %d starts", n), func() bool {
		return len(steps.startedSteps()) >= n
	})
	return steps.startedSteps()[n-1]
}

// briefOpenEnd ends step, a gatherer step, with note as its return. It waits until the loop watches step, then offers
// the step's end to its subscription.
func briefOpenEnd(t *testing.T, steps *briefOpenSteps, step, note string) {
	t.Helper()
	waittest.Until(t, nil, "the loop watches "+step, func() bool { return steps.subscription(step) != nil })
	steps.subscription(step).Offer(store.StepRow{
		ID: step, End: stepLoopEndDone, Result: `{"status":"done","note":"` + note + `"}`,
	})
}

// briefOpenRun returns the stored brief of the brief tests, and fails the test when it cannot be read.
func briefOpenRun(t *testing.T, st *store.Store) store.EvidenceRun {
	t.Helper()
	run, err := st.EvidenceRun(t.Context(), briefOpenRunID)
	if err != nil {
		t.Fatalf("EvidenceRun %s: %v", briefOpenRunID, err)
	}
	return run
}

// TestBriefOpenStaysWaitingWhenGathererEnds ends a gatherer that took a brief and left it unattached. The brief is
// waiting with no gatherer and one gatherer counted against it when the next gatherer starts, and the ended gatherer
// is recorded as ended.
func TestBriefOpenStaysWaitingWhenGathererEnds(t *testing.T) {
	t.Parallel()
	st, steps, g := briefOpenSetup(t, briefOpenRunID)
	g.Wake(t.Context(), gathererTestEpic)
	first := briefOpenAwaitStart(t, steps, 1)
	briefOpenEnd(t, steps, first.ID, "nothing attached")
	briefOpenAwaitStart(t, steps, 2)

	open := steps.openAt(2)
	if len(open) != 1 || open[0].ID != briefOpenRunID || open[0].State != store.RunWaiting ||
		open[0].Gatherer != "" || open[0].Gatherers != 1 {
		t.Fatalf("briefs open at the second start = %+v, want the brief waiting with no gatherer, left by one",
			open)
	}
	ended, found, err := st.EvidenceGathererOf(t.Context(), first.ID)
	if err != nil || !found {
		t.Fatalf("EvidenceGathererOf %s = found %t, err %v", first.ID, found, err)
	}
	if !ended.Ended.Equal(briefOpenNow()) {
		t.Fatalf("first gatherer ended at %v, want %v", ended.Ended, briefOpenNow())
	}
}

// TestBriefOpenLaterGathererTakesTheBriefsLeft ends a gatherer that left a brief unattached, then checks the next
// gatherer takes the brief: it is gathering, held by that gatherer, with one gatherer counted against it.
func TestBriefOpenLaterGathererTakesTheBriefsLeft(t *testing.T) {
	t.Parallel()
	st, steps, g := briefOpenSetup(t, briefOpenRunID)
	g.Wake(t.Context(), gathererTestEpic)
	first := briefOpenAwaitStart(t, steps, 1)
	briefOpenEnd(t, steps, first.ID, "nothing attached")
	second := briefOpenAwaitStart(t, steps, 2)
	waittest.Until(t, nil, "the second gatherer takes the brief", func() bool {
		run := briefOpenRun(t, st)
		return run.State == store.RunGathering && run.Gatherer == second.ID
	})

	run := briefOpenRun(t, st)
	if run.Gatherers != 1 {
		t.Fatalf("brief counts %d gatherers, want 1", run.Gatherers)
	}
	running, found, err := st.RunningEvidenceGatherer(t.Context(), gathererTestEpic)
	if err != nil || !found || running.Step != second.ID || running.Seq != 2 {
		t.Fatalf("running gatherer = %+v found %t err %v, want step %s as gatherer 2",
			running, found, err, second.ID)
	}
}

// TestBriefOpenThirdGathererLeavesBriefNotCaptured ends three gatherers, each of which leaves the brief unattached. The
// third ends the brief as not captured, with the reason that three gatherers left it, and no judge starts for it: no
// fourth gatherer starts, and no step is started for anything else.
func TestBriefOpenThirdGathererLeavesBriefNotCaptured(t *testing.T) {
	t.Parallel()
	st, steps, g := briefOpenSetup(t, briefOpenRunID)
	g.Wake(t.Context(), gathererTestEpic)
	for n := 1; n <= 3; n++ {
		step := briefOpenAwaitStart(t, steps, n)
		briefOpenEnd(t, steps, step.ID, "nothing attached")
	}
	waittest.Until(t, nil, "the brief ends not captured", func() bool {
		return briefOpenRun(t, st).State == briefOpenEnded
	})
	waittest.Until(t, nil, "the gatherer loop ends", g.idle)

	starts := steps.startedSteps()
	if len(starts) != 3 {
		t.Fatalf("started %d steps, want the three gatherers and no judge", len(starts))
	}
	for _, start := range starts {
		if start.Task != gathererTask {
			t.Fatalf("started a step of task %q, want only gatherers", start.Task)
		}
	}
	run := briefOpenRun(t, st)
	if run.NotCaptured != briefOpenUnattached || run.Gatherers != 3 || !run.Ended.Equal(briefOpenNow()) {
		t.Fatalf("brief = not captured %q, gatherers %d, ended %v; want %q, 3, %v",
			run.NotCaptured, run.Gatherers, run.Ended, briefOpenUnattached, briefOpenNow())
	}
	if run.JudgeStep != "" || run.Judges != 0 || run.Gatherer != "" {
		t.Fatalf("brief has judge step %q, %d judges, gatherer %q; want none",
			run.JudgeStep, run.Judges, run.Gatherer)
	}
}
