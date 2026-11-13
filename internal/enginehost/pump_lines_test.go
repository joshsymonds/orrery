package enginehost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// recordingRig is a manager whose writer applies to a store the test reads and writes too. The applier records every
// batch the writer applies, and sink records the notices the manager sends.
type recordingRig struct {
	manager *Manager
	store   *store.Store
	path    string
	applier *recordingApplier
	sink    *fakeSink
}

// newRecordingRig returns a rig whose manager runs the fake claude.
func newRecordingRig(t *testing.T) recordingRig {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	writer := NewWriter(WriterConfig{Applier: applier, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := NewManager(Config{
		Shell:    sh,
		Claude:   fakeClaudePath(t),
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Cap:      1,
		Writer:   writer,
		Now:      fixedClock,
		Notifier: sink,
		Store:    s,
	})
	t.Cleanup(m.Close)
	return recordingRig{manager: m, store: s, path: path, applier: applier, sink: sink}
}

// fakeTurnOutput returns the stdout the fake claude writes for one prompt. The session reads the same bytes as lines.
func fakeTurnOutput(t *testing.T, prompt string) []byte {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), sh, fakeClaudePath(t))
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader(`{"type":"user","message":{"role":"user","content":"` + prompt + `"}}` + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run fake claude for %q: %v", prompt, err)
	}
	return out
}

// lineEnd returns the offset just past the line of out that holds marker, numbered as the session numbers its lines.
func lineEnd(t *testing.T, out []byte, marker string) int64 {
	t.Helper()
	var end int64
	for line := range bytes.SplitAfterSeq(out, []byte("\n")) {
		end += int64(len(line))
		if bytes.Contains(line, []byte(marker)) {
			return end
		}
	}
	t.Fatalf("no line of the fake claude's output holds %q", marker)
	return 0
}

// startTurn starts a session of rig under chosenID whose first prompt is prompt.
func startTurn(t *testing.T, rig recordingRig, prompt string) {
	t.Helper()
	request := StartRequest{ID: chosenID, Dir: t.TempDir(), Prompt: prompt}
	if _, err := rig.manager.Start(t.Context(), request); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

// waitConsumed waits until the session chosenID of rig has consumed offset, and returns its row.
func waitConsumed(t *testing.T, rig recordingRig, offset int64) store.EngineRow {
	t.Helper()
	var row store.EngineRow
	waitUntil(t, fmt.Sprintf("session %s consumes to %d", chosenID, offset), func() bool {
		r, err := rig.store.EngineSession(t.Context(), chosenID)
		if err != nil {
			return false
		}
		row = r
		return r.Consumed == offset
	})
	return row
}

// batchHoldsBlock reports whether batch stores the finished block text.
func batchHoldsBlock(batch []store.EngineOp, text string) bool {
	for _, op := range batch {
		if block, ok := op.(store.EngineBlock); ok && block.Text == text {
			return true
		}
	}
	return false
}

// storedConsumed returns the offset of the EngineConsumed batch stores, and whether batch stores one.
func storedConsumed(batch []store.EngineOp) (int64, bool) {
	var consumed int64
	found := false
	for _, op := range batch {
		if c, ok := op.(store.EngineConsumed); ok {
			consumed = c.Consumed
			found = true
		}
	}
	return consumed, found
}

// assertBatchesCoverBlocks fails unless every batch that stores a block also stores an EngineConsumed whose offset
// reaches the end of the block's line, as ends gives it.
func assertBatchesCoverBlocks(t *testing.T, batches [][]store.EngineOp, ends map[string]int64) {
	t.Helper()
	for i, batch := range batches {
		consumed, found := storedConsumed(batch)
		for _, op := range batch {
			block, ok := op.(store.EngineBlock)
			if !ok {
				continue
			}
			end, known := ends[block.Text]
			if !known {
				t.Fatalf("batch %d stores block %q, which the fake claude does not write", i, block.Text)
			}
			if !found {
				t.Fatalf("batch %d stores block %q without an EngineConsumed", i, block.Text)
			}
			if consumed < end {
				t.Fatalf("batch %d stores block %q with consumed %d, short of the line's end %d",
					i, block.Text, consumed, end)
			}
		}
	}
}

// assertLineSharesBatch fails unless one batch stores every block of a line, and no batch stores only some of them.
func assertLineSharesBatch(t *testing.T, batches [][]store.EngineOp, texts ...string) {
	t.Helper()
	whole := false
	for i, batch := range batches {
		stored := 0
		for _, text := range texts {
			if batchHoldsBlock(batch, text) {
				stored++
			}
		}
		if stored == len(texts) {
			whole = true
		}
		if stored != 0 && stored != len(texts) {
			t.Fatalf("batch %d stores %d of the line's blocks %q, want all of them or none", i, stored, texts)
		}
	}
	if !whole {
		t.Fatalf("no batch stores the line's blocks %q together", texts)
	}
}

// fakeProcess is a session.Process with no stdout. It records the offset SetReplayFrom allows.
type fakeProcess struct {
	mu         sync.Mutex
	replayFrom int64
}

// nopWriteCloser is a stdin that discards what is written to it.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func (p *fakeProcess) Stdin() io.WriteCloser   { return nopWriteCloser{io.Discard} }
func (p *fakeProcess) Stdout() io.ReadCloser   { return io.NopCloser(strings.NewReader("")) }
func (p *fakeProcess) Stderr() string          { return "" }
func (p *fakeProcess) Stop() error             { return nil }
func (p *fakeProcess) Wait() error             { return nil }
func (p *fakeProcess) replayFromOffset() int64 { p.mu.Lock(); defer p.mu.Unlock(); return p.replayFrom }

// SetReplayFrom records off as the offset the process's output resumes from.
func (p *fakeProcess) SetReplayFrom(off int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replayFrom = off
}

// readoptLive returns the live session chosenID over process, which has no output to read. The session is stopped
// when the test ends. The pump is not running, so the test calls recordLine itself.
func readoptLive(t *testing.T, process session.Process) *liveSession {
	t.Helper()
	sess, _, err := session.Readopt(t.Context(), process, session.ReadoptOptions{})
	if err != nil {
		t.Fatalf("readopt fake session: %v", err)
	}
	t.Cleanup(func() {
		if err := sess.Stop(); err != nil {
			t.Errorf("stop fake session: %v", err)
		}
	})
	return &liveSession{id: chosenID, sess: sess, requests: map[string]struct{}{}}
}

// launchSession adds the engine row of chosenID and tracks its consumed offset from zero, as a launch does.
func launchSession(t *testing.T, rig recordingRig) {
	t.Helper()
	ops := []store.EngineOp{
		store.EngineAdd{ID: chosenID, Dir: "/work", State: "working"},
		store.EngineLaunch{ID: chosenID},
	}
	if err := rig.store.ApplyEngine(t.Context(), ops, fixedClock()); err != nil {
		t.Fatalf("add session %s: %v", chosenID, err)
	}
}

// noticeCount returns how many notices the rig's manager has sent.
func noticeCount(rig recordingRig) int {
	rig.sink.mu.Lock()
	defer rig.sink.mu.Unlock()
	return len(rig.sink.notices)
}

func TestPumpLinesPairStoresTheLineWithItsConsumedOffset(t *testing.T) {
	out := fakeTurnOutput(t, "PAIR")
	rig := newRecordingRig(t)
	startTurn(t, rig, "PAIR")

	total := int64(len(out))
	row := waitConsumed(t, rig, total)
	batches := rig.applier.callsOf()
	ends := map[string]int64{
		"fake pair 1.1": lineEnd(t, out, `{"type":"text","text":"fake pair 1.1"}`),
		"fake pair 1.2": lineEnd(t, out, `{"type":"text","text":"fake pair 1.2"}`),
	}
	assertLineSharesBatch(t, batches, "fake pair 1.1", "fake pair 1.2")
	assertBatchesCoverBlocks(t, batches, ends)
	if row.ReplayFrom != total {
		t.Fatalf("replay from = %d, want the turn's end %d", row.ReplayFrom, total)
	}
}

func TestPumpLinesFloodStoresEveryBatchWithItsLinesConsumedOffset(t *testing.T) {
	out := fakeTurnOutput(t, "FLOOD")
	rig := newRecordingRig(t)
	startTurn(t, rig, "FLOOD")

	total := int64(len(out))
	row := waitConsumed(t, rig, total)
	ends := map[string]int64{}
	for k := range 50 {
		text := fmt.Sprintf("fake flood 1.%d", k+1)
		ends[text] = lineEnd(t, out, `{"type":"text","text":"`+text+`"}`)
	}
	assertBatchesCoverBlocks(t, rig.applier.callsOf(), ends)
	if row.ReplayFrom != total {
		t.Fatalf("replay from = %d, want the turn's end %d", row.ReplayFrom, total)
	}
}

func TestPumpLinesBackgroundTurnStoresBackgroundOn(t *testing.T) {
	out := fakeTurnOutput(t, "BACKGROUND")
	rig := newRecordingRig(t)
	startTurn(t, rig, "BACKGROUND")

	row := waitConsumed(t, rig, int64(len(out)))
	if !row.Background {
		t.Fatalf("background = false, want true while the turn's background task runs")
	}
}

func TestPumpLinesTurnEndedStoresReplayFromOutputAndReachesTheSession(t *testing.T) {
	rig := newRecordingRig(t)
	launchSession(t, rig)
	process := &fakeProcess{}
	live := readoptLive(t, process)
	line := session.Line{Start: 0, End: 42, Events: []session.Event{session.TurnEnded{
		Subtype: "success", Result: "done", StructuredOutput: json.RawMessage(`{"ok":true}`),
	}}}

	rig.manager.recordLine(t.Context(), live, newRecorder(chosenID), line, "")

	row, err := rig.store.EngineSession(t.Context(), chosenID)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.Consumed != 42 || row.ReplayFrom != 42 {
		t.Fatalf("consumed %d and replay from %d, want both 42 at the turn's end", row.Consumed, row.ReplayFrom)
	}
	if row.TurnOutput != `{"ok":true}` {
		t.Fatalf("turn output = %q, want the structured output", row.TurnOutput)
	}
	if got := process.replayFromOffset(); got != 42 {
		t.Fatalf("session replay from = %d, want 42 once the turn end is stored", got)
	}
	if n := noticeCount(rig); n != 1 {
		t.Fatalf("notices = %d, want the turn's end sent once", n)
	}
}

func TestPumpLinesAnsweredRequestRecordsNoWaitingState(t *testing.T) {
	rig := newRecordingRig(t)
	launchSession(t, rig)
	live := readoptLive(t, &fakeProcess{})
	request := session.Request{
		Kind: session.RequestQuestion, RequestID: "ask-1", ToolUseID: "toolu_1", ToolName: "AskUserQuestion",
		Input:    json.RawMessage(`{"questions":[{"question":"Which color?"}]}`),
		Answered: true,
	}

	line := session.Line{End: 10, Events: []session.Event{request}}
	rig.manager.recordLine(t.Context(), live, newRecorder(chosenID), line, "")

	waitHistory(t, rig.path, chosenID, "the answered request is stored", func(h store.EngineHistory) bool {
		return countKind(h, store.EventEngineRequest) == 1
	})
	row, err := rig.store.EngineSession(t.Context(), chosenID)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "working" || row.Question != "" {
		t.Fatalf("row state %q question %q, want working with no question", row.State, row.Question)
	}
	if len(live.requests) != 0 {
		t.Fatalf("open requests = %v, want none for an answered request", live.requests)
	}
	if n := noticeCount(rig); n != 0 {
		t.Fatalf("notices = %d, want none for an answered request", n)
	}
}

func TestPumpLinesAnsweredRequestRecordsOnlyItsRequest(t *testing.T) {
	request := session.Request{
		Kind: session.RequestPermission, RequestID: "permit-1", ToolName: "Write", Input: json.RawMessage(`{}`),
		Answered: true,
	}

	got := newRecorder(chosenID).ops(request)
	want := []store.EngineOp{store.EngineRequest{
		ID: chosenID, Kind: "permission", RequestID: "permit-1", ToolName: "Write", Input: `{}`,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ops = %#v, want only the EngineRequest %#v", got, want)
	}
}

func TestPumpLinesLineWithNoOpsStillStoresItsConsumedOffset(t *testing.T) {
	rig := newRecordingRig(t)
	launchSession(t, rig)
	live := readoptLive(t, &fakeProcess{})

	rig.manager.recordLine(t.Context(), live, newRecorder(chosenID), session.Line{Start: 0, End: 7}, "")

	waitConsumed(t, rig, 7)
}

func TestPumpLinesExitLineStoresNoConsumedOffset(t *testing.T) {
	rig := newRecordingRig(t)
	launchSession(t, rig)
	live := readoptLive(t, &fakeProcess{})
	exit := session.Line{Start: 99, End: 99, Events: []session.Event{session.Exited{}}}

	rig.manager.recordLine(t.Context(), live, newRecorder(chosenID), exit, "")

	waitHistory(t, rig.path, chosenID, "the exit is stored", func(h store.EngineHistory) bool {
		return countKind(h, store.EventEngineChanged) > 0
	})
	for i, batch := range rig.applier.callsOf() {
		if consumed, found := storedConsumed(batch); found {
			t.Fatalf("batch %d stores consumed %d for the exit line, want no EngineConsumed", i, consumed)
		}
	}
}
