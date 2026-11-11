package enginehost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// chosenID is a valid client-chosen session id: 32 lowercase hex digits.
const chosenID = "0123456789abcdef0123456789abcdef"

// chosenHistory returns the engine history of chosenID as the store holds it now.
func chosenHistory(t *testing.T, path string) store.EngineHistory {
	t.Helper()
	h, err := store.NewReader(path).EngineHistory(t.Context(), chosenID, 0, 10_000)
	if err != nil {
		t.Fatalf("read history of %s: %v", chosenID, err)
	}
	return h
}

// countKind returns how many events of history have the given kind.
func countKind(history store.EngineHistory, kind string) int {
	n := 0
	for _, event := range history.Events {
		if event.Kind == kind {
			n++
		}
	}
	return n
}

func TestStartWithChosenIDStoresRowUnderThatID(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()

	id, err := m.Start(t.Context(), StartRequest{ID: chosenID, Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id != chosenID {
		t.Fatalf("Start id = %q, want the chosen %q", id, chosenID)
	}
	waitHistory(t, path, chosenID, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if n := countRows(t, path, "sessions"); n != 1 {
		t.Fatalf("sessions rows = %d, want 1", n)
	}
}

func TestStartWithKnownIDReturnsItWithoutEffect(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()

	if _, err := m.Start(t.Context(), StartRequest{ID: chosenID, Dir: dir, Prompt: "hi"}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	// Wait for the first turn to end, not just its reply block: the turn's end and its state change are recorded
	// after the block, and counting them against the repeat below made this test fail under load.
	waitHistory(t, path, chosenID, "the first turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == "done"
	})

	// The session is live here, and the cap of one is full, so a start that did not return early would fail.
	beforeLive := chosenHistory(t, path)
	beforeArgs := fakeArgs(t, dir)
	id, err := m.Start(t.Context(), StartRequest{ID: chosenID, Dir: dir, Prompt: "again"})
	if err != nil {
		t.Fatalf("Start of a live id: %v", err)
	}
	if id != chosenID {
		t.Fatalf("Start of a live id returned %q, want %q", id, chosenID)
	}
	if got := fakeArgs(t, dir); len(got) != len(beforeArgs) {
		t.Fatalf("fake claude starts = %q after a repeat of a live id, want %q", got, beforeArgs)
	}
	if got := chosenHistory(t, path); len(got.Events) != len(beforeLive.Events) {
		t.Fatalf("events = %d after a repeat of a live id, want %d", len(got.Events), len(beforeLive.Events))
	}

	if err := m.Stop(t.Context(), chosenID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	beforeStopped := chosenHistory(t, path)
	id, err = m.Start(t.Context(), StartRequest{ID: chosenID, Dir: dir, Prompt: "after stop"})
	if err != nil {
		t.Fatalf("Start of a stopped id: %v", err)
	}
	if id != chosenID {
		t.Fatalf("Start of a stopped id returned %q, want %q", id, chosenID)
	}
	if got := fakeArgs(t, dir); len(got) != len(beforeArgs) {
		t.Fatalf("fake claude starts = %q after a repeat of a stopped id, want %q", got, beforeArgs)
	}
	after := chosenHistory(t, path)
	if len(after.Events) != len(beforeStopped.Events) {
		t.Fatalf("events = %d after a repeat of a stopped id, want %d", len(after.Events), len(beforeStopped.Events))
	}
	if n := countKind(after, store.EventEnginePrompt); n != 1 {
		t.Fatalf("engine_prompt events = %d, want 1 (the first start's prompt only)", n)
	}
	if n := countRows(t, path, "sessions"); n != 1 {
		t.Fatalf("sessions rows = %d, want 1", n)
	}
}

func TestStartRefusesBadIDBeforeStoringOrSpawning(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	cases := []struct {
		name string
		id   string
	}{
		{name: "uppercase", id: "0123456789ABCDEF0123456789ABCDEF"},
		{name: "31 digits", id: strings.Repeat("a", 31)},
		{name: "33 digits", id: strings.Repeat("a", 33)},
		{name: "non-hex", id: strings.Repeat("g", 32)},
		{name: "dashed uuid", id: "01234567-89ab-cdef-0123-456789abcdef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			id, err := m.Start(t.Context(), StartRequest{ID: tc.id, Dir: dir, Prompt: "hi"})
			if !errors.Is(err, ErrBadID) {
				t.Fatalf("Start error = %v, want %v", err, ErrBadID)
			}
			if id != "" {
				t.Fatalf("refused Start returned id %q, want none", id)
			}
			if _, err := os.Stat(filepath.Join(dir, "fakeclaude.args")); !os.IsNotExist(err) {
				t.Fatalf("refused Start spawned claude: stat = %v", err)
			}
		})
	}
	if n := countRows(t, path, "sessions"); n != 0 {
		t.Fatalf("sessions rows = %d, want 0", n)
	}
	if n := countRows(t, path, "events"); n != 0 {
		t.Fatalf("events rows = %d, want 0", n)
	}
}

func TestConcurrentStartsWithOneIDStoreOneRow(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	const starts = 8
	ids := make([]string, starts)
	errs := make([]error, starts)
	var wg sync.WaitGroup
	for i := range starts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = m.Start(t.Context(), StartRequest{ID: chosenID, Dir: dir, Prompt: "hi"})
		}()
	}
	wg.Wait()

	for i := range starts {
		if errs[i] != nil {
			t.Fatalf("concurrent Start %d: %v", i, errs[i])
		}
		if ids[i] != chosenID {
			t.Fatalf("concurrent Start %d returned %q, want %q", i, ids[i], chosenID)
		}
	}
	if n := countRows(t, path, "sessions"); n != 1 {
		t.Fatalf("sessions rows = %d, want 1", n)
	}
	history := waitHistory(t, path, chosenID, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if n := countKind(history, store.EventEnginePrompt); n != 1 {
		t.Fatalf("engine_prompt events = %d, want 1", n)
	}
	if got := fakeArgs(t, dir); len(got) != 1 {
		t.Fatalf("fake claude starts = %q, want one", got)
	}
}
