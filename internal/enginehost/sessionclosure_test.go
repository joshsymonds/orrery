package enginehost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/closurejudge"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only; no test reaches the real claude binary or a real clock.
const (
	exampleClosureSession = "example-session"
	exampleClosureChanged = int64(1_700_000_100)
	exampleClosureWhy     = "example reason"
)

// errExampleClosure is the failure a fake judge returns.
var errExampleClosure = errors.New("example call failure")

// closureFakeSession is a session the fake store holds: its state, its change time in unix seconds and its turns, oldest
// first.
type closureFakeSession struct {
	state   string
	changed int64
	turns   []store.RecentTurn
}

// closureFakeStore is a ClosureStore over in-memory sessions. Like the store, its RecentTurns returns the newest items
// turns, oldest first, and its SetClosureVerdict records each verdict it is given.
type closureFakeStore struct {
	mu       sync.Mutex
	sessions map[string]closureFakeSession
	verdicts []store.ClosureVerdict
}

// newClosureFakeStore returns a fake store with no sessions and no verdicts.
func newClosureFakeStore() *closureFakeStore {
	return &closureFakeStore{sessions: make(map[string]closureFakeSession)}
}

// addSession adds the example session as sess.
func (s *closureFakeStore) addSession(sess closureFakeSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[exampleClosureSession] = sess
}

// ClosureSession returns the state and change time of the session id, and false when the fake does not hold it.
func (s *closureFakeStore) ClosureSession(_ context.Context, id string) (store.ClosureSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return store.ClosureSession{}, false, nil
	}
	return store.ClosureSession{State: sess.state, Changed: sess.changed}, true, nil
}

// RecentTurns returns the newest items turns of the session id, oldest first.
func (s *closureFakeStore) RecentTurns(_ context.Context, id string, items int) ([]store.RecentTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.sessions[id].turns
	if len(turns) > items {
		turns = turns[len(turns)-items:]
	}
	return slices.Clone(turns), nil
}

// SetClosureVerdict records verdict.
func (s *closureFakeStore) SetClosureVerdict(_ context.Context, verdict store.ClosureVerdict, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verdicts = append(s.verdicts, verdict)
	return nil
}

// verdictsOf returns the verdicts the fake has recorded, in order.
func (s *closureFakeStore) verdictsOf() []store.ClosureVerdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.verdicts)
}

// closureJudgeCall is what one fake Closed call recorded.
type closureJudgeCall struct {
	baseURL string
	env     map[string]string
	recent  string
}

// closureFakeJudge is a ClosureJudge that records its calls and answers each with its verdict and error.
type closureFakeJudge struct {
	mu      sync.Mutex
	verdict closurejudge.Verdict
	err     error
	calls   []closureJudgeCall
}

// Closed records the call and answers it with the fake's verdict and error.
func (j *closureFakeJudge) Closed(
	_ context.Context, baseURL string, env map[string]string, recent string,
) (closurejudge.Verdict, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, closureJudgeCall{baseURL: baseURL, env: env, recent: recent})
	return j.verdict, j.err
}

// callsOf returns the calls the fake has answered, in order.
func (j *closureFakeJudge) callsOf() []closureJudgeCall {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.calls)
}

// closureRun wakes the example session on a SessionClosures over st and judge, runs it until one pass has judged the
// wake, and then stops it.
func closureRun(t *testing.T, st ClosureStore, judge ClosureJudge) {
	t.Helper()
	passed := make(chan struct{}, 1)
	closures := NewSessionClosures(SessionClosuresConfig{
		Store:  st,
		Judge:  judge,
		Passed: func() { passed <- struct{}{} },
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		closures.Run(ctx)
	}()
	closures.Wake(exampleClosureSession)
	<-passed
	cancel()
	<-done
}

func TestInboxClosureJudgeDoneSessionGetsOneCallAndStoredVerdict(t *testing.T) {
	t.Parallel()
	var turns []store.RecentTurn
	for i := 1; i <= 13; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		turns = append(turns, store.RecentTurn{Role: role, Text: fmt.Sprintf("example turn %d", i)})
	}
	st := newClosureFakeStore()
	st.addSession(closureFakeSession{
		state: string(protocol.StateDone), changed: exampleClosureChanged, turns: turns,
	})
	judge := &closureFakeJudge{verdict: closurejudge.Verdict{Closed: true, Why: exampleClosureWhy}}

	closureRun(t, st, judge)

	calls := judge.callsOf()
	if len(calls) != 1 {
		t.Fatalf("Closed calls = %d, want 1", len(calls))
	}
	if calls[0].baseURL != "" || calls[0].env != nil {
		t.Errorf("baseURL, env = %q, %v, want \"\", nil", calls[0].baseURL, calls[0].env)
	}
	// The newest twelve turns are read, so the oldest of the thirteen is left out.
	wantLines := make([]string, 0, len(turns)-1)
	for _, turn := range turns[1:] {
		wantLines = append(wantLines, turn.Role+": "+turn.Text)
	}
	if want := strings.Join(wantLines, "\n"); calls[0].recent != want {
		t.Errorf("recent = %q, want %q", calls[0].recent, want)
	}
	want := store.ClosureVerdict{
		Session: exampleClosureSession, Changed: exampleClosureChanged, Closed: true, Why: exampleClosureWhy,
	}
	if got := st.verdictsOf(); !slices.Equal(got, []store.ClosureVerdict{want}) {
		t.Errorf("verdicts = %+v, want [%+v]", got, want)
	}
}

func TestInboxClosureJudgeWorkingOrWaitingSessionMakesNoCall(t *testing.T) {
	t.Parallel()
	for _, state := range []string{string(protocol.StateWorking), string(protocol.StateWaiting)} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			st := newClosureFakeStore()
			st.addSession(closureFakeSession{
				state: state, changed: exampleClosureChanged,
				turns: []store.RecentTurn{{Role: "user", Text: "example prompt"}},
			})
			judge := &closureFakeJudge{verdict: closurejudge.Verdict{Closed: true, Why: exampleClosureWhy}}

			closureRun(t, st, judge)

			if calls := judge.callsOf(); len(calls) != 0 {
				t.Errorf("Closed calls = %d, want 0", len(calls))
			}
			if got := st.verdictsOf(); len(got) != 0 {
				t.Errorf("verdicts = %+v, want none", got)
			}
		})
	}
}

func TestInboxClosureJudgeFailedCallStoresNothing(t *testing.T) {
	t.Parallel()
	st := newClosureFakeStore()
	st.addSession(closureFakeSession{
		state: string(protocol.StateDone), changed: exampleClosureChanged,
		turns: []store.RecentTurn{{Role: "user", Text: "example prompt"}},
	})
	judge := &closureFakeJudge{err: errExampleClosure}

	closureRun(t, st, judge)

	if calls := judge.callsOf(); len(calls) != 1 {
		t.Fatalf("Closed calls = %d, want 1", len(calls))
	}
	if got := st.verdictsOf(); len(got) != 0 {
		t.Errorf("verdicts = %+v, want none", got)
	}
}
