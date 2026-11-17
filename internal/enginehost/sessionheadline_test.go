package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// headlineFakeHaiku is a haikuCaller that answers each call with its current reply and error, and records the schema
// and input of each call in order.
type headlineFakeHaiku struct {
	mu      sync.Mutex
	reply   json.RawMessage
	err     error
	schemas []string
	inputs  []string
}

// Call records the call and answers it with the fake's reply and error.
func (h *headlineFakeHaiku) Call(_ context.Context, schema, input string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.schemas = append(h.schemas, schema)
	h.inputs = append(h.inputs, input)
	return h.reply, h.err
}

// answer sets the reply and error of the calls that follow.
func (h *headlineFakeHaiku) answer(reply json.RawMessage, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reply, h.err = reply, err
}

// calls returns how many calls the fake has answered.
func (h *headlineFakeHaiku) calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.inputs)
}

// lastInput returns the input of the newest call.
func (h *headlineFakeHaiku) lastInput() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.inputs[len(h.inputs)-1]
}

// headlineFakeSession is a session the fake store holds: its first prompt, its turns oldest first, the unix seconds of
// its newest turn end, and its headline with the unix seconds of the write.
type headlineFakeSession struct {
	firstPrompt        string
	turns              []store.RecentTurn
	turnAt, headlineAt int64
	headline           string
}

// headlineFakeStore is a SessionHeadlineStore over in-memory sessions. Like the store, it lists the sessions whose
// newest turn end is later than their headline_at, reads a session's newest turns oldest first, and writes a headline
// with its time. readErr and writeErr make its reads and writes fail.
type headlineFakeStore struct {
	mu       sync.Mutex
	sessions map[string]*headlineFakeSession
	readErr  error
	writeErr error
	reads    int
}

// newHeadlineFakeStore returns a fake store with no sessions.
func newHeadlineFakeStore() *headlineFakeStore {
	return &headlineFakeStore{sessions: make(map[string]*headlineFakeSession)}
}

// addSession adds the session id with first prompt firstPrompt, no turns and no headline.
func (s *headlineFakeStore) addSession(id, firstPrompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = &headlineFakeSession{firstPrompt: firstPrompt}
}

// endTurn appends turns to the session id and records its newest turn end at at.
func (s *headlineFakeStore) endTurn(id string, at time.Time, turns ...store.RecentTurn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	sess.turns = append(sess.turns, turns...)
	sess.turnAt = at.Unix()
}

// failWrites makes the writes that follow fail with err, or succeed when err is nil.
func (s *headlineFakeStore) failWrites(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeErr = err
}

// failReads makes the reads that follow fail with err, or succeed when err is nil.
func (s *headlineFakeStore) failReads(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readErr = err
}

// readCount returns how many turn reads the fake has answered.
func (s *headlineFakeStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// headlineOf returns the stored headline of the session id, or "" when it has none.
func (s *headlineFakeStore) headlineOf(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id].headline
}

// HeadlineDueSessions lists the sessions whose newest turn end is later than their headline_at.
func (s *headlineFakeStore) HeadlineDueSessions(context.Context) ([]store.HeadlineDueSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []store.HeadlineDueSession
	for id, sess := range s.sessions {
		if sess.turnAt > sess.headlineAt {
			due = append(due, store.HeadlineDueSession{
				ID: id, FirstPrompt: sess.firstPrompt, TurnAt: sess.turnAt, HeadlineAt: sess.headlineAt,
			})
		}
	}
	return due, nil
}

// RecentTurns returns the newest items turns of the session id, oldest first, or readErr when the fake fails reads.
func (s *headlineFakeStore) RecentTurns(_ context.Context, id string, items int) ([]store.RecentTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.readErr != nil {
		return nil, s.readErr
	}
	turns := s.sessions[id].turns
	if len(turns) > items {
		turns = turns[len(turns)-items:]
	}
	return slices.Clone(turns), nil
}

// SetSessionHeadline stores headline and at as the headline and its write time of the session id, or answers writeErr
// when the fake fails writes.
func (s *headlineFakeStore) SetSessionHeadline(_ context.Context, id, headline string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	sess := s.sessions[id]
	sess.headline = headline
	sess.headlineAt = at.Unix()
	return nil
}

// headlineClock is a synthetic clock that the test moves by hand.
type headlineClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the clock's time.
func (c *headlineClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// set moves the clock to at.
func (c *headlineClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

// headlineLoop is a SessionHeadlines started over a fake store and clock, and the channels a test drives it with.
type headlineLoop struct {
	clock  *headlineClock
	ticks  chan time.Time
	passes chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
}

// startHeadlineLoop starts a SessionHeadlines over st and haiku on a synthetic clock at start, and returns once its
// start pass is done. The test's end stops it too.
func startHeadlineLoop(t *testing.T, st SessionHeadlineStore, haiku haikuCaller, start time.Time) *headlineLoop {
	t.Helper()
	ticks := make(chan time.Time)
	passes := make(chan struct{}, 16)
	ctx, cancel := context.WithCancel(t.Context())
	clock := &headlineClock{now: start}
	loop := &headlineLoop{clock: clock, ticks: ticks, passes: passes, done: make(chan struct{}), cancel: cancel}
	headlines := NewSessionHeadlines(SessionHeadlinesConfig{
		Store:  st,
		Haiku:  haiku,
		Tick:   ticks,
		Now:    clock.Now,
		Passed: func() { passes <- struct{}{} },
	})
	go func() {
		defer close(loop.done)
		headlines.Run(ctx)
	}()
	t.Cleanup(loop.stop)
	<-passes
	return loop
}

// stop ends the loop and waits for Run to return. It is safe to call more than once.
func (l *headlineLoop) stop() {
	l.cancel()
	<-l.done
}

// passAt moves the clock to at, has the loop pass once more and waits until that pass is done.
func (l *headlineLoop) passAt(at time.Time) {
	l.clock.set(at)
	l.ticks <- at
	<-l.passes
}

// headlineStart returns the time the headline tests start their loops at.
func headlineStart() time.Time {
	return time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
}

// TestSessionHeadlineTurnEndWritesFromFirstPromptAndRecentTurns covers R1: a turn end makes the session due, the call's
// input carries the session's first prompt and its 12 newest turns oldest first, and the trimmed reply is stored as the
// headline.
func TestSessionHeadlineTurnEndWritesFromFirstPromptAndRecentTurns(t *testing.T) {
	t.Parallel()
	start := headlineStart()
	st := newHeadlineFakeStore()
	st.addSession("e-alpha", "fix the login redirect")
	turns := make([]store.RecentTurn, 0, 14)
	for i := range 14 {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		turns = append(turns, store.RecentTurn{Role: role, Text: fmt.Sprintf("reply %02d", i)})
	}
	st.endTurn("e-alpha", start, turns...)
	haiku := &headlineFakeHaiku{reply: json.RawMessage(`{"headline":"  Login redirect fix  "}`)}

	startHeadlineLoop(t, st, haiku, start)
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls after the turn end = %d, want 1", got)
	}
	if !slices.Contains(haiku.schemas, sessionHeadlineSchema) {
		t.Fatalf("schemas = %v, want the headline schema", haiku.schemas)
	}
	input := haiku.lastInput()
	if !strings.Contains(input, "fix the login redirect") {
		t.Fatalf("input = %q, want the first prompt", input)
	}
	if strings.Contains(input, "reply 01") || !strings.Contains(input, "reply 02") {
		t.Fatalf("input = %q, want the 12 newest turns and no older one", input)
	}
	if !strings.Contains(input, "user: reply 02") || !strings.Contains(input, "assistant: reply 13") {
		t.Fatalf("input = %q, want each turn with its role", input)
	}
	if first, last := strings.Index(input, "reply 02"), strings.Index(input, "reply 13"); first > last {
		t.Fatalf("input = %q, want the turns oldest first", input)
	}
	if got := st.headlineOf("e-alpha"); got != "Login redirect fix" {
		t.Fatalf("headline = %q, want the trimmed reply", got)
	}
}

// TestSessionHeadlineSecondTurnInsideWindowWritesNothingAtOnce covers R1: a turn end within 5 minutes of the last
// headline writes nothing at once.
func TestSessionHeadlineSecondTurnInsideWindowWritesNothingAtOnce(t *testing.T) {
	t.Parallel()
	start := headlineStart()
	st := newHeadlineFakeStore()
	st.addSession("e-beta", "p")
	st.endTurn("e-beta", start, store.RecentTurn{Role: "assistant", Text: "first"})
	haiku := &headlineFakeHaiku{reply: json.RawMessage(`{"headline":"First headline"}`)}
	loop := startHeadlineLoop(t, st, haiku, start)

	st.endTurn("e-beta", start.Add(time.Minute), store.RecentTurn{Role: "assistant", Text: "second"})
	loop.passAt(start.Add(time.Minute))
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls inside the window = %d, want still 1", got)
	}
	if got := st.headlineOf("e-beta"); got != "First headline" {
		t.Fatalf("headline inside the window = %q, want it unchanged", got)
	}
}

// TestSessionHeadlineWaitingTurnIsWrittenOncePastFiveMinutes covers R1: a turn end inside the window is written on the
// first pass after the window passes, with no further turn, and not again after that.
func TestSessionHeadlineWaitingTurnIsWrittenOncePastFiveMinutes(t *testing.T) {
	t.Parallel()
	start := headlineStart()
	st := newHeadlineFakeStore()
	st.addSession("e-gamma", "p")
	st.endTurn("e-gamma", start, store.RecentTurn{Role: "assistant", Text: "first"})
	haiku := &headlineFakeHaiku{reply: json.RawMessage(`{"headline":"First headline"}`)}
	loop := startHeadlineLoop(t, st, haiku, start)

	st.endTurn("e-gamma", start.Add(time.Minute), store.RecentTurn{Role: "assistant", Text: "second"})
	haiku.answer(json.RawMessage(`{"headline":"Second headline"}`), nil)
	loop.passAt(start.Add(5*time.Minute - time.Second))
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls just inside the window = %d, want still 1", got)
	}

	loop.passAt(start.Add(5 * time.Minute))
	if got := haiku.calls(); got != 2 {
		t.Fatalf("calls once the window passes = %d, want 2", got)
	}
	if got := st.headlineOf("e-gamma"); got != "Second headline" {
		t.Fatalf("headline once the window passes = %q, want the second reply", got)
	}

	loop.passAt(start.Add(10 * time.Minute))
	if got := haiku.calls(); got != 2 {
		t.Fatalf("calls with no further turn = %d, want still 2", got)
	}
}

// TestSessionHeadlineTurnAfterFiveMinutesRefreshesHeadline covers R1: a turn end after the window refreshes the
// headline.
func TestSessionHeadlineTurnAfterFiveMinutesRefreshesHeadline(t *testing.T) {
	t.Parallel()
	start := headlineStart()
	st := newHeadlineFakeStore()
	st.addSession("e-delta", "p")
	st.endTurn("e-delta", start, store.RecentTurn{Role: "assistant", Text: "first"})
	haiku := &headlineFakeHaiku{reply: json.RawMessage(`{"headline":"First headline"}`)}
	loop := startHeadlineLoop(t, st, haiku, start)

	haiku.answer(json.RawMessage(`{"headline":"Refreshed headline"}`), nil)
	at := start.Add(6 * time.Minute)
	st.endTurn("e-delta", at, store.RecentTurn{Role: "assistant", Text: "later"})
	loop.passAt(at)
	if got := haiku.calls(); got != 2 {
		t.Fatalf("calls after a turn past the window = %d, want 2", got)
	}
	if got := st.headlineOf("e-delta"); got != "Refreshed headline" {
		t.Fatalf("headline after a turn past the window = %q, want the refreshed reply", got)
	}
}

// TestSessionHeadlineFailedCallIsNotRetriedInsideFiveMinutes covers R1: a failed headline call holds its session off
// for 5 minutes, and the call is made again once they pass.
func TestSessionHeadlineFailedCallIsNotRetriedInsideFiveMinutes(t *testing.T) {
	t.Parallel()
	start := headlineStart()
	st := newHeadlineFakeStore()
	st.addSession("e-epsilon", "p")
	st.endTurn("e-epsilon", start, store.RecentTurn{Role: "assistant", Text: "first"})
	haiku := &headlineFakeHaiku{err: errors.New("claude exited 3")}
	loop := startHeadlineLoop(t, st, haiku, start)
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls after the failed start = %d, want 1", got)
	}

	loop.passAt(start.Add(time.Minute))
	loop.passAt(start.Add(5*time.Minute - time.Second))
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls inside the hold = %d, want still 1", got)
	}

	haiku.answer(json.RawMessage(`{"headline":"Recovered headline"}`), nil)
	loop.passAt(start.Add(5 * time.Minute))
	if got := haiku.calls(); got != 2 {
		t.Fatalf("calls once the hold passes = %d, want 2", got)
	}
	if got := st.headlineOf("e-epsilon"); got != "Recovered headline" {
		t.Fatalf("headline once the hold passes = %q, want the reply", got)
	}
}

// TestSessionHeadlineFailedWriteIsNotRetriedInsideFiveMinutes covers R1: a failed headline write holds its session off
// for 5 minutes, and the write is tried again once they pass.
func TestSessionHeadlineFailedWriteIsNotRetriedInsideFiveMinutes(t *testing.T) {
	t.Parallel()
	start := headlineStart()
	st := newHeadlineFakeStore()
	st.addSession("e-zeta", "p")
	st.endTurn("e-zeta", start, store.RecentTurn{Role: "assistant", Text: "first"})
	st.failWrites(errors.New("store is busy"))
	haiku := &headlineFakeHaiku{reply: json.RawMessage(`{"headline":"Written headline"}`)}
	loop := startHeadlineLoop(t, st, haiku, start)
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls after the failed write = %d, want 1", got)
	}
	if got := st.headlineOf("e-zeta"); got != "" {
		t.Fatalf("headline after a failed write = %q, want empty", got)
	}

	loop.passAt(start.Add(time.Minute))
	loop.passAt(start.Add(5*time.Minute - time.Second))
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls inside the hold = %d, want still 1", got)
	}

	st.failWrites(nil)
	loop.passAt(start.Add(5 * time.Minute))
	if got := haiku.calls(); got != 2 {
		t.Fatalf("calls once the hold passes = %d, want 2", got)
	}
	if got := st.headlineOf("e-zeta"); got != "Written headline" {
		t.Fatalf("headline once the hold passes = %q, want the reply", got)
	}
}

// TestSessionHeadlineFailedReadAndBlankReplyAreHeldOff covers R1: a failed recent-turns read and a blank reply each
// hold their session off, so neither the read nor the call is made again inside 5 minutes.
func TestSessionHeadlineFailedReadAndBlankReplyAreHeldOff(t *testing.T) {
	t.Parallel()
	start := headlineStart()
	cases := []struct {
		name    string
		readErr error
		reply   json.RawMessage
	}{
		{name: "recent turns read fails", readErr: errors.New("store is busy")},
		{name: "reply is blank", reply: json.RawMessage(`{"headline":" \n "}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := newHeadlineFakeStore()
			st.addSession("e-eta", "p")
			st.endTurn("e-eta", start, store.RecentTurn{Role: "assistant", Text: "first"})
			st.failReads(tc.readErr)
			haiku := &headlineFakeHaiku{reply: tc.reply}
			loop := startHeadlineLoop(t, st, haiku, start)
			reads, calls := st.readCount(), haiku.calls()

			loop.passAt(start.Add(5*time.Minute - time.Second))
			if got := st.readCount(); got != reads {
				t.Fatalf("reads inside the hold = %d, want %d", got, reads)
			}
			if got := haiku.calls(); got != calls {
				t.Fatalf("calls inside the hold = %d, want %d", got, calls)
			}
			if got := st.headlineOf("e-eta"); got != "" {
				t.Fatalf("headline = %q, want empty", got)
			}
		})
	}
}
