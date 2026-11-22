package integration_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/childproc"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// claudeSession is one Claude session a test runs in a pane, as orreryd reads it: a live child process whose environment
// names the pane, the registry entry <home>/.claude/sessions/<pid>.json that names the child, and the session's
// transcript, <home>/.claude/projects/<cwd>/<session id>.jsonl. The test writes the entry and appends to the transcript,
// and orreryd derives the pane's state from both.
type claudeSession struct {
	entry      string
	transcript string
	pid        int
	sessionID  string
	cwd        string
	tmux       string
}

// startClaudeSession starts a live child process in the environment that pane's processes have, so the registry entry
// of its pid binds pane. The child is a sleep, and env is added to its environment after TMUX, TMUX_PANE and
// ANTHROPIC_BASE_URL. Nothing is written to the registry yet: the session's first setStatus binds the pane. The child is
// killed at cleanup.
func startClaudeSession(t *testing.T, client *tmuxctl.Client, home, pane, cwd string, env ...string) *claudeSession {
	t.Helper()
	// tmux sets TMUX to the server's socket, its pid and the session's index, which is the session id without its '$'.
	tmux := strings.Replace(displayPane(t, client, pane, "#{socket_path},#{pid},#{session_id}"), ",$", ",", 1)
	cmd := exec.Command("sleep", "300")
	cmd.Env = append([]string{"TMUX=" + tmux, "TMUX_PANE=" + pane, "ANTHROPIC_BASE_URL=" + exampleProxyURL}, env...)
	if _, err := childproc.Start(cmd); err != nil {
		t.Fatalf("start the session's process: %v", err)
	}
	t.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("kill the session's process %d: %v", cmd.Process.Pid, err)
		}
	})
	id := randomSessionID(t)
	session := &claudeSession{
		entry:      filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", cmd.Process.Pid)),
		transcript: paneregistry.TranscriptPath(home, cwd, id),
		pid:        cmd.Process.Pid,
		sessionID:  id,
		cwd:        cwd,
		tmux:       displayPane(t, client, pane, "#{session_name}:#{window_id}.#{pane_id}"),
	}
	for _, dir := range []string{filepath.Dir(session.entry), filepath.Dir(session.transcript)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
	}
	return session
}

// displayPane returns format expanded for pane by tmux, without its trailing newline.
func displayPane(t *testing.T, client *tmuxctl.Client, pane, format string) string {
	t.Helper()
	out, err := client.Run(t.Context(), "display-message", "-p", "-t", pane, format)
	if err != nil {
		t.Fatalf("display-message %q for %s: %v", format, pane, err)
	}
	return strings.TrimSpace(out)
}

// randomSessionID returns a new session id: 32 random hex digits, which name no path separator.
func randomSessionID(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("random session id: %v", err)
	}
	return hex.EncodeToString(raw[:])
}

// setStatus writes the session's registry entry with status and waitingFor. The entry is written under a temporary name
// and renamed over the entry, so orreryd never reads it torn. The first write binds the pane, and each later write
// changes its state. orreryd applies the write a moment later, so a test waits for the pane's state to show it.
func (s *claudeSession) setStatus(t *testing.T, status, waitingFor string) {
	t.Helper()
	data, err := json.Marshal(paneregistry.Entry{
		PID:        s.pid,
		SessionID:  s.sessionID,
		Cwd:        s.cwd,
		Entrypoint: paneregistry.EntrypointCLI,
		Status:     status,
		WaitingFor: waitingFor,
		Tmux:       s.tmux,
	})
	if err != nil {
		t.Fatalf("encode registry entry: %v", err)
	}
	temp := s.entry + ".tmp"
	if err = os.WriteFile(temp, data, 0o600); err != nil {
		t.Fatalf("write registry entry: %v", err)
	}
	if err = os.Rename(temp, s.entry); err != nil {
		t.Fatalf("rename registry entry: %v", err)
	}
}

// appendTranscript appends text to the session's transcript in one write, so orreryd reads only whole lines.
func (s *claudeSession) appendTranscript(t *testing.T, text string) {
	t.Helper()
	file, err := os.OpenFile(s.transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	_, err = file.WriteString(text)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("append transcript: %v", err)
	}
}

// end removes the session's registry entry, as the end of a Claude session does. orreryd unbinds the pane once it reads
// the removal.
func (s *claudeSession) end(t *testing.T) {
	t.Helper()
	if err := os.Remove(s.entry); err != nil {
		t.Fatalf("remove registry entry: %v", err)
	}
}

// transcriptLine returns value encoded as one transcript line, with its newline.
func transcriptLine(t *testing.T, value map[string]any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode transcript line: %v", err)
	}
	return string(data) + "\n"
}

// assistantTextLine returns a transcript line of an assistant reply whose only block is text.
func assistantTextLine(t *testing.T, text string) string {
	t.Helper()
	return transcriptLine(t, map[string]any{
		"type":    "assistant",
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}},
	})
}

// toolUseLine returns a transcript line of an assistant turn that calls the tool name with input. The call has no
// result line, so it stays open, and a permission prompt waits on it.
func toolUseLine(t *testing.T, id, name string, input map[string]any) string {
	t.Helper()
	return transcriptLine(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": name, "input": input},
		}},
	})
}
