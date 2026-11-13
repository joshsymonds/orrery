package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
)

// readoptFrom is the stdout offset the replayed stream starts at. It is not zero, so offsets that start at zero fail
// the tests.
const readoptFrom = 1000

// readoptProcess is a session process that can be re-adopted. Its stdin records the write id of each write, its
// Detach leaves it running and counts the call, its Stop counts the call and ends the process, and its SetReplayFrom
// records each offset it is given.
type readoptProcess struct {
	*idProcess
	detaches    atomic.Int32
	stops       atomic.Int32
	replayFroms []int64
}

func newReadoptProcess() *readoptProcess {
	return &readoptProcess{idProcess: &idProcess{fakeProcess: newFakeProcess(), input: &idStdin{}}}
}

func (p *readoptProcess) Stop() error {
	p.stops.Add(1)
	return p.fakeProcess.Stop()
}

func (p *readoptProcess) Detach() error {
	p.detaches.Add(1)
	return errDetach
}

func (p *readoptProcess) SetReplayFrom(off int64) {
	p.replayFroms = append(p.replayFroms, off)
}

// readopt re-adopts process from opts, failing the test on error, and stops the session when the test ends.
func readopt(t *testing.T, process Process, opts ReadoptOptions) (*Session, []Line) {
	t.Helper()
	session, lines, err := Readopt(context.Background(), process, opts)
	if err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	t.Cleanup(func() { _ = stopWithin(t, session) })
	return session, lines
}

// requireWrites fails the test unless the process's stdin holds exactly want, each write with its id and bytes.
func requireWrites(t *testing.T, process *readoptProcess, want ...idWrite) {
	t.Helper()
	if got := process.input.all(); !slices.Equal(got, want) {
		t.Fatalf("stdin writes = %#v, want %#v", got, want)
	}
}

// requireDetachedOnce fails the test unless process was detached once and never stopped.
func requireDetachedOnce(t *testing.T, process *readoptProcess) {
	t.Helper()
	if got := process.detaches.Load(); got != 1 {
		t.Errorf("Detach called %d times, want once", got)
	}
	if got := process.stops.Load(); got != 0 {
		t.Errorf("Stop called %d times, want never", got)
	}
}

// requireReplayedRequest returns the single request of line, failing the test unless line carries exactly one.
func requireReplayedRequest(t *testing.T, line Line) Request {
	t.Helper()
	if len(line.Events) != 1 {
		t.Fatalf("line [%d, %d) events = %#v, want one request", line.Start, line.End, line.Events)
	}
	request, ok := line.Events[0].(Request)
	if !ok {
		t.Fatalf("event = %#v, want a Request", line.Events[0])
	}
	return request
}

func TestReadoptReturnsTheLinesUpToConsumedWithTheirOffsets(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	emit(t, process.fakeProcess, []byte(twoBlocksLine), []byte(controlResponseLine))
	firstEnd := int64(readoptFrom + len(twoBlocksLine) + 1)
	consumed := firstEnd + int64(len(controlResponseLine)+1)

	_, lines := readopt(t, process, ReadoptOptions{From: readoptFrom, Consumed: consumed})
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want the two replayed lines: %#v", len(lines), lines)
	}
	wantSpans := [][2]int64{{readoptFrom, firstEnd}, {firstEnd, consumed}}
	for i, want := range wantSpans {
		if lines[i].Start != want[0] || lines[i].End != want[1] {
			t.Errorf("line %d spans [%d, %d), want [%d, %d)", i, lines[i].Start, lines[i].End, want[0], want[1])
		}
	}
	requireEvents(t, lines[0].Events, []Event{
		Block{Kind: BlockText, Role: "assistant", MessageID: "msg_two", Text: "first block"},
		Block{Kind: BlockText, Role: "assistant", MessageID: "msg_two", Text: "second block"},
	})
	if len(lines[1].Events) != 0 {
		t.Errorf("control response line events = %#v, want none", lines[1].Events)
	}
	requireWrites(t, process)
	if got := process.detaches.Load() + process.stops.Load(); got != 0 {
		t.Errorf("a successful Readopt ended the process %d times, want never", got)
	}
}

func TestReadoptWritesNothingForReplayedLines(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	emit(t, process.fakeProcess, []byte(elicitationLine))
	consumed := int64(readoptFrom + len(elicitationLine) + 1)

	_, lines := readopt(t, process, ReadoptOptions{From: readoptFrom, Consumed: consumed})
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want the one replayed request", len(lines))
	}
	request := requireReplayedRequest(t, lines[0])
	if !request.Refused || request.RequestID != "req_elicit_1" {
		t.Fatalf("request = %#v, want the refused request req_elicit_1", request)
	}
	if want := fmt.Sprintf("event:%d.0", readoptFrom); request.WriteID != want {
		t.Errorf("request write id = %q, want %q", request.WriteID, want)
	}
	requireWrites(t, process)
}

func TestReadoptOpensAnUnansweredRequestSoAnswerWritesUnderItsResponseID(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	question := canUseTool("req_color_1", "AskUserQuestion", "toolu_color_1", colorInput)
	emit(t, process.fakeProcess, question)
	consumed := int64(readoptFrom + len(question) + 1)

	session, lines := readopt(t, process, ReadoptOptions{From: readoptFrom, Consumed: consumed})
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want the one replayed request", len(lines))
	}
	request := requireReplayedRequest(t, lines[0])
	if request.Kind != RequestQuestion || request.RequestID != "req_color_1" || request.Answered {
		t.Fatalf("request = %#v, want an unanswered question req_color_1", request)
	}
	if err := session.AnswerQuestion("req_color_1", "Blue"); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	requireWrites(t, process, idWrite{id: "response:req_color_1", data: colorAnswer + "\n"})
}

func TestReadoptNeverOpensOrAnswersARequestAlreadyAnswered(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	replayed := canUseTool("req_color_1", "AskUserQuestion", "toolu_color_1", colorInput)
	emit(t, process.fakeProcess, replayed)
	consumed := int64(readoptFrom + len(replayed) + 1)
	written := []string{"response:req_color_1", "response:req_later_1"}

	session, lines := readopt(t, process, ReadoptOptions{From: readoptFrom, Consumed: consumed, Written: written})
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want the one replayed request", len(lines))
	}
	if request := requireReplayedRequest(t, lines[0]); !request.Answered {
		t.Fatalf("replayed request = %#v, want Answered", request)
	}
	if err := session.AnswerQuestion("req_color_1", "Blue"); !errors.Is(err, ErrNoPendingRequest) {
		t.Fatalf("AnswerQuestion on the replayed answered request = %v, want %v", err, ErrNoPendingRequest)
	}

	later := canUseTool("req_later_1", "AskUserQuestion", "toolu_later_1", colorInput)
	emit(t, process.fakeProcess, later)
	line := nextLine(t, session)
	if request := requireReplayedRequest(t, line); !request.Answered || request.RequestID != "req_later_1" {
		t.Fatalf("later request = %#v, want Answered req_later_1", request)
	}
	if err := session.AnswerQuestion("req_later_1", "Blue"); !errors.Is(err, ErrNoPendingRequest) {
		t.Fatalf("AnswerQuestion on the later answered request = %v, want %v", err, ErrNoPendingRequest)
	}
	requireWrites(t, process)
}

func TestReadoptGoesLiveAndRefusesALaterRefusedRequest(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	emit(t, process.fakeProcess, []byte(controlResponseLine))
	consumed := int64(readoptFrom + len(controlResponseLine) + 1)

	session, lines := readopt(t, process, ReadoptOptions{From: readoptFrom, Consumed: consumed})
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want the one replayed line", len(lines))
	}
	requireWrites(t, process)

	emit(t, process.fakeProcess, []byte(elicitationLine))
	line := nextLine(t, session)
	if line.Start != consumed {
		t.Fatalf("live line starts at %d, want the consumed offset %d", line.Start, consumed)
	}
	request := requireReplayedRequest(t, line)
	if !request.Refused {
		t.Fatalf("live request = %#v, want Refused", request)
	}
	wantID := fmt.Sprintf("event:%d.0", consumed)
	requireWrites(t, process, idWrite{id: wantID, data: elicitationRefusal + "\n"})
}

func TestReadoptAtConsumedReplaysNothingAndGoesLive(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	session, lines := readopt(t, process, ReadoptOptions{From: readoptFrom, Consumed: readoptFrom})
	if len(lines) != 0 {
		t.Fatalf("got %d replayed lines, want none", len(lines))
	}
	emit(t, process.fakeProcess, []byte(controlResponseLine))
	if line := nextLine(t, session); line.Start != readoptFrom {
		t.Fatalf("first live line starts at %d, want %d", line.Start, readoptFrom)
	}
	requireWrites(t, process)
}

func TestReadoptFailsAndDetachesWhenTheStreamEndsBeforeConsumed(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	emit(t, process.fakeProcess, []byte(controlResponseLine))
	process.fakeProcess.exit(nil, "")
	consumed := int64(readoptFrom + len(controlResponseLine) + 1 + 100)

	session, lines, err := Readopt(context.Background(), process, ReadoptOptions{From: readoptFrom, Consumed: consumed})
	if err == nil {
		t.Fatalf("Readopt returned no error and %d lines, want the stream end", len(lines))
	}
	if session != nil || lines != nil {
		t.Errorf("Readopt failed with session %v and lines %v, want neither", session, lines)
	}
	requireDetachedOnce(t, process)
}

func TestReadoptFailsAndDetachesWhenALineEndsPastConsumed(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	emit(t, process.fakeProcess, []byte(controlResponseLine))
	consumed := int64(readoptFrom + len(controlResponseLine))

	opts := ReadoptOptions{From: readoptFrom, Consumed: consumed}
	if _, _, err := Readopt(context.Background(), process, opts); err == nil {
		t.Fatal("Readopt returned no error, want the line that ends past Consumed")
	}
	requireDetachedOnce(t, process)
}

func TestReadoptFailsAndDetachesWhenFromIsPastConsumed(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()

	opts := ReadoptOptions{From: readoptFrom, Consumed: readoptFrom - 1}
	if _, _, err := Readopt(context.Background(), process, opts); err == nil {
		t.Fatal("Readopt returned no error, want From past Consumed")
	}
	requireDetachedOnce(t, process)
}

func TestReadoptFailsAndDetachesWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := Readopt(ctx, process, ReadoptOptions{From: readoptFrom, Consumed: readoptFrom + 10})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Readopt error = %v, want context.Canceled", err)
	}
	requireDetachedOnce(t, process)
}

func TestSetReplayFromReachesTheProcess(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	session, _ := readopt(t, process, ReadoptOptions{From: readoptFrom, Consumed: readoptFrom})

	session.SetReplayFrom(4096)
	if !slices.Equal(process.replayFroms, []int64{4096}) {
		t.Fatalf("process SetReplayFrom calls = %v, want [4096]", process.replayFroms)
	}
}

func TestSetReplayFromWithoutProcessSupportLeavesTheProcessRunning(t *testing.T) {
	t.Parallel()
	session, process := startIDSession(t)

	session.SetReplayFrom(4096)
	if process.exited() {
		t.Fatal("SetReplayFrom ended a process without SetReplayFrom, want it left running")
	}
	requireIDWrites(t, process)
}
