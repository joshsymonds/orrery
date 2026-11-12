package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// haikuSchema is the JSON schema every test passes; it holds no newline so the argv file records it on one line.
const haikuSchema = `{"type":"object","properties":{"verdict":{"type":"string"}},"required":["verdict"]}`

// haikuSuccessLine is a success result line as claude -p --output-format json prints it.
const haikuSuccessLine = `{"type":"result","subtype":"success","is_error":false,"structured_output":{"verdict":"ok"}}`

// haikuFake is a fake claude script. It counts its runs in dir/runs, records its argv, cwd, env and stdin in dir,
// runs extra, then prints dir/stdout to stdout, dir/stderr to stderr and exits with status.
type haikuFake struct {
	stdout string
	stderr string
	status int
	extra  string
}

// haikuWriteFake writes the fake claude script for f into dir and returns its path.
func haikuWriteFake(t *testing.T, dir string, f haikuFake) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "stdout"), []byte(f.stdout), 0o600); err != nil {
		t.Fatalf("write fake stdout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stderr"), []byte(f.stderr), 0o600); err != nil {
		t.Fatalf("write fake stderr: %v", err)
	}
	script := fmt.Sprintf(`#!/bin/sh
dir='%s'
echo run >> "$dir/runs"
for a in "$@"; do printf '%%s\n' "$a"; done > "$dir/argv"
pwd > "$dir/cwd"
env > "$dir/env"
cat > "$dir/stdin"
%s
cat "$dir/stdout"
cat "$dir/stderr" >&2
exit %d
`, dir, f.extra, f.status)
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}

// haikuEnviron is the environment a test passes in: PATH for the script's tools, a base URL and a summarizer flag
// that the call must drop, and a marker variable that must survive.
func haikuEnviron() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"ANTHROPIC_BASE_URL=http://patchbay.invalid/ctx/work",
		"ORRERY_SUMMARIZER=0",
		"HAIKU_TEST_MARK=kept",
	}
}

// haikuReadFile returns the contents of name in dir, or fails the test.
func haikuReadFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// haikuRuns counts how many times the fake claude in dir ran.
func haikuRuns(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "runs"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}
	return strings.Count(string(data), "run\n")
}

// haikuBaseURLEntries returns the ANTHROPIC_BASE_URL entries of the env the fake claude recorded in dir.
func haikuBaseURLEntries(t *testing.T, dir string) []string {
	t.Helper()
	var entries []string
	for line := range strings.SplitSeq(haikuReadFile(t, dir, "env"), "\n") {
		if strings.HasPrefix(line, "ANTHROPIC_BASE_URL=") {
			entries = append(entries, line)
		}
	}
	return entries
}

// haikuDir returns a fresh directory for one fake's files.
func haikuDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// haikuWantArgv is the argv text the fake records for a call whose --settings value is settings.
func haikuWantArgv(settings string) string {
	argv := []string{
		"-p",
		"--model", protocol.DefaultSummaryModel,
		"--settings", settings,
		"--no-session-persistence",
		"--tools", "",
		"--max-turns", "1",
		"--output-format", "json",
		"--json-schema", haikuSchema,
	}
	return strings.Join(argv, "\n") + "\n"
}

// haikuAwaitFile waits up to 5 seconds for path to exist and fails the test with failMsg when it does not.
func haikuAwaitFile(t *testing.T, path, failMsg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s", failMsg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHaikuCallRunsClaudeWithArgvEnvCwdAndStdin(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{stdout: haikuSuccessLine, status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron()})

	out, err := h.Call(context.Background(), haikuSchema, "first line\nsecond line")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got, want := string(out), `{"verdict":"ok"}`; got != want {
		t.Fatalf("structured_output = %s, want %s", got, want)
	}

	wantArgvText := haikuWantArgv(`{"disableAllHooks":true}`)
	if got := haikuReadFile(t, dir, "argv"); got != wantArgvText {
		t.Fatalf("argv:\n%s\nwant:\n%s", got, wantArgvText)
	}
	if got := strings.TrimSpace(haikuReadFile(t, dir, "cwd")); got != "/" {
		t.Fatalf("cwd = %q, want /", got)
	}
	if got := haikuReadFile(t, dir, "stdin"); got != "first line\nsecond line" {
		t.Fatalf("stdin = %q", got)
	}

	env := strings.Split(haikuReadFile(t, dir, "env"), "\n")
	var summarizer, base int
	marked := false
	for _, line := range env {
		switch {
		case line == "ORRERY_SUMMARIZER=1":
			summarizer++
		case strings.HasPrefix(line, "ORRERY_SUMMARIZER="):
			t.Fatalf("env kept %q, want only ORRERY_SUMMARIZER=1", line)
		case strings.HasPrefix(line, "ANTHROPIC_BASE_URL="):
			base++
		case line == "HAIKU_TEST_MARK=kept":
			marked = true
		}
	}
	if summarizer != 1 {
		t.Fatalf("ORRERY_SUMMARIZER=1 count = %d, want 1", summarizer)
	}
	if base != 0 {
		t.Fatalf("env kept ANTHROPIC_BASE_URL")
	}
	if !marked {
		t.Fatalf("env dropped the caller's HAIKU_TEST_MARK")
	}
}

func TestHaikuCallErrors(t *testing.T) {
	cases := []struct {
		name   string
		fake   haikuFake
		errHas string
	}{
		{
			name:   "nonzero exit carries stderr",
			fake:   haikuFake{stdout: "", stderr: "boom from claude", status: 3},
			errHas: "boom from claude",
		},
		{
			name:   "stdout is not JSON",
			fake:   haikuFake{stdout: "not json at all", status: 0},
			errHas: "haiku",
		},
		{
			name:   "stdout is JSON but not a result",
			fake:   haikuFake{stdout: `{"type":"assistant","structured_output":{"verdict":"ok"}}`, status: 0},
			errHas: "result",
		},
		{
			name: "is_error true",
			fake: haikuFake{
				stdout: `{"type":"result","subtype":"success","is_error":true,` +
					`"structured_output":{"verdict":"ok"}}`,
			},
			errHas: "is_error",
		},
		{
			name: "subtype other than success",
			fake: haikuFake{
				stdout: `{"type":"result","subtype":"error_max_turns","is_error":false,` +
					`"structured_output":{"verdict":"ok"}}`,
			},
			errHas: "error_max_turns",
		},
		{
			name:   "structured_output missing",
			fake:   haikuFake{stdout: `{"type":"result","subtype":"success","is_error":false}`, status: 0},
			errHas: "structured_output",
		},
		{
			name: "structured_output null",
			fake: haikuFake{
				stdout: `{"type":"result","subtype":"success","is_error":false,` +
					`"structured_output":null}`,
			},
			errHas: "structured_output",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := haikuDir(t)
			claude := haikuWriteFake(t, dir, tc.fake)
			h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron()})

			out, err := h.Call(context.Background(), haikuSchema, "input")
			if err == nil {
				t.Fatalf("Call returned %s and no error", out)
			}
			if !strings.Contains(err.Error(), tc.errHas) {
				t.Fatalf("error %q does not contain %q", err, tc.errHas)
			}
			if out != nil {
				t.Fatalf("Call returned output %s with an error", out)
			}
		})
	}
}

func TestHaikuCallKillsHungClaudeAtTimeout(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{extra: "sleep 30", status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron(), Timeout: 300 * time.Millisecond})

	start := time.Now()
	_, err := h.Call(context.Background(), haikuSchema, "input")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("Call on a hung claude returned no error")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("hung claude ran %v past a 300ms timeout", elapsed)
	}
}

func TestHaikuCallEndsWhenContextEnds(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{extra: "sleep 30", status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron(), Timeout: 10 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := h.Call(ctx, haikuSchema, "input"); err == nil {
		t.Fatalf("Call under an ended context returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Call ran %v after its context ended", elapsed)
	}
}

// TestHaikuCallCapWaitsForSlot holds the only slot with a hung call and checks that a second call waits, never
// starts claude, and fails when its wait ends.
func TestHaikuCallCapWaitsForSlot(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{extra: `: > "$dir/started"
sleep 30`, status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron(), Cap: 1, Timeout: 10 * time.Second})

	holdCtx, releaseHold := context.WithCancel(context.Background())
	defer releaseHold()
	held := make(chan error, 1)
	go func() {
		_, err := h.Call(holdCtx, haikuSchema, "hold")
		held <- err
	}()
	haikuAwaitFile(t, filepath.Join(dir, "started"), "holding call never started claude")

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelWait()
	start := time.Now()
	if _, err := h.Call(waitCtx, haikuSchema, "waits"); err == nil {
		t.Fatalf("second call with the slot held returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("second call waited %v for a slot", elapsed)
	}
	if runs := haikuRuns(t, dir); runs != 1 {
		t.Fatalf("claude ran %d times, want 1: the waiting call must not start claude", runs)
	}

	releaseHold()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatalf("holding call did not end after its context ended")
	}
}

// TestHaikuCallMissingClaude points the call at a claude that is not on PATH and checks that the call fails with
// exec.ErrNotFound and returns no output.
func TestHaikuCallMissingClaude(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := NewHaiku(HaikuConfig{Claude: "orrery-haiku-no-such-claude", Environ: haikuEnviron()})

	out, err := h.Call(context.Background(), haikuSchema, "input")
	if err == nil {
		t.Fatalf("Call with a missing claude returned %s and no error", out)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("error %q does not wrap exec.ErrNotFound", err)
	}
	if out != nil {
		t.Fatalf("Call returned output %s with an error", out)
	}
}

func TestHaikuCallReleasesSlotAfterCall(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{stdout: haikuSuccessLine, status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron(), Cap: 1})

	for i := range 2 {
		if _, err := h.Call(context.Background(), haikuSchema, "input"); err != nil {
			t.Fatalf("call %d with Cap 1: %v", i+1, err)
		}
	}
	if runs := haikuRuns(t, dir); runs != 2 {
		t.Fatalf("claude ran %d times, want 2", runs)
	}
}

func TestNewHaikuDefaults(t *testing.T) {
	h := NewHaiku(HaikuConfig{Claude: "/bin/true"})
	if got := cap(h.slots); got != 2 {
		t.Fatalf("default cap = %d, want 2", got)
	}
	if h.timeout != 60*time.Second {
		t.Fatalf("default timeout = %v, want 60s", h.timeout)
	}
}

// haikuBoundEnv returns the env a bound caller carries in tests: a synthetic loopback base URL and one more key.
func haikuBoundEnv() map[string]string {
	return map[string]string{
		"ANTHROPIC_BASE_URL": "http://127.0.0.1:9/ctx/example",
		"CTX_EXAMPLE":        "example",
	}
}

// TestHaikuWithEnvPutsEnvInSettings checks that a bound caller runs claude as h.Call does, except that the env
// rides in the --settings value: the process env keeps no ANTHROPIC_BASE_URL and no bound key.
func TestHaikuWithEnvPutsEnvInSettings(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{stdout: haikuSuccessLine, status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron()})

	out, err := h.withEnv(haikuBoundEnv()).Call(context.Background(), haikuSchema, "input")
	if err != nil {
		t.Fatalf("bound Call: %v", err)
	}
	if got, want := string(out), `{"verdict":"ok"}`; got != want {
		t.Fatalf("structured_output = %s, want %s", got, want)
	}

	wantSettings := `{"disableAllHooks":true,"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9/ctx/example",` +
		`"CTX_EXAMPLE":"example"}}`
	if got, want := haikuReadFile(t, dir, "argv"), haikuWantArgv(wantSettings); got != want {
		t.Fatalf("argv:\n%s\nwant:\n%s", got, want)
	}
	if got := strings.TrimSpace(haikuReadFile(t, dir, "cwd")); got != "/" {
		t.Fatalf("cwd = %q, want /", got)
	}

	env := strings.Split(haikuReadFile(t, dir, "env"), "\n")
	var summarizer int
	marked := false
	for _, line := range env {
		switch {
		case strings.HasPrefix(line, "ANTHROPIC_BASE_URL="):
			t.Fatalf("process env kept %q; the bound base URL must reach claude only through --settings", line)
		case strings.HasPrefix(line, "CTX_EXAMPLE="):
			t.Fatalf("process env kept %q; the bound env must reach claude only through --settings", line)
		case line == "ORRERY_SUMMARIZER=1":
			summarizer++
		case line == "HAIKU_TEST_MARK=kept":
			marked = true
		}
	}
	if summarizer != 1 {
		t.Fatalf("ORRERY_SUMMARIZER=1 count = %d, want 1", summarizer)
	}
	if !marked {
		t.Fatalf("env dropped the caller's HAIKU_TEST_MARK")
	}
}

// TestHaikuWithEnvWithoutEnvKeepsTodaysArgv checks that a nil or empty env leaves the --settings value as h.Call's.
func TestHaikuWithEnvWithoutEnvKeepsTodaysArgv(t *testing.T) {
	for _, env := range []map[string]string{nil, {}} {
		dir := haikuDir(t)
		claude := haikuWriteFake(t, dir, haikuFake{stdout: haikuSuccessLine, status: 0})
		h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron()})

		if _, err := h.withEnv(env).Call(context.Background(), haikuSchema, "input"); err != nil {
			t.Fatalf("bound Call with env %v: %v", env, err)
		}
		if got, want := haikuReadFile(t, dir, "argv"), haikuWantArgv(`{"disableAllHooks":true}`); got != want {
			t.Fatalf("env %v: argv:\n%s\nwant:\n%s", env, got, want)
		}
	}
}

// TestHaikuWithEnvSharesCallSlots holds the only slot of h with a hung call and checks that a call bound to an env
// waits for that slot, never starts claude, and fails when its wait ends.
func TestHaikuWithEnvSharesCallSlots(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{extra: `: > "$dir/started"
sleep 30`, status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron(), Cap: 1, Timeout: 10 * time.Second})

	holdCtx, releaseHold := context.WithCancel(context.Background())
	defer releaseHold()
	held := make(chan error, 1)
	go func() {
		_, err := h.Call(holdCtx, haikuSchema, "hold")
		held <- err
	}()
	haikuAwaitFile(t, filepath.Join(dir, "started"), "holding call never started claude")

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelWait()
	bound := h.withEnv(haikuBoundEnv())
	start := time.Now()
	if _, err := bound.Call(waitCtx, haikuSchema, "waits"); err == nil {
		t.Fatalf("bound call with the slot held returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("bound call waited %v for a slot", elapsed)
	}
	if runs := haikuRuns(t, dir); runs != 1 {
		t.Fatalf("claude ran %d times, want 1: the waiting bound call must not start claude", runs)
	}

	releaseHold()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatalf("holding call did not end after its context ended")
	}
}

// haikuCallViaBaseURL and haikuCallViaModel are the base URL and model the CallVia tests pass.
const (
	haikuCallViaBaseURL = "http://patchbay.invalid/ctx/example"
	haikuCallViaModel   = "example-model"
)

// haikuCallViaEnv returns the --settings env the CallVia tests pass: the base URL they run against and one more key.
func haikuCallViaEnv() map[string]string {
	return map[string]string{
		"ANTHROPIC_BASE_URL": haikuCallViaBaseURL,
		"CTX_EXAMPLE":        "example",
	}
}

// TestHaikuCallViaRunsClaudeWithEnvBaseURLAndModel checks that CallVia runs claude with the given model, with the env
// in its --settings value, and with exactly one ANTHROPIC_BASE_URL, the given one, although the caller's environ holds
// a different base URL. The env key is not in the process env.
func TestHaikuCallViaRunsClaudeWithEnvBaseURLAndModel(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{stdout: haikuSuccessLine, status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron()})

	out, err := h.CallVia(context.Background(), haikuCallViaBaseURL, haikuCallViaModel, haikuCallViaEnv(), haikuSchema,
		"input")
	if err != nil {
		t.Fatalf("CallVia: %v", err)
	}
	if got, want := string(out), `{"verdict":"ok"}`; got != want {
		t.Fatalf("structured_output = %s, want %s", got, want)
	}

	wantArgv := []string{
		"-p",
		"--model", haikuCallViaModel,
		"--settings", `{"disableAllHooks":true,"env":{"ANTHROPIC_BASE_URL":"http://patchbay.invalid/ctx/example",` +
			`"CTX_EXAMPLE":"example"}}`,
		"--no-session-persistence",
		"--tools", "",
		"--max-turns", "1",
		"--output-format", "json",
		"--json-schema", haikuSchema,
	}
	wantArgvText := strings.Join(wantArgv, "\n") + "\n"
	if got := haikuReadFile(t, dir, "argv"); got != wantArgvText {
		t.Fatalf("argv:\n%s\nwant:\n%s", got, wantArgvText)
	}

	wantBase := []string{"ANTHROPIC_BASE_URL=" + haikuCallViaBaseURL}
	if got := haikuBaseURLEntries(t, dir); !slices.Equal(got, wantBase) {
		t.Fatalf("ANTHROPIC_BASE_URL entries = %q, want %q", got, wantBase)
	}
	if strings.Contains(haikuReadFile(t, dir, "env"), "CTX_EXAMPLE=") {
		t.Fatalf("process env kept CTX_EXAMPLE; the env must reach claude only through --settings")
	}
}

// TestHaikuCallViaEmptyBaseURLLeavesNoBaseURL checks that CallVia with no base URL drops the caller's
// ANTHROPIC_BASE_URL and sets none.
func TestHaikuCallViaEmptyBaseURLLeavesNoBaseURL(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{stdout: haikuSuccessLine, status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron()})

	if _, err := h.CallVia(context.Background(), "", haikuCallViaModel, nil, haikuSchema, "input"); err != nil {
		t.Fatalf("CallVia: %v", err)
	}
	if got := haikuBaseURLEntries(t, dir); len(got) != 0 {
		t.Fatalf("ANTHROPIC_BASE_URL entries = %q, want none", got)
	}
}

// TestHaikuCallViaEmptyModelRunsDefaultModel checks that CallVia with no model runs the default summary model.
func TestHaikuCallViaEmptyModelRunsDefaultModel(t *testing.T) {
	dir := haikuDir(t)
	claude := haikuWriteFake(t, dir, haikuFake{stdout: haikuSuccessLine, status: 0})
	h := NewHaiku(HaikuConfig{Claude: claude, Environ: haikuEnviron()})

	if _, err := h.CallVia(context.Background(), haikuCallViaBaseURL, "", nil, haikuSchema, "input"); err != nil {
		t.Fatalf("CallVia: %v", err)
	}
	argv := haikuReadFile(t, dir, "argv")
	if want := "--model\n" + protocol.DefaultSummaryModel + "\n"; !strings.Contains(argv, want) {
		t.Fatalf("argv does not carry %q:\n%s", want, argv)
	}
}

var _ haikuCaller = (*Haiku)(nil)
