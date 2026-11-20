package interpret

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// flowReads is the store a test wraps to count, by epic, the EpicFlow reads the runner makes, and its reads of the
// open epics' node run states.
type flowReads struct {
	Store
	flows  map[string]int
	states int
}

// newFlowReads returns s wrapped to count its reads.
func newFlowReads(s Store) *flowReads {
	return &flowReads{Store: s, flows: map[string]int{}}
}

// EpicFlow counts the read of epic id, then reads it.
func (f *flowReads) EpicFlow(ctx context.Context, id string) (store.EpicFlow, error) {
	f.flows[id]++
	return f.Store.EpicFlow(ctx, id)
}

// OpenNodeRunStates counts the read, then reads the states.
func (f *flowReads) OpenNodeRunStates(ctx context.Context) (map[string][]store.NodeRun, error) {
	f.states++
	return f.Store.OpenNodeRunStates(ctx)
}

// total returns how many EpicFlow reads were counted, over every epic.
func (f *flowReads) total() int {
	n := 0
	for _, reads := range f.flows {
		n += reads
	}
	return n
}

// reset forgets the counted reads.
func (f *flowReads) reset() {
	f.flows = map[string]int{}
	f.states = 0
}

// countReads makes the world's runner read its store through a flowReads, and returns it.
func (w *releaseWorld) countReads() *flowReads {
	reads := newFlowReads(w.runner.store)
	w.runner.store = reads
	return reads
}

// startRelease stores the release node run of epic id, running from runClock, as the pass that reaches the release
// node starts it.
func (w *releaseWorld) startRelease(id string) {
	w.t.Helper()
	if err := w.s.StartNodes(w.t.Context(), id, []store.NodeRun{{
		Path: releaseNodePath, ID: store.NodeRunID(id, releaseNodePath, 0, "start"), Node: releaseNodePath,
		Kind: kindRelease,
	}}, nil, runClock()); err != nil {
		w.t.Fatalf("StartNodes of %s: %v", id, err)
	}
}

// releasedEpic stores the epic id released, its release node run ended passed and the release slot free, as a
// release leaves an epic once its run has freed the slot and ended.
func (w *releaseWorld) releasedEpic(id string) {
	w.t.Helper()
	w.createEpic(id, "released")
	w.startRelease(id)
	if err := w.s.EndNodes(w.t.Context(), id, []store.NodeEnd{
		{Path: releaseNodePath, Outcome: outcomePassed},
	}, runClock()); err != nil {
		w.t.Fatalf("EndNodes of %s: %v", id, err)
	}
}

// restClean is the one epic of restWorld that is not at rest before the first pass: clean, with no release steps.
const restClean = "orrery-1a2b3c4/epic-clean"

// restWorld returns a world whose store holds many released epics, one an owner ended met at base with its run parked,
// one ended with gaps whose release run waits for the owner, one closed, and restClean, which the first pass ends done.
func restWorld(t *testing.T) *releaseWorld {
	t.Helper()
	w := newReleaseWorldIn(t, nil, filepath.Join(t.TempDir(), "no-repository"))
	w.runner.ending = w.s
	const released = 24
	for n := range released {
		w.releasedEpic(fmt.Sprintf("orrery-1a2b3c4/epic-released-%02d", n))
	}
	const metAtBase = "orrery-1a2b3c4/epic-met-at-base"
	w.createEpic(metAtBase, "met_at_base")
	w.startRelease(metAtBase)
	execStoreFile(t, w.path, "UPDATE epic_node_runs SET state = 'parked' WHERE epic = ?", metAtBase)
	const gaps = "orrery-1a2b3c4/epic-gaps"
	w.createEpic(gaps, "ended_with_gaps")
	w.startRelease(gaps)
	w.createEpic("orrery-1a2b3c4/epic-closed", store.PhaseClosedShipped)
	w.createEpic(restClean, store.PhaseClean)
	return w
}

// TestPassReadsNoEpicAtRest counts the EpicFlow reads of the passes over a store of many released epics, one an owner
// ended met at base with its run parked, one ended with gaps whose release run waits for the owner, one closed, and one
// clean epic with no release steps. Every pass reads the node run states of the open epics once. The first pass reads
// only the clean epic, which it ends done and whose release run it ends; the second reads none, since every epic is
// then at rest. Before the rest check, each pass read every epic that is not closed at least once: with nothing to
// decide, 27 reads a pass for this store.
func TestPassReadsNoEpicAtRest(t *testing.T) {
	t.Parallel()
	w := restWorld(t)
	const clean = restClean
	reads := w.countReads()

	w.pass()
	if reads.states != 1 {
		t.Errorf("first pass read the node run states %d times, want once", reads.states)
	}
	for id, n := range reads.flows {
		if id != clean {
			t.Errorf("first pass read epic %s %d times, want it at rest and unread", id, n)
		}
	}
	if reads.flows[clean] == 0 {
		t.Errorf("first pass did not read the clean epic, want its release decided")
	}
	if phase := w.flowOf(clean).Epic.Phase; phase != "done" {
		t.Fatalf("clean epic phase after the first pass = %q, want done", phase)
	}

	for pass := 2; pass <= 3; pass++ {
		reads.reset()
		w.pass()
		if reads.states != 1 || reads.total() != 0 {
			t.Errorf("pass %d read the node run states %d times and epics %v, want once and no epic",
				pass, reads.states, reads.flows)
		}
	}
}

// TestPassReadsEndedWithGapsEpicAgainOnceReleased: an epic that ended with gaps has its release run started by the pass
// that reaches its release node, and is then at rest: its release waits for the owner, so the passes that follow do not
// read it. Once the owner releases it, which moves it to clean, the next pass reads it and ends it done, and the epic
// is at rest again.
func TestPassReadsEndedWithGapsEpicAgainOnceReleased(t *testing.T) {
	t.Parallel()
	w := newReleaseWorldIn(t, nil, filepath.Join(t.TempDir(), "no-repository"))
	w.runner.ending = w.s
	w.createEpic(runEpic, "ended_with_gaps")
	reads := w.countReads()

	w.pass()
	if reads.flows[runEpic] == 0 {
		t.Fatalf("first pass did not read the epic, want its release run started")
	}
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeRunning {
		t.Fatalf("release run state = %q after the first pass, want running", run.State)
	}
	for pass := 2; pass <= 3; pass++ {
		reads.reset()
		w.pass()
		if reads.total() != 0 {
			t.Errorf("pass %d read %v while the release waits for the owner, want no read", pass, reads.flows)
		}
	}

	if err := w.s.ReleaseEpic(t.Context(), runEpic, nil, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	reads.reset()
	w.pass()
	if reads.flows[runEpic] == 0 {
		t.Fatalf("pass after the owner's release did not read the epic, want its release decided")
	}
	if phase := w.flowOf(runEpic).Epic.Phase; phase != "done" {
		t.Errorf("epic phase after the owner's release = %q, want done", phase)
	}
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("release run = %s %q after the owner's release, want ended passed", run.State, run.Outcome)
	}
	reads.reset()
	w.pass()
	if reads.total() != 0 {
		t.Errorf("pass after the epic ended read %v, want no read", reads.flows)
	}
}

// TestPassFreesReleasedEpicsSlotThenRests: an epic the judge released still holds the release slot and its release run
// runs, so the pass reads it, frees the slot and ends the run passed. The epic is at rest after that, and the next pass
// does not read it.
func TestPassFreesReleasedEpicsSlotThenRests(t *testing.T) {
	t.Parallel()
	w := newReleaseWorldIn(t, nil, filepath.Join(t.TempDir(), "no-repository"))
	w.runner.ending = w.s
	w.createEpic(runEpic, store.PhaseReleasing)
	if _, err := w.s.TakeReleaseSlot(t.Context(), runEpic, runClock()); err != nil {
		t.Fatalf("TakeReleaseSlot: %v", err)
	}
	w.startRelease(runEpic)
	if err := w.s.EndEpic(t.Context(), runEpic, "released", "released", nil, runClock()); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}
	reads := w.countReads()

	w.pass()
	if reads.flows[runEpic] == 0 {
		t.Fatalf("pass did not read the released epic that holds the slot")
	}
	flow := w.flowOf(runEpic)
	if flow.Slot != "" {
		t.Errorf("slot = %q after the pass, want it free", flow.Slot)
	}
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("release run = %s %q after the pass, want ended passed", run.State, run.Outcome)
	}
	reads.reset()
	w.pass()
	if reads.total() != 0 {
		t.Errorf("pass after the release ended read %v, want no read", reads.flows)
	}
}

// failingStates is a flowReads whose read of the node run states fails.
type failingStates struct {
	*flowReads
}

// OpenNodeRunStates counts the read and fails it.
func (f failingStates) OpenNodeRunStates(context.Context) (map[string][]store.NodeRun, error) {
	f.states++
	return nil, errors.New("states unreadable")
}

// TestPassDecidesEveryEpicWhenStatesFail: a pass whose read of the node run states fails cannot tell which epics rest,
// so it decides every epic that is not closed, as a pass did before the rest check, and logs the failure.
func TestPassDecidesEveryEpicWhenStatesFail(t *testing.T) {
	t.Parallel()
	w := newReleaseWorldIn(t, nil, filepath.Join(t.TempDir(), "no-repository"))
	w.releasedEpic(runEpic)
	reads := newFlowReads(w.runner.store)
	w.runner.store = failingStates{reads}

	logs := w.pass()
	if reads.flows[runEpic] != 1 {
		t.Errorf("pass read the released epic %d times with the states unreadable, want once", reads.flows[runEpic])
	}
	if !strings.Contains(logs, "list node run states") {
		t.Errorf("logs = %q, want the failed read of the node run states", logs)
	}
}

// TestRestsAgreesWithDecide checks Rests over the built-in epic: an epic rests when the walk stops at a parked node,
// at a node that ended failed or at the end of the list, or at a running release in a phase its kind waits in, and
// not when a node is still to start or a running node is decided. Wherever Rests holds, Decide over the same rows gives
// no action.
func TestRestsAgreesWithDecide(t *testing.T) {
	t.Parallel()
	pinned, err := workflow.Load("epic", noRepoFile)
	if err != nil {
		t.Fatalf("Load built-in epic: %v", err)
	}
	reg := New(Config{}).kinds
	ended := func(paths ...string) []store.NodeRun {
		runs := make([]store.NodeRun, 0, len(paths))
		for _, path := range paths {
			runs = append(runs, store.NodeRun{Path: path, State: store.NodeEnded, Outcome: outcomePassed})
		}
		return runs
	}
	head := ended("red", "plan", "review", "correct", "conclude")
	release := store.NodeRun{Path: releaseNodePath, Kind: kindRelease, State: store.NodeRunning, Attempt: 1}
	replan := store.NodeRun{
		Path: kinds.ReplanPrefix(releaseNodePath, 1) + "fix", Kind: kindPlan, State: store.NodeRunning,
	}
	cases := []struct {
		name  string
		phase string
		runs  []store.NodeRun
		rests bool
	}{
		{"no run yet", "accepted", nil, false},
		{"red running", "accepted", []store.NodeRun{{Path: "red", State: store.NodeRunning}}, false},
		{"red parked", "met_at_base", []store.NodeRun{{Path: "red", State: store.NodeParked}}, true},
		{"red ended failed", "accepted", []store.NodeRun{{
			Path: "red", State: store.NodeEnded, Outcome: outcomeFailed,
		}}, true},
		{"every node ended", "released", ended("red", "plan", "review", "correct", "conclude", "release"), true},
		{"release still to start", "ended_with_gaps", head, false},
		{"release running while released", "released", append(head[:len(head):len(head)], release), false},
		{"release waiting with gaps", "ended_with_gaps", append(head[:len(head):len(head)], release), true},
		{"release replanning with gaps", "ended_with_gaps", append(head[:len(head):len(head)], release, replan), false},
		{"review running unregistered", "reviewing", append(ended("red", "plan"), store.NodeRun{
			Path: "review", Kind: reviewNodeKind, State: store.NodeRunning,
		}), true},
		{"correct loop running", "correcting", append(ended("red", "plan", "review"), store.NodeRun{
			Path: "correct", Kind: kindLoop, State: store.NodeRunning, LoopRound: 1,
		}), false},
		{"batch rerun under the ended plan of a finished flow", "released", append(
			ended("red", "plan", "review", "correct", "conclude", "release", "correct@1/fix"), store.NodeRun{
				Path: "correct@1/fix/S49/reintegrate", Kind: kindIntegrate, State: store.NodeRunning, Attempt: 1,
			}), false},
		{"batch parked under the ended plan of a finished flow", "released", append(
			ended("red", "plan", "review", "correct", "conclude", "release", "correct@1/fix"), store.NodeRun{
				Path: "correct@1/fix/S49/reintegrate", Kind: kindIntegrate, State: store.NodeParked, Attempt: 1,
			}), true},
	}
	for _, tc := range cases {
		rows := kinds.Rows{Epic: store.EpicRow{ID: runEpic, Phase: tc.phase, Base: runBase}, Runs: tc.runs}
		if got := Rests(pinned.Workflow, rows, reg); got != tc.rests {
			t.Errorf("%s: Rests = %v, want %v", tc.name, got, tc.rests)
		}
		if acts := Decide(pinned.Workflow, rows, reg); tc.rests && len(acts) != 0 {
			t.Errorf("%s: Decide = %+v where Rests holds, want no action", tc.name, acts)
		}
	}
}

// revisionsOf returns the revision of each epic w's store holds, by id.
func revisionsOf(t *testing.T, w *releaseWorld) map[string]int64 {
	t.Helper()
	epics, err := w.s.Epics(t.Context())
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	revisions := make(map[string]int64, len(epics))
	for _, epic := range epics {
		revisions[epic.ID] = epic.Revision
	}
	return revisions
}

// TestInterpreterPassAtRestMovesNoRevision runs passes over restWorld's store once every epic is at rest. An epic's
// revision counts every write to its row and its parts, a write that stores the values a row already holds among them,
// so a pass that leaves every revision where it was wrote no row of any epic. The first pass ends the clean epic done
// and so moves its revision, which shows the count moves at all.
func TestInterpreterPassAtRestMovesNoRevision(t *testing.T) {
	t.Parallel()
	w := restWorld(t)
	before := revisionsOf(t, w)
	w.pass()
	rested := revisionsOf(t, w)
	if rested[restClean] <= before[restClean] {
		t.Fatalf("first pass left the clean epic at revision %d, want it past %d as the pass ends it done",
			rested[restClean], before[restClean])
	}
	for pass := 2; pass <= 3; pass++ {
		w.pass()
		after := revisionsOf(t, w)
		for id, revision := range rested {
			if after[id] != revision {
				t.Errorf("pass %d moved the revision of %s, at rest, from %d to %d", pass, id, revision, after[id])
			}
		}
	}
}
