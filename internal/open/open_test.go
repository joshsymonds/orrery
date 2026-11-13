package open

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Every test tmux server starts with -f /dev/null so the user's tmux config never applies.
const (
	noConfig     = "/dev/null"
	testCommand  = "sleep 300"
	activityTick = 1100 * time.Millisecond // session_activity has one-second resolution
)

// newClient returns a client for a unique private server name. The server is started only by the
// tests that create sessions; cleanup kills it when it exists.
func newClient(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-test-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		err := tmuxtest.Stop(t, tmuxctl.Server{Name: name})
		if err == nil {
			return
		}
		msg := err.Error()
		if !strings.Contains(msg, "no server running") && !strings.Contains(msg, "error connecting to") {
			t.Errorf("kill-server: %v", err)
		}
	})
	return client
}

func mustRun(t *testing.T, client *tmuxctl.Client, args ...string) string {
	t.Helper()
	out, err := client.Run(t.Context(), args...)
	if err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
	return out
}

// startSession creates detached session name in dir running a long-lived command, starting the server if needed.
func startSession(t *testing.T, client *tmuxctl.Client, name, dir string) {
	t.Helper()
	mustRun(t, client, "-f", noConfig, "new-session", "-d", "-s", name, "-c", dir, testCommand)
}

// setDir sets @orrery_dir on session name, as the session-scoped option orrery open reads.
func setDir(t *testing.T, client *tmuxctl.Client, name, dir string) {
	t.Helper()
	mustRun(t, client, "set-option", "-t", name, protocol.OptionDir, dir)
}

func config(client *tmuxctl.Client) Config {
	return Config{Tmux: client, Claude: "unused-claude"}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func resolve(t *testing.T, cfg Config, req Request) Plan {
	t.Helper()
	plan, err := Resolve(t.Context(), cfg, req)
	if err != nil {
		t.Fatalf("Resolve(%+v): %v", req, err)
	}
	return plan
}

func TestListSessionsServerNotRunningIsEmpty(t *testing.T) {
	client := newClient(t)
	sessions, err := listSessions(t.Context(), config(client))
	if err != nil {
		t.Fatalf("listSessions with no server: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("listSessions with no server = %+v, want none", sessions)
	}
}

func TestListSessionsReadsNameActivityAndDir(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	startSession(t, client, "alpha", root)
	setDir(t, client, "alpha", root)
	startSession(t, client, "beta", root)

	sessions, err := listSessions(t.Context(), config(client))
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("listSessions = %+v, want two sessions", sessions)
	}
	byName := map[string]session{}
	for _, s := range sessions {
		byName[s.name] = s
	}
	if got := byName["alpha"]; got.dir != root || got.activity <= 0 {
		t.Errorf("alpha = %+v, want dir %q and activity > 0", got, root)
	}
	if got := byName["beta"]; got.dir != "" || got.activity <= 0 {
		t.Errorf("beta = %+v, want empty dir and activity > 0", got)
	}
}

func TestResolveNoArgumentPicksMostRecentNonShellSession(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "alpha", t.TempDir())
	time.Sleep(activityTick)
	startSession(t, client, "zeta", t.TempDir())
	startSession(t, client, protocol.ShellSessionPrefix+"newest", t.TempDir())

	plan := resolve(t, config(client), Request{})
	if want := (Plan{Session: "zeta", Tree: true}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve() = %+v, want %+v", plan, want)
	}
}

func TestResolveNoArgumentWithoutNonShellSessionIsError(t *testing.T) {
	cases := map[string]func(t *testing.T, client *tmuxctl.Client){
		"no server": func(t *testing.T, _ *tmuxctl.Client) {
			t.Helper()
		},
		"only shell sessions": func(t *testing.T, client *tmuxctl.Client) {
			t.Helper()
			startSession(t, client, protocol.ShellSessionPrefix+"only", t.TempDir())
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			client := newClient(t)
			setup(t, client)
			if _, err := Resolve(t.Context(), config(client), Request{}); err == nil {
				t.Fatal("Resolve with no argument and no non-shell session returned nil error")
			}
		})
	}
}

func TestResolveExactSessionNameMatches(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "work", t.TempDir())

	plan := resolve(t, config(client), Request{Args: []string{"work"}})
	if want := (Plan{Session: "work"}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(work) = %+v, want %+v", plan, want)
	}
}

func TestResolveShellSessionNameDoesNotMatchWord(t *testing.T) {
	client := newClient(t)
	shell := protocol.ShellSessionPrefix + "x"
	startSession(t, client, shell, t.TempDir())

	plan := resolve(t, config(client), Request{Args: []string{shell}})
	if want := (Plan{Text: shell}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(shell name) = %+v, want %+v", plan, want)
	}
}

func TestResolveOneWordKnownDirUsesSessionForDir(t *testing.T) {
	client := newClient(t)
	proj := newDir(t, "proj")
	startSession(t, client, "elsewhere", t.TempDir())
	setDir(t, client, "elsewhere", proj)

	plan := resolve(t, config(client), Request{Args: []string{"proj"}})
	if want := (Plan{Session: "elsewhere"}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(proj) = %+v, want %+v", plan, want)
	}
}

func TestResolveOneWordDirWithEmptySessionNamedBasename(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, "proj.1"))
	startSession(t, client, "proj_1", t.TempDir())
	cfg := config(client)
	cfg.Root = root

	plan := resolve(t, cfg, Request{Args: []string{"proj.1"}})
	if want := (Plan{Session: "proj_1"}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(proj.1) = %+v, want %+v", plan, want)
	}
}

func TestResolveDirectoryRuleIgnoresNamedSessionWithOtherDir(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	other := filepath.Join(root, "other")
	startSession(t, client, "proj", t.TempDir())
	setDir(t, client, "proj", other)

	plan := resolve(t, config(client), Request{Args: []string{"."}, Cwd: proj})
	if want := (Plan{Session: "proj-2", Dir: proj}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(.) = %+v, want %+v", plan, want)
	}
}

func TestResolveDirectoryRuleIgnoresShellSessionName(t *testing.T) {
	client := newClient(t)
	shell := protocol.ShellSessionPrefix + "proj"
	dir := filepath.Join(t.TempDir(), shell)
	startSession(t, client, shell, t.TempDir())

	plan := resolve(t, config(client), Request{Args: []string{"."}, Cwd: dir})
	if want := (Plan{Session: shell + "-2", Dir: dir}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(.) = %+v, want %+v", plan, want)
	}
}

func TestResolveOneWordKnownDirWithNoSessionPlansSession(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	mkdirAll(t, proj)
	cfg := config(newClient(t))
	cfg.Root = root

	plan := resolve(t, cfg, Request{Args: []string{"proj"}})
	if want := (Plan{Session: "proj", Dir: proj}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(proj) = %+v, want %+v", plan, want)
	}
}

func TestResolveOneWordWithNoKnownDirIsText(t *testing.T) {
	plan := resolve(t, config(newClient(t)), Request{Args: []string{"fix"}})
	if want := (Plan{Text: "fix"}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(fix) = %+v, want %+v", plan, want)
	}
}

func TestResolveDotUsesCleanedCwd(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")

	plan := resolve(t, config(newClient(t)), Request{Args: []string{"."}, Cwd: proj + "/."})
	if want := (Plan{Session: "proj", Dir: proj}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(.) = %+v, want %+v", plan, want)
	}
}

func TestResolveDotWithRemoteIsError(t *testing.T) {
	req := Request{Args: []string{"."}, Cwd: t.TempDir(), Remote: true}
	_, err := Resolve(t.Context(), config(newClient(t)), req)
	if err == nil {
		t.Fatal("Resolve(.) with Remote returned nil error")
	}
}

func TestResolveSameBasenameRootsResolveToTheirOwnSessions(t *testing.T) {
	client := newClient(t)
	first := filepath.Join(t.TempDir(), "proj")
	second := filepath.Join(t.TempDir(), "proj")
	for _, dir := range []string{first, second} {
		mkdirAll(t, dir)
	}
	cfg := config(client)
	// first is known as a child of its root; second is under another root and is not known.
	cfg.Root = filepath.Dir(first)

	plan := resolve(t, cfg, Request{Args: []string{"proj"}})
	if want := (Plan{Session: "proj", Dir: first}); !reflect.DeepEqual(plan, want) {
		t.Fatalf("Resolve(proj) for first root = %+v, want %+v", plan, want)
	}
	startSession(t, client, plan.Session, plan.Dir)
	setDir(t, client, plan.Session, plan.Dir)

	plan = resolve(t, cfg, Request{Args: []string{"."}, Cwd: second + string(filepath.Separator)})
	if want := (Plan{Session: "proj-2", Dir: second}); !reflect.DeepEqual(plan, want) {
		t.Fatalf("Resolve(.) for second root = %+v, want %+v", plan, want)
	}
	startSession(t, client, plan.Session, plan.Dir)
	setDir(t, client, plan.Session, plan.Dir)

	got := resolve(t, cfg, Request{Args: []string{"."}, Cwd: first})
	if !reflect.DeepEqual(got, Plan{Session: "proj"}) {
		t.Errorf("Resolve(.) in first root = %+v, want session proj", got)
	}
	want := Plan{Session: "proj-2"}
	if got := resolve(t, cfg, Request{Args: []string{"."}, Cwd: second}); !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve(.) in second root = %+v, want session proj-2", got)
	}
	if _, err := Resolve(t.Context(), cfg, Request{Args: []string{"."}, Cwd: first, Remote: true}); err == nil {
		t.Error("Resolve(.) with Remote returned nil error")
	}
}

func TestPathAbsoluteDirWithNothingRecordedIsCreatePlan(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "proj")
	mkdirAll(t, proj)

	plan := resolve(t, config(newClient(t)), Request{Args: []string{proj}})
	if want := (Plan{Session: "proj", Dir: proj}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(%s) = %+v, want %+v", proj, plan, want)
	}
}

func TestPathRelativeIsJoinedToCwd(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sub", "dir")
	mkdirAll(t, dir)

	plan := resolve(t, config(newClient(t)), Request{Args: []string{"sub/dir"}, Cwd: root})
	if want := (Plan{Session: "dir", Dir: dir}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(sub/dir) = %+v, want %+v", plan, want)
	}
}

func TestPathTrailingSlashIsCleaned(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "proj")
	mkdirAll(t, proj)

	plan := resolve(t, config(newClient(t)), Request{Args: []string{proj + "/"}})
	if want := (Plan{Session: "proj", Dir: proj}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(%s/) = %+v, want %+v", proj, plan, want)
	}
}

func TestPathDotDotIsParentOfCwd(t *testing.T) {
	root := filepath.Join(t.TempDir(), "parent")
	child := filepath.Join(root, "child")
	mkdirAll(t, child)

	plan := resolve(t, config(newClient(t)), Request{Args: []string{".."}, Cwd: child})
	if want := (Plan{Session: "parent", Dir: root}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(..) = %+v, want %+v", plan, want)
	}
}

func TestPathDirWhoseSessionRecordsItGivesSession(t *testing.T) {
	client := newClient(t)
	proj := filepath.Join(t.TempDir(), "proj")
	mkdirAll(t, proj)
	startSession(t, client, "work", t.TempDir())
	setDir(t, client, "work", proj)

	plan := resolve(t, config(client), Request{Args: []string{proj}})
	if want := (Plan{Session: "work"}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(%s) = %+v, want %+v", proj, plan, want)
	}
}

func TestPathFailMissingPathNamesItAndCreatesNothing(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing")

	_, err := Resolve(t.Context(), config(client), Request{Args: []string{missing}})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("Resolve(%s) error = %v, want an error naming the path", missing, err)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Errorf("stat %s: err %v, want not exist", missing, statErr)
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
		t.Errorf("sessions = %q, want only keeper", got)
	}
}

func TestPathFailRegularFileNamesItAndCreatesNothing(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	file := filepath.Join(t.TempDir(), "notes.txt")
	writeFile(t, file, "x")

	_, err := Resolve(t.Context(), config(client), Request{Args: []string{file}})
	if err == nil || !strings.Contains(err.Error(), file) {
		t.Fatalf("Resolve(%s) error = %v, want an error naming the path", file, err)
	}
	if content, readErr := os.ReadFile(file); readErr != nil || string(content) != "x" {
		t.Errorf("read %s = %q, %v; want x, nil", file, content, readErr)
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
		t.Errorf("sessions = %q, want only keeper", got)
	}
}

func TestPathFailRelativeNeedsCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sub", "dir")
	mkdirAll(t, dir)
	cases := map[string]Request{
		"remote":        {Args: []string{"sub/dir"}, Cwd: root, Remote: true},
		"no cwd":        {Args: []string{"sub/dir"}},
		"dotdot no cwd": {Args: []string{".."}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			client := newClient(t)
			startSession(t, client, "keeper", t.TempDir())

			_, err := Resolve(t.Context(), config(client), req)
			if err == nil || !strings.Contains(err.Error(), "current directory") {
				t.Fatalf("Resolve(%+v) error = %v, want an error saying it needs a current directory", req, err)
			}
			if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
				t.Errorf("sessions = %q, want only keeper", got)
			}
		})
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("stat %s = %v, %v; want the directory left as it was", dir, info, err)
	}
}

func TestPathChoicesRelativeIsErrorNotText(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	claude := installClaude(t, "pane d1\n", 0)
	cfg := intentConfig(client, claude, t.TempDir())

	for _, text := range []string{"sub/dir", "./x", ".."} {
		t.Run(text, func(t *testing.T) {
			if _, err := Choices(t.Context(), cfg, text); err == nil {
				t.Fatalf("Choices(%q) returned nil error", text)
			}
		})
	}
	if runs := claudeRuns(t, claude); len(runs) != 0 {
		t.Errorf("claude ran %d times for a path, want 0", len(runs))
	}
}

func TestPathChoicesMissingAbsolutePathIsErrorNotText(t *testing.T) {
	client := newClient(t)
	missing := filepath.Join(t.TempDir(), "missing")
	claude := installClaude(t, "pane d1\n", 0)
	cfg := intentConfig(client, claude, t.TempDir())

	_, err := Choices(t.Context(), cfg, missing)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("Choices(%s) error = %v, want an error naming the path", missing, err)
	}
	if runs := claudeRuns(t, claude); len(runs) != 0 {
		t.Errorf("claude ran %d times for a missing path, want 0", len(runs))
	}
}

func TestPathChoicesAbsoluteDirGivesResumeRows(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "proj")
	mkdirAll(t, proj)
	archivePath := resumeArchive(t, archive.Record{
		Dir: proj, ClaudeSession: "sess-a", Transcript: resumeTranscript(t, "a.jsonl"),
		Summary: "summary a", Closed: 100,
	})
	cfg := Config{
		Tmux:    newClient(t),
		Claude:  installClaude(t, "", 0),
		Archive: archivePath,
	}

	got, err := Choices(t.Context(), cfg, proj)
	if err != nil {
		t.Fatalf("Choices(%s): %v", proj, err)
	}
	want := []Action{
		{Kind: ActionResume, Dir: proj, Session: "sess-a", Note: "summary a"},
		{Kind: ActionFresh, Dir: proj},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Choices(%s) = %+v, want %+v", proj, got, want)
	}
}

func TestPathChoicesAbsoluteDirWithSessionJumpsToIt(t *testing.T) {
	client := newClient(t)
	proj := filepath.Join(t.TempDir(), "proj")
	mkdirAll(t, proj)
	startSession(t, client, "work", t.TempDir())
	setDir(t, client, "work", proj)
	pane := activePane(t, client, "work")
	cfg := intentConfig(client, installClaude(t, "", 0), t.TempDir())

	got, err := Choices(t.Context(), cfg, proj)
	if err != nil {
		t.Fatalf("Choices(%s): %v", proj, err)
	}
	if want := []Action{{Kind: ActionJump, Pane: pane}}; !slices.Equal(got, want) {
		t.Errorf("Choices(%s) = %+v, want %+v", proj, got, want)
	}
}

func TestResolveCreatesNoDirectoryAndNoSession(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	fresh := filepath.Join(root, "fresh")
	made := filepath.Join(root, "made")
	mkdirAll(t, fresh)
	cfg := config(client)
	cfg.Root = root

	resolve(t, cfg, Request{Args: []string{"new", "made", "--root", root}})
	resolve(t, cfg, Request{Args: []string{"fresh"}, Cwd: root})
	resolve(t, cfg, Request{Args: []string{"."}, Cwd: fresh})

	if _, err := os.Stat(made); !os.IsNotExist(err) {
		t.Errorf("stat %s after Resolve: err %v, want not exist", made, err)
	}
	if info, err := os.Stat(fresh); err != nil || !info.IsDir() {
		t.Errorf("stat %s after Resolve = %v, %v; want the directory left as it was", fresh, info, err)
	}
	sessions, err := listSessions(t.Context(), cfg)
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("sessions after Resolve = %+v, want none", sessions)
	}
}

func TestResolveNewPlansDirectoryUnderRoot(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	cfg := config(client)
	cfg.Root = root

	plan := resolve(t, cfg, Request{Args: []string{"new", "fresh"}})
	want := Plan{Session: "fresh", Dir: filepath.Join(root, "fresh"), MakeDir: true}
	if !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(new fresh) = %+v, want %+v", plan, want)
	}
}

func TestResolveNewRootFlagOverridesDefaultRoot(t *testing.T) {
	cfg := config(newClient(t))
	cfg.Root = t.TempDir()
	other := t.TempDir()

	plan := resolve(t, cfg, Request{Args: []string{"new", "fresh", "--root", other}})
	want := Plan{Session: "fresh", Dir: filepath.Join(other, "fresh"), MakeDir: true}
	if !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(new fresh --root) = %+v, want %+v", plan, want)
	}
}

func TestResolveNewFreeNameAndSanitizedName(t *testing.T) {
	client := newClient(t)
	root := t.TempDir()
	startSession(t, client, "fresh", t.TempDir())
	cfg := config(client)
	cfg.Root = root

	plan := resolve(t, cfg, Request{Args: []string{"new", "fresh"}})
	want := Plan{Session: "fresh-2", Dir: filepath.Join(root, "fresh"), MakeDir: true}
	if !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(new fresh) with fresh taken = %+v, want %+v", plan, want)
	}
	plan = resolve(t, cfg, Request{Args: []string{"new", "a.b"}})
	want = Plan{Session: "a_b", Dir: filepath.Join(root, "a.b"), MakeDir: true}
	if !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(new a.b) = %+v, want %+v", plan, want)
	}
}

func TestResolveNewRejectsBadInput(t *testing.T) {
	cases := map[string][]string{
		"new alone":     {"new"},
		"empty name":    {"new", ""},
		"dot name":      {"new", "."},
		"dotdot name":   {"new", ".."},
		"slash in name": {"new", "a/b"},
		"relative root": {"new", "fresh", "--root", "relative/dir"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := config(newClient(t))
			cfg.Root = t.TempDir()
			if _, err := Resolve(t.Context(), cfg, Request{Args: args}); err == nil {
				t.Fatalf("Resolve(%q) returned nil error", args)
			}
		})
	}
}

func TestResolveNewWithoutAnyRootIsError(t *testing.T) {
	cfg := config(newClient(t))
	if _, err := Resolve(t.Context(), cfg, Request{Args: []string{"new", "fresh"}}); err == nil {
		t.Fatal("Resolve(new fresh) with no default root and no --root returned nil error")
	}
}

func TestResolveOtherShapesWithManyWordsAreText(t *testing.T) {
	cases := map[string][]string{
		"two words":          {"fix", "the", "thing"},
		"new with extra":     {"new", "fresh", "extra"},
		"new with root only": {"new", "fresh", "--root"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			plan := resolve(t, config(newClient(t)), Request{Args: args})
			if want := (Plan{Text: strings.Join(args, " ")}); !reflect.DeepEqual(plan, want) {
				t.Errorf("Resolve(%q) = %+v, want %+v", args, plan, want)
			}
		})
	}
}

// resumeArchive writes records, in order, to a fresh synthetic archive under t.TempDir and returns its path.
func resumeArchive(t *testing.T, records ...archive.Record) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.jsonl")
	for _, r := range records {
		if err := archive.Append(path, r); err != nil {
			t.Fatalf("append archive record: %v", err)
		}
	}
	return path
}

// resumeTranscript creates a transcript file named name under t.TempDir and returns its path.
func resumeTranscript(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	writeFile(t, path, "{}\n")
	return path
}

func TestResumeActionsListsKeptSessionsNewestFirstThenFresh(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	other := filepath.Join(root, "other")
	archivePath := resumeArchive(t,
		archive.Record{
			Dir: proj, ClaudeSession: "sess-a", Transcript: resumeTranscript(t, "a-old.jsonl"),
			Summary: "older a", Closed: 100,
		},
		archive.Record{
			Dir: proj, ClaudeSession: "sess-b", Transcript: resumeTranscript(t, "b.jsonl"),
			Summary: "summary b", Closed: 200,
		},
		archive.Record{
			Dir: proj, ClaudeSession: "sess-gone", Transcript: filepath.Join(root, "gone.jsonl"),
			Summary: "summary gone", Closed: 300,
		},
		archive.Record{
			Dir: proj, ClaudeSession: "sess-a", Transcript: resumeTranscript(t, "a-new.jsonl"),
			Summary: "summary a", Closed: 400,
		},
		archive.Record{
			Dir: other, ClaudeSession: "sess-other", Transcript: resumeTranscript(t, "other.jsonl"),
			Summary: "summary other", Closed: 500,
		},
	)

	got, err := ResumeActions(Config{Archive: archivePath}, proj)
	if err != nil {
		t.Fatalf("ResumeActions: %v", err)
	}
	want := []Action{
		{Kind: ActionResume, Dir: proj, Session: "sess-a", Note: "summary a"},
		{Kind: ActionResume, Dir: proj, Session: "sess-b", Note: "summary b"},
		{Kind: ActionFresh, Dir: proj},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ResumeActions = %+v, want %+v", got, want)
	}
}

func TestResumeActionsWithNoUsableRecordIsContinueThenFresh(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "proj")
	archivePath := resumeArchive(t,
		archive.Record{
			Dir: proj, ClaudeSession: "sess-gone", Transcript: filepath.Join(t.TempDir(), "gone.jsonl"),
			Summary: "summary gone", Closed: 1,
		},
		archive.Record{
			Dir: proj, ClaudeSession: "sess-no-transcript", Transcript: "",
			Summary: "no transcript", Closed: 2,
		},
		archive.Record{
			Dir: proj, ClaudeSession: "", Transcript: resumeTranscript(t, "unnamed.jsonl"),
			Summary: "no session", Closed: 3,
		},
	)

	got, err := ResumeActions(Config{Archive: archivePath}, proj)
	if err != nil {
		t.Fatalf("ResumeActions: %v", err)
	}
	want := []Action{{Kind: ActionContinue, Dir: proj}, {Kind: ActionFresh, Dir: proj}}
	if !slices.Equal(got, want) {
		t.Errorf("ResumeActions = %+v, want %+v", got, want)
	}
}

func TestResumeActionsWithoutArchiveFileIsContinueThenFresh(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "proj")
	absent := filepath.Join(t.TempDir(), "absent.jsonl")

	got, err := ResumeActions(Config{Archive: absent}, proj)
	if err != nil {
		t.Fatalf("ResumeActions with no archive file: %v", err)
	}
	want := []Action{{Kind: ActionContinue, Dir: proj}, {Kind: ActionFresh, Dir: proj}}
	if !slices.Equal(got, want) {
		t.Errorf("ResumeActions = %+v, want %+v", got, want)
	}
}

func TestResumeActionsEmptyArchiveIsError(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "proj")
	if _, err := ResumeActions(Config{}, proj); err == nil {
		t.Fatal("ResumeActions with an empty Archive returned nil error")
	}
}

func TestResumeActionsMalformedArchiveLineIsError(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "proj")
	archivePath := filepath.Join(t.TempDir(), "archive.jsonl")
	writeFile(t, archivePath, "not json\n")

	if _, err := ResumeActions(Config{Archive: archivePath}, proj); err == nil {
		t.Fatal("ResumeActions with a malformed archive line returned nil error")
	}
}

func TestResumeActionsTranscriptStatErrorIsError(t *testing.T) {
	proj := filepath.Join(t.TempDir(), "proj")
	notDir := filepath.Join(t.TempDir(), "file")
	writeFile(t, notDir, "x")
	archivePath := resumeArchive(t,
		archive.Record{
			Dir: proj, ClaudeSession: "sess-x", Transcript: filepath.Join(notDir, "x.jsonl"),
			Summary: "stat fails", Closed: 1,
		},
	)

	if _, err := ResumeActions(Config{Archive: archivePath}, proj); err == nil {
		t.Fatal("ResumeActions with an unstattable transcript returned nil error")
	}
}

func TestCheckName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", "/abs"} {
		if err := checkName(name); err == nil {
			t.Errorf("checkName(%q) = nil, want error", name)
		}
	}
	for _, name := range []string{"fresh", "a.b", "a:b"} {
		if err := checkName(name); err != nil {
			t.Errorf("checkName(%q) = %v, want nil", name, err)
		}
	}
}

func TestFreeName(t *testing.T) {
	cases := []struct {
		name     string
		sessions []session
		base     string
		want     string
	}{
		{"free base", nil, "proj", "proj"},
		{"taken base", []session{{name: "proj"}}, "proj", "proj-2"},
		{"taken through two", []session{{name: "proj"}, {name: "proj-2"}}, "proj", "proj-3"},
		{"dot and colon replaced", nil, "a.b:c", "a_b_c"},
		{"shell session is taken", []session{{name: "_orrery-shell-x"}}, "_orrery-shell-x", "_orrery-shell-x-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := freeName(tc.sessions, tc.base); got != tc.want {
				t.Errorf("freeName(%+v, %q) = %q, want %q", tc.sessions, tc.base, got, tc.want)
			}
		})
	}
}

func TestSessionForDir(t *testing.T) {
	sessions := []session{
		{name: "a", dir: "/synthetic/x"},
		{name: "b"},
		{name: protocol.ShellSessionPrefix + "z", dir: "/synthetic/z"},
	}
	if got, ok := sessionForDir(sessions, "/synthetic/x/"); !ok || got.name != "a" {
		t.Errorf("sessionForDir(/synthetic/x/) = %+v, %v; want a, true", got, ok)
	}
	if _, ok := sessionForDir(sessions, "/synthetic/y"); ok {
		t.Error("sessionForDir(/synthetic/y) matched, want no match")
	}
	if _, ok := sessionForDir(sessions, "/synthetic/z"); ok {
		t.Error("sessionForDir(/synthetic/z) matched a shell session, want no match")
	}
}

func TestMostRecentTieBreaksByLowestNameAndSkipsShell(t *testing.T) {
	sessions := []session{
		{name: "zeta", activity: 100},
		{name: "beta", activity: 200},
		{name: "alpha", activity: 200},
		{name: protocol.ShellSessionPrefix + "newest", activity: 999},
	}
	got, ok := mostRecent(sessions)
	if !ok || got.name != "alpha" {
		t.Errorf("mostRecent = %+v, %v; want alpha, true", got, ok)
	}
	if _, ok := mostRecent(nil); ok {
		t.Error("mostRecent(nil) returned true, want false")
	}
}

// claudeScript writes a stand-in claude that sleeps, so a created pane runs it without the real claude.
func claudeScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 300\n"), 0o755); err != nil {
		t.Fatalf("write claude stand-in: %v", err)
	}
	return path
}

// executeConfig is a Config whose created panes run claude.
func executeConfig(client *tmuxctl.Client, claude string) Config {
	cfg := config(client)
	cfg.Claude = claude
	return cfg
}

// sessionNames returns the sorted names of every session on client's server.
func sessionNames(t *testing.T, client *tmuxctl.Client) []string {
	t.Helper()
	sessions, err := listSessions(t.Context(), config(client))
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	names := make([]string, 0, len(sessions))
	for _, s := range sessions {
		names = append(names, s.name)
	}
	slices.Sort(names)
	return names
}

// paneValue reads one display-message format from the active pane of session name.
func paneValue(t *testing.T, client *tmuxctl.Client, name, format string) string {
	t.Helper()
	return strings.TrimSpace(mustRun(t, client, "display-message", "-p", "-t", "="+name+":", format))
}

// dirOption reads @orrery_dir from session name. The exact-match target needs the trailing colon on tmux.
func dirOption(t *testing.T, client *tmuxctl.Client, name string) string {
	t.Helper()
	return strings.TrimSpace(mustRun(t, client, "show-options", "-t", "="+name+":", "-v", protocol.OptionDir))
}

func TestExecuteDirPlanCreatesSessionWithClaudePane(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	claude := claudeScript(t)
	dir := t.TempDir()

	target, err := Execute(t.Context(), executeConfig(client, claude), Plan{Session: "proj", Dir: dir})
	if err != nil {
		t.Fatalf("Execute(Dir plan): %v", err)
	}
	if want := (Target{Session: "proj"}); target != want {
		t.Errorf("Execute(Dir plan) = %+v, want %+v", target, want)
	}
	if got := paneValue(t, client, "proj", "#{pane_current_path}"); got != dir {
		t.Errorf("pane directory = %q, want %q", got, dir)
	}
	if got := paneValue(t, client, "proj", "#{pane_start_command}"); got != claude {
		t.Errorf("pane command = %q, want %q", got, claude)
	}
	if got := dirOption(t, client, "proj"); got != dir {
		t.Errorf("@orrery_dir = %q, want %q", got, dir)
	}
}

func TestExecuteMakeDirCreatesExactlyDir(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	root := t.TempDir()
	dir := filepath.Join(root, "fresh")

	plan := Plan{Session: "fresh", Dir: dir, MakeDir: true}
	target, err := Execute(t.Context(), executeConfig(client, claudeScript(t)), plan)
	if err != nil {
		t.Fatalf("Execute(MakeDir plan): %v", err)
	}
	if want := (Target{Session: "fresh"}); target != want {
		t.Errorf("Execute(MakeDir plan) = %+v, want %+v", target, want)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("stat %s = %v, %v; want a directory", dir, info, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "fresh" {
		t.Errorf("root entries = %v, want only fresh", entries)
	}
	if got := paneValue(t, client, "fresh", "#{pane_current_path}"); got != dir {
		t.Errorf("pane directory = %q, want %q", got, dir)
	}
	if got := dirOption(t, client, "fresh"); got != dir {
		t.Errorf("@orrery_dir = %q, want %q", got, dir)
	}
}

func TestExecuteMakeDirDoesNotCreateParents(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	root := t.TempDir()
	dir := filepath.Join(root, "missing", "fresh")

	plan := Plan{Session: "fresh", Dir: dir, MakeDir: true}
	if _, err := Execute(t.Context(), executeConfig(client, claudeScript(t)), plan); err == nil {
		t.Fatal("Execute with a missing parent returned nil error")
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Errorf("stat missing parent: err %v, want not exist", err)
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
		t.Errorf("sessions = %q, want only keeper", got)
	}
}

func TestExecuteMakeDirExistingDirIsErrorWithNoSession(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	dir := t.TempDir()

	plan := Plan{Session: "fresh", Dir: dir, MakeDir: true}
	if _, err := Execute(t.Context(), executeConfig(client, claudeScript(t)), plan); err == nil {
		t.Fatal("Execute with an existing directory returned nil error")
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
		t.Errorf("sessions = %q, want only keeper", got)
	}
}

func TestExecuteTextPlanIsErrorAndCreatesNothing(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	root := t.TempDir()

	plan := Plan{Session: "made", Dir: filepath.Join(root, "made"), MakeDir: true, Text: "fix the thing"}
	if _, err := Execute(t.Context(), executeConfig(client, claudeScript(t)), plan); err == nil {
		t.Fatal("Execute(Text plan) returned nil error")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Errorf("root entries = %v, %v; want none", entries, err)
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"keeper"}) {
		t.Errorf("sessions = %q, want only keeper", got)
	}
}

// ambiguousDirs makes two directories named proj: first, which a session records, and second, which an archive
// record names. It returns both and the Config that knows them, in that order.
func ambiguousDirs(t *testing.T, client *tmuxctl.Client) (string, string, Config) {
	t.Helper()
	first := newDir(t, "proj")
	second := newDir(t, "proj")
	startSession(t, client, "work", t.TempDir())
	setDir(t, client, "work", first)
	cfg := config(client)
	cfg.Archive = historyArchive(t, second)
	return first, second, cfg
}

func TestAmbigSameNamedDirsGiveDirsInKnownOrder(t *testing.T) {
	client := newClient(t)
	first, second, cfg := ambiguousDirs(t, client)

	plan := resolve(t, cfg, Request{Args: []string{"proj"}})
	if want := (Plan{Dirs: []string{first, second}}); !reflect.DeepEqual(plan, want) {
		t.Errorf("Resolve(proj) = %+v, want %+v", plan, want)
	}
}

func TestAmbigExecuteRefusesSeveralDirsAndCreatesNothing(t *testing.T) {
	client := newClient(t)
	first, second, cfg := ambiguousDirs(t, client)
	cfg.Claude = claudeScript(t)

	if _, err := Execute(t.Context(), cfg, Plan{Dirs: []string{first, second}}); err == nil {
		t.Fatal("Execute(several-directory plan) returned nil error")
	}
	if got := sessionNames(t, client); !slices.Equal(got, []string{"work"}) {
		t.Errorf("sessions = %q, want only work", got)
	}
}

func TestAmbigPaneActionOnSecondDirOpensClaudePaneInIt(t *testing.T) {
	client := newClient(t)
	_, second, cfg := ambiguousDirs(t, client)
	cfg.Claude = claudeScript(t)

	actions := PaneActions([]string{"unused-first", second})
	target, err := ExecuteAction(t.Context(), cfg, actions[1])
	if err != nil {
		t.Fatalf("ExecuteAction(second pane): %v", err)
	}
	if want := (Target{Session: "proj"}); target != want {
		t.Errorf("ExecuteAction(second pane) = %+v, want %+v", target, want)
	}
	if got := paneValue(t, client, "proj", "#{pane_current_path}"); got != second {
		t.Errorf("pane directory = %q, want %q", got, second)
	}
	if got := dirOption(t, client, "proj"); got != second {
		t.Errorf("@orrery_dir = %q, want %q", got, second)
	}
}

func TestExecuteSessionPlanReturnsTargetWithoutCreating(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	startSession(t, client, "work", t.TempDir())
	cfg := executeConfig(client, claudeScript(t))

	got, err := Execute(t.Context(), cfg, Plan{Session: "work"})
	if err != nil || got != (Target{Session: "work"}) {
		t.Errorf("Execute(session plan) = %+v, %v; want work, nil", got, err)
	}
	got, err = Execute(t.Context(), cfg, Plan{Session: "work", Tree: true})
	if err != nil || got != (Target{Session: "work", Tree: true}) {
		t.Errorf("Execute(session plan with Tree) = %+v, %v; want work with tree, nil", got, err)
	}
	if names := sessionNames(t, client); !slices.Equal(names, []string{"keeper", "work"}) {
		t.Errorf("sessions = %q, want keeper and work only", names)
	}
}

func TestExecuteSameBasenameRootsResolveToTheirOwnSessions(t *testing.T) {
	client := newClient(t)
	startSession(t, client, "keeper", t.TempDir())
	first := filepath.Join(t.TempDir(), "proj")
	second := filepath.Join(t.TempDir(), "proj")
	for _, dir := range []string{first, second} {
		mkdirAll(t, dir)
	}
	cfg := executeConfig(client, claudeScript(t))
	cfg.Root = filepath.Dir(first)

	plan := resolve(t, cfg, Request{Args: []string{"proj"}})
	if want := (Plan{Session: "proj", Dir: first}); !reflect.DeepEqual(plan, want) {
		t.Fatalf("Resolve(proj) for first root = %+v, want %+v", plan, want)
	}
	if target, err := Execute(t.Context(), cfg, plan); err != nil || target != (Target{Session: "proj"}) {
		t.Fatalf("Execute(first root) = %+v, %v; want proj, nil", target, err)
	}

	plan = resolve(t, cfg, Request{Args: []string{"."}, Cwd: second})
	if want := (Plan{Session: "proj-2", Dir: second}); !reflect.DeepEqual(plan, want) {
		t.Fatalf("Resolve(.) for second root = %+v, want %+v", plan, want)
	}
	if target, err := Execute(t.Context(), cfg, plan); err != nil || target != (Target{Session: "proj-2"}) {
		t.Fatalf("Execute(second root) = %+v, %v; want proj-2, nil", target, err)
	}

	got := resolve(t, cfg, Request{Args: []string{"."}, Cwd: first})
	if !reflect.DeepEqual(got, Plan{Session: "proj"}) {
		t.Errorf("Resolve(.) in first root = %+v, want session proj", got)
	}
	want := Plan{Session: "proj-2"}
	if got := resolve(t, cfg, Request{Args: []string{"."}, Cwd: second}); !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve(.) in second root = %+v, want session proj-2", got)
	}
}
