package enginehost

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// requireNothingSent checks that session id was sent no prompt and that its row is still state. A Resume that went on to
// Say would store its prompt and the working state before returning, so both checks hold as soon as the call returns.
func requireNothingSent(t *testing.T, s *store.Store, path, id, state string) {
	t.Helper()
	row, err := s.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("EngineSession %s: %v", id, err)
	}
	if row.State != state {
		t.Fatalf("state of session %s after the refused resume = %q, want %q", id, row.State, state)
	}
	if texts := promptTexts(t, path, id); len(texts) != 0 {
		t.Fatalf("prompts of session %s after the refused resume = %q, want none", id, texts)
	}
}

func TestResumeSendsResumePromptToInterruptedSession(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	dir := t.TempDir()
	seedClaudeRow(t, s, "interrupted", dir, "interrupted", store.KindEngine)
	stallNextPrompt(t, dir)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	m := newReconcileManager(t, s, writer, &fakeSink{})

	if err := m.Resume(t.Context(), "interrupted"); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	row, err := s.EngineSession(t.Context(), "interrupted")
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "working" {
		t.Fatalf("state after Resume = %q, want working", row.State)
	}
	if texts := promptTexts(t, path, "interrupted"); !slices.Equal(texts, []string{"Resume working."}) {
		t.Fatalf("prompts after Resume = %q, want [Resume working.]", texts)
	}
	waittest.Until(t, nil, "the fake claude to start with --resume session_1", func() bool {
		starts := fakeStarts(t, dir)
		return len(starts) > 0 && strings.Contains(starts[len(starts)-1], "--resume session_1")
	})
	waittest.Until(t, nil, "the fake claude to read Resume working.", func() bool {
		return stdinLinesWith(t, dir, "Resume working.") == 1
	})
}

func TestResumeRefusesRowNotInterrupted(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	dir := t.TempDir()
	seedClaudeRow(t, s, "done", dir, "done", store.KindEngine)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	m := newReconcileManager(t, s, writer, &fakeSink{})

	err := m.Resume(t.Context(), "done")
	if !errors.Is(err, ErrNotResumable) {
		t.Fatalf("Resume of a done session = %v, want ErrNotResumable", err)
	}
	if !strings.Contains(err.Error(), "state is done") {
		t.Fatalf("Resume refusal %q does not say the state is done", err)
	}
	requireNothingSent(t, s, path, "done", "done")
}

func TestResumeRefusesInterruptedRowWithoutClaudeSession(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	dir := t.TempDir()
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: "noclaude", Dir: dir, State: "interrupted", Kind: store.KindEngine},
	}, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed engine row: %v", err)
	}
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	m := newReconcileManager(t, s, writer, &fakeSink{})

	err := m.Resume(t.Context(), "noclaude")
	if !errors.Is(err, ErrNotResumable) {
		t.Fatalf("Resume of a session with no Claude session = %v, want ErrNotResumable", err)
	}
	if !strings.Contains(err.Error(), "no Claude session") {
		t.Fatalf("Resume refusal %q does not say the session has no Claude session", err)
	}
	requireNothingSent(t, s, path, "noclaude", "interrupted")
}

func TestResumeUnknownIDIsNoSession(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t, 1, fakeClaudePath(t))

	if err := m.Resume(t.Context(), "missing"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Resume of an unknown id = %v, want ErrNoSession", err)
	}
}
