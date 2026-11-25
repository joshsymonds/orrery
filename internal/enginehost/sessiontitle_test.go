package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// titleFakeHaiku is a haikuCaller that answers every call with reply and err, and records the schema and input of each
// call in order.
type titleFakeHaiku struct {
	reply json.RawMessage
	err   error

	mu      sync.Mutex
	schemas []string
	inputs  []string
}

// Call records the call and answers it with the fake's reply and error.
func (h *titleFakeHaiku) Call(_ context.Context, schema, input string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.schemas = append(h.schemas, schema)
	h.inputs = append(h.inputs, input)
	return h.reply, h.err
}

// calls returns how many calls the fake has answered.
func (h *titleFakeHaiku) calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.inputs)
}

// lastInput returns the input of the newest call.
func (h *titleFakeHaiku) lastInput() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.inputs[len(h.inputs)-1]
}

// titleFakeStore is a SessionTitleStore over an in-memory list of sessions. Like the store, it writes a title only when
// the session has none, and it lists only the sessions that have none.
type titleFakeStore struct {
	untitled []store.UntitledSession
	writeErr error

	mu     sync.Mutex
	titles map[string]string
}

// UntitledSessions lists the sessions that have no title.
func (s *titleFakeStore) UntitledSessions(context.Context) ([]store.UntitledSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var untitled []store.UntitledSession
	for _, row := range s.untitled {
		if s.titles[row.ID] == "" {
			untitled = append(untitled, row)
		}
	}
	return untitled, nil
}

// UntitledSession returns the session id when it has no title, and false when it has one or the fake does not hold it.
func (s *titleFakeStore) UntitledSession(_ context.Context, id string) (store.UntitledSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.untitled {
		if row.ID == id && s.titles[id] == "" {
			return row, true, nil
		}
	}
	return store.UntitledSession{}, false, nil
}

// SetSessionTitle stores title for id when the session has none, and answers writeErr when the fake fails writes.
func (s *titleFakeStore) SetSessionTitle(_ context.Context, id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	if s.titles == nil {
		s.titles = make(map[string]string)
	}
	if s.titles[id] == "" {
		s.titles[id] = title
	}
	return nil
}

// titleOf returns the stored title of id, or "" when it has none.
func (s *titleFakeStore) titleOf(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.titles[id]
}

// titleLoop is a SessionTitles started over a fake store, and the channels a test drives it with. Its retry timer is the
// fake one clock arms.
type titleLoop struct {
	titles *SessionTitles
	clock  *retryClock
	passes chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
}

// startTitleLoop starts a SessionTitles over st and haiku, and returns once its start pass is done. The test's end
// stops it too.
func startTitleLoop(t *testing.T, st SessionTitleStore, haiku haikuCaller) *titleLoop {
	t.Helper()
	passes := make(chan struct{}, 16)
	ctx, cancel := context.WithCancel(t.Context())
	loop := &titleLoop{
		clock: &retryClock{armed: make(chan armedRetry, 8)}, passes: passes, done: make(chan struct{}), cancel: cancel,
	}
	loop.titles = NewSessionTitles(SessionTitlesConfig{
		Store:    st,
		Haiku:    haiku,
		NewTimer: loop.clock.newTimer,
		Passed:   func() { passes <- struct{}{} },
	})
	go func() {
		defer close(loop.done)
		loop.titles.Run(ctx)
	}()
	t.Cleanup(loop.stop)
	<-passes
	return loop
}

// stop ends the loop and waits for Run to return. It is safe to call more than once.
func (l *titleLoop) stop() {
	l.cancel()
	<-l.done
}

// wake wakes the session with id, as the store's first-prompt hook does, and waits until the pass it starts is done.
func (l *titleLoop) wake(id string) {
	l.titles.Wake(id)
	<-l.passes
}

// tick fires the retry the loop armed, and waits until the pass it starts is done. A retry is armed while a title the
// store refused is kept.
func (l *titleLoop) tick() {
	retry := <-l.clock.armed
	retry.fire <- time.Time{}
	<-l.passes
}

// TestSessionTitleWritesOnceFromTheFirstPrompt covers R3: the title call carries the title schema and the session's first
// prompt, the trimmed reply is stored as the title, and later ticks make no second call for the session.
func TestSessionTitleWritesOnceFromTheFirstPrompt(t *testing.T) {
	t.Parallel()
	st := &titleFakeStore{untitled: []store.UntitledSession{
		{ID: "e-alpha", Dir: "/work/alpha", FirstPrompt: "fix the login redirect"},
	}}
	haiku := &titleFakeHaiku{reply: json.RawMessage(`{"title":"  Login redirect  "}`)}

	loop := startTitleLoop(t, st, haiku)
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls after start = %d, want 1", got)
	}
	if !slices.Contains(haiku.schemas, sessionTitleSchema) {
		t.Fatalf("schemas = %v, want the title schema", haiku.schemas)
	}
	if input := haiku.lastInput(); !strings.Contains(input, "fix the login redirect") {
		t.Fatalf("input = %q, want the first prompt", input)
	}
	if got := st.titleOf("e-alpha"); got != "Login redirect" {
		t.Fatalf("title after start = %q, want the trimmed reply", got)
	}
	loop.wake("e-alpha")
	loop.wake("e-alpha")
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls after two wakes = %d, want still 1", got)
	}
	if got := st.titleOf("e-alpha"); got != "Login redirect" {
		t.Fatalf("title after two wakes = %q, want it unchanged", got)
	}
}

// TestSessionTitleBlankOrFailedReplyStoresNothing covers R3: a failed call, a blank title, a reply that is not an
// object and a failed write each leave the session untitled, and a wake before the session's deadline fires calls for it
// no more: a failed call or reply waits out its deadline (see callRetries), and a failed write keeps the title for the
// retry to write.
func TestSessionTitleBlankOrFailedReplyStoresNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		reply    json.RawMessage
		err      error
		writeErr error
	}{
		{name: "call fails", err: errors.New("claude exited 3")},
		{name: "title is blank", reply: json.RawMessage(`{"title":" \n "}`)},
		{name: "reply is not an object", reply: json.RawMessage(`not json`)},
		{name: "write fails", reply: json.RawMessage(`{"title":"Fine"}`), writeErr: errors.New("store is busy")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := &titleFakeStore{
				untitled: []store.UntitledSession{{ID: "e-beta", Dir: "/work/beta", FirstPrompt: "p"}},
				writeErr: tc.writeErr,
			}
			haiku := &titleFakeHaiku{reply: tc.reply, err: tc.err}

			loop := startTitleLoop(t, st, haiku)
			if got := haiku.calls(); got != 1 {
				t.Fatalf("calls after start = %d, want 1", got)
			}
			if got := st.titleOf("e-beta"); got != "" {
				t.Fatalf("title after a failed reply = %q, want empty", got)
			}
			loop.wake("e-beta")
			if got := haiku.calls(); got != 1 {
				t.Fatalf("calls after a wake = %d, want still 1", got)
			}
		})
	}
}

// TestSessionTitleSecondStartRetriesAnUntitledSessionOnce covers R3: a start calls at once for a session a failed call
// left untitled, and once that call succeeds a wake calls for it no more.
func TestSessionTitleSecondStartRetriesAnUntitledSessionOnce(t *testing.T) {
	t.Parallel()
	st := &titleFakeStore{untitled: []store.UntitledSession{{ID: "e-gamma", Dir: "/work/gamma", FirstPrompt: "p"}}}

	first := startTitleLoop(t, st, &titleFakeHaiku{reply: json.RawMessage(`not json`)})
	first.wake("e-gamma")
	first.stop()

	haiku := &titleFakeHaiku{reply: json.RawMessage(`{"title":"Gamma work"}`)}
	second := startTitleLoop(t, st, haiku)
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls of the second start = %d, want 1", got)
	}
	if got := st.titleOf("e-gamma"); got != "Gamma work" {
		t.Fatalf("title after the second start = %q, want the reply", got)
	}
	second.wake("e-gamma")
	if got := haiku.calls(); got != 1 {
		t.Fatalf("calls after a wake of the second start = %d, want still 1", got)
	}
}
