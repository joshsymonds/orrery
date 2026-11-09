package closer_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/closer"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// noClientTTY names a client tty that no attached client has.
const noClientTTY = "/dev/pts/none"

// closeTime returns the close time the tests record.
func closeTime() time.Time {
	return time.Unix(1700000000, 0)
}

// closerServer returns a client for a unique private tmux server that is killed at cleanup.
func closerServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-test-closer-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if _, err := client.Run(context.Background(), "kill-server"); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return client
}

// newSession starts a detached session named name in dir, running a long-lived command, and returns
// the id of its pane. The server reads no user configuration.
func newSession(t *testing.T, client *tmuxctl.Client, name, dir string) string {
	t.Helper()
	out, err := client.Run(t.Context(), "-f", "/dev/null", "new-session", "-d", "-P", "-F", "#{pane_id}",
		"-s", name, "-c", dir, "exec sleep 300")
	if err != nil {
		t.Fatalf("new-session %s: %v", name, err)
	}
	return strings.TrimSpace(out)
}

// setPaneOptions sets each option on the pane id.
func setPaneOptions(t *testing.T, client *tmuxctl.Client, id string, options map[string]string) {
	t.Helper()
	for name, value := range options {
		if err := client.SetPaneOption(t.Context(), id, name, value); err != nil {
			t.Fatalf("set %s on %s: %v", name, id, err)
		}
	}
}

// paneListed reports whether the server still lists the pane id.
func paneListed(t *testing.T, client *tmuxctl.Client, id string) bool {
	t.Helper()
	panes, err := client.ListPanes(t.Context())
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	return slices.ContainsFunc(panes, func(p protocol.Pane) bool { return p.ID == id })
}

// clientSessions maps the tty of each attached client to the session it shows.
func clientSessions(ctx context.Context, client *tmuxctl.Client) (map[string]string, error) {
	out, err := client.Run(ctx, "list-clients", "-F", "#{client_tty} #{client_session}")
	if err != nil {
		return nil, fmt.Errorf("list-clients: %w", err)
	}
	clients := make(map[string]string)
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		tty, session, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("list-clients line %q has no session", line)
		}
		clients[tty] = session
	}
	return clients, nil
}

// attach starts a client of session through script, with its stdin held open until cleanup, and
// returns its tty once tmux lists it on that session.
func attach(t *testing.T, client *tmuxctl.Client, session string) string {
	t.Helper()
	socket, err := client.Run(t.Context(), "display-message", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("socket path: %v", err)
	}
	command := fmt.Sprintf("stty cols 80 rows 24; tmux -S %s attach -t =%s", strings.TrimSpace(socket), session)
	cmd := exec.CommandContext(t.Context(), "script", "-qfc", command, "/dev/null")
	cmd.Env = withoutEnv(os.Environ(), "TMUX")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start client on %s: %v", session, err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	var tty string
	waitFor(t, "client on "+session, func() error {
		clients, err := clientSessions(t.Context(), client)
		if err != nil {
			return err
		}
		for clientTTY, clientSession := range clients {
			if clientSession == session {
				tty = clientTTY
				return nil
			}
		}
		return fmt.Errorf("no client on %s in %v", session, clients)
	})
	return tty
}

// withoutEnv returns env without the entries for name, so that a client started from a process
// inside tmux does not refuse to attach.
func withoutEnv(env []string, name string) []string {
	var kept []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			kept = append(kept, kv)
		}
	}
	return kept
}

// waitFor polls cond until it returns nil, and fails the test with its last error at the deadline.
func waitFor(t *testing.T, what string, cond func() error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := cond()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %v", what, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// pastSecond sleeps past one whole second. Session activity is recorded in whole seconds, so a
// session created after this sleep is strictly newer than one created before it.
func pastSecond() {
	time.Sleep(1100 * time.Millisecond)
}

func TestCloseArchivesClaudePaneBeforeClosingIt(t *testing.T) {
	client := closerServer(t)
	dir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "archive.jsonl")
	// The keeper session holds the server up after alpha closes, so the test can list panes.
	newSession(t, client, "keeper", dir)
	id := newSession(t, client, "alpha", dir)
	setPaneOptions(t, client, id, map[string]string{
		protocol.OptionClaudeSession: "claude-alpha",
		protocol.OptionTranscript:    "/home/user/.claude/projects/alpha/session.jsonl",
		protocol.OptionLabel:         "alpha label",
		protocol.OptionSummary:       "alpha summary",
	})

	plan, err := closer.PlanPane(t.Context(), client, id, noClientTTY)
	if err != nil {
		t.Fatalf("PlanPane: %v", err)
	}
	if err := closer.Close(t.Context(), client, plan, archivePath, closeTime()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	content, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if lines := strings.Count(string(content), "\n"); lines != 1 {
		t.Fatalf("archive has %d lines, want 1:\n%s", lines, content)
	}
	records, err := archive.ForDir(archivePath, dir)
	if err != nil {
		t.Fatalf("ForDir: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("archive holds %d records for %s, want 1", len(records), dir)
	}
	want := archive.Record{
		Dir:           dir,
		Session:       "alpha",
		ClaudeSession: "claude-alpha",
		Transcript:    "/home/user/.claude/projects/alpha/session.jsonl",
		Label:         "alpha label",
		Summary:       "alpha summary",
		Closed:        closeTime().Unix(),
	}
	if records[0] != want {
		t.Errorf("archived record = %+v, want %+v", records[0], want)
	}
	if paneListed(t, client, id) {
		t.Errorf("pane %s is still open after Close", id)
	}
}

func TestCloseWithoutClaudeSessionClosesPaneAndWritesNothing(t *testing.T) {
	client := closerServer(t)
	dir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "archive.jsonl")
	newSession(t, client, "keeper", dir)
	id := newSession(t, client, "alpha", dir)
	setPaneOptions(t, client, id, map[string]string{protocol.OptionLabel: "alpha label"})

	plan, err := closer.PlanPane(t.Context(), client, id, noClientTTY)
	if err != nil {
		t.Fatalf("PlanPane: %v", err)
	}
	if err := closer.Close(t.Context(), client, plan, archivePath, closeTime()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("archive file after closing a pane with no Claude session: stat err = %v, want not exist", err)
	}
	if paneListed(t, client, id) {
		t.Errorf("pane %s is still open after Close", id)
	}
}

func TestCloseArchiveFailureLeavesPaneOpen(t *testing.T) {
	client := closerServer(t)
	dir := t.TempDir()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	// The archive's parent is a regular file, so the archive cannot be created.
	archivePath := filepath.Join(blocker, "archive.jsonl")
	id := newSession(t, client, "alpha", dir)
	setPaneOptions(t, client, id, map[string]string{protocol.OptionClaudeSession: "claude-alpha"})

	plan, err := closer.PlanPane(t.Context(), client, id, noClientTTY)
	if err != nil {
		t.Fatalf("PlanPane: %v", err)
	}
	if err := closer.Close(t.Context(), client, plan, archivePath, closeTime()); err == nil {
		t.Fatal("Close returned nil although the archive cannot be written")
	}
	if !paneListed(t, client, id) {
		t.Errorf("pane %s closed although its archive write failed", id)
	}
}

func TestCloseMovesInvokingClientToNewestOtherSession(t *testing.T) {
	client := closerServer(t)
	dir := t.TempDir()
	alphaID := newSession(t, client, "alpha", dir)
	invoking := attach(t, client, "alpha")
	newSession(t, client, "beta", dir)
	second := attach(t, client, "beta")
	pastSecond()
	newSession(t, client, "delta", dir)
	pastSecond()
	newSession(t, client, "gamma", dir)
	pastSecond()
	newSession(t, client, protocol.ShellSessionPrefix+"popup", dir)

	plan, err := closer.PlanPane(t.Context(), client, alphaID, invoking)
	if err != nil {
		t.Fatalf("PlanPane: %v", err)
	}
	if plan.MoveTo != "gamma" || plan.Detach {
		t.Fatalf("plan MoveTo=%q Detach=%t, want MoveTo gamma and Detach false", plan.MoveTo, plan.Detach)
	}
	if err := closer.Close(
		t.Context(),
		client,
		plan,
		filepath.Join(t.TempDir(), "archive.jsonl"),
		closeTime(),
	); err != nil {
		t.Fatalf("Close: %v", err)
	}

	clients, err := clientSessions(t.Context(), client)
	if err != nil {
		t.Fatalf("clientSessions: %v", err)
	}
	if got := clients[invoking]; got != "gamma" {
		t.Errorf("invoking client shows %q, want gamma", got)
	}
	if got := clients[second]; got != "beta" {
		t.Errorf("second client shows %q, want beta (untouched)", got)
	}
	if paneListed(t, client, alphaID) {
		t.Errorf("pane %s is still open after Close", alphaID)
	}
}

func TestPlanPaneReportsDetachWithoutOtherSession(t *testing.T) {
	client := closerServer(t)
	dir := t.TempDir()
	id := newSession(t, client, "alpha", dir)
	newSession(t, client, protocol.ShellSessionPrefix+"popup", dir)
	invoking := attach(t, client, "alpha")

	plan, err := closer.PlanPane(t.Context(), client, id, invoking)
	if err != nil {
		t.Fatalf("PlanPane: %v", err)
	}
	if !plan.Detach || plan.MoveTo != "" {
		t.Errorf("plan MoveTo=%q Detach=%t, want MoveTo empty and Detach true", plan.MoveTo, plan.Detach)
	}
}

func TestPlanPaneKeepsClientWhenSessionKeepsAnotherPane(t *testing.T) {
	client := closerServer(t)
	dir := t.TempDir()
	id := newSession(t, client, "alpha", dir)
	newSession(t, client, "beta", dir)
	if _, err := client.Run(t.Context(), "split-window", "-d", "-t", id, "exec sleep 300"); err != nil {
		t.Fatalf("split-window: %v", err)
	}
	invoking := attach(t, client, "alpha")

	plan, err := closer.PlanPane(t.Context(), client, id, invoking)
	if err != nil {
		t.Fatalf("PlanPane: %v", err)
	}
	if plan.MoveTo != "" || plan.Detach {
		t.Errorf("plan MoveTo=%q Detach=%t, want the client to stay", plan.MoveTo, plan.Detach)
	}
}
