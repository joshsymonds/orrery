package capture

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cliFakeSession is the session id the fake claude reports. A sanitized fixture must not contain it.
const cliFakeSession = "00000000-0000-4000-8000-000000000009"

// cliOtherSession is a session id that differs from cliFakeSession.
const cliOtherSession = "00000000-0000-4000-8000-000000000001"

// cliFake describes the fake claude. extra is one shell command run after the initialize reply. source is
// the session id a run without --resume reports, resumed the session id a run with --resume reports, and
// guard the text the Bash result of the guard prompt carries.
type cliFake struct {
	extra   string
	guard   string
	source  string
	resumed string
}

// cliWellBehaved() is the fake that answers every probe the way Claude Code does.
func cliWellBehaved() cliFake {
	return cliFake{guard: GuardRefusal, source: cliFakeSession, resumed: cliFakeSession}
}

// cliExtra returns the well-behaved fake with extra run after its initialize reply.
func cliExtra(extra string) cliFake {
	fake := cliWellBehaved()
	fake.extra = extra
	return fake
}

// cliFakeClaude returns an sh script that stands in for claude. For each run it appends one line to
// <script>.log: the working directory, the ORRERY_SUMMARIZER and TMUX values it sees, and its arguments.
// It answers the initialize, set_permission_mode and interrupt requests with the runner's fixed ids. It
// prints one result for each user line. A run with a permission prompt tool first sends a can_use_tool
// request for the tool its arguments name and reads the answer.
func cliFakeClaude(f cliFake) string {
	return `#!/bin/sh
prompting=no
tool=Write
input='{"file_path":"/home/user/note.txt","content":"ok"}'
session='` + f.source + `'
printf '%s|%s|%s|%s\n' "$(pwd -P)" "${ORRERY_SUMMARIZER-unset}" "${TMUX-unset}" "$*" >> "$0.log"
case "$*" in
  *--permission-prompt-tool*) prompting=yes ;;
esac
case "$*" in
  *AskUserQuestion*)
    tool=AskUserQuestion
    input='{"questions":[{"question":"Which letter do you prefer?","options":[{"label":"A"},{"label":"B"}]}]}'
    ;;
  *"--tools Bash"*)
    tool=Bash
    input='{"command":"echo ok"}'
    ;;
esac
case "$*" in
  *--resume*) session='` + f.resumed + `' ;;
esac
while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*)
      printf '%s\n' '{"type":"system","subtype":"init","session_id":"'"$session"'"}'
      printf '%s\n' '{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}'
      printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"req_init"}}'
      ` + f.extra + `
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
      reply=DONE
      case "$line" in
        *'empty scratch directory'*)
          printf '%s\n' '{"type":"user","message":{"role":"user","content":"` + f.guard + `"}}'
          ;;
        *'git --version'*)
          printf '%s\n' '{"type":"user","message":{"role":"user","content":"git version 2.47.0"}}'
          ;;
        *run_in_background*)
          printf '%s\n' '{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"sleep 20","run_in_background":true}}]}}'
          printf '%s\n' '{"type":"result","subtype":"success"}'
          exit 0
          ;;
        *'the word AFTER'*) reply=AFTER ;;
      esac
      case "$*" in
        *--resume*) reply=QUILLFEATHER ;;
      esac
      printf '%s\n' '{"type":"result","subtype":"success","result":"'"$reply"'"}'
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

// cliConfig returns a Config that finds claude at path, starts the real-settings probes from path through
// the login shell, and captures both output streams.
func cliConfig(path string) (Config, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return Config{
		Environ:  []string{"PATH=" + os.Getenv("PATH")},
		LookPath: func(string) (string, error) { return path, nil },
		Daemon:   func() (Daemon, error) { return cliDaemon(path) },
		Stdout:   &stdout,
		Stderr:   &stderr,
	}, &stdout, &stderr
}

// cliShellLog returns the file that the shell wrapper of the fake at path appends to, one line per start.
func cliShellLog(path string) string {
	return filepath.Join(filepath.Dir(path), "shell.log")
}

// cliDaemon returns the daemon that starts path through the shell sh, with the directory of path as the
// daemon's home and runtime directory. The shell is a wrapper that logs "login" or "plain" to cliShellLog
// for each start, according to whether its first argument is -l, and then execs sh with the same arguments.
func cliDaemon(path string) (Daemon, error) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		return Daemon{}, err
	}
	dir := filepath.Dir(path)
	wrapper := filepath.Join(dir, "shell")
	script := `#!/bin/sh
if [ "$1" = "-l" ]; then form=login; else form=plain; fi
printf '%s\n' "$form" >> '` + cliShellLog(path) + `'
exec '` + shell + `' "$@"
`
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		return Daemon{}, err
	}
	return Daemon{Shell: wrapper, Claude: path, Tmux: path, Home: dir, User: "orrery", Lang: "C", RuntimeDir: dir}, nil
}

// cliFailingDaemon returns a Config.Daemon that always fails.
func cliFailingDaemon() (Daemon, error) {
	return Daemon{}, errors.New("no daemon for test")
}

// cliRun is one run of the fake claude, as the fake logged it.
type cliRun struct {
	dir        string
	summarizer string
	tmux       string
	args       string
}

// cliReadRuns returns every run that the fake claude at path logged, in order.
func cliReadRuns(t *testing.T, path string) []cliRun {
	t.Helper()
	logged := strings.Split(strings.TrimSuffix(cliReadFile(t, path+".log"), "\n"), "\n")
	runs := make([]cliRun, 0, len(logged))
	for _, line := range logged {
		fields := strings.SplitN(line, "|", 4)
		if len(fields) != 4 {
			t.Fatalf("fake log line %q has %d fields, want 4", line, len(fields))
		}
		runs = append(runs, cliRun{dir: fields[0], summarizer: fields[1], tmux: fields[2], args: fields[3]})
	}
	return runs
}

// cliSettingsRuns returns the runs of the real-settings set. A run of the hooks-off set carries the
// --settings flag, and a run of the real-settings set never does.
func cliSettingsRuns(runs []cliRun) []cliRun {
	var settings []cliRun
	for _, run := range runs {
		if !strings.Contains(run.args, "--settings ") {
			settings = append(settings, run)
		}
	}
	return settings
}

// cliLine returns the summary line of the probe named name, or fails the test when there is none.
func cliLine(t *testing.T, stdout, name string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, name+": ") {
			return line
		}
	}
	t.Fatalf("no summary line for %s in:\n%s", name, stdout)
	return ""
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
	cfg, stdout, stderr := cliConfig(cliFakePath(t, cliFakeClaude(cliWellBehaved())))
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
	cfg, _, stderr := cliConfig(cliFakePath(t, cliFakeClaude(cliWellBehaved())))
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
	cfg, _, stderr := cliConfig(cliFakePath(t, cliFakeClaude(cliWellBehaved())))
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
	cfg, stdout, stderr := cliConfig(cliFakePath(t, cliFakeClaude(cliWellBehaved())))

	if code := Main([]string{"accept"}, cfg); code != 0 {
		t.Fatalf("Main(accept) = %d, want 0; stdout: %s stderr: %s", code, stdout, stderr)
	}
	cliRequireEmpty(t, scratch)
}

func TestCliAcceptRunsSettingsSetAfterHooksOffSet(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cfg, stdout, stderr := cliConfig(cliFakePath(t, cliFakeClaude(cliWellBehaved())))

	if code := Main([]string{"accept"}, cfg); code != 0 {
		t.Fatalf("Main(accept) = %d, want 0; stdout: %s stderr: %s", code, stdout, stderr)
	}
	all := append(Probes(), SettingsProbes()...)
	names := make([]string, 0, len(all))
	for _, probe := range all {
		names = append(names, probe.Name)
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != len(names) {
		t.Fatalf("summary has %d lines, want %d:\n%s", len(lines), len(names), stdout)
	}
	for index, name := range names {
		if !strings.HasPrefix(lines[index], name+": completed; exit 0; missing none; ") {
			t.Errorf("summary line %d = %q, want %s completed with every expectation met", index, lines[index], name)
		}
	}
	if guard := cliLine(t, stdout.String(), "guard"); !strings.Contains(guard, "answered can_use_tool Bash") {
		t.Errorf("guard summary = %q, want the Bash permission named as answered", guard)
	}
}

func TestCliAcceptSettingsRunInFreshScratchDirectoriesWithMarker(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	path := cliFakePath(t, cliFakeClaude(cliWellBehaved()))
	cfg, stdout, stderr := cliConfig(path)

	if code := Main([]string{"accept"}, cfg); code != 0 {
		t.Fatalf("Main(accept) = %d, want 0; stdout: %s stderr: %s", code, stdout, stderr)
	}
	runs := cliSettingsRuns(cliReadRuns(t, path))
	if len(runs) != len(SettingsProbes()) {
		t.Fatalf("real-settings runs = %d, want %d", len(runs), len(SettingsProbes()))
	}
	resolved, err := filepath.EvalSymlinks(scratch)
	if err != nil {
		t.Fatalf("resolve scratch: %v", err)
	}
	for _, run := range runs {
		if run.summarizer != "1" {
			t.Errorf("run %q: ORRERY_SUMMARIZER = %q, want 1", run.args, run.summarizer)
		}
		if run.tmux != "unset" {
			t.Errorf("run %q: TMUX = %q, want unset", run.args, run.tmux)
		}
		if filepath.Dir(run.dir) != resolved {
			t.Errorf("run %q: directory %s is not a fresh directory in TMPDIR %s", run.args, run.dir, resolved)
		}
	}
	// Index 1 is resume-plant and index 2 is resume-ask, which runs in the directory of resume-plant.
	seen := map[string]bool{}
	for index, run := range runs {
		if index == 2 {
			continue
		}
		if seen[run.dir] {
			t.Errorf("two real-settings runs share directory %s", run.dir)
		}
		seen[run.dir] = true
	}
	cliRequireEmpty(t, scratch)
}

func TestCliAcceptStartsEverySettingsProbeInLoginForm(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	path := cliFakePath(t, cliFakeClaude(cliWellBehaved()))
	cfg, stdout, stderr := cliConfig(path)

	if code := Main([]string{"accept"}, cfg); code != 0 {
		t.Fatalf("Main(accept) = %d, want 0; stdout: %s stderr: %s", code, stdout, stderr)
	}
	forms := strings.Split(strings.TrimSuffix(cliReadFile(t, cliShellLog(path)), "\n"), "\n")
	if len(forms) != len(SettingsProbes()) {
		t.Fatalf("shell starts = %d, want one per real-settings probe (%d)", len(forms), len(SettingsProbes()))
	}
	for index, form := range forms {
		if form != "login" {
			t.Errorf("shell start %d began in the %s form, want login", index, form)
		}
	}
}

func TestCliAcceptResumeRunsInSourceDirectoryWithSourceSession(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	path := cliFakePath(t, cliFakeClaude(cliWellBehaved()))
	cfg, stdout, stderr := cliConfig(path)

	if code := Main([]string{"accept"}, cfg); code != 0 {
		t.Fatalf("Main(accept) = %d, want 0; stdout: %s stderr: %s", code, stdout, stderr)
	}
	runs := cliSettingsRuns(cliReadRuns(t, path))
	if len(runs) != len(SettingsProbes()) {
		t.Fatalf("real-settings runs = %d, want %d", len(runs), len(SettingsProbes()))
	}
	plant, ask := runs[1], runs[2]
	if strings.Contains(plant.args, "--resume") {
		t.Errorf("resume-plant args %q carry --resume", plant.args)
	}
	if !strings.Contains(ask.args, "--resume "+cliFakeSession) {
		t.Errorf("resume-ask args %q lack --resume with the source session", ask.args)
	}
	if ask.dir != plant.dir {
		t.Errorf("resume-ask ran in %s, want the source directory %s", ask.dir, plant.dir)
	}
	if line := cliLine(t, stdout.String(), "resume-ask"); !strings.HasSuffix(line, "; session same") {
		t.Errorf("resume-ask summary = %q, want it to end with session same", line)
	}
}

func TestCliAcceptNamesResumedSessionChangeOrMissingID(t *testing.T) {
	tests := []struct {
		name    string
		resumed string
		want    string
	}{
		{name: "changed", resumed: cliOtherSession, want: "; session changed"},
		{name: "unknown", resumed: "", want: "; session unknown: capture: system init line has no session_id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			fake := cliWellBehaved()
			fake.resumed = tc.resumed
			cfg, stdout, _ := cliConfig(cliFakePath(t, cliFakeClaude(fake)))

			Main([]string{"accept"}, cfg)
			if line := cliLine(t, stdout.String(), "resume-ask"); !strings.HasSuffix(line, tc.want) {
				t.Errorf("resume-ask summary = %q, want it to end with %q", line, tc.want)
			}
		})
	}
}

func TestCliAcceptResumeNotStartedWithoutSourceSession(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	fake := cliWellBehaved()
	fake.source = ""
	path := cliFakePath(t, cliFakeClaude(fake))
	cfg, stdout, _ := cliConfig(path)

	if code := Main([]string{"accept"}, cfg); code != 1 {
		t.Fatalf("Main(accept) = %d, want 1 when resume-ask cannot read its source session", code)
	}
	if runs := cliSettingsRuns(cliReadRuns(t, path)); len(runs) != len(SettingsProbes())-1 {
		t.Errorf("real-settings runs = %d, want %d: resume-ask must not start", len(runs), len(SettingsProbes())-1)
	}
	want := "exit not started; missing none; unknown none; unanswered none; answered none; " +
		"error: capture: prepare resume-ask: capture: system init line has no session_id"
	if line := cliLine(t, stdout.String(), "resume-ask"); !strings.HasSuffix(line, want) {
		t.Errorf("resume-ask summary = %q, want it to end with %q", line, want)
	}
}

func TestCliAcceptGuardMustNameItsRefusal(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	fake := cliWellBehaved()
	fake.guard = "fatal: not a git repository (or any of the parent directories): .git"
	cfg, stdout, _ := cliConfig(cliFakePath(t, cliFakeClaude(fake)))

	if code := Main([]string{"accept"}, cfg); code != 1 {
		t.Fatalf("Main(accept) = %d, want 1 when the guard answers with git's own error", code)
	}
	want := "missing user containing " + strconv.Quote(GuardRefusal)
	if line := cliLine(t, stdout.String(), "guard"); !strings.Contains(line, want) {
		t.Errorf("guard summary = %q, want it to contain %q", line, want)
	}
	if line := cliLine(t, stdout.String(), "resume-plant"); !strings.Contains(line, "completed") {
		t.Errorf("resume-plant summary = %q, want the set to go on after a failing probe", line)
	}
}

func TestCliAcceptRefusesSettingsScratchInsideRepository(t *testing.T) {
	path := cliFakePath(t, cliFakeClaude(cliWellBehaved()))
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatalf("make .git: %v", err)
	}
	t.Setenv("TMPDIR", repo)
	cfg, stdout, _ := cliConfig(path)

	if code := Main([]string{"accept"}, cfg); code != 1 {
		t.Fatalf("Main(accept) = %d, want 1 when scratch is inside a repository", code)
	}
	for _, probe := range SettingsProbes() {
		line := cliLine(t, stdout.String(), probe.Name)
		if !strings.Contains(line, "exit not started") || !strings.Contains(line, "; error: ") {
			t.Errorf("%s summary = %q, want it not started with an error", probe.Name, line)
		}
		if probe.ResumeOf == "" && !strings.Contains(line, "is inside a git repository") {
			t.Errorf("%s summary = %q, want the refusal as inside a repository", probe.Name, line)
		}
	}
	if runs := cliSettingsRuns(cliReadRuns(t, path)); len(runs) != 0 {
		t.Errorf("real-settings runs = %d, want none", len(runs))
	}
	entries, err := os.ReadDir(repo)
	if err != nil {
		t.Fatalf("read repo: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("repo holds %d entries, want only .git", len(entries))
	}
}

func TestCliAcceptFailingDaemonStartsNoSettingsProbe(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	path := cliFakePath(t, cliFakeClaude(cliWellBehaved()))
	cfg, stdout, stderr := cliConfig(path)
	cfg.Daemon = cliFailingDaemon

	if code := Main([]string{"accept"}, cfg); code != 1 {
		t.Fatalf("Main(accept) = %d, want 1 when the daemon cannot be found", code)
	}
	if lines := strings.Count(stdout.String(), "\n"); lines != len(Probes()) {
		t.Errorf("summary has %d lines, want only the hooks-off set (%d):\n%s", lines, len(Probes()), stdout)
	}
	if !strings.Contains(stderr.String(), "no daemon for test") {
		t.Errorf("stderr = %q, want the daemon error", stderr)
	}
	if runs := cliSettingsRuns(cliReadRuns(t, path)); len(runs) != 0 {
		t.Errorf("real-settings runs = %d, want none", len(runs))
	}
}

func TestCliAcceptProbeFlagSkipsSettingsSet(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cfg, stdout, stderr := cliConfig(cliFakePath(t, cliFakeClaude(cliWellBehaved())))
	cfg.Daemon = cliFailingDaemon

	if code := Main([]string{"accept", "-probe", "two-turns"}, cfg); code != 0 {
		t.Fatalf("Main(accept -probe two-turns) = %d, want 0; stdout: %s stderr: %s", code, stdout, stderr)
	}
	if lines := strings.Count(stdout.String(), "\n"); lines != 1 {
		t.Errorf("summary has %d lines, want the one probe:\n%s", lines, stdout)
	}
	cliLine(t, stdout.String(), "two-turns")
}

func TestCliSummaryNamesAnsweredExpectationsAndSession(t *testing.T) {
	result := cliProbe{
		name:    "guard",
		started: true,
		outcome: Outcome{Completed: true, Answered: []string{"can_use_tool Bash"}},
		report: Report{Missing: []Expectation{
			{Type: "user", Contains: "blocked"},
			{Type: "result", Subtype: "success", Count: 2},
		}},
		session: "session changed",
	}
	want := `guard: completed; exit 0; missing user containing "blocked", result/success x2; ` +
		`unknown none; unanswered none; answered can_use_tool Bash; session changed`
	if got := cliSummary(result); got != want {
		t.Errorf("cliSummary = %q, want %q", got, want)
	}
}

func TestCliAcceptKeepsRawOutputInTheNamedDirectory(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	cfg, stdout, stderr := cliConfig(cliFakePath(t, cliFakeClaude(cliWellBehaved())))
	keep := filepath.Join(t.TempDir(), "kept")

	if code := Main([]string{"accept", "-keep", keep}, cfg); code != 0 {
		t.Fatalf("Main(accept -keep) = %d, want 0; stdout: %s stderr: %s", code, stdout, stderr)
	}
	cliRequireDir(t, keep, 0o700)
	cliRequireEmpty(t, scratch)
	for _, probe := range append(Probes(), SettingsProbes()...) {
		for _, suffix := range []string{".out.ndjson", ".in.ndjson", ".stderr"} {
			cliRequireFile(t, filepath.Join(keep, probe.Name+suffix), 0o600)
		}
	}
}

func TestCliAcceptFailsOnNonEnvelopeLineAndRemovesItsDirectory(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	cfg, stdout, _ := cliConfig(cliFakePath(t, cliFakeClaude(cliExtra(`printf '%s\n' 'not json'`))))

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
			cfg, _, stderr := cliConfig(cliFakePath(t, cliFakeClaude(cliWellBehaved())))

			if code := Main(tc.args, cfg); code != 2 {
				t.Fatalf("Main(%q) = %d, want 2", tc.args, code)
			}
			if !strings.Contains(stderr.String(), "usage") {
				t.Errorf("stderr = %q, want usage", stderr)
			}
		})
	}
}
