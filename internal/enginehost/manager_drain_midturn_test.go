package enginehost

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestDrainFollowsSayWhileOldHolderRuns re-adopts a holder one version older while its turn holds, and sends it a Say
// meanwhile. The turn ends without listing the Say as answered, so claude runs the Say as a following turn on that same
// holder. The holder stops only after that turn ends.
func TestDrainFollowsSayWhileOldHolderRuns(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))

	restarted, _ := restartDrain(t, rig, rig.store, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}

	if err := restarted.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say while the turn holds: %v", err)
	}
	releaseHeldTurn(t, dir)
	waitHistory(t, rig.path, id, "the reply to the Say that followed the held turn", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after the reply's turn ended, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start: the Say answered on the same holder", starts)
	}
}

// TestDrainFoldsSayIntoRunningTurn re-adopts a holder one version older while its FOLD turn holds, and sends it a Say
// meanwhile. claude folds the Say into the running turn, and that turn ends by listing both prompts as answered. No
// following turn runs, and the holder stops at that turn's end.
func TestDrainFoldsSayIntoRunningTurn(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "FOLD"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))

	restarted, _ := restartDrain(t, rig, rig.store, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}

	if err := restarted.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say while the turn holds: %v", err)
	}
	releaseHeldTurn(t, dir)
	history := waitHistory(t, rig.path, id, "the done state of the folded turn", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake fold 1")
	})
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after the folded turn ended, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if got := blockTexts(t, history); !slices.Equal(got, []string{"fake fold 1"}) {
		t.Fatalf("finished blocks = %q, want only the folded turn's block: no following turn", got)
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start", starts)
	}
	if n := stdinLinesWith(t, dir, "again"); n != 1 {
		t.Fatalf("stdin lines with the Say = %d, want the Say read once", n)
	}
}

// TestFakeClaudeResultsListAnsweredPrompts runs the fake claude alone. The result of a turn lists the uuid of the prompt
// that opened it, and a FOLD turn's result lists the uuid of the message it folded in as well.
func TestFakeClaudeResultsListAnsweredPrompts(t *testing.T) {
	t.Parallel()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), managerGuard)
	defer cancel()
	cmd := exec.CommandContext(ctx, sh, fakeClaudePath(t))
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake claude: %v", err)
	}
	lines := bufio.NewScanner(stdout)
	send := func(uuid, text string) {
		t.Helper()
		line := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":%q}]},"uuid":%q}`,
			text, uuid)
		if _, err := io.WriteString(stdin, line+"\n"); err != nil {
			t.Fatalf("write prompt %s: %v", uuid, err)
		}
	}
	result := func() []string {
		t.Helper()
		for lines.Scan() {
			var line struct {
				Type             string   `json:"type"`
				UserMessageUUIDs []string `json:"user_message_uuids"`
			}
			if err := json.Unmarshal(lines.Bytes(), &line); err != nil {
				t.Fatalf("decode fake claude line %q: %v", lines.Text(), err)
			}
			if line.Type == "result" {
				return line.UserMessageUUIDs
			}
		}
		t.Fatalf("fake claude ended before its result: %v", lines.Err())
		return nil
	}

	send("prompt-a", "hi")
	if got := result(); !slices.Equal(got, []string{"prompt-a"}) {
		t.Fatalf("answered by the first turn = %q, want [prompt-a]", got)
	}
	send("prompt-b", "FOLD")
	send("prompt-c", "folded")
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("write fake claude release: %v", err)
	}
	if got := result(); !slices.Equal(got, []string{"prompt-b", "prompt-c"}) {
		t.Fatalf("answered by the fold turn = %q, want [prompt-b prompt-c]", got)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("close fake claude stdin: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("fake claude exit: %v", err)
	}
}
