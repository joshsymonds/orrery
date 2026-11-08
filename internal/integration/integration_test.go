// Package integration_test builds the orrery and orreryd binaries and proves each orrery subcommand
// by running the built orrery against private tmux servers and a listening test socket.
package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	binDirEnv       = "ORRERY_TEST_BIN_DIR"
	noConfig        = "/dev/null"
	exampleQuestion = "example question? yes or no"
	exampleHookJSON = `{"event":"example"}`
	exampleContext  = "http://example.invalid/ctx/example-context"
	sentinelKey     = "z"
	waitLimit       = 10 * time.Second
	pollEvery       = 10 * time.Millisecond
)

func TestMain(m *testing.M) {
	os.Exit(runWithBinaries(m))
}

// runWithBinaries builds orrery and orreryd into a fresh directory, points the tests at it, and runs them.
func runWithBinaries(m *testing.M) int {
	dir, err := os.MkdirTemp("", "orrery-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "make binary directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	for _, name := range []string{"orrery", "orreryd"} {
		out, buildErr := exec.Command("go", "build", "-o", filepath.Join(dir, name), "../../cmd/"+name).CombinedOutput()
		if buildErr != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", name, buildErr, out)
			return 1
		}
	}
	if err = os.Setenv(binDirEnv, dir); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", binDirEnv, err)
		return 1
	}
	return m.Run()
}

// outcome is what one orrery run printed and how it exited.
type outcome struct {
	stdout string
	stderr string
	code   int
}

// runOrrery runs the built orrery with args, stdin, and env added to the test's environment.
func runOrrery(t *testing.T, stdin string, env []string, args ...string) outcome {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), filepath.Join(os.Getenv(binDirEnv), "orrery"), args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("run orrery %v: %v", args, err)
	}
	code := 0
	if exitErr != nil {
		code = exitErr.ExitCode()
	}
	return outcome{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// wantExit fails the test unless out exited with code, showing both streams.
func wantExit(t *testing.T, out outcome, code int) {
	t.Helper()
	if out.code != code {
		t.Fatalf("exit %d, want %d; stdout %q stderr %q", out.code, code, out.stdout, out.stderr)
	}
}

// newServer returns a client for a unique private tmux server, killed at cleanup.
func newServer(t *testing.T) (*tmuxctl.Client, string) {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-integration-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if _, killErr := client.Run(context.Background(), "kill-server"); killErr != nil {
			t.Errorf("kill-server: %v", killErr)
		}
	})
	return client, name
}

// startPane starts detached session s1 running command and returns the ID of its only pane.
func startPane(t *testing.T, client *tmuxctl.Client, command string) string {
	t.Helper()
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1", "-c", t.TempDir(),
		command); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return strings.TrimSpace(out)
}

// screen returns the captured text of pane.
func screen(t *testing.T, client *tmuxctl.Client, pane string) string {
	t.Helper()
	out, err := client.Run(t.Context(), "capture-pane", "-p", "-J", "-t", pane)
	if err != nil {
		t.Fatalf("capture-pane %s: %v", pane, err)
	}
	return out
}

// waitForScreen polls pane until its screen shows want, failing the test after waitLimit.
func waitForScreen(t *testing.T, client *tmuxctl.Client, pane, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for !strings.Contains(screen(t, client, pane), want) {
		select {
		case <-ctx.Done():
			t.Fatalf("pane %s never showed %q; screen:\n%s", pane, want, screen(t, client, pane))
		case <-ticker.C:
		}
	}
}

// assertNothingTyped sends the sentinel key and waits for it to echo. Keys reach the pane in order, so any
// refused key sent before the sentinel would already be on screen; the check then finds no typed "1".
func assertNothingTyped(t *testing.T, client *tmuxctl.Client, name, pane string) {
	t.Helper()
	wantExit(t, runOrrery(t, "", nil, "-L", name, "send", "--pane", pane, "--keys", sentinelKey), 0)
	waitForScreen(t, client, pane, sentinelKey)
	if got := screen(t, client, pane); strings.Contains(got, "1") {
		t.Fatalf("pane %s shows a typed 1:\n%s", pane, got)
	}
}

func TestHookForwardsFrameSilently(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "orreryd.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	frames := make(chan []byte, 1)
	failures := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			failures <- acceptErr
			return
		}
		defer conn.Close()
		frame, readErr := io.ReadAll(conn)
		if readErr != nil {
			failures <- readErr
			return
		}
		frames <- frame
	}()

	out := runOrrery(t, exampleHookJSON, []string{"TMUX_PANE=%7"}, "--socket", socket, "hook")
	wantExit(t, out, 0)
	if out.stdout != "" || out.stderr != "" {
		t.Fatalf("hook printed stdout %q stderr %q, want nothing", out.stdout, out.stderr)
	}

	select {
	case frame := <-frames:
		var got protocol.HookFrame
		if err = json.Unmarshal(frame, &got); err != nil {
			t.Fatalf("decode frame %q: %v", frame, err)
		}
		if got.TmuxPane != "%7" || string(got.Hook) != exampleHookJSON {
			t.Fatalf("frame pane %q hook %s, want pane %%7 hook %s", got.TmuxPane, got.Hook, exampleHookJSON)
		}
	case err = <-failures:
		t.Fatalf("daemon socket: %v", err)
	case <-time.After(waitLimit):
		t.Fatal("no frame reached the daemon socket")
	}
}

func TestHookAbsentSocketIsSilentWithJunkArgs(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.sock")
	out := runOrrery(t, exampleHookJSON, nil, "--socket", absent, "hook", "--junk", "-x", "extra")
	wantExit(t, out, 0)
	if out.stdout != "" || out.stderr != "" {
		t.Fatalf("hook printed stdout %q stderr %q, want nothing", out.stdout, out.stderr)
	}
}

func TestListJSONPrintsPanes(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")

	out := runOrrery(t, "", nil, "-L", name, "list", "--json")
	wantExit(t, out, 0)
	if out.stderr != "" {
		t.Fatalf("list wrote stderr %q", out.stderr)
	}
	var panes []protocol.Pane
	if err := json.Unmarshal([]byte(out.stdout), &panes); err != nil {
		t.Fatalf("decode list output %q: %v", out.stdout, err)
	}
	if len(panes) != 1 || panes[0].ID != pane {
		t.Fatalf("panes %+v, want exactly pane %s", panes, pane)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	if panes[0].Host != host {
		t.Fatalf("pane host %q, want %q", panes[0].Host, host)
	}
	if panes[0].Session != "s1" {
		t.Fatalf("pane session %q, want s1", panes[0].Session)
	}
}

func TestSeenSetsOption(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")

	before := time.Now().Unix()
	wantExit(t, runOrrery(t, "", nil, "-L", name, "seen", "--pane", pane), 0)
	after := time.Now().Unix()

	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionSeen)
	if err != nil {
		t.Fatalf("read %s: %v", protocol.OptionSeen, err)
	}
	if !ok {
		t.Fatalf("%s is unset after seen", protocol.OptionSeen)
	}
	seen, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		t.Fatalf("%s = %q, not a unix time: %v", protocol.OptionSeen, got, err)
	}
	if seen < before || seen > after {
		t.Fatalf("%s = %d, want between %d and %d", protocol.OptionSeen, seen, before, after)
	}
}

func TestSendRefusesSendFlagsAfterKeys(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "cat")

	refused := [][]string{
		{"--expect-question", "0000"},
		{"-expect-question=0000"},
		{"--text=example"},
		{"-text"},
		{"--keys"},
		{"-pane=" + pane},
	}
	for _, extra := range refused {
		args := append([]string{"-L", name, "send", "--pane", pane, "--keys", "1"}, extra...)
		out := runOrrery(t, "", nil, args...)
		wantExit(t, out, 2)
		if !strings.HasPrefix(out.stderr, "orrery: ") {
			t.Fatalf("args %v: stderr %q, want orrery: prefix", args, out.stderr)
		}
	}
	assertNothingTyped(t, client, name, pane)
}

func TestSendGuardedKeysRequireMatchingQuestion(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "printf '"+exampleQuestion+"\\n'; exec cat")
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionQuestion, exampleQuestion); err != nil {
		t.Fatalf("set question: %v", err)
	}
	waitForScreen(t, client, pane, exampleQuestion)

	wrong := protocol.QuestionHash("example other question?")
	out := runOrrery(t, "", nil, "-L", name, "send", "--pane", pane, "--expect-question", wrong, "--keys", "1")
	wantExit(t, out, 3)
	if out.stdout != "" {
		t.Fatalf("refused send printed stdout %q", out.stdout)
	}
	assertNothingTyped(t, client, name, pane)

	right := protocol.QuestionHash(exampleQuestion)
	wantExit(t, runOrrery(t, "", nil, "-L", name, "send", "--pane", pane, "--expect-question", right,
		"--keys", "1"), 0)
	waitForScreen(t, client, pane, "1")
}

func TestDebugSummarizeWritesSummaryAndLabel(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")

	dir := t.TempDir()
	transcript := filepath.Join(dir, "transcript.jsonl")
	transcriptLines := `{"type":"user","message":{"role":"user","content":"example prompt"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"example reply"}]}}` + "\n"
	if err := os.WriteFile(transcript, []byte(transcriptLines), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	fakeClaude := filepath.Join(dir, "fake-claude.sh")
	script := "#!/bin/sh\ncat >/dev/null\nprintf 'summary: example session in progress\\nlabel: example task name\\n'\n"
	if err := os.WriteFile(fakeClaude, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	out := runOrrery(t, "", []string{"ANTHROPIC_BASE_URL=" + exampleContext},
		"-L", name, "debug", "summarize", "--pane", pane, "--transcript", transcript, "--claude", fakeClaude)
	wantExit(t, out, 0)
	if out.stdout != "" || out.stderr != "" {
		t.Fatalf("debug summarize printed stdout %q stderr %q, want nothing", out.stdout, out.stderr)
	}

	for option, want := range map[string]string{
		protocol.OptionSummary: "example session in progress",
		protocol.OptionLabel:   "example task name",
	} {
		got, ok, err := client.PaneOption(t.Context(), pane, option)
		if err != nil {
			t.Fatalf("read %s: %v", option, err)
		}
		if !ok || got != want {
			t.Fatalf("%s = %q (set %t), want %q", option, got, ok, want)
		}
	}
}

func TestUsageAndFailureExitCodes(t *testing.T) {
	// No server is started here: every case exits before any tmux call.
	const name = "orrery-integration-unused"
	cases := []struct {
		name string
		args []string
		code int
	}{
		{name: "no subcommand", args: []string{}, code: 2},
		{name: "unknown subcommand", args: []string{"bogus"}, code: 2},
		{name: "both -L and -S", args: []string{"-L", name, "-S", "/dev/null", "list", "--json"}, code: 2},
		{name: "debug without subcommand", args: []string{"debug"}, code: 2},
		{name: "debug summarize without flags", args: []string{"debug", "summarize"}, code: 2},
		{name: "list without --json is a runtime error", args: []string{"-L", name, "list"}, code: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runOrrery(t, "", nil, tc.args...)
			wantExit(t, out, tc.code)
			if !strings.HasPrefix(out.stderr, "orrery: ") {
				t.Fatalf("stderr %q, want orrery: prefix", out.stderr)
			}
		})
	}
}
