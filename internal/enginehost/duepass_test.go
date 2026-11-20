package enginehost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// duePassEpic is the epic the due pass tests create, and duePassStep the step the fake-steps tests give a due row.
const (
	duePassEpic = "synthetic-repo-0d1e2f3/due-pass"
	duePassStep = "due-pass-step"
	duePassRepo = "/work/synthetic-repo"
	// duePassStamp is the time, in unix seconds, the store writes for the epic and turn writes of these tests.
	duePassStamp int64 = 1_700_000_000
)

// dueStepCall is one AutoResume a scriptedDueSteps was asked for.
type dueStepCall struct {
	id     string
	resets int64
}

// scriptedDueSteps is the DueSteps a test hands a DuePass when no step runs. Each AutoResume is recorded and returns err.
type scriptedDueSteps struct {
	err   error
	calls []dueStepCall
}

// AutoResume records the call and returns the scripted error.
func (s *scriptedDueSteps) AutoResume(_ context.Context, id string, resets time.Time) error {
	s.calls = append(s.calls, dueStepCall{id: id, resets: resets.Unix()})
	return s.err
}

// resumeCountingStore is the DueStore a test hands a DuePass: the store, with each ResumeDueEpic that resumed counted.
type resumeCountingStore struct {
	*store.Store
	resumed int
}

// ResumeDueEpic resumes through the store and counts each resume.
func (c *resumeCountingStore) ResumeDueEpic(ctx context.Context, id string) (bool, error) {
	resumed, err := c.Store.ResumeDueEpic(ctx, id)
	if resumed {
		c.resumed++
	}
	return resumed, err
}

// passCountingStore is the DueStore a test hands a DuePass to see each pass: every DueResumes sends on passes.
type passCountingStore struct {
	*store.Store
	passes chan struct{}
}

// DueResumes sends on passes and reads the due rows of the store.
func (p passCountingStore) DueResumes(ctx context.Context) ([]store.DueResume, error) {
	p.passes <- struct{}{}
	return p.Store.DueResumes(ctx)
}

// failingEpicStore is the DueStore a test hands a DuePass whose ResumeDueEpic always fails.
type failingEpicStore struct {
	*store.Store
}

// ResumeDueEpic fails without writing.
func (failingEpicStore) ResumeDueEpic(context.Context, string) (bool, error) {
	return false, errors.New("synthetic write failure")
}

// newDuePass returns a DuePass over st and steps whose clock reads the time *at.
func newDuePass(st DueStore, steps DueSteps, at *time.Time) *DuePass {
	return NewDuePass(DuePassConfig{Store: st, Steps: steps, Now: func() time.Time { return *at }})
}

// allDueRows returns every due row st stores that is still due.
func allDueRows(t *testing.T, st *store.Store) []store.DueResume {
	t.Helper()
	rows, err := st.DueResumes(t.Context())
	if err != nil {
		t.Fatalf("DueResumes: %v", err)
	}
	return rows
}

// addDueStep stores an open step row of id whose loop is limited until resets, so its due row is due. The row has no
// loop, so a test that uses it gives the pass a Steps that runs none.
func addDueStep(t *testing.T, st *store.Store, id string, resets time.Time) {
	t.Helper()
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{
		store.StepAdd{
			ID: id, Agent: "implementer", Prompt: "synthetic prompt", Schema: "{}", Workspace: duePassRepo,
			ReadOnly: true, DoneValue: "done",
		},
		store.StepLoop{ID: id, Limited: true, LimitKind: "usage", LimitResets: resets},
	}, time.Unix(duePassStamp, 0)); err != nil {
		t.Fatalf("add limited step %s: %v", id, err)
	}
}

// createDuePassEpic stores duePassEpic in st. CreateEpic seeds it with one pending trigger, so it can claim a turn.
func createDuePassEpic(t *testing.T, st *store.Store) {
	t.Helper()
	created := time.Unix(duePassStamp, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: duePassEpic, Repository: duePassRepo, Title: "Synthetic due epic", Intent: "Synthetic intent.",
		Care: "serious", Phase: "accepted", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("CreateEpic %s: %v", duePassEpic, err)
	}
}

// claimDuePassTurn starts a turn of duePassEpic and returns it.
func claimDuePassTurn(t *testing.T, st *store.Store) store.EpicTurn {
	t.Helper()
	turn, err := st.ClaimTurn(t.Context(), duePassEpic, time.Unix(duePassStamp, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	return turn
}

// freeDuePassTurnToLimit frees turn of duePassEpic as a usage limit that resets at resets.
func freeDuePassTurnToLimit(t *testing.T, st *store.Store, turn store.EpicTurn, resets time.Time) {
	t.Helper()
	free := store.TurnFree{Reason: "usage limit", Pause: true, Limit: true, Resets: resets}
	if err := st.FreeTurn(t.Context(), duePassEpic, turn.Seq, free, time.Unix(duePassStamp, 0)); err != nil {
		t.Fatalf("FreeTurn of turn %d: %v", turn.Seq, err)
	}
}

// duePassEpicPaused reports whether duePassEpic is paused as the store reads it.
func duePassEpicPaused(t *testing.T, st *store.Store) bool {
	t.Helper()
	row, err := st.Epic(t.Context(), duePassEpic)
	if err != nil {
		t.Fatalf("Epic %s: %v", duePassEpic, err)
	}
	return row.Paused
}

// TestDuePassResumesPastDueStepOnceAtResetPlusGrace covers R1: a step row whose reset has passed by 60 s is resumed by
// one pass, and a pass 59 s past the reset resumes nothing.
func TestDuePassResumesPastDueStepOnceAtResetPlusGrace(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"step limited",
		func() bool { return stepRowOf(t, rig.steps, id).LoopLimited },
	)
	requireGone(t, pid)
	writeResumeNext(t, dir)
	at := time.Unix(probeResetUnix, 0).Add(dueGrace - time.Second)
	pass := newDuePass(rig.store, rig.steps, &at)

	pass.Pass(t.Context())
	if got := dueResumesOf(t, rig, id); len(got) != 1 {
		t.Fatalf("due rows at reset+59s = %+v, want the row still due", got)
	}
	if got := resumePromptEventsOf(t, rig, id); len(got) != 0 {
		t.Fatalf("resume prompts at reset+59s = %d, want none", len(got))
	}
	if !stepRowOf(t, rig.steps, id).LoopLimited {
		t.Fatal("LoopLimited = false at reset+59s, want the step still limited")
	}

	at = at.Add(time.Second)
	pass.Pass(t.Context())
	if got := dueResumesOf(t, rig, id); len(got) != 0 {
		t.Fatalf("due rows at reset+60s = %+v, want none", got)
	}
	requireResumedStart(t, dir)
	waittest.Until(t, nil, "resume prompt delivered once", func() bool { return resumePromptsLogged(t, dir) == 1 })
	if got := resumePromptEventsOf(t, rig, id); len(got) != 1 {
		t.Fatalf("resume prompts stored at reset+60s = %d, want one", len(got))
	}

	at = at.Add(time.Minute)
	pass.Pass(t.Context())
	if got := resumePromptEventsOf(t, rig, id); len(got) != 1 {
		t.Fatalf("resume prompts after a later pass = %d, want still one", len(got))
	}
	releaseResumedTurn(t, dir)
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"resumed step done",
		func() bool { return stepRowOf(t, rig.steps, id).End != "" },
	)
	if row := stepRowOf(t, rig.steps, id); row.End != stepLoopEndDone {
		t.Fatalf("End = %q reason %q, want %q", row.End, row.Reason, stepLoopEndDone)
	}
}

// TestDuePassKeepsStepRowDueWhileStepCapIsFull covers R1: a pass the step cap refuses leaves the row due and resumes
// nothing, and the next pass resumes the step once its slot frees.
func TestDuePassKeepsStepRowDueWhileStepCapIsFull(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"step limited",
		func() bool { return stepRowOf(t, rig.steps, id).LoopLimited },
	)
	requireGone(t, pid)
	waitStepSlotsFree(t, rig)
	writeResumeNext(t, dir)
	holder := startLimitProbe(t, rig, repo, revision, "STEPDONE")
	at := time.Unix(probeResetUnix, 0).Add(dueGrace)
	pass := newDuePass(rig.store, rig.steps, &at)

	pass.Pass(t.Context())
	if got := dueResumesOf(t, rig, id); len(got) != 1 {
		t.Fatalf("due rows with the step cap full = %+v, want the row still due", got)
	}
	if row := stepRowOf(t, rig.steps, id); !row.LoopLimited || row.End != "" {
		t.Fatalf("limited %t, end %q with the step cap full, want limited and open", row.LoopLimited, row.End)
	}
	if got := resumePromptEventsOf(t, rig, id); len(got) != 0 {
		t.Fatalf("resume prompts with the step cap full = %d, want none", len(got))
	}

	if err := rig.steps.Stop(t.Context(), holder); err != nil {
		t.Fatalf("Stop of the step holding the slot: %v", err)
	}
	pass.Pass(t.Context())
	if got := dueResumesOf(t, rig, id); len(got) != 0 {
		t.Fatalf("due rows once the slot frees = %+v, want none", got)
	}
	requireResumedStart(t, dir)
	releaseResumedTurn(t, dir)
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"resumed step done",
		func() bool { return stepRowOf(t, rig.steps, id).End != "" },
	)
	if row := stepRowOf(t, rig.steps, id); row.End != stepLoopEndDone {
		t.Fatalf("End = %q reason %q, want %q", row.End, row.Reason, stepLoopEndDone)
	}
}

// TestDuePassResumesPastDueStepAfterRestart covers R1: a fresh pass after a restart resumes a step whose reset passed
// while orreryd was down, and logs no error.
func TestDuePassResumesPastDueStepAfterRestart(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"step limited",
		func() bool { return stepRowOf(t, rig.steps, id).LoopLimited },
	)
	requireGone(t, pid)
	writeResumeNext(t, dir)

	var logs bytes.Buffer
	_, steps := restartLimitedStep(t, rig, &logs)
	at := time.Unix(probeResetUnix, 0).Add(dueGrace)
	newDuePass(rig.store, steps, &at).Pass(t.Context())

	if got := dueResumesOf(t, rig, id); len(got) != 0 {
		t.Fatalf("due rows after the restarted pass = %+v, want none", got)
	}
	requireResumedStart(t, dir)
	waittest.Until(t, nil, "resume prompt delivered once", func() bool { return resumePromptsLogged(t, dir) == 1 })
	releaseResumedTurn(t, dir)
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps, steps),
		"resumed step done",
		func() bool { return stepRowOf(t, rig.steps, id).End != "" },
	)
	requireNoLoggedError(t, &logs)
}

// TestDuePassResumesPlannerTurnThroughItsEpicRowOnce covers R1: a limited planner turn's step is stopped, which closes
// its own due row, so the epic's row resumes the epic once and the stopped step is not resumed.
func TestDuePassResumesPlannerTurnThroughItsEpicRowOnce(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	createDuePassEpic(t, rig.store)
	turn := claimDuePassTurn(t, rig.store)
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	pid := fakePID(t, filepath.Join(rig.worktrees, id))
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"step limited",
		func() bool { return stepRowOf(t, rig.steps, id).LoopLimited },
	)
	requireGone(t, pid)
	if err := rig.store.SetTurnStep(t.Context(), duePassEpic, turn.Seq, id); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	if err := rig.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of the planner step: %v", err)
	}
	reset := time.Unix(probeResetUnix, 0)
	freeDuePassTurnToLimit(t, rig.store, turn, reset)
	if got := allDueRows(t, rig.store); len(got) != 1 || got[0].Kind != dueKindEpic {
		t.Fatalf("due rows after the stopped step and the limit = %+v, want only the epic's row", got)
	}

	counting := &resumeCountingStore{Store: rig.store}
	at := reset.Add(dueGrace)
	pass := newDuePass(counting, rig.steps, &at)
	pass.Pass(t.Context())
	if duePassEpicPaused(t, rig.store) {
		t.Fatal("epic paused at reset+60s, want resumed")
	}
	if counting.resumed != 1 {
		t.Fatalf("epic resumes = %d, want one", counting.resumed)
	}
	if got := resumePromptEventsOf(t, rig, id); len(got) != 0 {
		t.Fatalf("resume prompts to the stopped planner step = %d, want none", len(got))
	}

	pass.Pass(t.Context())
	if counting.resumed != 1 {
		t.Fatalf("epic resumes after a later pass = %d, want still one", counting.resumed)
	}
}

// TestDuePassLeavesOwnerPausedEpicPausedPastItsReset covers A1: an owner pause made while a turn runs wins over the
// turn's limit, so the limit stores no due row and the epic stays paused past the reset.
func TestDuePassLeavesOwnerPausedEpicPausedPastItsReset(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	createDuePassEpic(t, st)
	turn := claimDuePassTurn(t, st)
	if err := st.PauseEpic(
		t.Context(),
		duePassEpic,
		store.Maker{By: store.MakerOwner},
		time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	reset := time.Unix(probeResetUnix, 0)
	freeDuePassTurnToLimit(t, st, turn, reset)
	if got := allDueRows(t, st); len(got) != 0 {
		t.Fatalf("due rows after the limit over the owner's pause = %+v, want none", got)
	}

	counting := &resumeCountingStore{Store: st}
	steps := &scriptedDueSteps{}
	at := reset.Add(time.Hour)
	newDuePass(counting, steps, &at).Pass(t.Context())
	if !duePassEpicPaused(t, st) {
		t.Fatal("epic resumed past the reset, want the owner's pause kept")
	}
	if counting.resumed != 0 {
		t.Fatalf("epic resumes = %d, want none", counting.resumed)
	}
}

// TestDuePassResumesLimitPausedEpicOnce covers R1: an epic a usage limit paused resumes once, when its row's reset has
// passed by 60 s, and a pass 59 s past the reset resumes nothing.
func TestDuePassResumesLimitPausedEpicOnce(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	createDuePassEpic(t, st)
	freeDuePassTurnToLimit(t, st, claimDuePassTurn(t, st), time.Unix(probeResetUnix, 0))
	counting := &resumeCountingStore{Store: st}
	at := time.Unix(probeResetUnix, 0).Add(dueGrace - time.Second)
	pass := newDuePass(counting, &scriptedDueSteps{}, &at)

	pass.Pass(t.Context())
	paused := duePassEpicPaused(t, st)
	if !paused || counting.resumed != 0 {
		t.Fatalf("at reset+59s: paused %t, resumes %d, want paused and none resumed", paused, counting.resumed)
	}
	if got := allDueRows(t, st); len(got) != 1 {
		t.Fatalf("due rows at reset+59s = %+v, want the row still due", got)
	}

	at = at.Add(time.Second)
	pass.Pass(t.Context())
	if duePassEpicPaused(t, st) {
		t.Fatal("epic paused at reset+60s, want resumed")
	}
	if counting.resumed != 1 {
		t.Fatalf("epic resumes at reset+60s = %d, want one", counting.resumed)
	}
	if got := allDueRows(t, st); len(got) != 0 {
		t.Fatalf("due rows after the resume = %+v, want none", got)
	}

	at = at.Add(time.Minute)
	pass.Pass(t.Context())
	if counting.resumed != 1 {
		t.Fatalf("epic resumes after a later pass = %d, want still one", counting.resumed)
	}
}

// TestDuePassLeavesEpicRowDueWhenItsWriteFails covers R1: an epic row whose ResumeDueEpic fails is not closed, so the
// next pass tries it again.
func TestDuePassLeavesEpicRowDueWhenItsWriteFails(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	createDuePassEpic(t, st)
	freeDuePassTurnToLimit(t, st, claimDuePassTurn(t, st), time.Unix(probeResetUnix, 0))
	at := time.Unix(probeResetUnix, 0).Add(dueGrace)

	newDuePass(failingEpicStore{Store: st}, &scriptedDueSteps{}, &at).Pass(t.Context())
	if got := allDueRows(t, st); len(got) != 1 {
		t.Fatalf("due rows after a failed epic write = %+v, want the row still due", got)
	}
}

// TestDuePassSettlesStepRowByResumeError covers R1: the outcome of a step row follows the error AutoResume returns. A
// refusal by the step cap or the gate, and any other error, leave the row due. A step that is not limited, has ended, or
// has no row closes it with no resume.
func TestDuePassSettlesStepRowByResumeError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		closed bool
	}{
		{name: "cap refusal", err: fmt.Errorf("continue step: %w", ErrCapReached), closed: false},
		{name: "gating", err: ErrStepGating, closed: false},
		{name: "other error", err: errors.New("synthetic continue failure"), closed: false},
		{name: "not limited", err: ErrStepNotLimited, closed: true},
		{name: "ended", err: ErrStepNotRunning, closed: true},
		{name: "no row", err: fmt.Errorf("step %s: %w", duePassStep, store.ErrNoStep), closed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, _ := openStore(t)
			resets := time.Unix(probeResetUnix, 0)
			addDueStep(t, st, duePassStep, resets)
			steps := &scriptedDueSteps{err: tc.err}
			at := resets.Add(dueGrace)

			newDuePass(st, steps, &at).Pass(t.Context())
			want := []dueStepCall{{id: duePassStep, resets: resets.Unix()}}
			if len(steps.calls) != 1 || steps.calls[0] != want[0] {
				t.Fatalf("AutoResume calls = %+v, want %+v", steps.calls, want)
			}
			rows := allDueRows(t, st)
			if tc.closed && len(rows) != 0 {
				t.Fatalf("due rows = %+v, want the row closed", rows)
			}
			if !tc.closed && len(rows) != 1 {
				t.Fatalf("due rows = %+v, want the row still due", rows)
			}
		})
	}
}

// TestDuePassRetriesAGatingStepOnTheNextPass covers R1: a step running its gate leaves its row due, and the next pass
// resumes it for the same reset.
func TestDuePassRetriesAGatingStepOnTheNextPass(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	resets := time.Unix(probeResetUnix, 0)
	addDueStep(t, st, duePassStep, resets)
	steps := &scriptedDueSteps{err: ErrStepGating}
	at := resets.Add(dueGrace)
	pass := newDuePass(st, steps, &at)

	pass.Pass(t.Context())
	if rows := allDueRows(t, st); len(rows) != 1 {
		t.Fatalf("due rows after the gating refusal = %+v, want the row still due", rows)
	}
	steps.err = nil
	at = at.Add(5 * time.Second)
	pass.Pass(t.Context())
	call := dueStepCall{id: duePassStep, resets: resets.Unix()}
	if len(steps.calls) != 2 || steps.calls[0] != call || steps.calls[1] != call {
		t.Fatalf("AutoResume calls = %+v, want the step resumed again for reset %d on the next pass", steps.calls,
			resets.Unix())
	}
}

// TestDuePassRunsAPassAtStartAndOnEachWake covers R1: Run passes once when it starts and once for each wake, until its
// context ends.
func TestDuePassRunsAPassAtStartAndOnEachWake(t *testing.T) {
	t.Parallel()
	st, _ := openStore(t)
	passes := make(chan struct{}, 4)
	at := time.Unix(probeResetUnix, 0)
	pass := NewDuePass(DuePassConfig{
		Store: passCountingStore{Store: st, passes: passes}, Steps: &scriptedDueSteps{},
		Now: func() time.Time { return at }, NewTimer: neverTimer,
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		pass.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	awaitPass(t, passes, done, "the pass at start")
	pass.Wake()
	awaitPass(t, passes, done, "the pass on the wake")
	cancel()
	<-done
	if got := len(passes); got != 0 {
		t.Fatalf("passes after Run returned = %d, want none", got)
	}
}

// awaitPass waits for a pass to be sent on passes. It has no deadline of its own, and fails at once, naming what, when
// done, the end of the Run that passes, closes first.
func awaitPass(t *testing.T, passes, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-passes:
	case <-done:
		select {
		case <-passes:
		default:
			t.Fatalf("%s never happened: Run returned first", what)
		}
	}
}
