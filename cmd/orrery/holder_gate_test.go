package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
)

// startGateHolderCommand runs `orrery holder --state-dir root --session session --gate-limit limit -- child...` in
// process, where child is PATH followed by its ARGS. Its cleanup stops a holder still serving, as startHolderCommand
// does.
func startGateHolderCommand(t *testing.T, root, session, limit string, child ...string) *holderCommand {
	t.Helper()
	socket, err := holder.SocketPath(sessionDirOf(t, root, session))
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	args := append([]string{"holder", "--state-dir", root, "--session", session, "--gate-limit", limit, "--"}, child...)
	hc := &holderCommand{t: t, ch: startOrrery(t, io.Discard, args...), socket: socket}
	t.Cleanup(hc.stopIfServing)
	return hc
}

// gateExit returns the exit record the holder left in session's holder directory under root.
func gateExit(t *testing.T, root, session string) holder.Exit {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sessionDirOf(t, root, session), holder.ExitName))
	if err != nil {
		t.Fatalf("read exit file: %v", err)
	}
	var exit holder.Exit
	if err = json.Unmarshal(data, &exit); err != nil {
		t.Fatalf("decode exit file: %v", err)
	}
	return exit
}

// awaitHolderRefusal runs orrery with args, which must be refused: a usage error that exits 2 with the usage text, with
// nothing started. The child, which touches "started" in work, must not start, and no holders directory may be made
// under root. It returns the run's result for the caller to check its message.
func awaitHolderRefusal(t *testing.T, work, root string, args []string) runResult {
	t.Helper()
	res := awaitOrrery(t, startOrrery(t, io.Discard, args...))
	if res.code != exitUsage {
		t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
	}
	if !strings.HasPrefix(res.stderr, "orrery: holder: ") {
		t.Errorf("stderr %q, want a usage error naming holder", res.stderr)
	}
	if !strings.HasSuffix(res.stderr, usageText) {
		t.Errorf("stderr does not end with the usage text: %q", res.stderr)
	}
	if _, err := os.Stat(filepath.Join(work, "started")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("child check: %v, want the child not to have started", err)
	}
	if _, err := os.Stat(filepath.Join(root, "holders")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("holders directory check: %v, want it not to exist", err)
	}
	return res
}

// TestHolderGateLimitStopsGateWithMergedTail runs a gate with a one-second limit whose child prints to stdout and to
// stderr, then sleeps. The limit stops the child, so the exit record gives ReasonLimit with both outputs in its stderr
// tail, and the holder exits 0.
func TestHolderGateLimitStopsGateWithMergedTail(t *testing.T) {
	chdirTemp(t)
	root := shortStateDir(t)
	hc := startGateHolderCommand(t, root, "gate", "1", shellChild(t, "echo out; echo err >&2; sleep 30")...)
	if res := hc.result(); res.code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
	exit := gateExit(t, root, "gate")
	if exit.Reason != holder.ReasonLimit {
		t.Errorf("exit reason %q, want %q", exit.Reason, holder.ReasonLimit)
	}
	if want := "out\nerr\n"; exit.Stderr != want {
		t.Errorf("exit stderr %q, want both outputs %q", exit.Stderr, want)
	}
}

// TestHolderGateExitedChildRecordsExit runs a gate whose limit is far off, with a child that exits 3 on its own. The
// exit record gives ReasonExited with code 3, and the holder exits 3 as it does for any child.
func TestHolderGateExitedChildRecordsExit(t *testing.T) {
	const want = 3
	chdirTemp(t)
	root := shortStateDir(t)
	hc := startGateHolderCommand(t, root, "gate", "30", shellChild(t, "exit 3")...)
	if res := hc.result(); res.code != want {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, want, res.stderr)
	}
	exit := gateExit(t, root, "gate")
	if exit.Reason != holder.ReasonExited || exit.Code != want {
		t.Errorf("exit record reason %q code %d, want reason %q code %d", exit.Reason, exit.Code, holder.ReasonExited,
			want)
	}
}

// TestHolderGateLimitRefusesNotPositiveWhole gives --gate-limit a value that is not a positive whole number of seconds.
// Each is a usage error that exits 2 with the usage text, and nothing is started.
func TestHolderGateLimitRefusesNotPositiveWhole(t *testing.T) {
	for _, limit := range []string{"0", "-1", "x", "1.5", "9223372036854775807"} {
		t.Run(limit, func(t *testing.T) {
			work := chdirTemp(t)
			root := shortStateDir(t)
			args := append([]string{
				"holder", "--state-dir", root, "--session", "s1", "--gate-limit", limit, "--",
			}, shellChild(t, "touch started")...)
			awaitHolderRefusal(t, work, root, args)
		})
	}
}

// TestHolderGateTailRefusesNotWholeBytesInRange gives --gate-tail a value that is not a whole number of bytes from 1 to
// holder.MaxGateTail, with a gate limit. Each is a usage error, and nothing is started.
func TestHolderGateTailRefusesNotWholeBytesInRange(t *testing.T) {
	tails := []string{"0", "-1", "x", "1.5", strconv.Itoa(holder.MaxGateTail + 1)}
	for _, tail := range tails {
		t.Run(tail, func(t *testing.T) {
			work := chdirTemp(t)
			root := shortStateDir(t)
			args := append([]string{
				"holder", "--state-dir", root, "--session", "s1", "--gate-limit", "30", "--gate-tail", tail, "--",
			}, shellChild(t, "touch started")...)
			awaitHolderRefusal(t, work, root, args)
		})
	}
}

// TestHolderGateTailNeedsGateLimit gives --gate-tail without --gate-limit, which is a usage error that names the flag
// it needs, and nothing is started.
func TestHolderGateTailNeedsGateLimit(t *testing.T) {
	work := chdirTemp(t)
	root := shortStateDir(t)
	args := append([]string{
		"holder", "--state-dir", root, "--session", "s1", "--gate-tail", "1024", "--",
	}, shellChild(t, "touch started")...)
	res := awaitHolderRefusal(t, work, root, args)
	if !strings.Contains(res.stderr, "--gate-tail needs --gate-limit") {
		t.Errorf("stderr %q, want it to say --gate-tail needs --gate-limit", res.stderr)
	}
}

// TestHolderGateTailAcceptsItsBounds runs a gate limited to 30 seconds whose child exits 3 on its own, with --gate-tail
// at each bound. The holder exits 3, as it does for any child that exits 3.
func TestHolderGateTailAcceptsItsBounds(t *testing.T) {
	const want = 3
	for _, tail := range []string{"1", strconv.Itoa(holder.MaxGateTail)} {
		t.Run(tail, func(t *testing.T) {
			chdirTemp(t)
			root := shortStateDir(t)
			args := append([]string{
				"holder", "--state-dir", root, "--session", "s1", "--gate-limit", "30", "--gate-tail", tail, "--",
			}, shellChild(t, "exit 3")...)
			res := awaitOrrery(t, startOrrery(t, io.Discard, args...))
			if res.code != want {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, want, res.stderr)
			}
		})
	}
}

// TestUsageTextNamesGateLimit lists the holder's gate form, which names --gate-limit SECONDS.
func TestUsageTextNamesGateLimit(t *testing.T) {
	want := "\n  holder --session NAME [--state-dir DIR] --gate-limit SECONDS -- PATH [ARGS...]\n"
	if !strings.Contains(usageText, want) {
		t.Errorf("usage text does not name --gate-limit as %q:\n%s", want, usageText)
	}
}
