package enginehost

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/store"
)

// projectCase is one target a move of the test epic names. target prepares the target in env and returns the session the
// move names and the refusal it gets, which is "" when the move is accepted.
type projectCase struct {
	name   string
	target func(t *testing.T, env moveEnv) (session, refusal string)
}

// outsideRefusal is the refusal of a move of the test epic to session, a target whose directory dir lies outside the
// epic's repository.
func outsideRefusal(session, dir string) string {
	return fmt.Sprintf("session %s runs in %s, outside the repository of epic %s", session, dir, epicServiceID)
}

// subdirOf creates the directory name inside the repository of env and returns its path.
func subdirOf(t *testing.T, env moveEnv, name string) string {
	t.Helper()
	dir := filepath.Join(env.repo, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	return dir
}

func TestSteerProjectRefusesTargetThatIsNoConversationOrIsOutsideRepository(t *testing.T) {
	t.Parallel()
	cases := []projectCase{
		{name: "no session row", target: func(_ *testing.T, _ moveEnv) (string, string) {
			return "session-missing", "session session-missing is not a session"
		}},
		{name: "step session", target: func(t *testing.T, env moveEnv) (string, string) {
			t.Helper()
			step := store.EngineAdd{ID: "step-1", Dir: env.repo, State: "idle", Kind: store.KindStep}
			if err := env.st.ApplyEngine(t.Context(), []store.EngineOp{step}, fixedClock()); err != nil {
				t.Fatalf("ApplyEngine: %v", err)
			}
			return "step-1", "session step-1 is a step, not a conversation"
		}},
		{name: "pane with empty state", target: func(t *testing.T, env moveEnv) (string, string) {
			t.Helper()
			pane := env.syncPane(t, "orrery-empty", "%3", "")
			return pane, "session orrery-empty is a pane with no Claude"
		}},
		{name: "headless directory outside", target: func(t *testing.T, env moveEnv) (string, string) {
			t.Helper()
			dir := t.TempDir()
			session := startedIn(t, env.m, dir)
			return session, outsideRefusal(session, dir)
		}},
		{name: "pane path outside", target: func(t *testing.T, env moveEnv) (string, string) {
			t.Helper()
			dir := t.TempDir()
			env.panes.dir = dir
			pane := env.syncPane(t, "orrery-pane", "%5", "working")
			return pane, outsideRefusal(pane, dir)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			env := newMoveEnv(t)
			session, refusal := c.target(t, env)

			err := env.move(t, session, nil)
			wantConnectError(t, err, connect.CodeFailedPrecondition, refusal)
			env.wantUnmoved(t)
		})
	}
}

// TestSteerProjectEmptyPanePathIsRefusedNotReadFromWorkingDirectory runs in the epic's repository, so a git read of an
// empty pane path that fell back to the process directory would find the repository and accept the move. It is not
// parallel, since t.Chdir changes the process directory.
func TestSteerProjectEmptyPanePathIsRefusedNotReadFromWorkingDirectory(t *testing.T) {
	env := newMoveEnv(t)
	t.Chdir(env.repo)
	pane := env.syncPane(t, "orrery-pane", "%5", "working")

	err := env.move(t, pane, nil)
	wantConnectError(t, err, connect.CodeFailedPrecondition, outsideRefusal(pane, ""))
	env.wantUnmoved(t)
}

func TestSteerProjectAcceptsTargetInsideRepository(t *testing.T) {
	t.Parallel()
	cases := []projectCase{
		{name: "headless subdirectory", target: func(t *testing.T, env moveEnv) (string, string) {
			t.Helper()
			return startedIn(t, env.m, subdirOf(t, env, "sub")), ""
		}},
		{name: "headless linked worktree", target: func(t *testing.T, env moveEnv) (string, string) {
			t.Helper()
			linked := filepath.Join(t.TempDir(), "linked")
			stepGitTestRun(t, env.repo, "worktree", "add", "-q", "--detach", linked)
			return startedIn(t, env.m, linked), ""
		}},
		{name: "pane path inside", target: func(t *testing.T, env moveEnv) (string, string) {
			t.Helper()
			env.panes.dir = subdirOf(t, env, "pane")
			return env.syncPane(t, "orrery-pane", "%5", "working"), ""
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			env := newMoveEnv(t)
			session, _ := c.target(t, env)

			if err := env.move(t, session, nil); err != nil {
				t.Fatalf("MoveEpic: %v", err)
			}
			env.wantSteerer(t, session)
			env.wantOnlyMove(t, store.Maker{By: store.MakerOwner}, env.steerer, session)
		})
	}
}
