package enginehost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// matchGate forwards to inner, but holds the first ApplyEngine call whose ops include one that match accepts. entered
// closes when the gate holds that call, and the call proceeds once release closes.
type matchGate struct {
	inner    Applier
	match    func(store.EngineOp) bool
	once     sync.Once
	entered  chan struct{}
	release  chan struct{}
	openOnce sync.Once
}

func (g *matchGate) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	for _, op := range ops {
		if g.match(op) {
			g.once.Do(func() {
				close(g.entered)
				<-g.release
			})
			break
		}
	}
	return g.inner.ApplyEngine(ctx, ops, now)
}

// open releases the held call, if any. It is safe to call more than once.
func (g *matchGate) open() {
	g.openOnce.Do(func() { close(g.release) })
}

// gatedManager returns a manager with a cap of one whose writer stores through a matchGate that holds the first batch
// with an op match accepts. The gate opens at the end of the test, before the manager closes, so a held write cannot
// block Close.
func gatedManager(t *testing.T, match func(store.EngineOp) bool) (*Manager, *matchGate, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	gate := &matchGate{inner: s, match: match, entered: make(chan struct{}), release: make(chan struct{})}
	writer := NewWriter(WriterConfig{Applier: gate, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:   sh,
		Claude:  fakeClaudePath(t),
		Environ: []string{"PATH=" + os.Getenv("PATH")},
		Cap:     1,
		Writer:  writer,
		Now:     fixedClock,
		Store:   s,
	})
	t.Cleanup(m.Close)
	t.Cleanup(gate.open)
	return m, gate, path
}

// isPromptWithID reports whether op is an engine prompt that carries promptID.
func isPromptWithID(promptID string) func(store.EngineOp) bool {
	return func(op store.EngineOp) bool {
		prompt, ok := op.(store.EnginePrompt)
		return ok && prompt.PromptID == promptID
	}
}

// TestSayRetryAfterCanceledSayRunsPromptOnce cancels a keyed Say while its prompt is held in the writer. The Say
// returns its context's error, the retry with the same prompt id returns nil, and the prompt is sent once.
func TestSayRetryAfterCanceledSayRunsPromptOnce(t *testing.T) {
	m, gate, path := gatedManager(t, isPromptWithID("p1"))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- m.Say(ctx, SayRequest{ID: id, Text: "again", PromptID: "p1"}) }()
	receive(t, gate.entered)
	cancel()
	if err := receive(t, errc); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Say: got %v, want context.Canceled", err)
	}

	gate.open()
	waitHistory(t, path, id, "the held prompt stored", func(h store.EngineHistory) bool {
		return len(promptIDsOf(t, h)) == 2
	})
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again", PromptID: "p1"}); err != nil {
		t.Fatalf("retry Say: got %v, want nil", err)
	}
	if got := promptIDsOf(t, historyOf(t, path, id)); !slices.Equal(got, []string{"", "p1"}) {
		t.Fatalf("prompt ids after retry = %q, want [\"\" \"p1\"]", got)
	}
	history := waitHistory(t, path, id, "the reply to the retried prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []string{"prompt: hi", "reply: fake reply 1", "prompt: again", "reply: fake reply 2"}
	if got := conversationOf(t, history); !slices.Equal(got, want) {
		t.Fatalf("conversation = %q, want %q", got, want)
	}
}

// TestStartRetryAfterCanceledStartRunsOnce cancels a keyed Start while its row is held in the writer. The Start
// returns its context's error, the retry with the same id returns that id, and one process starts.
func TestStartRetryAfterCanceledStartRunsOnce(t *testing.T) {
	m, gate, path := gatedManager(t, func(op store.EngineOp) bool {
		add, ok := op.(store.EngineAdd)
		return ok && add.ID == chosenID
	})
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		_, err := m.Start(ctx, StartRequest{ID: chosenID, Dir: dir, Prompt: "hi"})
		errc <- err
	}()
	receive(t, gate.entered)
	cancel()
	if err := receive(t, errc); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Start: got %v, want context.Canceled", err)
	}

	gate.open()
	waitHistory(t, path, chosenID, "the held row stored", func(h store.EngineHistory) bool {
		return countKind(h, store.EventEngineAdded) == 1
	})
	id, err := m.Start(t.Context(), StartRequest{ID: chosenID, Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("retry Start: %v", err)
	}
	if id != chosenID {
		t.Fatalf("retry Start id = %q, want %q", id, chosenID)
	}
	waitUntil(t, "the fake claude start", func() bool {
		_, statErr := os.Stat(filepath.Join(dir, "fakeclaude.args"))
		return statErr == nil
	})
	history := waitHistory(t, path, chosenID, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if got := fakeArgs(t, dir); len(got) != 1 {
		t.Fatalf("fake claude starts = %d, want 1: %q", len(got), got)
	}
	want := []string{"prompt: hi", "reply: fake reply 1"}
	if got := conversationOf(t, history); !slices.Equal(got, want) {
		t.Fatalf("conversation = %q, want %q", got, want)
	}
}

// TestRetryWhileAttemptHeldWaitsForItsResult sends the retry of a keyed Say while the canceled first attempt is still
// held in the writer. The retry must not return before the attempt ends, must return its result once it does, and the
// prompt must be sent once.
func TestRetryWhileAttemptHeldWaitsForItsResult(t *testing.T) {
	m, gate, path := gatedManager(t, isPromptWithID("p1"))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstc := make(chan error, 1)
	go func() { firstc <- m.Say(ctx, SayRequest{ID: id, Text: "again", PromptID: "p1"}) }()
	receive(t, gate.entered)
	cancel()
	if err := receive(t, firstc); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Say: got %v, want context.Canceled", err)
	}

	retryc := make(chan error, 1)
	go func() { retryc <- m.Say(t.Context(), SayRequest{ID: id, Text: "again", PromptID: "p1"}) }()
	// The pause lets the retry reach the held attempt before release. A retry that ran on its own would find the held
	// prompt unstored and send it again, which the prompt ids below would show.
	select {
	case err := <-retryc:
		t.Fatalf("retry returned %v while the attempt was held", err)
	case <-time.After(200 * time.Millisecond):
	}

	gate.open()
	if err := receive(t, retryc); err != nil {
		t.Fatalf("retry Say: got %v, want nil", err)
	}
	if got := promptIDsOf(t, historyOf(t, path, id)); !slices.Equal(got, []string{"", "p1"}) {
		t.Fatalf("prompt ids after retry = %q, want [\"\" \"p1\"]", got)
	}
	history := waitHistory(t, path, id, "the reply to the retried prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []string{"prompt: hi", "reply: fake reply 1", "prompt: again", "reply: fake reply 2"}
	if got := conversationOf(t, history); !slices.Equal(got, want) {
		t.Fatalf("conversation = %q, want %q", got, want)
	}
}
