package open

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/archive"
)

func TestChoicesExactSessionIsOneJumpToActivePane(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "example-session-a", t.TempDir())
	pane := activePane(t, client, "example-session-a")
	cfg := intentConfig(client, installClaude(t, "", 0), t.TempDir())

	got, err := Choices(t.Context(), cfg, "example-session-a")
	if err != nil {
		t.Fatalf("Choices: %v", err)
	}
	if want := []Action{{Kind: ActionJump, Pane: pane}}; !slices.Equal(got, want) {
		t.Errorf("Choices() = %+v, want %+v", got, want)
	}
}

func TestChoicesOneWordKnownDirWithoutSessionGivesResumeThenFresh(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "example-proj")
	mkdirAll(t, proj)
	archivePath := resumeArchive(t, archive.Record{
		Dir: proj, ClaudeSession: "sess-a", Transcript: resumeTranscript(t, "a.jsonl"),
		Summary: "summary a", Closed: 100,
	})
	cfg := Config{
		Tmux:    newClient(t),
		Claude:  installClaude(t, "", 0),
		Archive: archivePath,
	}

	got, err := Choices(t.Context(), cfg, "example-proj")
	if err != nil {
		t.Fatalf("Choices: %v", err)
	}
	want := []Action{
		{Kind: ActionResume, Dir: proj, Session: "sess-a", Note: "summary a"},
		{Kind: ActionFresh, Dir: proj},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Choices() = %+v, want %+v", got, want)
	}
}

func TestAmbigChoicesGiveOnePaneRowPerDirInKnownOrder(t *testing.T) {
	client := newClient(t)
	first, second, cfg := ambiguousDirs(t, client)

	got, err := Choices(t.Context(), cfg, "proj")
	if err != nil {
		t.Fatalf("Choices(proj): %v", err)
	}
	want := []Action{{Kind: ActionPane, Dir: first}, {Kind: ActionPane, Dir: second}}
	if !slices.Equal(got, want) {
		t.Errorf("Choices(proj) = %+v, want %+v", got, want)
	}
}

func TestAmbigChoicesWithNoKnownDirAreIntentText(t *testing.T) {
	client := newClient(t)
	claude := installClaude(t, "task: no\n", 0)
	cfg := intentConfig(client, claude, t.TempDir())

	if _, err := Choices(t.Context(), cfg, "zz"); err != nil {
		t.Fatalf("Choices(zz): %v", err)
	}
	runs := claudeRuns(t, claude)
	if len(runs) != 1 {
		t.Fatalf("claude ran %d times, want 1", len(runs))
	}
	if !slices.Contains(strings.Split(runs[0].stdin, "\n"), "zz") {
		t.Errorf("stdin has no TEXT line %q: %q", "zz", runs[0].stdin)
	}
}

func TestChoicesMultiWordGivesIntentRowsWithRootLast(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	makeDirs(t, root, 1)
	pane := startDefaultPane(t, client, "example-session-a", "waiting", "example-label-a", "example summary a")
	claude := installClaude(t, "jump "+pane+"\npane d1\ntask: no\n", 0)
	cfg := intentConfig(client, claude, root)

	got, err := Choices(t.Context(), cfg, "  example   text ")
	if err != nil {
		t.Fatalf("Choices: %v", err)
	}
	want := []Action{
		{Kind: ActionJump, Pane: pane},
		{Kind: ActionPane, Dir: filepath.Join(root, "dir-01")},
		{Kind: ActionPane, Dir: root},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Choices() = %+v, want %+v", got, want)
	}
	runs := claudeRuns(t, claude)
	if len(runs) != 1 {
		t.Fatalf("claude ran %d times, want 1", len(runs))
	}
	if !slices.Contains(strings.Split(runs[0].stdin, "\n"), "example text") {
		t.Errorf("stdin has no TEXT line %q with words joined by single spaces: %q", "example text", runs[0].stdin)
	}
}

func TestChoicesNewAndDotReachModelAsTextAndCreateNothing(t *testing.T) {
	for _, text := range []string{"new foo", "."} {
		t.Run(text, func(t *testing.T) {
			client := newClient(t)
			startSession(t, client, "example-session-a", t.TempDir())
			root := t.TempDir()
			makeDirs(t, root, 1)
			dir := filepath.Join(root, "dir-01")
			claude := installClaude(t, "pane d1\n", 0)
			cfg := intentConfig(client, claude, root)
			before := sessionNames(t, client)

			got, err := Choices(t.Context(), cfg, text)
			if err != nil {
				t.Fatalf("Choices(%q): %v", text, err)
			}
			if want := []Action{{Kind: ActionPane, Dir: dir}, {Kind: ActionPane, Dir: root}}; !slices.Equal(got, want) {
				t.Errorf("Choices(%q) = %+v, want %+v", text, got, want)
			}
			runs := claudeRuns(t, claude)
			if len(runs) != 1 {
				t.Fatalf("claude ran %d times, want 1", len(runs))
			}
			if !slices.Contains(strings.Split(runs[0].stdin, "\n"), text) {
				t.Errorf("stdin has no TEXT line %q: %q", text, runs[0].stdin)
			}
			if _, err := os.Stat(filepath.Join(root, "foo")); !os.IsNotExist(err) {
				t.Errorf("stat %s: err %v, want not exist", filepath.Join(root, "foo"), err)
			}
			if after := sessionNames(t, client); !slices.Equal(after, before) {
				t.Errorf("sessions after Choices = %q, want %q", after, before)
			}
		})
	}
}

func TestChoicesBlankTextIsErrorWithNothingRun(t *testing.T) {
	for _, text := range []string{"", " ", "\t \n"} {
		t.Run(fmt.Sprintf("%q", text), func(t *testing.T) {
			claude := installClaude(t, "pane d1\n", 0)
			// A nil client panics if the call reaches tmux, which fails the test.
			cfg := Config{Claude: claude, Root: "/example"}

			if _, err := Choices(t.Context(), cfg, text); err == nil {
				t.Fatalf("Choices(%q) returned nil error", text)
			}
			if runs := claudeRuns(t, claude); len(runs) != 0 {
				t.Errorf("claude ran %d times, want 0", len(runs))
			}
		})
	}
}
