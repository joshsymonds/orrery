package main

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/donejudge"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Synthetic values only; no test reaches the real claude binary or a real attain folder.
const (
	judgeStampURL     = "http://127.0.0.1:9/ctx/example"
	judgeStampFrame   = "http://127.0.0.1:9/ctx/other"
	judgeStampModel   = "example-model"
	judgeStampOther   = "other-model"
	judgeStampMessage = `{"last_assistant_message":"example final message"}`
	judgeStampReply   = `{"needed":false,"why":"finished"}`
)

// judgeStampModels returns the models of the example and other contexts.
func judgeStampModels() map[string]string {
	return map[string]string{"example": judgeStampModel, "other": judgeStampOther}
}

// judgeStampCall is what one fake CallVia call recorded.
type judgeStampCall struct {
	baseURL, model string
	env            map[string]string
}

// judgeStampCaller is a donejudge.Caller that records its calls and returns a canned verdict.
type judgeStampCaller struct {
	calls []judgeStampCall
}

func (c *judgeStampCaller) CallVia(
	_ context.Context, baseURL, model string, env map[string]string, _, _ string,
) (json.RawMessage, error) {
	c.calls = append(c.calls, judgeStampCall{baseURL: baseURL, model: model, env: env})
	return json.RawMessage(judgeStampReply), nil
}

// judgeStampFolder makes folder under home with a .claude/settings.local.json holding settings, and returns folder.
func judgeStampFolder(t *testing.T, home, folder, settings string) string {
	t.Helper()
	dir := filepath.Join(home, folder, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.local.json"), []byte(settings), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return filepath.Join(home, folder)
}

// judgeStampCwd makes cwd below a stamped folder and returns it.
func judgeStampCwd(t *testing.T, stamped string) string {
	t.Helper()
	cwd := filepath.Join(stamped, "pkg")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatalf("make cwd: %v", err)
	}
	return cwd
}

// judgeStampTransition is a done transition whose frame runs in cwd through the base URL frameURL.
func judgeStampTransition(cwd, frameURL string) daemon.Transition {
	return daemon.Transition{Frame: protocol.HookFrame{
		Hook:             json.RawMessage(judgeStampMessage),
		Cwd:              cwd,
		AnthropicBaseURL: frameURL,
	}}
}

// TestJudgeDoneResolvesFromStampedFolder checks that the done judge asks through the URL, the model and the whole env of
// the context stamp in effect for the frame's cwd, not through the frame's own base URL.
func TestJudgeDoneResolvesFromStampedFolder(t *testing.T) {
	home := t.TempDir()
	stamped := judgeStampFolder(t, home, "work/attain",
		`{"env":{"ANTHROPIC_BASE_URL":"`+judgeStampURL+`","CTX_EXAMPLE":"example"}}`)
	caller := &judgeStampCaller{}
	r := &relay{home: home, judge: donejudge.New(caller, judgeStampModels())}

	if _, err := r.judgeDone(t.Context(), judgeStampTransition(judgeStampCwd(t, stamped), judgeStampFrame))(
		t.Context()); err != nil {
		t.Fatalf("judgeDone: %v", err)
	}

	if len(caller.calls) != 1 {
		t.Fatalf("CallVia calls = %d, want 1", len(caller.calls))
	}
	got := caller.calls[0]
	if got.baseURL != judgeStampURL {
		t.Errorf("baseURL = %q, want the stamp's %q", got.baseURL, judgeStampURL)
	}
	if got.model != judgeStampModel {
		t.Errorf("model = %q, want %q", got.model, judgeStampModel)
	}
	wantEnv := map[string]string{"ANTHROPIC_BASE_URL": judgeStampURL, "CTX_EXAMPLE": "example"}
	if !maps.Equal(got.env, wantEnv) {
		t.Errorf("env = %v, want the stamp's whole env %v", got.env, wantEnv)
	}
}

// TestJudgeDoneWithoutStampUsesFrameURL checks that a cwd with no stamp in effect is judged through the frame's base
// URL, with that URL as the whole env and the model the URL maps to.
func TestJudgeDoneWithoutStampUsesFrameURL(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "work", "plain")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatalf("make cwd: %v", err)
	}
	caller := &judgeStampCaller{}
	r := &relay{home: home, judge: donejudge.New(caller, judgeStampModels())}

	if _, err := r.judgeDone(t.Context(), judgeStampTransition(cwd, judgeStampFrame))(t.Context()); err != nil {
		t.Fatalf("judgeDone: %v", err)
	}

	if len(caller.calls) != 1 {
		t.Fatalf("CallVia calls = %d, want 1", len(caller.calls))
	}
	got := caller.calls[0]
	if got.baseURL != judgeStampFrame {
		t.Errorf("baseURL = %q, want the frame's %q", got.baseURL, judgeStampFrame)
	}
	if got.model != judgeStampOther {
		t.Errorf("model = %q, want %q", got.model, judgeStampOther)
	}
	if wantEnv := map[string]string{"ANTHROPIC_BASE_URL": judgeStampFrame}; !maps.Equal(got.env, wantEnv) {
		t.Errorf("env = %v, want %v", got.env, wantEnv)
	}
}

// TestJudgeDoneWithoutStampOrFrameURLRunsWithNoEnv checks that a cwd with no stamp and a frame with no base URL run
// the judge with no base URL and no env, on the default summary model.
func TestJudgeDoneWithoutStampOrFrameURLRunsWithNoEnv(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "work", "plain")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatalf("make cwd: %v", err)
	}
	caller := &judgeStampCaller{}
	r := &relay{home: home, judge: donejudge.New(caller, judgeStampModels())}

	if _, err := r.judgeDone(t.Context(), judgeStampTransition(cwd, ""))(t.Context()); err != nil {
		t.Fatalf("judgeDone: %v", err)
	}

	if len(caller.calls) != 1 {
		t.Fatalf("CallVia calls = %d, want 1", len(caller.calls))
	}
	got := caller.calls[0]
	if got.baseURL != "" || got.env != nil {
		t.Errorf("baseURL = %q, env = %v, want no base URL and no env", got.baseURL, got.env)
	}
	if got.model != protocol.DefaultSummaryModel {
		t.Errorf("model = %q, want %q", got.model, protocol.DefaultSummaryModel)
	}
}

// TestJudgeDoneUnparseableStampMakesNoCall checks that a stamp the lookup cannot read fails the judge with an error and
// runs no claude call.
func TestJudgeDoneUnparseableStampMakesNoCall(t *testing.T) {
	home := t.TempDir()
	stamped := judgeStampFolder(t, home, "work/attain", `{"env": not json`)
	caller := &judgeStampCaller{}
	r := &relay{home: home, judge: donejudge.New(caller, judgeStampModels())}

	_, err := r.judgeDone(t.Context(), judgeStampTransition(judgeStampCwd(t, stamped), judgeStampFrame))(t.Context())
	if err == nil {
		t.Fatal("judgeDone: want an error for an unparseable stamp, got nil")
	}
	if len(caller.calls) != 0 {
		t.Fatalf("CallVia calls = %d, want none", len(caller.calls))
	}
}

// judgeStampWorktree makes folder under home a linked worktree of the primary checkout at primary: its .git file names a
// gitdir under primary/.git/worktrees, and that gitdir's commondir is a symlink to a missing path. It returns the
// commondir path.
func judgeStampWorktree(t *testing.T, home, primary, folder string) string {
	t.Helper()
	gitdir := filepath.Join(primary, ".git", "worktrees", "linked")
	if err := os.MkdirAll(gitdir, 0o700); err != nil {
		t.Fatalf("make %s: %v", gitdir, err)
	}
	commondir := filepath.Join(gitdir, "commondir")
	if err := os.Symlink(filepath.Join(home, "missing", ".git"), commondir); err != nil {
		t.Fatalf("symlink commondir: %v", err)
	}
	worktree := filepath.Join(home, folder)
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatalf("make worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o600); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	return commondir
}

// TestJudgeDoneDanglingCommondirMakesNoCall checks that a cwd in a linked worktree whose commondir dangles fails the
// judge with an error naming that commondir, and runs no claude call, even though the primary checkout is stamped and
// the frame names a base URL.
func TestJudgeDoneDanglingCommondirMakesNoCall(t *testing.T) {
	home := t.TempDir()
	primary := judgeStampFolder(t, home, "work/primary",
		`{"env":{"ANTHROPIC_BASE_URL":"`+judgeStampURL+`","CTX_EXAMPLE":"example"}}`)
	commondir := judgeStampWorktree(t, home, primary, "work/linked")
	cwd := judgeStampCwd(t, filepath.Join(home, "work", "linked"))
	caller := &judgeStampCaller{}
	r := &relay{home: home, judge: donejudge.New(caller, judgeStampModels())}

	_, err := r.judgeDone(t.Context(), judgeStampTransition(cwd, judgeStampFrame))(t.Context())
	if err == nil {
		t.Fatal("judgeDone: want an error for a dangling commondir, got nil")
	}
	if !strings.Contains(err.Error(), commondir) {
		t.Fatalf("judgeDone error = %q, want it to name the commondir %q", err, commondir)
	}
	if len(caller.calls) != 0 {
		t.Fatalf("CallVia calls = %d, want none below a dangling commondir", len(caller.calls))
	}
}
