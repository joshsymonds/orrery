package enginehost

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestDrainFollowsSayWhileOldHolderRuns re-adopts a holder one version older while its turn runs, and sends it a Say meanwhile.
// The Say is written at once, to the old holder. The turn's end leaves the Say unanswered, so the holder runs the Say's turn,
// and only then is drained and stops. The next Say starts the session from its row, and it follows the Say the old holder
// answered.
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
		t.Fatalf("Say while the turn runs: %v", err)
	}
	if got := writtenPrompts(t, historyOf(t, rig.path, id)); !slices.Contains(got, "again") {
		t.Fatalf("prompts written when Say returns = %q, want the Say written at once", got)
	}
	releaseHeldTurn(t, dir)
	waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the turn's end of the Say the old holder answers",
		func(h store.EngineHistory) bool {
			return turnEndCount(h) == 2 && h.State == "done"
		},
	)
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after the Say was answered, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if n := stdinLinesWith(t, dir, "again"); n != 1 {
		t.Fatalf("stdin lines with the Say on the old holder = %d, want it read once", n)
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start: nothing starts the session", starts)
	}

	if err := restarted.Say(t.Context(), SayRequest{ID: id, Text: "after"}); err != nil {
		t.Fatalf("Say after the stop: %v", err)
	}
	history := waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the reply to the Say after the stop",
		func(h store.EngineHistory) bool {
			return h.State == "done" && hasBlock(t, h, "fake reply 3")
		},
	)
	written := writtenPrompts(t, history)
	if again, after := slices.Index(
		written,
		"again",
	), slices.Index(
		written,
		"after",
	); again < 0 || after < 0 ||
		again > after {
		t.Fatalf("prompts written = %q, want the held Say before the new one", written)
	}
	starts := fakeStarts(t, dir)
	if len(starts) != 2 || !strings.Contains(starts[1], "--resume") {
		t.Fatalf("starts = %q, want a second start with --resume", starts)
	}
}

// TestDrainFoldsSayIntoRunningTurn re-adopts a holder one version older while its JUDGEHOLDS turn runs, and sends it a Say
// meanwhile. The turn reads nothing of the Say, so the Say is not folded into it: the Say is written at once, and claude runs
// it as the turn after the running one. The holder stops only once that turn ends.
func TestDrainFoldsSayIntoRunningTurn(t *testing.T) {
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
		t.Fatalf("Say while the turn runs: %v", err)
	}
	if got := writtenPrompts(t, historyOf(t, rig.path, id)); !slices.Contains(got, "again") {
		t.Fatalf("prompts written when Say returns = %q, want the Say written at once", got)
	}
	releaseHeldTurn(t, dir)
	history := waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the done state after the Say's turn",
		func(h store.EngineHistory) bool {
			return turnEndCount(h) == 2 && h.State == "done"
		},
	)
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after its turns ended, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if turns := turnEndCount(history); turns != 2 {
		t.Fatalf("turn ends = %d, want 2: the running turn, then the Say's turn", turns)
	}
	if n := stdinLinesWith(t, dir, "again"); n != 1 {
		t.Fatalf("stdin lines with the Say on the old holder = %d, want it read once", n)
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start", starts)
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
	ctx := t.Context()
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
