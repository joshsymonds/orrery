package enginehost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// hasFlagValue reports whether the argument line args holds flag immediately followed by value.
func hasFlagValue(args, flag, value string) bool {
	fields := strings.Fields(args)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == flag && fields[i+1] == value {
			return true
		}
	}
	return false
}

func TestStartPassesModelToClaudeAndResumeOmitsIt(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi", Model: "haiku"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say of a stopped session: %v", err)
	}
	waitHistory(t, path, id, "the resumed reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})

	args := fakeArgs(t, dir)
	if len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want two", args)
	}
	if !hasFlagValue(args[0], "--model", "haiku") {
		t.Fatalf("first start args = %q, want --model haiku", args[0])
	}
	if strings.Contains(args[1], "--model") {
		t.Fatalf("resumed start args = %q, want no --model", args[1])
	}
}

func TestStartRefusesBadModelBeforeStoringOrSpawning(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	cases := []struct {
		name  string
		model string
	}{
		{name: "leading dash", model: "-x"},
		{name: "space", model: "has space"},
		{name: "shell metacharacter", model: "a;b"},
		{name: "65 bytes", model: strings.Repeat("a", 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi", Model: tc.model})
			if !errors.Is(err, ErrBadModel) {
				t.Fatalf("Start error = %v, want %v", err, ErrBadModel)
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
	if _, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi", Model: "haiku"}); err != nil {
		t.Fatalf("Start after refusals = %v, want the cap slot still free", err)
	}
}

func TestCheckModelAcceptsAliasesAndRefusesOthers(t *testing.T) {
	accepted := []string{
		"",
		"haiku",
		"claude-opus-4-5",
		"a.b_c-d[1m]:beta",
		"[x]",
		strings.Repeat("a", 64),
	}
	for _, model := range accepted {
		if err := checkModel(model); err != nil {
			t.Errorf("checkModel(%q) = %v, want nil", model, err)
		}
	}
	refused := []string{
		"-x",
		"has space",
		"a;b",
		"a/b",
		"café",
		"a\nb",
		strings.Repeat("a", 65),
	}
	for _, model := range refused {
		if err := checkModel(model); !errors.Is(err, ErrBadModel) {
			t.Errorf("checkModel(%q) = %v, want %v", model, err, ErrBadModel)
		}
	}
}
