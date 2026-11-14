package planner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// scratchEpic returns an epic whose repository is a new git repository under a new HOME, with epicGates as its gates,
// and the environ that names that HOME. Claude Code reads a project's agents only below HOME, so the repository sits
// there.
func scratchEpic(t *testing.T, epicGates ...store.EpicGate) (store.EpicRow, []string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "src", "app")
	if err = os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, gitErr := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); gitErr != nil {
		t.Fatalf("git init %s: %v: %s", repo, gitErr, out)
	}
	epic := store.EpicRow{ID: "orrery-1234567/demo", Repository: repo, Gates: epicGates}
	return epic, []string{"HOME=" + home}
}

// commandGate is an epic gate that Define accepts as a command gate.
func commandGate(name string) store.EpicGate {
	return store.EpicGate{
		Name:  name,
		Argv:  []string{"/opt/tools/run", "-c", "check"},
		Limit: 60,
		Needs: []store.EpicGateNeed{{Pool: "gate", Count: 1}},
	}
}

// projectLint is a project file that defines lint and no epic gate does.
const projectLint = "[gates]\n[gates.lint]\nargv = [\"/opt/tools/lint\"]\nlimit = 60\n"

// agentDefinition is an agent file that defines an agent named name.
func agentDefinition(name string) string {
	return "---\nname: " + name + "\ndescription: Reviews code\n---\n"
}

// writeScratchFile writes content to path, creating its directory.
func writeScratchFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolverGateResolvesEpicGate(t *testing.T) {
	t.Parallel()
	epic, environ := scratchEpic(t, commandGate("check"))
	if err := NewResolver(environ, "").Gate(context.Background(), epic, "check"); err != nil {
		t.Fatalf("Gate(check) = %v, want nil", err)
	}
}

func TestResolverGateResolvesGateOnlyProjectFileDefines(t *testing.T) {
	t.Parallel()
	epic, environ := scratchEpic(t)
	writeScratchFile(t, filepath.Join(epic.Repository, ".orrery", "project.toml"), projectLint)
	if err := NewResolver(environ, "").Gate(context.Background(), epic, "lint"); err != nil {
		t.Fatalf("Gate(lint) = %v, want nil", err)
	}
}

func TestResolverGateUnknownWrapsErrUnknown(t *testing.T) {
	t.Parallel()
	epic, environ := scratchEpic(t, commandGate("check"))
	err := NewResolver(environ, "").Gate(context.Background(), epic, "missing")
	if !errors.Is(err, gates.ErrUnknown) {
		t.Fatalf("Gate(missing) = %v, want an error wrapping gates.ErrUnknown", err)
	}
}

func TestResolverGateRefusesAgentGate(t *testing.T) {
	t.Parallel()
	review := store.EpicGate{Name: "review", Agent: "reviewer", Limit: 60, Must: []string{"the diff is correct"}}
	epic, environ := scratchEpic(t, review)
	err := NewResolver(environ, "").Gate(context.Background(), epic, "review")
	const want = `gate "review" is an agent gate, not a command gate`
	if err == nil || err.Error() != want {
		t.Fatalf("Gate(review) = %v, want %q", err, want)
	}
}

func TestResolverGateNamesEpicGateThatFailsDefine(t *testing.T) {
	t.Parallel()
	bad := store.EpicGate{Name: "Check", Argv: []string{"/opt/tools/run"}, Limit: 60}
	epic, environ := scratchEpic(t, bad)
	err := NewResolver(environ, "").Gate(context.Background(), epic, "check")
	if err == nil || !strings.Contains(err.Error(), `gate "Check"`) {
		t.Fatalf("Gate(check) = %v, want an error naming the epic gate %q", err, "Check")
	}
}

func TestResolverJudgeResolvesBuiltInAgent(t *testing.T) {
	t.Parallel()
	epic, environ := scratchEpic(t)
	if err := NewResolver(environ, "").Judge(epic, "general-purpose"); err != nil {
		t.Fatalf("Judge(general-purpose) = %v, want nil", err)
	}
}

func TestResolverJudgeResolvesProjectAgent(t *testing.T) {
	t.Parallel()
	epic, environ := scratchEpic(t)
	writeScratchFile(t, filepath.Join(epic.Repository, ".claude", "agents", "reviewer.md"), agentDefinition("reviewer"))
	if err := NewResolver(environ, "").Judge(epic, "reviewer"); err != nil {
		t.Fatalf("Judge(reviewer) = %v, want nil", err)
	}
}

func TestResolverJudgeUnknownWrapsErrUnresolved(t *testing.T) {
	t.Parallel()
	epic, environ := scratchEpic(t)
	err := NewResolver(environ, "").Judge(epic, "nobody")
	if !errors.Is(err, agents.ErrUnresolved) {
		t.Fatalf("Judge(nobody) = %v, want an error wrapping agents.ErrUnresolved", err)
	}
}
