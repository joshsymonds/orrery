package capture

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliFakeSession is the session id the fake claude reports. A sanitized fixture must not contain it.
const cliFakeSession = "00000000-0000-4000-8000-000000000009"

// cliFakeClaude returns an sh script that stands in for claude. It answers the initialize,
// set_permission_mode and interrupt requests with the runner's fixed ids. It prints one result for each
// user line. For a probe that passes --permission-prompt-tool, it first sends a can_use_tool request for
// the tool named in its arguments and reads the answer. extra is one shell command run after the
// initialize reply.
func cliFakeClaude(extra string) string {
	return `#!/bin/sh
prompting=no
tool=Write
input='{"file_path":"/home/user/note.txt","content":"ok"}'
case "$*" in
  *--permission-prompt-tool*) prompting=yes ;;
esac
case "$*" in
  *AskUserQuestion*)
    tool=AskUserQuestion
    input='{"questions":[{"question":"Which letter do you prefer?","options":[{"label":"A"},{"label":"B"}]}]}'
    ;;
esac
while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*)
      printf '%s\n' '{"type":"system","subtype":"init","session_id":"` + cliFakeSession + `"}'
      printf '%s\n' '{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}'
      printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"req_init"}}'
      ` + extra + `
      ;;
    *'"subtype":"set_permission_mode"'*)
      printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"req_mode"}}'
      ;;
    *'numbers from 1 to 400'*)
      printf '%s\n' '{"type":"stream_event","event":{"type":"message_start"}}'
      IFS= read -r interrupt
      printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"req_interrupt"}}'
      printf '%s\n' '{"type":"result","subtype":"success"}'
      ;;
    *'"type":"user"'*)
      if [ "$prompting" = yes ]; then
        printf '%s\n' '{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool","tool_name":"'"$tool"'","input":'"$input"',"tool_use_id":"toolu_1"}}'
        IFS= read -r answer
      fi
      printf '%s\n' '{"type":"result","subtype":"success"}'
      ;;
  esac
done
`
}

// cliFakePath writes script to an executable file in a fresh directory and returns its path.
func cliFakePath(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}

// cliConfig returns a Config that finds claude at path and captures both output streams.
func cliConfig(path string) (Config, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return Config{
		Environ:  []string{"PATH=" + os.Getenv("PATH")},
		LookPath: func(string) (string, error) { return path, nil },
		Stdout:   &stdout,
		Stderr:   &stderr,
	}, &stdout, &stderr
}

// cliRequireFile fails the test unless path is a regular file with exactly the given permissions.
func cliRequireFile(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != perm {
		t.Errorf("%s mode = %v, want regular file with %v", path, info.Mode(), perm)
	}
}

// cliRequireDir fails the test unless path is a directory with exactly the given permissions.
func cliRequireDir(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.IsDir() || info.Mode().Perm() != perm {
		t.Errorf("%s mode = %v, want directory with %v", path, info.Mode(), perm)
	}
}

// cliRequireEmpty fails the test when dir holds any entry.
func cliRequireEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("%s holds %d entries, want none", dir, len(entries))
	}
}

func TestCliRecordWritesThreeFilesPerProbe(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	cfg, stdout, stderr := cliConfig(cliFakePath(t, cliFakeClaude("")))
	raw := filepath.Join(t.TempDir(), "raw")

	if code := Main([]string{"record", "-raw", raw}, cfg); code != 0 {
		t.Fatalf("Main(record) = %d, want 0; stderr: %s", code, stderr)
	}
	for _, probe := range Probes() {
		for _, suffix := range []string{".out.ndjson", ".in.ndjson", ".stderr"} {
			cliRequireFile(t, filepath.Join(raw, probe.Name+suffix), 0o600)
		}
	}
	cliRequireEmpty(t, scratch)
	if lines := strings.Count(stdout.String(), "\n"); lines != len(Probes()) {
		t.Errorf("summary has %d lines, want one per probe (%d):\n%s", lines, len(Probes()), stdout)
	}
}

func TestCliRecordRunsOnlyTheNamedProbe(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cfg, _, stderr := cliConfig(cliFakePath(t, cliFakeClaude("")))
	raw := t.TempDir()

	if code := Main([]string{"record", "-raw", raw, "-probe", "two-turns"}, cfg); code != 0 {
		t.Fatalf("Main(record -probe two-turns) = %d, want 0; stderr: %s", code, stderr)
	}
	cliRequireFile(t, filepath.Join(raw, "two-turns.out.ndjson"), 0o600)
	if _, err := os.Stat(filepath.Join(raw, "interrupt.out.ndjson")); !os.IsNotExist(err) {
		t.Errorf("interrupt.out.ndjson stat error = %v, want not exist", err)
	}
}

func TestCliRecordExitsOneWhenAProbeCannotStart(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cfg, stdout, _ := cliConfig("claude")
	raw := t.TempDir()

	if code := Main([]string{"record", "-raw", raw, "-probe", "two-turns"}, cfg); code != 1 {
		t.Fatalf("Main(record) = %d, want 1 for a relative claude path", code)
	}
	if !strings.Contains(stdout.String(), "two-turns") || !strings.Contains(stdout.String(), "error") {
		t.Errorf("summary = %q, want the probe named with its error", stdout)
	}
}

func TestCliSanitiseWritesFixturesWithoutRawIDsOrRateLimitLines(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cfg, _, stderr := cliConfig(cliFakePath(t, cliFakeClaude("")))
	raw := t.TempDir()
	fixtures := filepath.Join(t.TempDir(), "fixtures")

	if code := Main([]string{"record", "-raw", raw}, cfg); code != 0 {
		t.Fatalf("Main(record) = %d, want 0; stderr: %s", code, stderr)
	}
	if code := Main([]string{"sanitize", "-raw", raw, "-out", fixtures}, cfg); code != 0 {
		t.Fatalf("Main(sanitize) = %d, want 0; stderr: %s", code, stderr)
	}
	for _, probe := range Probes() {
		rawOut := cliReadFile(t, filepath.Join(raw, probe.Name+".out.ndjson"))
		if !strings.Contains(rawOut, cliFakeSession) || !strings.Contains(rawOut, "rate_limit_event") {
			t.Fatalf("%s raw output lacks the session id or the rate_limit_event line the test relies on",
				probe.Name)
		}
		for _, suffix := range []string{".out.ndjson", ".in.ndjson"} {
			path := filepath.Join(fixtures, probe.Name+suffix)
			cliRequireFile(t, path, 0o644)
			fixture := cliReadFile(t, path)
			if strings.Contains(fixture, cliFakeSession) {
				t.Errorf("%s holds the raw session id", path)
			}
			if strings.Contains(fixture, "rate_limit_event") {
				t.Errorf("%s keeps a rate_limit_event line", path)
			}
		}
		if !strings.Contains(cliReadFile(t, filepath.Join(fixtures, probe.Name+".out.ndjson")), `"type":"result"`) {
			t.Errorf("%s fixture has no result line", probe.Name)
		}
	}
}

// cliReadFile returns the contents of path, failing the test on a read error.
func cliReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestCliAcceptPassesGoodFakeAndRemovesItsDirectory(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	cfg, stdout, stderr := cliConfig(cliFakePath(t, cliFakeClaude("")))

	if code := Main([]string{"accept"}, cfg); code != 0 {
		t.Fatalf("Main(accept) = %d, want 0; stdout: %s stderr: %s", code, stdout, stderr)
	}
	cliRequireEmpty(t, scratch)
}

func TestCliAcceptKeepsRawOutputInTheNamedDirectory(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	cfg, stdout, stderr := cliConfig(cliFakePath(t, cliFakeClaude("")))
	keep := filepath.Join(t.TempDir(), "kept")

	if code := Main([]string{"accept", "-keep", keep}, cfg); code != 0 {
		t.Fatalf("Main(accept -keep) = %d, want 0; stdout: %s stderr: %s", code, stdout, stderr)
	}
	cliRequireDir(t, keep, 0o700)
	cliRequireEmpty(t, scratch)
	for _, probe := range Probes() {
		for _, suffix := range []string{".out.ndjson", ".in.ndjson", ".stderr"} {
			cliRequireFile(t, filepath.Join(keep, probe.Name+suffix), 0o600)
		}
	}
}

func TestCliAcceptFailsOnNonEnvelopeLineAndRemovesItsDirectory(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	cfg, stdout, _ := cliConfig(cliFakePath(t, cliFakeClaude(`printf '%s\n' 'not json'`)))

	if code := Main([]string{"accept"}, cfg); code != 1 {
		t.Fatalf("Main(accept) = %d, want 1 for a non-JSON line; stdout: %s", code, stdout)
	}
	cliRequireEmpty(t, scratch)
}

func TestCliUsageErrorsExitTwo(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no subcommand", args: nil},
		{name: "unknown subcommand", args: []string{"replay"}},
		{name: "record without raw", args: []string{"record"}},
		{name: "sanitize without out", args: []string{"sanitize", "-raw", "x"}},
		{name: "unknown probe", args: []string{"accept", "-probe", "nope"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _, stderr := cliConfig(cliFakePath(t, cliFakeClaude("")))

			if code := Main(tc.args, cfg); code != 2 {
				t.Fatalf("Main(%q) = %d, want 2", tc.args, code)
			}
			if !strings.Contains(stderr.String(), "usage") {
				t.Errorf("stderr = %q, want usage", stderr)
			}
		})
	}
}
