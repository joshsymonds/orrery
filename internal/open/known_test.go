package open

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// newDir creates directory name under a fresh temp directory and returns its path.
func newDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	mkdirAll(t, dir)
	return dir
}

// engineOps returns one engine session op per dir, each with its own ID.
func engineOps(dirs ...string) []store.EngineOp {
	ops := make([]store.EngineOp, 0, len(dirs))
	for i, dir := range dirs {
		ops = append(ops, store.EngineAdd{ID: fmt.Sprintf("e-%d", i), Dir: dir, State: "idle"})
	}
	return ops
}

// storeWith creates a store file under t.TempDir, applies ops to it, and returns the file's path.
func storeWith(t *testing.T, ops ...store.EngineOp) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err = s.ApplyEngine(t.Context(), ops, time.Now()); err != nil {
		t.Fatalf("ApplyEngine: %v", errors.Join(err, s.Close()))
	}
	if err = s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return path
}

// garbageStore writes bytes that are not a SQLite database under t.TempDir and returns the file's path.
func garbageStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	writeFile(t, path, "not a database\n")
	return path
}

// historyArchive returns the path of an archive whose records name dirs, dirs[0] newest, so that archive.Dirs
// lists them in that order. With no dirs the file is never written.
func historyArchive(t *testing.T, dirs ...string) string {
	t.Helper()
	records := make([]archive.Record, 0, len(dirs))
	for i, dir := range dirs {
		records = append(records, archive.Record{Dir: dir, Closed: int64(len(dirs) - i)})
	}
	return resumeArchive(t, records...)
}

// historyOf returns the history directories cfg names, given cfg's live sessions.
func historyOf(t *testing.T, cfg Config) []string {
	t.Helper()
	sessions, err := listSessions(t.Context(), cfg)
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	dirs, err := historyDirs(t.Context(), cfg, sessions)
	if err != nil {
		t.Fatalf("historyDirs: %v", err)
	}
	return dirs
}

func TestDirsSessionDirsOfNonShellSessionsInSessionOrder(t *testing.T) {
	client := newClient(t)
	a, b := newDir(t, "work-a"), newDir(t, "work-b")
	startSession(t, client, "work-a", t.TempDir())
	setDir(t, client, "work-a", a)
	startSession(t, client, "work-b", t.TempDir())
	setDir(t, client, "work-b", b)
	startSession(t, client, "nodir", t.TempDir())
	shell := newDir(t, "shell")
	startSession(t, client, protocol.ShellSessionPrefix+"x", t.TempDir())
	setDir(t, client, protocol.ShellSessionPrefix+"x", shell)

	if got, want := historyOf(t, config(client)), []string{a, b}; !slices.Equal(got, want) {
		t.Errorf("history = %q, want %q", got, want)
	}
}

func TestDirsArchiveDirsNewestFirst(t *testing.T) {
	old, recent := newDir(t, "old"), newDir(t, "recent")
	cfg := Config{Tmux: newClient(t), Archive: resumeArchive(t,
		archive.Record{Dir: old, Closed: 1},
		archive.Record{Dir: recent, Closed: 2},
	)}

	if got, want := historyOf(t, cfg), []string{recent, old}; !slices.Equal(got, want) {
		t.Errorf("history = %q, want %q", got, want)
	}
}

func TestDirsStoreDirsFollowArchiveDirs(t *testing.T) {
	fromArchive, fromStore := newDir(t, "archived"), newDir(t, "stored")
	cfg := Config{
		Tmux:    newClient(t),
		Archive: resumeArchive(t, archive.Record{Dir: fromArchive, Closed: 1}),
		Store:   store.NewReader(storeWith(t, engineOps(fromStore)...)),
	}

	if got, want := historyOf(t, cfg), []string{fromArchive, fromStore}; !slices.Equal(got, want) {
		t.Errorf("history = %q, want %q", got, want)
	}
}

func TestDirsStepWorktreeIsNeverListed(t *testing.T) {
	workspace, worktree := newDir(t, "workspace"), newDir(t, "worktree")
	op := store.StepAdd{ID: "example-step", Workspace: workspace, Worktree: worktree}
	cfg := Config{Tmux: newClient(t), Store: store.NewReader(storeWith(t, op))}

	if got, want := historyOf(t, cfg), []string{workspace}; !slices.Equal(got, want) {
		t.Errorf("history = %q, want %q: the step's worktree must never be listed", got, want)
	}
}

func TestDirsMissingStoreFileAddsNoneWithoutError(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent", "orrery.db")
	cfg := Config{Tmux: newClient(t), Store: store.NewReader(absent)}

	if got := historyOf(t, cfg); len(got) != 0 {
		t.Errorf("history = %q, want none", got)
	}
}

func TestDirsStoreGarbageFailsTheLookup(t *testing.T) {
	cfg := Config{Tmux: newClient(t), Store: store.NewReader(garbageStore(t))}

	sessions, err := listSessions(t.Context(), cfg)
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	if _, err = historyDirs(t.Context(), cfg, sessions); err == nil {
		t.Fatal("historyDirs with a store file of garbage bytes returned nil error")
	}
	if _, err = Resolve(t.Context(), cfg, Request{Args: []string{"proj"}}); err == nil {
		t.Fatal("Resolve with a store file of garbage bytes returned nil error")
	}
}

func TestDirsDeletedArchivedDirIsNotACandidate(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	cfg := Config{Tmux: newClient(t), Archive: resumeArchive(t, archive.Record{Dir: gone, Closed: 1})}

	if got := historyOf(t, cfg); len(got) != 0 {
		t.Errorf("history = %q, want none for a deleted directory", got)
	}
}

func TestDirsEachDirListedOnceAndCleaned(t *testing.T) {
	client := newClient(t)
	dir := newDir(t, "proj")
	startSession(t, client, "work", t.TempDir())
	setDir(t, client, "work", dir)
	cfg := Config{
		Tmux: client,
		Archive: resumeArchive(t,
			archive.Record{Dir: dir + "/", Closed: 1},
			archive.Record{Dir: dir, Closed: 2},
		),
		Store: store.NewReader(storeWith(t, engineOps(dir)...)),
	}

	if got, want := historyOf(t, cfg), []string{dir}; !slices.Equal(got, want) {
		t.Errorf("history = %q, want %q", got, want)
	}
}

// knownBuild is one source of known directories, alone: it names the source in the Config it returns and the plan
// a bare word "proj" gives for the one directory the source holds.
type knownBuild func(t *testing.T, client *tmuxctl.Client) (Config, Plan)

func TestKnownEachSourceAloneGivesOneMatchAndItsPlan(t *testing.T) {
	cases := []struct {
		name  string
		build knownBuild
	}{
		{"session directory", func(t *testing.T, client *tmuxctl.Client) (Config, Plan) {
			t.Helper()
			dir := newDir(t, "proj")
			startSession(t, client, "work", t.TempDir())
			setDir(t, client, "work", dir)
			return Config{}, Plan{Session: "work"}
		}},
		{"archive directory", func(t *testing.T, _ *tmuxctl.Client) (Config, Plan) {
			t.Helper()
			dir := newDir(t, "proj")
			return Config{Archive: historyArchive(t, dir)}, Plan{Session: "proj", Dir: dir}
		}},
		{"store directory", func(t *testing.T, _ *tmuxctl.Client) (Config, Plan) {
			t.Helper()
			dir := newDir(t, "proj")
			return Config{Store: store.NewReader(storeWith(t, engineOps(dir)...))}, Plan{Session: "proj", Dir: dir}
		}},
		{"child of root", func(t *testing.T, _ *tmuxctl.Client) (Config, Plan) {
			t.Helper()
			root := t.TempDir()
			dir := filepath.Join(root, "proj")
			mkdirAll(t, dir)
			return Config{Root: root}, Plan{Session: "proj", Dir: dir}
		}},
		{"roots directory", func(t *testing.T, _ *tmuxctl.Client) (Config, Plan) {
			t.Helper()
			home := t.TempDir()
			dir := filepath.Join(home, "Work", "example-client", "proj")
			mkdirAll(t, dir)
			roots := []Root{{Dir: filepath.Join(home, "Work"), Depth: 2}}
			return Config{Roots: roots}, Plan{Session: "proj", Dir: dir}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newClient(t)
			cfg, want := tc.build(t, client)
			cfg.Tmux = client

			got := resolve(t, cfg, Request{Args: []string{"proj"}})
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Resolve(proj) = %+v, want %+v", got, want)
			}
		})
	}
}

func TestKnownDirFromTwoSourcesIsOneMatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "proj")
	mkdirAll(t, dir)
	cfg := Config{Tmux: newClient(t), Root: root, Archive: historyArchive(t, dir)}

	got := resolve(t, cfg, Request{Args: []string{"proj"}})
	if want := (Plan{Session: "proj", Dir: dir}); !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve(proj) = %+v, want %+v", got, want)
	}
}

func TestKnownRelativeRootListsNothing(t *testing.T) {
	cwd := t.TempDir()
	mkdirAll(t, filepath.Join(cwd, "rel", "proj"))
	t.Chdir(cwd)
	cfg := config(newClient(t))
	cfg.Root = "rel"

	got, want := resolve(t, cfg, Request{Args: []string{"proj"}}), Plan{Text: "proj"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve(proj) = %+v, want %+v: a relative root is not listed", got, want)
	}
}

func TestKnownWordMustEqualBasename(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, "proj"))
	cfg := config(newClient(t))
	cfg.Root = root

	got, want := resolve(t, cfg, Request{Args: []string{"pro"}}), Plan{Text: "pro"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve(pro) = %+v, want %+v: a partial name is not a match", got, want)
	}
}
