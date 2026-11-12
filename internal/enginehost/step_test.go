package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepStartSessions is a fake stepSessions. StartStep records each step and commits its ops through the writer as the
// real StartStep does, with the session's engine row first, so Status reads the step. startErr is returned after that
// commit when it is set.
type stepStartSessions struct {
	writer   *Writer
	startErr error
	// onStart, when set, runs after StartStep has stored the step's rows, before it returns startErr.
	onStart func()

	mu      sync.Mutex
	started []StepSession
}

func (f *stepStartSessions) StartStep(ctx context.Context, step StepSession) error {
	f.mu.Lock()
	f.started = append(f.started, step)
	f.mu.Unlock()
	ops := append([]store.EngineOp{
		store.EngineAdd{ID: step.ID, Dir: step.Dir, State: "working", Kind: store.KindStep},
	}, step.Ops...)
	if err := f.writer.Commit(ctx, step.ID, ops...); err != nil {
		return err
	}
	if f.onStart != nil {
		f.onStart()
	}
	return f.startErr
}

func (f *stepStartSessions) Interrupt(context.Context, string) error {
	return errors.New("stepStartSessions: Interrupt is not used")
}

func (f *stepStartSessions) Continue(context.Context, string, string) error {
	return errors.New("stepStartSessions: Continue is not used")
}

func (f *stepStartSessions) Stop(context.Context, string) error {
	return nil
}

// startCalls returns how many times StartStep was called.
func (f *stepStartSessions) startCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

// first returns the step StartStep was called with first. The test fails if there is none.
func (f *stepStartSessions) first(t *testing.T) StepSession {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.started) == 0 {
		t.Fatal("StartStep was not called")
	}
	return f.started[0]
}

// stepStartFixture is a Steps over a real store and writer, with fake sessions and its worktrees under t.TempDir().
type stepStartFixture struct {
	steps     *Steps
	sessions  *stepStartSessions
	store     *store.Store
	worktrees string
}

// newStepStartFixture returns a fixture whose Steps starts its loops on contexts the test cancels at cleanup.
func newStepStartFixture(t *testing.T) *stepStartFixture {
	t.Helper()
	s, _ := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sessions := &stepStartSessions{writer: writer}
	worktrees := t.TempDir()
	steps, err := NewSteps(StepsConfig{
		Sessions:  sessions,
		Store:     s,
		Writer:    writer,
		Locks:     []string{filepath.Join(t.TempDir(), "gate.lock")},
		Worktrees: worktrees,
		Environ:   []string{"PATH=" + os.Getenv("PATH")},
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(func() {
		steps.mu.Lock()
		defer steps.mu.Unlock()
		for _, cancel := range steps.loops {
			cancel()
		}
	})
	return &stepStartFixture{steps: steps, sessions: sessions, store: s, worktrees: worktrees}
}

// stepStartWriting returns a valid writing start in workspace, with one owned path and no gate.
func stepStartWriting(workspace string) StepStart {
	return StepStart{
		Agent:     "implementer",
		Prompt:    "do it",
		Schema:    stepLoopSchema,
		Workspace: workspace,
		Owned:     []string{stepDoneOwned},
		DoneValue: stepLoopDone,
	}
}

// stepStartReadOnly returns a valid read-only start of the repository at workspace, at revision.
func stepStartReadOnly(workspace, revision string) StepStart {
	return StepStart{
		Agent:     "reviewer",
		Prompt:    "look",
		Schema:    stepLoopSchema,
		Workspace: workspace,
		Revision:  revision,
		ReadOnly:  true,
	}
}

// stepStartAwait fails the test unless ready reports true within waitTimeout.
func stepStartAwait(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stepStartGone reports whether nothing exists at path.
func stepStartGone(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}

func TestStepsStartRefusesBadInputAndStoresNothing(t *testing.T) {
	mainTree := stepGitTestRepo(t)
	nonGit := t.TempDir()
	cases := []struct {
		name   string
		mutate func(*StepStart)
		want   error
		text   string
	}{
		{name: "empty prompt", mutate: func(s *StepStart) { s.Prompt = "" }, want: ErrEmptyPrompt},
		{name: "schema not an object", mutate: func(s *StepStart) {
			s.Schema = `{"type":"array"}`
		}, want: ErrBadSchema},
		{name: "relative workspace", mutate: func(s *StepStart) { s.Workspace = "relative/dir" }, want: ErrBadDir},
		{name: "absolute owned path", mutate: func(s *StepStart) {
			s.Owned = []string{"/etc/passwd"}
		}, want: ErrBadOwned},
		{name: "owned path outside repository", mutate: func(s *StepStart) {
			s.Owned = []string{"../outside.txt"}
		}, want: ErrBadOwned},
		{
			name:   "writing in main working tree",
			mutate: func(s *StepStart) { s.Workspace = mainTree },
			want:   ErrMainWorktree,
		},
		{name: "writing in non-git folder", mutate: func(s *StepStart) { s.Workspace = nonGit }, want: ErrNotGit},
		{name: "read-only unknown revision", mutate: func(s *StepStart) {
			s.ReadOnly = true
			s.Workspace = mainTree
			s.Revision = "no-such-revision"
		}, text: "resolve revision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepStartFixture(t)
			linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
			start := stepStartWriting(linked)
			tc.mutate(&start)

			id, err := f.steps.Start(t.Context(), start)
			if err == nil {
				t.Fatalf("Start = %q, nil error; want a refusal", id)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Start error = %v, want %v", err, tc.want)
			}
			if tc.text != "" && !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("Start error = %v, want it to name %q", err, tc.text)
			}
			if id != "" {
				t.Errorf("Start id = %q on refusal, want empty", id)
			}
			if n := f.sessions.startCalls(); n != 0 {
				t.Errorf("StartStep calls = %d, want 0", n)
			}
			rows, err := f.store.OpenSteps(t.Context())
			if err != nil {
				t.Fatalf("OpenSteps: %v", err)
			}
			if len(rows) != 0 {
				t.Errorf("stored %d step rows on refusal, want none", len(rows))
			}
			if entries, _ := os.ReadDir(f.worktrees); len(entries) != 0 {
				t.Errorf("refusal left %d entries under the worktrees dir", len(entries))
			}
		})
	}
}

func TestStepsStartWritingHandsStartStepTheStepAndRunsItsLoop(t *testing.T) {
	f := newStepStartFixture(t)
	repo := stepGitTestRepo(t)
	linked := stepWorktreeTestAddLinked(t, repo)
	commit := stepGitTestRun(t, linked, "rev-parse", "HEAD")
	start := stepStartWriting(linked)

	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := checkID(id); err != nil {
		t.Fatalf("Start id %q: %v", id, err)
	}

	step := f.sessions.first(t)
	if step.ID != id || step.Dir != linked || step.Agent != start.Agent || step.Prompt != start.Prompt ||
		step.Schema != start.Schema {
		t.Fatalf("StartStep got id %q dir %q agent %q prompt %q schema %q", step.ID, step.Dir, step.Agent,
			step.Prompt, step.Schema)
	}
	if step.Observer == nil {
		t.Fatal("StartStep got a nil Observer")
	}
	want := store.StepAdd{
		ID:         id,
		Agent:      start.Agent,
		Prompt:     start.Prompt,
		Schema:     start.Schema,
		Workspace:  linked,
		Revision:   commit,
		DoneValue:  stepLoopDone,
		Owned:      []string{stepDoneOwned},
		GateLimit:  stepDefaultGateLimit,
		ModelLimit: stepDefaultModelLimit,
	}
	if len(step.Ops) != 1 || !reflect.DeepEqual(step.Ops[0], want) {
		t.Fatalf("StartStep ops = %#v, want the one StepAdd %#v", step.Ops, want)
	}

	step.Observer.Return(session.TurnEnded{StructuredOutput: []byte(`{"status":"stopped"}`)})
	stepStartAwait(t, "the step to end", func() bool {
		row, err := f.steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndReturned || row.Status != "stopped" {
		t.Errorf("end = %q status %q, want returned stopped", row.End, row.Status)
	}
}

func TestStepsStartWritingDefaultsDoneValueGateLimitAndRevision(t *testing.T) {
	f := newStepStartFixture(t)
	repo := stepGitTestRepo(t)
	linked := stepWorktreeTestAddLinked(t, repo)
	commit := stepGitTestRun(t, linked, "rev-parse", "HEAD")
	start := stepStartWriting(linked)
	start.DoneValue = ""

	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	want := store.StepAdd{
		ID:         id,
		Agent:      start.Agent,
		Prompt:     start.Prompt,
		Schema:     start.Schema,
		Workspace:  linked,
		Revision:   commit,
		DoneValue:  stepDefaultDone,
		Owned:      []string{stepDoneOwned},
		GateLimit:  stepDefaultGateLimit,
		ModelLimit: stepDefaultModelLimit,
	}
	got, ok := f.sessions.first(t).Ops[0].(store.StepAdd)
	if !ok {
		t.Fatalf("StartStep op is %T, want store.StepAdd", f.sessions.first(t).Ops[0])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StepAdd = %#v, want %#v", got, want)
	}
}

func TestStepsStartReadOnlyRunsDetachedWorktreeAndRemovesItAtEnd(t *testing.T) {
	f := newStepStartFixture(t)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	stepGitTestCommit(t, repo, map[string]string{"README": "second\n"})

	id, err := f.steps.Start(t.Context(), stepStartReadOnly(repo, revision))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	wt := filepath.Join(f.worktrees, id)
	step := f.sessions.first(t)
	if step.Dir != wt {
		t.Fatalf("StartStep dir = %q, want %q", step.Dir, wt)
	}
	if got := stepGitTestRun(t, wt, "rev-parse", "HEAD"); got != revision {
		t.Errorf("worktree HEAD = %s, want the revision %s", got, revision)
	}
	if got := stepGitTestRun(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
		t.Errorf("worktree is on %q, want detached HEAD", got)
	}
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !row.ReadOnly || row.Worktree != wt || row.Revision != revision {
		t.Errorf("row readonly %v worktree %q revision %q, want true %q %q",
			row.ReadOnly, row.Worktree, row.Revision, wt, revision)
	}
	if stepStartGone(wt) {
		t.Fatal("worktree is missing while the step runs")
	}

	step.Observer.Return(session.TurnEnded{StructuredOutput: []byte(`{"status":"done"}`)})
	stepStartAwait(t, "the step to end and its worktree to go", func() bool {
		row, err := f.steps.Status(t.Context(), id)
		return err == nil && row.End != "" && stepStartGone(wt)
	})
	row, err = f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndDone || row.Status != stepDefaultDone {
		t.Errorf("end = %q status %q, want done %s", row.End, row.Status, stepDefaultDone)
	}
	if list := stepWorktreeTestWorktrees(t, repo); strings.Contains(list, id) {
		t.Errorf("git still lists the step worktree after the step ended:\n%s", list)
	}
}

func TestStepsStartStartStepErrorEndsFailedAndRemovesWorktree(t *testing.T) {
	f := newStepStartFixture(t)
	spawnErr := errors.New("spawn refused")
	f.sessions.startErr = spawnErr
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")

	id, err := f.steps.Start(t.Context(), stepStartReadOnly(repo, revision))
	if !errors.Is(err, spawnErr) {
		t.Fatalf("Start error = %v, want it to wrap %v", err, spawnErr)
	}
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndFailed || !strings.Contains(row.Reason, spawnErr.Error()) {
		t.Errorf("end = %q reason %q, want failed naming %q", row.End, row.Reason, spawnErr)
	}
	if wt := filepath.Join(f.worktrees, id); !stepStartGone(wt) {
		t.Errorf("worktree %s still exists after the start failed", wt)
	}
}

func TestStepsStartCanceledStartStepStillEndsFailedAndRemovesWorktree(t *testing.T) {
	f := newStepStartFixture(t)
	f.sessions.startErr = context.Canceled
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	ctx, cancel := context.WithCancel(t.Context())
	f.sessions.onStart = cancel

	id, err := f.steps.Start(ctx, stepStartReadOnly(repo, revision))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want it to wrap %v", err, context.Canceled)
	}
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != stepLoopEndFailed {
		t.Errorf("end = %q, want failed after a start whose context ended", row.End)
	}
	if wt := filepath.Join(f.worktrees, id); !stepStartGone(wt) {
		t.Errorf("worktree %s still exists after the start's context ended", wt)
	}
}

func TestNewStepsNegativeWatchTickReturnsErrorAndNoSteps(t *testing.T) {
	steps, err := NewSteps(StepsConfig{WatchTick: -time.Second})
	if !errors.Is(err, ErrBadWatchTick) {
		t.Fatalf("NewSteps error = %v, want it to match %v", err, ErrBadWatchTick)
	}
	if steps != nil {
		t.Errorf("NewSteps returned Steps with the error, want nil")
	}
}

func TestNewStepsZeroWatchTickKeepsDefault(t *testing.T) {
	steps, err := NewSteps(StepsConfig{})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	if steps.tick != stepDefaultWatchTick {
		t.Errorf("tick = %v, want the default %v", steps.tick, stepDefaultWatchTick)
	}
}
