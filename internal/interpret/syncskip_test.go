package interpret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// errServiceRefused is the error the refusing check and evidence services return for every start.
var errServiceRefused = errors.New("the service is closed")

// closedChecks is a check service whose every start fails.
type closedChecks struct{}

// Run refuses check.
func (closedChecks) Run(context.Context, store.NodeCheck) (store.NodeCheck, error) {
	return store.NodeCheck{}, errServiceRefused
}

// closedEvidence is an evidence service whose every start fails.
type closedEvidence struct{}

// RunItem refuses the run.
func (closedEvidence) RunItem(context.Context, string, string, string, string, string) (store.EvidenceRun, error) {
	return store.EvidenceRun{}, errServiceRefused
}

// RunAmendment refuses the run.
func (closedEvidence) RunAmendment(
	context.Context, string, string, string, evidence.Item, string, string,
) (store.EvidenceRun, error) {
	return store.EvidenceRun{}, errServiceRefused
}

// parkingStore is the store a test wraps so that the owner's park of the epic lands just after a pass read the epic's
// rows: once armed, the next EpicFlow returns the rows it read and then parks the epic.
type parkingStore struct {
	Store
	parker *store.Store
	armed  bool
}

// EpicFlow reads the epic's rows, and parks the epic after the read once armed.
func (p *parkingStore) EpicFlow(ctx context.Context, id string) (store.EpicFlow, error) {
	flow, err := p.Store.EpicFlow(ctx, id)
	if err != nil || !p.armed {
		return flow, err
	}
	p.armed = false
	parkErr := p.parker.ParkEpic(ctx, id, store.ParkClassOrchestrator, "held by its owner", runClock())
	if parkErr != nil {
		return store.EpicFlow{}, fmt.Errorf("park epic %s: %w", id, parkErr)
	}
	return flow, nil
}

// passLogged carries one pass out on the integrate flow with a logger of its own, and returns what it logged and
// whether the plan run was given its turn.
func (w *integrateWorld) passLogged() (string, bool) {
	w.t.Helper()
	logs := &bytes.Buffer{}
	logger := w.runner.logger
	w.runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	defer func() { w.runner.logger = logger }()
	planned := w.runner.passFlow(context.Background(), epicOf(w.t, w.s), integrateFlow())
	return logs.String(), planned
}

// requireMergeOf fails unless the sync run at path is running at a merge of origin's commit origin into base, the
// stored base.
func (w *integrateWorld) requireMergeOf(path, base, origin string) {
	w.t.Helper()
	sync := runAt(w.t, w.s, path)
	if sync.State != store.NodeRunning || sync.Revision == "" {
		w.t.Fatalf("sync %s = %+v, want it running at a merge of origin's commit %s", path, sync, origin)
	}
	if parents := w.git("rev-parse", sync.Revision+"^1", sync.Revision+"^2"); parents != base+"\n"+origin {
		w.t.Errorf("merge parents of sync %s = %q, want %s and %s", path, parents, base, origin)
	}
}

// retriedAtTheNextQuietPoint runs the next turn's batch on base through its advance, and fails unless the quiet point
// after it starts the sync of turn and merges origin's commit origin, which an earlier sync skipped, into the batch's
// candidate.
func (w *syncWorld) retriedAtTheNextQuietPoint(base, origin string, turn int64) {
	w.t.Helper()
	w.nextTurn(base, 3, 4, integrateNextBatch)
	next := w.candidate(integrateNextBatch)
	if w.passed() {
		w.t.Error("the planner got a turn while the sync of the next quiet point runs")
	}
	w.requireMergeOf(syncPath("plan", turn), next, origin)
}

// TestSyncNotAdvancedWhileBusyIsTriedAgain: a planner turn that started while the sync's checks ran keeps the advance
// from moving the branch, and the sync ends skipped. Skipped tried nothing, so once that turn has ended, free, the
// next turn's quiet point merges the same origin commit again.
func TestSyncNotAdvancedWhileBusyIsTriedAgain(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncAdvance()
	turn := w.claimTurn()
	w.pass()
	w.requireSyncEnded(syncFirst, "skipped", fmt.Sprintf(
		"merge %s of origin's commit %s into the stored base %s\nnot advanced: planner turn %d is running",
		sync.Revision, originHead, candidate, turn))
	free := store.TurnFree{Reason: "the step cap is full"}
	if err := w.s.FreeTurn(context.Background(), runEpic, turn, free, runClock()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	w.retriedAtTheNextQuietPoint(candidate, originHead, 3)
}

// TestSyncPausedBeforeItsAdvanceIsNotMovedAndIsTriedAgain: the owner pauses the epic while the sync's checks run. The
// advance does not move the branch of a paused epic: the sync ends skipped with why, tells nothing, and moves nothing.
// Once the epic is resumed, the next turn's quiet point merges the same origin commit again.
func TestSyncPausedBeforeItsAdvanceIsNotMovedAndIsTriedAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncAdvance()
	if err := w.s.PauseEpic(ctx, runEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	w.pass()
	w.requireSyncEnded(syncFirst, "skipped", fmt.Sprintf(
		"merge %s of origin's commit %s into the stored base %s\nnot advanced: the epic is paused",
		sync.Revision, originHead, candidate))
	w.requireUnmoved(candidate)
	if tells := w.syncTells(); len(tells) != 0 {
		t.Errorf("tells = %q, want none", tells)
	}
	if err := w.s.ResumeEpic(ctx, runEpic); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	w.retriedAtTheNextQuietPoint(candidate, originHead, 2)
}

// TestSyncParkedJustBeforeItsAdvanceIsNotMoved: the owner's park lands after the pass read the epic's rows, so the
// advance is decided for an epic at rest. The executor reads the epic again just before it moves the branch, finds it
// parked, and moves nothing: the sync ends skipped with why, the branch and stored base stay, the sync tells nothing,
// and origin's commit is not counted as tried.
func TestSyncParkedJustBeforeItsAdvanceIsNotMoved(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncAdvance()
	parking := &parkingStore{Store: w.s, parker: w.s, armed: true}
	w.runner = runNodes(parking, w.fakes)
	if logs, _ := w.passLogged(); logs != "" {
		t.Errorf("pass logged %q, want nothing", logs)
	}
	w.requireSyncEnded(syncFirst, "skipped", fmt.Sprintf(
		"merge %s of origin's commit %s into the stored base %s\nnot advanced: the epic is parked",
		sync.Revision, originHead, candidate))
	epic := epicOf(t, w.s)
	if epic.StoredBase != candidate || epic.Synced != "" || epic.Parked != "held by its owner" {
		t.Errorf("stored base %q, synced %q, parked %q, want %q, none and the owner's park", epic.StoredBase,
			epic.Synced, epic.Parked, candidate)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != candidate {
		t.Errorf("branch = %s, want it left at %s", tip, candidate)
	}
	if tells := w.syncTells(); len(tells) != 0 {
		t.Errorf("sync tells = %q, want none", tells)
	}
	flow, err := w.s.EpicFlow(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if tried := kinds.SyncTriedBy(flowRows(flow), originHead); tried != "" {
		t.Errorf("origin's commit tried by %s, want it not tried", tried)
	}
}

// TestSyncCheckLostTwiceEndsSkippedToldAndIsTriedAgain: the sync's Done gate is lost at a restart, and so is its rerun.
// That decides nothing of the merge, so the sync ends skipped and tells it once, a sync parks nothing, and the next
// turn's quiet point merges the same origin commit again.
func TestSyncCheckLostTwiceEndsSkippedToldAndIsTriedAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncGate()
	lost := store.NodeCheckEnd{NotRun: store.LostAtRestart}
	if err := w.s.EndNodeCheck(ctx, workID(t, w.s, syncFirst, "done 1"), lost, runClock()); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	w.pass()
	if err := w.s.EndNodeCheck(ctx, workID(t, w.s, syncFirst, "done 1 rerun"), lost, runClock()); err != nil {
		t.Fatalf("EndNodeCheck of the rerun: %v", err)
	}
	if !w.passed() {
		t.Error("the planner got no turn once the sync ended")
	}
	output := fmt.Sprintf(
		"merge %s of origin's commit %s into the stored base %s\nsync plan/M1: Done gate lint lost at a restart twice",
		sync.Revision, originHead, candidate)
	w.requireSyncEnded(syncFirst, "skipped", output)
	w.requireUnmoved(candidate)
	w.requireNoPark()
	if tells := w.syncTells(); !reflect.DeepEqual(tells, []string{"workflow node plan/M1 skipped: " + output}) {
		t.Errorf("tells = %q, want the lost gate told once", tells)
	}
	w.retriedAtTheNextQuietPoint(candidate, originHead, 2)
}

// TestSyncWorkThatFailsEndsSkippedAndIsTriedAgain: the start of the sync's Done gate, the start of its capture, or its
// advance fails. The failure is logged, and the sync ends skipped with it in the same pass, so the planner gets its
// turn rather than waiting on work that fails on every pass. It tells nothing, and a pass after it tells nothing
// either. Skipped tried nothing, so the next turn's quiet point merges the same origin commit again.
func TestSyncWorkThatFailsEndsSkippedAndIsTriedAgain(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// fail runs the world to the pass whose sync work fails and breaks that work. It returns the stored base,
		// origin's commit, the work's error, and mend, which undoes the break.
		fail func(w *syncWorld) (string, string, string, func())
	}{
		{"the gate's start", func(w *syncWorld) (string, string, string, func()) {
			candidate := w.toCaptured()
			originHead := w.originCommit("origin.txt")
			w.runner.checks = closedChecks{}
			return candidate, originHead, "run check %s: " + errServiceRefused.Error(), func() {
				w.runner.checks = w.fakes.checks
			}
		}},
		{"the capture's start", func(w *syncWorld) (string, string, string, func()) {
			candidate, originHead, _ := w.toSyncGate()
			w.endGate(syncFirst, 0, "")
			w.runner.evidence = closedEvidence{}
			return candidate, originHead, "run evidence R1: " + errServiceRefused.Error(), func() {
				w.runner.evidence = w.fakes.evidence
			}
		}},
		{"the advance", func(w *syncWorld) (string, string, string, func()) {
			candidate, originHead, _ := w.toSyncAdvance()
			w.git("update-ref", "-d", integrateBranch)
			return candidate, originHead, "epic " + runEpic + " has no branch", func() {
				w.git("update-ref", integrateBranch, candidate)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newSyncWorld(t)
			candidate, originHead, cause, mend := tc.fail(w)
			logs, planned := w.passLogged()
			if !planned {
				t.Error("the planner got no turn after the sync's work failed")
			}
			sync := runAt(t, w.s, syncFirst)
			if strings.Contains(cause, "%s") {
				cause = fmt.Sprintf(cause, workID(t, w.s, syncFirst, "done 1"))
			}
			w.requireSyncEnded(syncFirst, "skipped", fmt.Sprintf(
				"merge %s of origin's commit %s into the stored base %s\nnot finished: %s", sync.Revision, originHead,
				candidate, cause))
			if !strings.Contains(logs, "sync of origin's main did not finish") || !strings.Contains(logs, cause) {
				t.Errorf("logs = %q, want the failed work logged", logs)
			}
			mend()
			w.requireUnmoved(candidate)
			if !w.passed() {
				t.Error("the planner got no turn on the pass after the skipped sync")
			}
			if tells := w.syncTells(); len(tells) != 0 {
				t.Errorf("tells = %q, want none", tells)
			}
			w.retriedAtTheNextQuietPoint(candidate, originHead, 2)
		})
	}
}
