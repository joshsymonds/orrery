package enginehost

import (
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

// subdirOf creates the directory name inside the repository of env and returns its path.
func subdirOf(t *testing.T, env moveEnv, name string) string {
	t.Helper()
	dir := filepath.Join(env.repo, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	return dir
}

// TestSteerProjectRefusesTargetThatIsNoConversation moves the epic to a session that is no row, a step and a pane with
// no Claude. Each is refused by name, and the epic keeps its steerer.
func TestSteerProjectRefusesTargetThatIsNoConversation(t *testing.T) {
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

// TestSteerProjectAcceptsAConversationAnywhere moves the epic to a conversation in a subdirectory of its repository, in
// a linked worktree of it, and in a directory outside it, headless or a pane. A steerer answers the epic's questions and
// changes nothing in its repository, so each move is accepted and is the epic's one move.
func TestSteerProjectAcceptsAConversationAnywhere(t *testing.T) {
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
		{name: "headless directory outside", target: func(t *testing.T, env moveEnv) (string, string) {
			t.Helper()
			return startedIn(t, env.m, t.TempDir()), ""
		}},
		{name: "pane", target: func(t *testing.T, env moveEnv) (string, string) {
			t.Helper()
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
