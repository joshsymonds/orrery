package holderhost

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/holder"
)

// fakeClaudeScript stands in for claude. It writes the path it runs as and then its arguments, one to a line, to a file
// named args in its working directory. It then writes two stdout lines, an init line and a second line, and reads its
// stdin until the stdin ends.
const fakeClaudeScript = `#!/bin/sh
for arg in "$0" "$@"; do printf '%s\n' "$arg"; done > args
printf '%s\n' '{"type":"system","subtype":"init"}'
printf '%s\n' '{"type":"fake","n":2}'
while IFS= read -r _; do :; done
`

// fakeClaude writes the fake claude into a new directory of the test and returns its absolute path.
func fakeClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	exectest.WriteFile(t, path, []byte(fakeClaudeScript), 0o700)
	return path
}

// shortRoot creates a temporary directory to use as a state directory. A socket path must fit in sun_path, which a
// t.TempDir path can exceed. The caller removes the root.
func shortRoot() (string, error) {
	root, err := os.MkdirTemp("", "hold")
	if err != nil {
		return "", fmt.Errorf("make state directory: %w", err)
	}
	return root, nil
}

// holderRoot returns a shortRoot directory that is removed when the test ends.
func holderRoot(t *testing.T) string {
	t.Helper()
	root, err := shortRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
	return root
}

// testLocal returns a Local that runs the claude at claude under the shell sh, keeps its holders in a state directory of
// its own, and has a HOME of its own. Its CronHook is cronHook.
func testLocal(t *testing.T, claude string, cronHook bool) *Local {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	return NewLocal(LocalConfig{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		StateDir: holderRoot(t),
		CronHook: cronHook,
	})
}

// launch starts the headless session id on l in a new directory, and returns the session and the directory. The
// session's holder is cleared when the test ends, so no holder outlives the test.
func launch(t *testing.T, l *Local, id string) (*session.Session, string) {
	t.Helper()
	dir := t.TempDir()
	sess, err := l.Launch(t.Context(), Spec{Session: id, Dir: dir})
	if err != nil {
		t.Fatalf("Launch %s: %v", id, err)
	}
	t.Cleanup(func() {
		if err := l.Clear(context.Background(), id); err != nil {
			t.Errorf("clear session %s: %v", id, err)
		}
	})
	return sess, dir
}

// awaitFirstLine receives the first stdout line of sess, which the fake claude writes after it records its arguments. It
// fails the test when the session ends first.
func awaitFirstLine(t *testing.T, sess *session.Session) session.Line {
	t.Helper()
	line, ok := <-sess.Lines()
	if !ok {
		t.Fatal("session ended before its first line")
	}
	return line
}

// readArgs returns the arguments the fake claude recorded in dir, its own path first. The fake records them before it
// writes its first line, so a test reads them after awaitFirstLine.
func readArgs(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatalf("read the fake claude's arguments: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// argAfter returns the argument that follows flag in args.
func argAfter(t *testing.T, args []string, flag string) string {
	t.Helper()
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("arguments %q have no value for %s", args, flag)
	}
	return args[i+1]
}

func TestLocalHostLaunchRunsSessionUnderConfiguredClaude(t *testing.T) {
	t.Parallel()
	claude := fakeClaude(t)
	l := testLocal(t, claude, false)
	sess, dir := launch(t, l, "launched")
	awaitFirstLine(t, sess)

	if args := readArgs(t, dir); args[0] != claude {
		t.Fatalf("claude ran as %q, want the configured claude %q", args[0], claude)
	}
	if _, err := os.Stat(filepath.Join(l.cfg.StateDir, "holders", "launched")); err != nil {
		t.Fatalf("the session has no holder under the state directory: %v", err)
	}
}

func TestLocalHostReattachSessionAtOffset(t *testing.T) {
	t.Parallel()
	l := testLocal(t, fakeClaude(t), false)
	sess, _ := launch(t, l, "reattached")
	first := awaitFirstLine(t, sess)
	if err := sess.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	process, _, err := l.Reattach(t.Context(), "reattached", first.End, holder.WireVersion)
	if err != nil {
		t.Fatalf("Reattach at offset %d: %v", first.End, err)
	}
	want := `{"type":"fake","n":2}` + "\n"
	got := make([]byte, len(want))
	if _, err = io.ReadFull(process.Stdout(), got); err != nil {
		t.Fatalf("read stdout from offset %d: %v", first.End, err)
	}
	if string(got) != want {
		t.Fatalf("stdout from offset %d = %q, want %q", first.End, got, want)
	}
	if err = process.Detach(); err != nil {
		t.Fatalf("Detach the reattached process: %v", err)
	}
}

func TestLocalHostSessionsListsLaunchedSession(t *testing.T) {
	t.Parallel()
	l := testLocal(t, fakeClaude(t), false)
	before, err := l.Sessions(t.Context())
	if err != nil {
		t.Fatalf("Sessions before any launch: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("Sessions before any launch = %q, want none", before)
	}

	launch(t, l, "listed")
	got, err := l.Sessions(t.Context())
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if !slices.Equal(got, []string{"listed"}) {
		t.Fatalf("Sessions = %q, want [listed]", got)
	}
}

func TestLocalHostStopAndClearEndSession(t *testing.T) {
	t.Parallel()
	l := testLocal(t, fakeClaude(t), false)
	sess, _ := launch(t, l, "ended")
	awaitFirstLine(t, sess)
	if err := sess.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	if err := l.Stop(t.Context(), "ended"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	gone, err := l.OpenGone(t.Context(), "ended", 0)
	if err != nil {
		t.Fatalf("OpenGone after Stop: %v", err)
	}
	if exit, found := gone.Exit(); !found || exit.Reason != holder.ReasonStopped {
		t.Fatalf("exit record after Stop = %+v (found %v), want reason %q", exit, found, holder.ReasonStopped)
	}
	stopped, err := l.Sessions(t.Context())
	if err != nil {
		t.Fatalf("Sessions after Stop: %v", err)
	}
	if !slices.Equal(stopped, []string{"ended"}) {
		t.Fatalf("Sessions after Stop = %q, want the stopped session's directory left in place", stopped)
	}

	if err = l.Clear(t.Context(), "ended"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	cleared, err := l.Sessions(t.Context())
	if err != nil {
		t.Fatalf("Sessions after Clear: %v", err)
	}
	if len(cleared) != 0 {
		t.Fatalf("Sessions after Clear = %q, want none", cleared)
	}
}

func TestLocalHostHeadlessSettingsCarryCronHookOnlyWhenSet(t *testing.T) {
	t.Parallel()
	claude := fakeClaude(t)
	for _, tc := range []struct {
		name     string
		cronHook bool
	}{
		{name: "hooked", cronHook: true},
		{name: "plain", cronHook: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := testLocal(t, claude, tc.cronHook)
			sess, dir := launch(t, l, tc.name)
			awaitFirstLine(t, sess)

			settings := argAfter(t, readArgs(t, dir), "--settings")
			if got := strings.Contains(settings, "hook cron"); got != tc.cronHook {
				t.Fatalf("settings %s carry the cron hook = %v, want %v", settings, got, tc.cronHook)
			}
		})
	}
}
