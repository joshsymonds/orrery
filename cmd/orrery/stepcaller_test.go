package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// stepCallerAt is the time the rows the step caller test stores are written at.
func stepCallerAt() time.Time { return time.Unix(1700003000, 0) }

// stepCallerSchema is the schema of the step the caller test starts.
const stepCallerSchema = `{"type":"object","properties":{"status":{"type":"string"}}}`

// stepCallerSessions is the sessions behind the test steps. StartStep commits the step's engine row and its ops
// through writer, as enginehost's own test fake does. The other methods do nothing.
type stepCallerSessions struct {
	writer *enginehost.Writer
}

func (f stepCallerSessions) StartStep(ctx context.Context, step enginehost.StepSession) error {
	ops := append([]store.EngineOp{
		store.EngineAdd{ID: step.ID, Dir: step.Dir, State: "working", Kind: store.KindStep},
	}, step.Ops...)
	return f.writer.Commit(ctx, step.ID, ops...)
}

func (stepCallerSessions) Interrupt(context.Context, string) error { return nil }

func (stepCallerSessions) Continue(context.Context, string, enginehost.StepPrompt) error { return nil }

func (stepCallerSessions) Release(context.Context, string) error { return nil }

func (stepCallerSessions) Stop(context.Context, string) error { return nil }

// startStepCallerSteps serves a real Steps over a store opened in a temporary directory on the session socket, and
// returns the daemon socket path that `--socket` names along with the store. The shutdown runs in this order: the
// server, then the steps, then the writer, then the store.
func startStepCallerSteps(t *testing.T) (string, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	writer := enginehost.NewWriter(enginehost.WriterConfig{Applier: s})
	writerCtx, cancelWriter := context.WithCancel(context.Background())
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writer.Run(writerCtx)
	}()
	t.Cleanup(func() {
		cancelWriter()
		<-writerDone
	})

	steps := enginehost.NewSteps(enginehost.StepsConfig{
		Sessions:  stepCallerSessions{writer: writer},
		Store:     s,
		Writer:    writer,
		Worktrees: t.TempDir(),
	})
	t.Cleanup(steps.Close)
	return startMCPDaemon(t, steps), s
}

// stepCallerRepo returns a temporary git repository with one commit, the workspace the step starts in.
func stepCallerRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stepCallerGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("base\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	stepCallerGit(t, dir, "add", "README")
	stepCallerGit(t, dir, "commit", "-q", "-m", "base")
	return dir
}

// stepCallerGit runs git in dir with a test identity and no global config, and fails the test on error.
func stepCallerGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// stepCallerStepID returns the id that step_start's result names.
func stepCallerStepID(t *testing.T, result json.RawMessage) string {
	t.Helper()
	var called struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(result, &called); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if called.IsError || len(called.Content) != 1 {
		t.Fatalf("step_start result = %s, want one content block without error", result)
	}
	var started struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(called.Content[0].Text), &started); err != nil {
		t.Fatalf("decode step_start text %q: %v", called.Content[0].Text, err)
	}
	if started.ID == "" {
		t.Fatalf("step_start result = %s, want a step id", result)
	}
	return started.ID
}

// TestStepCallerMCPStepStartRecordsTheRowOfItsCaller runs `orrery --socket S mcp` in each caller's environment, calls
// step_start over MCP against the real step service, and checks that the store records as the step's caller the row
// of the session that made the call.
func TestStepCallerMCPStepStartRecordsTheRowOfItsCaller(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// setup stores the row the environment names and returns the environment, and the id of the row the step's
		// caller must be.
		setup func(t *testing.T, s *store.Store) (map[string]string, string)
	}{
		{
			name: "orrery session names its headless row",
			setup: func(t *testing.T, s *store.Store) (map[string]string, string) {
				t.Helper()
				engine := store.EngineAdd{ID: "session-7", Dir: "/work", State: "idle", Kind: store.KindEngine}
				if err := s.ApplyEngine(t.Context(), []store.EngineOp{engine}, stepCallerAt()); err != nil {
					t.Fatalf("ApplyEngine: %v", err)
				}
				return map[string]string{protocol.EnvSession: "session-7"}, "session-7"
			},
		},
		{
			name: "tmux pane names its pane row",
			setup: func(t *testing.T, s *store.Store) (map[string]string, string) {
				t.Helper()
				pane := store.Pane{ID: "orrery-1", Pane: "%3"}
				if err := s.SyncPane(t.Context(), "-S /tmp/tmux-1000/orrery-test", pane, stepCallerAt()); err != nil {
					t.Fatalf("SyncPane: %v", err)
				}
				return map[string]string{"TMUX": "/tmp/tmux-1000/orrery-test,4242,0", "TMUX_PANE": "%3"}, "orrery-1"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			socket, s := startStepCallerSteps(t)
			env, want := tc.setup(t, s)
			args, err := json.Marshal(map[string]any{
				"agent":     "reviewer",
				"prompt":    "check the change",
				"workspace": stepCallerRepo(t),
				"readOnly":  true,
				"schema":    json.RawMessage(stepCallerSchema),
			})
			if err != nil {
				t.Fatalf("encode step_start arguments: %v", err)
			}
			cmd := startCallerRun(t, envFrom(env), "--socket", socket, "mcp")

			cmd.send(mcpInitialize)
			requireResult(t, cmd.receive(), 1)
			cmd.send(mcpInitialized)
			cmd.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"step_start","arguments":` +
				string(args) + `}}`)
			id := stepCallerStepID(t, requireResult(t, cmd.receive(), 2))

			if code := cmd.finish(); code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr %q", code, cmd.stderr.String())
			}
			step, err := s.Step(t.Context(), id)
			if err != nil {
				t.Fatalf("Step(%s): %v", id, err)
			}
			if step.Caller != want {
				t.Fatalf("stored caller of step %s = %q, want %q", id, step.Caller, want)
			}
		})
	}
}
