package enginehost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// openTestStore opens a second handle on the store at path, closed when the test ends.
func openTestStore(t *testing.T, path string) *store.Store {
	t.Helper()
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store Close: %v", err)
		}
	})
	return s
}

// fakeStdinUserLines returns how many user lines the fake claude has logged from its stdin in dir.
func fakeStdinUserLines(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
	if err != nil {
		t.Fatalf("read fake stdin log: %v", err)
	}
	n := 0
	for line := range strings.Lines(string(data)) {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			t.Fatalf("fake stdin line is not JSON: %v\n%s", err, line)
		}
		if head.Type == "user" {
			n++
		}
	}
	return n
}

func TestStartTracksRowAtZeroAndStoresPromptWithItsWriteID(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := openTestStore(t, path)
	dir := t.TempDir()

	if _, err := m.Start(t.Context(), StartRequest{ID: chosenID, Dir: dir, Prompt: "hi"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	row, err := s.EngineSession(t.Context(), chosenID)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if !row.Tracked || row.Consumed != 0 || row.ReplayFrom != 0 {
		t.Fatalf("row = %+v, want tracked at offset 0", row)
	}
	prompts, err := s.HolderPrompts(t.Context(), chosenID)
	if err != nil {
		t.Fatalf("HolderPrompts: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Text != "hi" || prompts[0].WriteID == "" {
		t.Fatalf("holder prompts = %+v, want one prompt \"hi\" with a write id", prompts)
	}
	history := waitHistory(t, path, chosenID, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if n := countKind(history, store.EventEnginePrompt); n != 1 {
		t.Fatalf("engine_prompt events = %d, want 1", n)
	}
	if n := fakeStdinUserLines(t, dir); n != 1 {
		t.Fatalf("fake claude stdin user lines = %d, want the prompt once", n)
	}
}

func TestSayStoresEachPromptWithItsOwnWriteID(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := openTestStore(t, path)
	dir := t.TempDir()

	if _, err := m.Start(t.Context(), StartRequest{ID: chosenID, Dir: dir, Prompt: "hi"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, chosenID, "the first turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == "done"
	})
	if err := m.Say(t.Context(), SayRequest{ID: chosenID, Text: "again"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	waitHistory(t, path, chosenID, "the second reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	prompts, err := s.HolderPrompts(t.Context(), chosenID)
	if err != nil {
		t.Fatalf("HolderPrompts: %v", err)
	}
	if len(prompts) != 2 || prompts[0].Text != "hi" || prompts[1].Text != "again" {
		t.Fatalf("holder prompts = %+v, want \"hi\" then \"again\"", prompts)
	}
	if prompts[0].WriteID == "" || prompts[0].WriteID == prompts[1].WriteID {
		t.Fatalf("write ids = %q and %q, want two distinct non-empty ids", prompts[0].WriteID, prompts[1].WriteID)
	}
	if n := fakeStdinUserLines(t, dir); n != 2 {
		t.Fatalf("fake claude stdin user lines = %d, want each prompt once", n)
	}
}

func TestStartStepStoresPromptWithItsWriteIDAndSendsItOnce(t *testing.T) {
	t.Parallel()
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	s := openTestStore(t, path)
	dir := t.TempDir()

	if err := m.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	prompts, err := s.HolderPrompts(t.Context(), stepA)
	if err != nil {
		t.Fatalf("HolderPrompts: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Text != "hi" || prompts[0].WriteID == "" {
		t.Fatalf("holder prompts = %+v, want one prompt \"hi\" with a write id", prompts)
	}
	if n := fakeStdinUserLines(t, dir); n != 1 {
		t.Fatalf("fake claude stdin user lines = %d, want the step prompt once", n)
	}
}
