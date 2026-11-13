package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// errStoreRefused is what a crashApplier returns for every batch it refuses.
var errStoreRefused = errors.New("store refused the write")

// crashApplier stores through inner until it refuses. It starts refusing at the first batch trigger accepts, or when
// refuse is called, and every batch it refuses fails with errStoreRefused, so the store keeps only what it stored before
// the refusal began. With storeTrigger set, the batch trigger accepts is stored before the refusal, so the writer still
// holds its ops although the store has them.
type crashApplier struct {
	inner        Applier
	trigger      func([]store.EngineOp) bool
	storeTrigger bool

	mu      sync.Mutex
	refusal bool
}

// ApplyEngine stores ops through inner unless the applier refuses.
func (a *crashApplier) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.refusal && a.trigger != nil && a.trigger(ops) {
		a.refusal = true
		if a.storeTrigger {
			if err := a.inner.ApplyEngine(ctx, ops, now); err != nil {
				return err
			}
			return errStoreRefused
		}
	}
	if a.refusal {
		return errStoreRefused
	}
	return a.inner.ApplyEngine(ctx, ops, now)
}

// refuse makes every batch from now on fail.
func (a *crashApplier) refuse() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refusal = true
}

// isRefusing reports whether the applier refuses batches.
func (a *crashApplier) isRefusing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refusal
}

// blockTrigger accepts a batch that stores the finished block text.
func blockTrigger(text string) func([]store.EngineOp) bool {
	return func(ops []store.EngineOp) bool { return batchHoldsBlock(ops, text) }
}

// crashRig is a manager whose writer stores through crash, to the store s at path. Its sessions run their holders under
// the fake systemd-run in root, as an orreryd with a holder scope does.
type crashRig struct {
	manager *Manager
	store   *store.Store
	path    string
	root    string
	crash   *crashApplier
}

// newCrashRig returns a crashRig whose writer refuses from the first batch trigger accepts. storeTrigger stores that
// batch before the refusal. Every holder under the rig's root is cleared when the test ends.
func newCrashRig(t *testing.T, trigger func([]store.EngineOp) bool, storeTrigger bool) crashRig {
	t.Helper()
	s, path := openStore(t)
	root := holderRoot(t)
	t.Cleanup(func() { clearHolderDirs(t, root) })
	crash := &crashApplier{inner: s, trigger: trigger, storeTrigger: storeTrigger}
	writer := NewWriter(WriterConfig{Applier: crash, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := newStateManager(t, s, writer, &fakeSink{}, fakeSystemdRunPath(t), root, nil)
	return crashRig{manager: m, store: s, path: path, root: root, crash: crash}
}

// clearHolderDirs clears every holder directory under the holders dir of root. A missing holders dir has none.
func clearHolderDirs(t *testing.T, root string) {
	t.Helper()
	holders := filepath.Join(root, "holders")
	entries, err := os.ReadDir(holders)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		t.Errorf("read holders dir %s: %v", holders, err)
		return
	}
	for _, entry := range entries {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), managerGuard)
		if err := holder.Clear(ctx, filepath.Join(holders, entry.Name())); err != nil {
			t.Errorf("clear holder %s: %v", entry.Name(), err)
		}
		cancel()
	}
}

// restartAfterCrash ends the manager of rig, detaching its sessions as a crash leaves them, and returns the manager a
// restart starts after its Reconcile, with the notices it sent. The restarted manager has a fresh writer on the same
// store and state dir, and the same holder scope.
func restartAfterCrash(t *testing.T, rig crashRig) (*Manager, *fakeSink) {
	t.Helper()
	rig.manager.Close()
	writer := NewWriter(WriterConfig{Applier: rig.store, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	restarted := newStateManager(t, rig.store, writer, sink, fakeSystemdRunPath(t), rig.root, nil)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}
	return restarted, sink
}

// requireProcessRunning fails unless /proc shows pid as a process that has not exited.
func requireProcessRunning(t *testing.T, pid int) {
	t.Helper()
	if gateProcessGone(pid) {
		t.Fatalf("fake claude process %d is not running", pid)
	}
}

// stdinLinesWith counts the lines the fake claude logged as read from stdin in dir that hold text. A log not yet written
// counts none.
func stdinLinesWith(t *testing.T, dir, text string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read fake stdin log: %v", err)
	}
	n := 0
	for line := range strings.Lines(string(data)) {
		if strings.Contains(line, text) {
			n++
		}
	}
	return n
}

// blockTexts returns the text of each finished block history stores, in seq order.
func blockTexts(t *testing.T, history store.EngineHistory) []string {
	t.Helper()
	var texts []string
	for _, event := range history.Events {
		if event.Kind != store.EventEngineBlock {
			continue
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode block payload %q: %v", event.Payload, err)
		}
		texts = append(texts, payload.Text)
	}
	return texts
}

// unfinishedTexts returns the text of each unfinished block history holds.
func unfinishedTexts(history store.EngineHistory) []string {
	texts := make([]string, 0, len(history.Unfinished))
	for _, text := range history.Unfinished {
		texts = append(texts, text.Text)
	}
	return texts
}

func TestReadoptStalledTurnKeepsItsTextAndState(t *testing.T) {
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	pid := fakePID(t, dir)

	rig.crash.refuse()
	restarted, sink := restartAfterCrash(t, rig)

	requireProcessRunning(t, pid)
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}
	history := historyOf(t, rig.path, id)
	if history.State != "working" || history.Reason != "" {
		t.Fatalf("state after the restart = %q reason %q, want working with no reason", history.State, history.Reason)
	}
	if got := unfinishedTexts(history); !slices.Equal(got, []string{"fake stalling 1"}) {
		t.Fatalf("unfinished text after the restart = %q, want the stalled block's text", got)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the restart = %+v, want none", got)
	}
}

func TestReadoptAskedQuestionIsAnsweredOnce(t *testing.T) {
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waiting := waitHistory(t, rig.path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	requestID := requestIDOf(t, waiting)

	rig.crash.refuse()
	restarted, _ := restartAfterCrash(t, rig)

	history := historyOf(t, rig.path, id)
	if history.State != "waiting" {
		t.Fatalf("state after the restart = %q, want waiting", history.State)
	}
	if question, ok := lastQuestion(t, history); !ok || question != "Which color?" {
		t.Fatalf("question after the restart = %q (set %v), want %q", question, ok, "Which color?")
	}
	if err := restarted.Answer(t.Context(), id, requestID, "red"); err != nil {
		t.Fatalf("Answer red after the restart: %v", err)
	}
	waitHistory(t, rig.path, id, "the answered reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake answered 1")
	})
	waitHistory(t, rig.path, id, "the done state after the answered turn", func(h store.EngineHistory) bool {
		return h.State == "done"
	})
	if n := stdinLinesWith(t, dir, `"answers"`); n != 1 {
		t.Fatalf("fake claude read %d answers, want the one answer once", n)
	}
}

func TestReadoptFloodStoresEveryBlockOnce(t *testing.T) {
	for _, storeTrigger := range []bool{false, true} {
		t.Run(fmt.Sprintf("trigger batch stored %t", storeTrigger), func(t *testing.T) {
			rig := newCrashRig(t, blockTrigger("fake flood 1.20"), storeTrigger)
			dir := t.TempDir()
			id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "FLOOD"})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			waitUntil(t, "the store refuses the batch of fake flood 1.20", rig.crash.isRefusing)

			restartAfterCrash(t, rig)

			waitHistory(t, rig.path, id, "the done state after the flood", func(h store.EngineHistory) bool {
				return h.State == "done"
			})
			var want []string
			for k := 1; k <= 50; k++ {
				want = append(want, fmt.Sprintf("fake flood 1.%d", k))
			}
			if got := blockTexts(t, historyOf(t, rig.path, id)); !slices.Equal(got, want) {
				t.Fatalf("finished blocks after the restart = %q, want each of the 50 once in order", got)
			}
		})
	}
}

func TestReadoptPairLineStoresEachBlockOnce(t *testing.T) {
	for _, storeTrigger := range []bool{false, true} {
		t.Run(fmt.Sprintf("trigger batch stored %t", storeTrigger), func(t *testing.T) {
			rig := newCrashRig(t, blockTrigger("fake pair 1.1"), storeTrigger)
			dir := t.TempDir()
			id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "PAIR"})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			waitUntil(t, "the store refuses the batch of fake pair 1.1", rig.crash.isRefusing)

			restartAfterCrash(t, rig)

			waitHistory(t, rig.path, id, "the done state after the pair", func(h store.EngineHistory) bool {
				return h.State == "done"
			})
			want := []string{"fake pair 1.1", "fake pair 1.2"}
			if got := blockTexts(t, historyOf(t, rig.path, id)); !slices.Equal(got, want) {
				t.Fatalf("finished blocks after the restart = %q, want %q once each", got, want)
			}
		})
	}
}

func TestReadoptWritesStoredPromptOnceAndSkipsWrittenOne(t *testing.T) {
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	// The prompt is stored, but the crash came before the holder received its write.
	unwritten := store.EnginePrompt{ID: id, Text: "after the crash", WriteID: "unwritten-prompt"}
	if err := rig.store.ApplyEngine(t.Context(), []store.EngineOp{unwritten}, fixedClock()); err != nil {
		t.Fatalf("store unwritten prompt: %v", err)
	}

	rig.crash.refuse()
	restartAfterCrash(t, rig)

	waitUntil(t, "the unwritten prompt reaches the fake claude", func() bool {
		return stdinLinesWith(t, dir, `"text":"after the crash"`) > 0
	})
	if n := stdinLinesWith(t, dir, `"text":"after the crash"`); n != 1 {
		t.Fatalf("fake claude read the unwritten prompt %d times, want once", n)
	}
	if n := stdinLinesWith(t, dir, `"text":"STALL"`); n != 1 {
		t.Fatalf("fake claude read the written prompt %d times, want once", n)
	}
}

func TestReadoptWaitingRowWithNoOpenRequestGoesWorking(t *testing.T) {
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, rig.path, id, "the done state of the first turn", func(h store.EngineHistory) bool {
		return h.State == "done"
	})
	// The row says a question waits, but the holder's claude has no request open.
	waiting, question := "waiting", "Which color?"
	set := store.EngineSet{ID: id, Changes: store.EngineChanges{State: &waiting, Question: &question}}
	if err := rig.store.ApplyEngine(t.Context(), []store.EngineOp{set}, fixedClock()); err != nil {
		t.Fatalf("set waiting row: %v", err)
	}

	rig.crash.refuse()
	restarted, _ := restartAfterCrash(t, rig)

	waitUntil(t, "the row goes working with no question", func() bool {
		row, err := rig.store.EngineSession(t.Context(), id)
		return err == nil && row.State == "working" && row.Question == ""
	})
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}
	requireProcessRunning(t, fakePID(t, dir))
}

func TestReconcileStopsStepRowHolder(t *testing.T) {
	root := holderRoot(t)
	s, _ := openStore(t)
	dir := t.TempDir()
	// The row is tracked, so only its step kind keeps its holder from being re-adopted.
	add := store.EngineAdd{ID: "step", Dir: dir, State: "working", Kind: store.KindStep}
	launch := store.EngineLaunch{ID: "step"}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{add, launch}, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed step row: %v", err)
	}
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	startLeftHolder(t, root, "step", dir)
	pid := fakePID(t, dir)
	m := newStateManager(t, s, writer, &fakeSink{}, "", root, nil)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, root, "step"))
	requireEngineRows(t, s, map[string]restartRow{
		"step": {state: "interrupted", reason: restartedReason},
	})
}

func TestReadoptFailedRowHolderIsReleasedUndrained(t *testing.T) {
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	pid := fakePID(t, dir)
	failed, reason := "failed", "the process failed"
	set := store.EngineSet{ID: id, Changes: store.EngineChanges{State: &failed, Reason: &reason}}
	if err := rig.store.ApplyEngine(t.Context(), []store.EngineOp{set}, fixedClock()); err != nil {
		t.Fatalf("set failed row: %v", err)
	}

	restarted, sink := restartAfterCrash(t, rig)

	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, rig.root, id))
	requireEngineRows(t, rig.store, map[string]restartRow{
		id: {state: "failed", reason: reason},
	})
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("failed session %s is live after the restart", id)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the restart = %+v, want none", got)
	}
}

func TestReadoptHolderThatEndedIsClearedAndRowSettles(t *testing.T) {
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "the stalled block's unfinished text", func() bool {
		return countRows(t, rig.path, "engine_text") == 1
	})
	pid := fakePID(t, dir)
	rig.manager.Close()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), managerGuard)
	defer cancel()
	if err := holder.Stop(ctx, holderDir(t, rig.root, id)); err != nil {
		t.Fatalf("stop holder of session %s: %v", id, err)
	}
	gateWaitGone(t, pid)

	_, sink := restartAfterCrash(t, rig)

	requireDirGone(t, holderDir(t, rig.root, id))
	requireEngineRows(t, rig.store, map[string]restartRow{
		id: {state: "interrupted", reason: restartedReason},
	})
	want := Notice{Session: id, Dir: dir, State: "interrupted", Body: restartedReason}
	if got := sink.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
}

func TestReconcileClearsHolderWithNoRow(t *testing.T) {
	root := holderRoot(t)
	s, _ := openStore(t)
	dir := t.TempDir()
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	startLeftHolder(t, root, "orphan", dir)
	pid := fakePID(t, dir)
	m := newStateManager(t, s, writer, &fakeSink{}, "", root, nil)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, root, "orphan"))
}

func TestReconcileClearsHolderOfUntrackedRow(t *testing.T) {
	root := holderRoot(t)
	s, _ := openStore(t)
	dir := t.TempDir()
	// No launch tracked the row, so it has no consumed offset to re-adopt from.
	add := store.EngineAdd{ID: "untracked", Dir: dir, State: "working", Kind: store.KindEngine}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{add}, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed untracked row: %v", err)
	}
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	startLeftHolder(t, root, "untracked", dir)
	pid := fakePID(t, dir)
	m := newStateManager(t, s, writer, &fakeSink{}, "", root, nil)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	gateWaitGone(t, pid)
	requireDirGone(t, holderDir(t, root, "untracked"))
	requireEngineRows(t, s, map[string]restartRow{
		"untracked": {state: "interrupted", reason: restartedReason},
	})
}
