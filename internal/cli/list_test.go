package cli_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Every test server starts with -f /dev/null so the user's tmux config never applies.
const (
	listNoConfig    = "/dev/null"
	listTestCommand = "sleep 300"
	listGoldenPath  = "testdata/list.golden.json"
	listGoldenHost  = "example-host"
	listGoldenCwd   = "/home/user/project"
	listCommandWait = 10 * time.Second
	listPollEvery   = 10 * time.Millisecond
)

// listNewServer returns a client for a unique private server name and kills that server at cleanup.
func listNewServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-list-%d-%x", os.Getpid(), suffix)
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

// listStartSession creates detached session name in dir running a long-lived command.
func listStartSession(t *testing.T, client *tmuxctl.Client, name, dir string) {
	t.Helper()
	if _, err := client.Run(t.Context(), "-f", listNoConfig, "new-session", "-d", "-s", name, "-c", dir,
		listTestCommand); err != nil {
		t.Fatalf("new-session %s: %v", name, err)
	}
}

// listPaneIDs returns the pane IDs of session in creation order.
func listPaneIDs(t *testing.T, client *tmuxctl.Client, session string) []string {
	t.Helper()
	out, err := client.Run(t.Context(), "list-panes", "-t", session, "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes -t %s: %v", session, err)
	}
	return strings.Fields(out)
}

// listWaitForCommand polls pane until its foreground command is want, failing the test after listCommandWait.
// A pane reports its command only after its shell has exec'd it, so listing earlier shows the shell instead.
func listWaitForCommand(t *testing.T, client *tmuxctl.Client, pane, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), listCommandWait)
	defer cancel()
	ticker := time.NewTicker(listPollEvery)
	defer ticker.Stop()
	for {
		out, err := client.Run(ctx, "display-message", "-p", "-t", pane, "#{pane_current_command}")
		if err != nil {
			t.Fatalf("pane %s pane_current_command: %v", pane, err)
		}
		got := strings.TrimSpace(out)
		if got == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane %s pane_current_command = %q after %v, want %q", pane, got, listCommandWait, want)
		case <-ticker.C:
		}
	}
}

func listSetOption(t *testing.T, client *tmuxctl.Client, pane, name, value string) {
	t.Helper()
	if err := client.SetPaneOption(t.Context(), pane, name, value); err != nil {
		t.Fatalf("SetPaneOption(%s, %s, %q): %v", pane, name, value, err)
	}
}

func TestListJSON(t *testing.T) {
	client := listNewServer(t)
	dir := filepath.Join(t.TempDir(), "example dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	listStartSession(t, client, "example-a", dir)
	if _, err := client.Run(t.Context(), "split-window", "-t", "example-a", "-c", dir, listTestCommand); err != nil {
		t.Fatalf("split-window: %v", err)
	}
	listStartSession(t, client, "example-b", dir)

	panesA := listPaneIDs(t, client, "example-a")
	panesB := listPaneIDs(t, client, "example-b")
	if len(panesA) != 2 || len(panesB) != 1 {
		t.Fatalf("pane IDs example-a=%v example-b=%v, want 2 and 1", panesA, panesB)
	}
	for _, pane := range append(panesA, panesB...) {
		listWaitForCommand(t, client, pane, "sleep")
	}

	listSetOption(t, client, panesA[0], protocol.OptionState, string(protocol.StateWaiting))
	listSetOption(t, client, panesA[0], protocol.OptionSummary, "Example summary of the pane's work.")
	listSetOption(t, client, panesA[0], protocol.OptionLabel, "example-label")
	listSetOption(t, client, panesA[0], protocol.OptionChanged, "1700000000")
	listSetOption(t, client, panesA[0], protocol.OptionSeen, "1700000100")
	listSetOption(t, client, panesA[0], protocol.OptionQuestion, "Example question for the user?")
	listSetOption(t, client, panesA[1], protocol.OptionState, string(protocol.StateWorking))
	listSetOption(t, client, panesA[1], protocol.OptionChanged, "1700000200")

	var buf bytes.Buffer
	if err := cli.List(t.Context(), client, []string{"--json"}, &buf); err != nil {
		t.Fatalf("List: %v", err)
	}

	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("Hostname: %v", err)
	}
	var panes []protocol.Pane
	if err := json.Unmarshal(buf.Bytes(), &panes); err != nil {
		t.Fatalf("List output is not a JSON array: %v\n%s", err, buf.String())
	}
	for _, pane := range panes {
		if pane.Host != host {
			t.Errorf("pane %s host = %q, want os.Hostname() %q", pane.ID, pane.Host, host)
		}
		if pane.Cwd != resolved {
			t.Errorf("pane %s cwd = %q, want %q", pane.ID, pane.Cwd, resolved)
		}
	}

	// Replace only the quoted values of the host and cwd keys, so the bytes are otherwise compared as printed.
	hostJSON, err := json.Marshal(host)
	if err != nil {
		t.Fatalf("marshal host: %v", err)
	}
	cwdJSON, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("marshal cwd: %v", err)
	}
	normalized := strings.ReplaceAll(buf.String(), `"host":`+string(hostJSON), `"host":"`+listGoldenHost+`"`)
	normalized = strings.ReplaceAll(normalized, `"cwd":`+string(cwdJSON), `"cwd":"`+listGoldenCwd+`"`)

	golden, err := os.ReadFile(listGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if normalized != string(golden) {
		t.Fatalf("List output mismatch\n got: %s\nwant: %s", normalized, golden)
	}
}

func TestListNotRunningPrintsEmptyArray(t *testing.T) {
	client, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(t.TempDir(), "never-started.sock")})
	if err != nil {
		t.Fatalf("New(Socket): %v", err)
	}

	var buf bytes.Buffer
	if err := cli.List(t.Context(), client, []string{"--json"}, &buf); err != nil {
		t.Fatalf("List on a server that is not running: %v", err)
	}
	if got := buf.String(); got != "[]\n" {
		t.Fatalf("List output = %q, want %q", got, "[]\n")
	}
}

func TestListReturnsOtherTmuxErrors(t *testing.T) {
	// A socket path under a regular file makes tmux fail with "(Not a directory)", which is not a server
	// that is not running. (A regular file at the socket path itself makes tmux report "no server running".)
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	client, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(parent, "orrery.sock")})
	if err != nil {
		t.Fatalf("New(Socket): %v", err)
	}

	var buf bytes.Buffer
	err = cli.List(t.Context(), client, []string{"--json"}, &buf)
	if err == nil {
		t.Fatal("List with an unreachable server path: want error, got nil")
	}
	if !strings.Contains(err.Error(), "error connecting to") {
		t.Fatalf("error %q does not carry the tmux connection error", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("List wrote %q on error, want nothing", buf.String())
	}
}

func TestListRejectsBadArguments(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "without --json", args: []string{}, wantErr: "--json"},
		{name: "unknown flag", args: []string{"--bogus"}, wantErr: "bogus"},
		{name: "positional operand", args: []string{"--json", "extra"}, wantErr: "extra"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(t.TempDir(), "unused.sock")})
			if err != nil {
				t.Fatalf("New(Socket): %v", err)
			}

			var buf bytes.Buffer
			err = cli.List(t.Context(), client, tc.args, &buf)
			if err == nil {
				t.Fatalf("List(%v): want error, got nil", tc.args)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("List(%v) error %q does not name %q", tc.args, err, tc.wantErr)
			}
			if buf.Len() != 0 {
				t.Fatalf("List(%v) wrote %q on error, want nothing", tc.args, buf.String())
			}
		})
	}
}
