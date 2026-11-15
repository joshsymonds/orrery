package enginehost

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// stepIDDerived is a well-formed derived step id: 32 lowercase hex digits.
const stepIDDerived = "0123456789abcdef0123456789abcdef"

// pausedStartSessions is a stepSessions whose first StartStep signals entered and waits on release before it starts the
// step, so a test can start a second step under the same id while the first is in flight and its row is not yet stored.
type pausedStartSessions struct {
	stepSessions
	entered chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
}

func (p *pausedStartSessions) StartStep(ctx context.Context, step StepSession) error {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.mu.Unlock()
	if first {
		close(p.entered)
		<-p.release
	}
	return p.stepSessions.StartStep(ctx, step)
}

// stepIDStartRefused asserts that a start refused for its id returned err matching want, an empty id, and stored nothing,
// started no session and left no worktree.
func stepIDStartRefused(t *testing.T, f *stepStartFixture, id string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("Start = %q, %v; want an error matching %v", id, err, want)
	}
	if id != "" {
		t.Errorf("Start id = %q on refusal, want empty", id)
	}
	if n := f.sessions.startCalls(); n != 0 {
		t.Errorf("StartStep calls = %d, want 0", n)
	}
	rows, rowsErr := f.store.OpenSteps(t.Context())
	if rowsErr != nil {
		t.Fatalf("OpenSteps: %v", rowsErr)
	}
	if len(rows) != 0 {
		t.Errorf("stored %d step rows on refusal, want none", len(rows))
	}
	if names := refusalWorktrees(t, f.worktrees); len(names) != 0 {
		t.Errorf("worktrees = %v, want none after a refused start", names)
	}
}

func TestStepsStartRunsUnderDerivedID(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	start := stepStartWriting(linked)
	start.ID = stepIDDerived

	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id != stepIDDerived {
		t.Fatalf("Start id = %q, want the derived id %q", id, stepIDDerived)
	}
	if step := f.sessions.first(t); step.ID != stepIDDerived {
		t.Errorf("StartStep id = %q, want %q", step.ID, stepIDDerived)
	}
	row, err := f.steps.Status(t.Context(), stepIDDerived)
	if err != nil {
		t.Fatalf("Status(%s): %v", stepIDDerived, err)
	}
	if row.ID != stepIDDerived {
		t.Errorf("stored row id = %q, want %q", row.ID, stepIDDerived)
	}
}

func TestStepsStartRefusesMalformedIDAndStoresNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   string
	}{
		{name: "too short", id: "0123456789abcdef0123456789abcde"},
		{name: "too long", id: "0123456789abcdef0123456789abcdef0"},
		{name: "upper case", id: "0123456789ABCDEF0123456789abcdef"},
		{name: "not hex", id: "0123456789abcdef0123456789abcdeg"},
		{name: "trailing dash", id: "0123456789abcdef0123456789abcde-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepStartFixture(t)
			start := stepStartReadOnly(stepGitTestRepo(t), "HEAD")
			start.ID = tc.id

			id, err := f.steps.Start(t.Context(), start)
			stepIDStartRefused(t, f, id, err, ErrBadStepID)
		})
	}
}

func TestStepsStartRefusesStoredIDAndStartsNothingNew(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	first := stepStartWriting(linked)
	first.ID = stepIDDerived
	if _, err := f.steps.Start(t.Context(), first); err != nil {
		t.Fatalf("first Start: %v", err)
	}

	second := stepStartReadOnly(linked, "HEAD")
	second.ID = stepIDDerived
	id, err := f.steps.Start(t.Context(), second)
	if !errors.Is(err, ErrStepExists) {
		t.Fatalf("second Start = %q, %v; want an error matching ErrStepExists", id, err)
	}
	if id != "" {
		t.Errorf("second Start id = %q on refusal, want empty", id)
	}
	if n := f.sessions.startCalls(); n != 1 {
		t.Errorf("StartStep calls = %d, want 1", n)
	}
	rows, err := f.store.OpenSteps(t.Context())
	if err != nil {
		t.Fatalf("OpenSteps: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("stored %d step rows, want the one the first start stored", len(rows))
	}
	if names := refusalWorktrees(t, f.worktrees); len(names) != 0 {
		t.Errorf("worktrees = %v, want none after a refused start", names)
	}
}

func TestStepsStartRefusesIDInFlightAndStartsNothingNew(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	start := stepStartWriting(linked)
	start.ID = stepIDDerived
	paused := &pausedStartSessions{
		stepSessions: f.sessions,
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	f.steps.sessions = paused
	releasePaused := sync.OnceFunc(func() { close(paused.release) })
	defer releasePaused()

	type result struct {
		id  string
		err error
	}
	firstDone := make(chan result, 1)
	go func() {
		id, err := f.steps.Start(t.Context(), start)
		firstDone <- result{id: id, err: err}
	}()
	select {
	case <-paused.entered:
	case got := <-firstDone:
		t.Fatalf("first Start returned %q, %v before it reached StartStep", got.id, got.err)
	}

	id, err := f.steps.Start(t.Context(), start)
	if !errors.Is(err, ErrStepExists) {
		t.Fatalf("second Start = %q, %v; want an error matching ErrStepExists", id, err)
	}
	if id != "" {
		t.Errorf("second Start id = %q on refusal, want empty", id)
	}
	if n := f.sessions.startCalls(); n != 0 {
		t.Errorf("StartStep calls = %d while the first start is in flight, want 0", n)
	}

	releasePaused()
	got := <-firstDone
	if got.err != nil {
		t.Fatalf("first Start: %v", got.err)
	}
	if got.id != stepIDDerived {
		t.Errorf("first Start id = %q, want %q", got.id, stepIDDerived)
	}
	rows, err := f.store.OpenSteps(t.Context())
	if err != nil {
		t.Fatalf("OpenSteps: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != stepIDDerived {
		t.Errorf("stored rows = %+v, want the one step %q", rows, stepIDDerived)
	}
}
