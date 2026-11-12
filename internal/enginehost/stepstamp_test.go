package enginehost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stepStampValid is the settings file of a stamped folder: a stamp whose env carries a second key beside its base URL.
const stepStampValid = `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9/ctx/example","ORRERY_STAMP_MARK":"kept"}}`

// stepStampSettings is the --settings value of a call from a folder under stepStampValid.
const stepStampSettings = `{"disableAllHooks":true,"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9/ctx/example",` +
	`"ORRERY_STAMP_MARK":"kept"}}`

// stepStampPlain is the --settings value of a call from a folder no stamp applies to.
const stepStampPlain = `{"disableAllHooks":true}`

// stepStampTestHome returns a synthetic home under t.TempDir() that holds the repository home/stamped/repo. When body is
// not empty it is written to home/stamped/.claude/settings.local.json, which stamps home/stamped. It returns home and the
// repository.
func stepStampTestHome(t *testing.T, body string) (string, string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home := t.TempDir()
	stamped := filepath.Join(home, "stamped")
	if body != "" {
		stepGitTestWrite(t, stamped, filepath.Join(".claude", "settings.local.json"), body)
	}
	repo := filepath.Join(stamped, "repo")
	stepGitTestWrite(t, repo, "README", "base\n")
	stepGitTestRun(t, repo, "init", "-q", "-b", "main")
	stepGitTestRun(t, repo, "add", "README")
	stepGitTestRun(t, repo, "commit", "-q", "-m", "base")
	return home, repo
}

// stepStampTestLinked adds a detached linked worktree of repo at home/work/linked and returns its path with symlinks
// resolved, the form stamp.Find looks it up under.
func stepStampTestLinked(t *testing.T, home, repo string) string {
	t.Helper()
	path := filepath.Join(home, "work", "linked")
	stepGitTestRun(t, repo, "worktree", "add", "--detach", path, "HEAD")
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// stepStampStart returns a start with a two-minute model limit: a writing step in a linked worktree of repo, or a
// read-only step of repo.
func stepStampStart(t *testing.T, home, repo string, readOnly bool) StepStart {
	t.Helper()
	var start StepStart
	if readOnly {
		start = stepStartReadOnly(repo, "")
	} else {
		start = stepStartWriting(stepStampTestLinked(t, home, repo))
	}
	start.ModelLimit = 2 * time.Minute
	return start
}

// newStepStampFixture returns the wiring fixture of newStepWireFixture with environ as the Steps' environment, worktrees
// as the directory read-only worktrees are added under, and haiku making the calls.
func newStepStampFixture(t *testing.T, environ []string, worktrees string, haiku *Haiku) *stepWireFixture {
	t.Helper()
	f := newStepWireFixture(t, haiku)
	f.steps.environ = environ
	f.steps.worktrees = worktrees
	return f
}

// stepStampTestSettings returns the --settings value of the one call the fake claude in dir ran.
func stepStampTestSettings(t *testing.T, dir string) string {
	t.Helper()
	argv := strings.Split(haikuReadFile(t, dir, "argv"), "\n")
	for i, arg := range argv {
		if arg == "--settings" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	t.Fatalf("the fake claude's argv has no --settings value: %q", argv)
	return ""
}

func TestStepsStartGivesEachStepItsFolderStamp(t *testing.T) {
	cases := []struct {
		name     string
		readOnly bool
		body     string
		noHome   bool
		want     string
	}{
		{name: "writing step under a stamp", body: stepStampValid, want: stepStampSettings},
		{name: "read-only step under a stamp", readOnly: true, body: stepStampValid, want: stepStampSettings},
		{name: "writing step under no stamp", want: stepStampPlain},
		{name: "read-only step under no stamp", readOnly: true, want: stepStampPlain},
		{name: "writing step with no HOME entry", body: stepStampValid, noHome: true, want: stepStampPlain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, repo := stepStampTestHome(t, tc.body)
			dir := haikuDir(t)
			h := NewHaiku(HaikuConfig{Claude: haikuWriteFake(t, dir, haikuFake{stdout: stepWireMonitorLine}),
				Environ: haikuEnviron()})
			environ := []string{"PATH=" + os.Getenv("PATH")}
			if !tc.noHome {
				environ = append(environ, "HOME="+home)
			}
			f := newStepStampFixture(t, environ, filepath.Join(home, "worktrees"), h)

			id, err := f.steps.Start(t.Context(), stepStampStart(t, home, repo, tc.readOnly))
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			f.clock.set(2 * time.Minute)

			stepStartAwait(t, "the overtime end", func() bool {
				row, err := f.store.Step(t.Context(), id)
				return err == nil && row.End != ""
			})
			row, err := f.store.Step(t.Context(), id)
			if err != nil {
				t.Fatalf("read step: %v", err)
			}
			if row.End != "overtime" {
				t.Fatalf("step ended %q with reason %q, want overtime", row.End, row.Reason)
			}
			if runs := haikuRuns(t, dir); runs != 1 {
				t.Fatalf("haiku claude ran %d times, want one monitor call", runs)
			}
			if got := stepStampTestSettings(t, dir); got != tc.want {
				t.Errorf("monitor --settings is %s, want %s", got, tc.want)
			}
		})
	}
}

func TestStepsStartRefusesStepUnderUnparseableStamp(t *testing.T) {
	cases := []struct {
		name     string
		readOnly bool
	}{
		{name: "writing step", readOnly: false},
		{name: "read-only step", readOnly: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, repo := stepStampTestHome(t, "{")
			worktrees := filepath.Join(home, "worktrees")
			if err := os.Mkdir(worktrees, 0o755); err != nil {
				t.Fatal(err)
			}
			dir := haikuDir(t)
			h := NewHaiku(HaikuConfig{Claude: haikuWriteFake(t, dir, haikuFake{stdout: stepWireMonitorLine}),
				Environ: haikuEnviron()})
			f := newStepStampFixture(t, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}, worktrees, h)

			id, err := f.steps.Start(t.Context(), stepStampStart(t, home, repo, tc.readOnly))
			if err == nil {
				t.Fatalf("Start = %q, nil error; want a refusal", id)
			}
			if !strings.Contains(err.Error(), "settings.local.json") {
				t.Fatalf("Start error = %v, want it to name the stamp's settings file", err)
			}
			if id != "" {
				t.Errorf("Start id = %q on refusal, want empty", id)
			}
			if n := f.sessions.startCalls(); n != 0 {
				t.Errorf("StartStep calls = %d, want 0", n)
			}
			rows, err := f.store.OpenSteps(t.Context())
			if err != nil {
				t.Fatalf("OpenSteps: %v", err)
			}
			if len(rows) != 0 {
				t.Errorf("stored %d step rows on refusal, want none", len(rows))
			}
			entries, err := os.ReadDir(worktrees)
			if err != nil {
				t.Fatalf("read worktrees dir: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("refusal left %d entries under the worktrees dir, want none", len(entries))
			}
			if runs := haikuRuns(t, dir); runs != 0 {
				t.Errorf("haiku claude ran %d times on refusal, want none", runs)
			}
		})
	}
}
