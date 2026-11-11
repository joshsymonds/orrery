package enginehost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// newManagerOn returns a manager with the given cap that runs the claude at claude, keeps its engine rows in the store at
// path through a running writer of its own, and reports notices to sink. Close runs before the writer stops and the store
// closes.
func newManagerOn(t *testing.T, path string, limit int, claude string, sink NoticeSink) *Manager {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store Close: %v", err)
		}
	})
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH")},
		Cap:      limit,
		Writer:   writer,
		Now:      fixedClock,
		Notifier: sink,
		Store:    s,
	})
	t.Cleanup(m.Close)
	return m
}

// fakeArgs returns the argument line of each process the fake claude started in dir, in start order.
func fakeArgs(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "fakeclaude.args"))
	if err != nil {
		t.Fatalf("read fake claude args: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestSayResumesStoppedSession(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := m.Say(t.Context(), id, "again"); err != nil {
		t.Fatalf("Say of a stopped session: %v", err)
	}
	history := waitHistory(t, path, id, "the resumed reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	if got := conversationOf(t, history); !reflect.DeepEqual(got,
		[]string{"prompt: hi", "reply: fake reply 1", "prompt: again", "reply: fake reply 2"}) {
		t.Fatalf("conversation after resume = %q", got)
	}
	args := fakeArgs(t, dir)
	if len(args) != 2 || !strings.Contains(args[1], "--resume session_1") {
		t.Fatalf("fake claude starts = %q, want a second start resuming session_1", args)
	}
}

func TestSecondManagerResumesSession(t *testing.T) {
	first, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := first.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	first.Close()

	second := newManagerOn(t, path, 1, fakeClaudePath(t), &fakeSink{})
	if err := second.Say(t.Context(), id, "again"); err != nil {
		t.Fatalf("Say on a second manager: %v", err)
	}
	waitHistory(t, path, id, "the resumed reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	if err := second.Seen(t.Context(), id); err != nil {
		t.Fatalf("Seen on a second manager: %v", err)
	}
	want := strconv.FormatInt(fixedClock().Unix(), 10)
	seen := false
	for _, fields := range changedFields(t, historyOf(t, path, id)) {
		if fields["seen"] == want {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("no engine_changed sets seen to %s on a session the first manager started", want)
	}
}

func TestSayAtCapRefusesAndStoresNothing(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	stoppedDir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: stoppedDir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	before := historyOf(t, path, id)

	other, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hold the slot"})
	if err != nil {
		t.Fatalf("Start of the session that fills the cap: %v", err)
	}
	waitHistory(t, path, other, "the cap-filling reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	if err := m.Say(t.Context(), id, "again"); !errors.Is(err, ErrCapReached) {
		t.Fatalf("Say at the cap error = %v, want ErrCapReached", err)
	}
	if after := historyOf(t, path, id); len(after.Events) != len(before.Events) {
		t.Fatalf("events of the stopped session at the cap = %d, want %d unchanged",
			len(after.Events), len(before.Events))
	}
	if args := fakeArgs(t, stoppedDir); len(args) != 1 {
		t.Fatalf("fake claude starts for the stopped session = %q, want only its first start", args)
	}
}

func TestSayToBadDirectoryRecordsFailed(t *testing.T) {
	cases := []struct {
		name    string
		replace func(t *testing.T, dir string)
	}{
		{"missing", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.RemoveAll(dir); err != nil {
				t.Fatalf("remove session dir: %v", err)
			}
		}},
		{"not a directory", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.RemoveAll(dir); err != nil {
				t.Fatalf("remove session dir: %v", err)
			}
			if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
				t.Fatalf("write session path as a file: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, path := openStore(t)
			sink := &fakeSink{}
			m := newManagerOn(t, path, 1, fakeClaudePath(t), sink)
			dir := t.TempDir()
			id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
				return hasBlock(t, h, "fake reply 1")
			})
			if err := m.Stop(t.Context(), id); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			tc.replace(t, dir)

			if err := m.Say(t.Context(), id, "again"); !errors.Is(err, ErrBadDir) {
				t.Fatalf("Say to a bad directory error = %v, want ErrBadDir", err)
			}
			history := historyOf(t, path, id)
			if history.State != "failed" {
				t.Fatalf("state after Say to a bad directory = %q, want failed", history.State)
			}
			reasoned := false
			for _, fields := range changedFields(t, history) {
				if fields["state"] == "failed" && fields["reason"] != "" {
					reasoned = true
				}
			}
			if !reasoned {
				t.Fatalf("no engine_changed records failed with a reason in %+v", changedFields(t, history))
			}
			notified := false
			for _, notice := range sink.got() {
				if notice.Session == id && notice.State == "failed" && notice.Body != "" {
					notified = true
				}
			}
			if !notified {
				t.Fatalf("notices = %+v, want one that reports session %s failed", sink.got(), id)
			}
		})
	}
}

func TestConcurrentSaysStartOneProcess(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	const prompts = 5
	errs := make(chan error, prompts)
	var wg sync.WaitGroup
	for i := range prompts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- m.Say(t.Context(), id, fmt.Sprintf("again %d", i))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Say: %v", err)
		}
	}

	waitHistory(t, path, id, "a reply to every concurrent prompt", func(h store.EngineHistory) bool {
		for k := 2; k <= prompts+1; k++ {
			if !hasBlock(t, h, fmt.Sprintf("fake reply %d", k)) {
				return false
			}
		}
		return true
	})
	if args := fakeArgs(t, dir); len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want one resume for all concurrent Says", args)
	}
}
