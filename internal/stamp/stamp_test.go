package stamp

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Synthetic loopback base URLs. No test reads a real home folder: every home and folder lives under t.TempDir().
const (
	urlOuter = "http://127.0.0.1:7101"
	urlInner = "http://127.0.0.1:7102"
	urlLocal = "http://127.0.0.1:7103"
)

// stampJSON returns a settings file that stamps its folder with url.
func stampJSON(url string) string {
	return `{"env": {"ANTHROPIC_BASE_URL": "` + url + `"}}`
}

// makeDir creates dir and its parents, and returns dir.
func makeDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

// writeSettings writes content as the settings file name of folder, creating folder's .claude directory.
func writeSettings(t *testing.T, folder, name, content string) {
	t.Helper()
	claude := makeDir(t, filepath.Join(folder, ".claude"))
	if err := os.WriteFile(filepath.Join(claude, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// resolved returns path with its symlinks resolved, the form Find reports folders in.
func resolved(t *testing.T, path string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return got
}

// findStamp runs Find and fails the test when it errors.
func findStamp(t *testing.T, home, dir string) Stamp {
	t.Helper()
	got, err := Find(home, dir)
	if err != nil {
		t.Fatalf("Find(%q, %q): %v", home, dir, err)
	}
	return got
}

// assertStamp checks that got is stamped at folder with the base URL url.
func assertStamp(t *testing.T, got Stamp, folder, url string) {
	t.Helper()
	if got.Folder != folder {
		t.Fatalf("stamp folder = %q, want %q", got.Folder, folder)
	}
	if got.BaseURL() != url {
		t.Fatalf("stamp base URL = %q, want %q", got.BaseURL(), url)
	}
}

// assertNoStamp checks that got is the zero value: no stamp.
func assertNoStamp(t *testing.T, got Stamp) {
	t.Helper()
	if got.Folder != "" || got.Env != nil {
		t.Fatalf("stamp = %+v, want no stamp", got)
	}
}

// assertErrorNames checks that err is non-nil and names path.
func assertErrorNames(t *testing.T, err error, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Find returned no error, want one naming %s", path)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name %s", err, path)
	}
}

// gitIn runs git in dir with a synthetic identity, and fails the test when git fails.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-c", "user.name=synthetic", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// makeCheckout makes folder a git repository with one empty commit, and returns folder.
func makeCheckout(t *testing.T, folder string) string {
	t.Helper()
	makeDir(t, folder)
	gitIn(t, folder, "init", "-q")
	gitIn(t, folder, "commit", "-q", "--allow-empty", "-m", "synthetic")
	return folder
}

// makeWorktree adds a linked worktree of checkout at folder, and returns folder.
func makeWorktree(t *testing.T, checkout, folder string) string {
	t.Helper()
	gitIn(t, checkout, "worktree", "add", "-q", folder)
	return folder
}

func TestFindStampsFolderItself(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))

	assertStamp(t, findStamp(t, home, stamped), resolved(t, stamped), urlOuter)
}

func TestFindStampsPlainChild(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))
	child := makeDir(t, filepath.Join(stamped, "child"))

	assertStamp(t, findStamp(t, home, child), resolved(t, stamped), urlOuter)
}

func TestFindStampsTwoLevelsBelow(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))
	deep := makeDir(t, filepath.Join(stamped, "middle", "deep"))

	assertStamp(t, findStamp(t, home, deep), resolved(t, stamped), urlOuter)
}

func TestFindLocalSettingsWinOverSettingsJSON(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", `{"env": {"ANTHROPIC_BASE_URL": "`+urlOuter+`", "SYNTHETIC": "shared"}}`)
	writeSettings(t, stamped, "settings.local.json",
		`{"env": {"ANTHROPIC_BASE_URL": "`+urlLocal+`", "SYNTHETIC": "local"}}`)

	got := findStamp(t, home, stamped)
	assertStamp(t, got, resolved(t, stamped), urlLocal)
	want := map[string]string{"ANTHROPIC_BASE_URL": urlLocal, "SYNTHETIC": "local"}
	if !reflect.DeepEqual(got.Env, want) {
		t.Fatalf("stamp env = %v, want the whole local env %v", got.Env, want)
	}
}

func TestFindStampsFromSettingsJSONAlone(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))

	assertStamp(t, findStamp(t, home, stamped), resolved(t, stamped), urlOuter)
}

func TestFindNearestStampWinsWithItsWholeEnv(t *testing.T) {
	home := t.TempDir()
	outer := makeDir(t, filepath.Join(home, "outer"))
	writeSettings(t, outer, "settings.json", `{"env": {"ANTHROPIC_BASE_URL": "`+urlOuter+`", "SYNTHETIC": "outer"}}`)
	inner := makeDir(t, filepath.Join(outer, "inner"))
	writeSettings(t, inner, "settings.local.json", stampJSON(urlInner))
	child := makeDir(t, filepath.Join(inner, "child"))

	got := findStamp(t, home, child)
	assertStamp(t, got, resolved(t, inner), urlInner)
	want := map[string]string{"ANTHROPIC_BASE_URL": urlInner}
	if !reflect.DeepEqual(got.Env, want) {
		t.Fatalf("stamp env = %v, want only the nearest stamp's env %v", got.Env, want)
	}
}

func TestFindSkipsFileWithURLAbsent(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))
	child := makeDir(t, filepath.Join(stamped, "child"))
	writeSettings(t, child, "settings.local.json", `{"env": {"SYNTHETIC": "no-url"}}`)

	assertStamp(t, findStamp(t, home, child), resolved(t, stamped), urlOuter)
}

func TestFindSkipsFileWithURLEmpty(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))
	child := makeDir(t, filepath.Join(stamped, "child"))
	writeSettings(t, child, "settings.json", `{"env": {"ANTHROPIC_BASE_URL": ""}}`)

	assertStamp(t, findStamp(t, home, child), resolved(t, stamped), urlOuter)
}

func TestFindIgnoresHomeStamp(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, "settings.json", stampJSON(urlOuter))
	session := makeDir(t, filepath.Join(home, "session"))

	assertNoStamp(t, findStamp(t, home, session))
	assertNoStamp(t, findStamp(t, home, home))
}

func TestFindFollowsSymlinkedDir(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))
	child := makeDir(t, filepath.Join(stamped, "child"))
	link := filepath.Join(home, "link")
	if err := os.Symlink(child, link); err != nil {
		t.Fatalf("symlink %s: %v", link, err)
	}

	assertStamp(t, findStamp(t, home, link), resolved(t, stamped), urlOuter)
}

func TestFindLinkedWorktreeOfCheckoutBelowStamp(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))
	checkout := makeCheckout(t, filepath.Join(stamped, "repo"))
	worktree := makeWorktree(t, checkout, filepath.Join(home, "repo-wt"))
	sub := makeDir(t, filepath.Join(worktree, "sub"))

	cases := []struct {
		name string
		dir  string
	}{
		{"worktree root", worktree},
		{"subfolder of worktree", sub},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertStamp(t, findStamp(t, home, tc.dir), resolved(t, stamped), urlOuter)
		})
	}
}

func TestFindLinkedWorktreeReachedThroughSymlinkedCheckout(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))
	checkout := makeCheckout(t, filepath.Join(stamped, "repo"))
	worktree := makeWorktree(t, checkout, filepath.Join(home, "repo-wt"))
	// The worktree's gitdir names the checkout through a symlink, so the primary checkout's path is not resolved.
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(stamped, alias); err != nil {
		t.Fatalf("symlink %s: %v", alias, err)
	}
	gitdir := filepath.Join(alias, "repo", ".git", "worktrees", "repo-wt")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	assertStamp(t, findStamp(t, home, worktree), resolved(t, stamped), urlOuter)
}

func TestFindUnreadableSettingsFileErrors(t *testing.T) {
	for _, name := range []string{"settings.json", "settings.local.json"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			parent := makeDir(t, filepath.Join(home, "parent"))
			child := makeDir(t, filepath.Join(parent, "child"))
			// A directory where the settings file should be exists but cannot be read as a file.
			makeDir(t, filepath.Join(parent, ".claude", name))

			_, err := Find(home, child)
			assertErrorNames(t, err, filepath.Join(resolved(t, parent), ".claude", name))
		})
	}
}

func TestFindUnreadableGitFileErrors(t *testing.T) {
	home := t.TempDir()
	worktree := makeDir(t, filepath.Join(home, "wt"))
	gitdir := makeDir(t, filepath.Join(home, "absent-checkout", ".git", "worktrees", "wt"))
	// A commondir that is a directory exists but cannot be read as a file.
	makeDir(t, filepath.Join(gitdir, "commondir"))
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	_, err := Find(home, worktree)
	assertErrorNames(t, err, filepath.Join(gitdir, "commondir"))
}

// writeFile writes content to path.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// replaceWithSymlink makes link a symlink to target, replacing whatever link holds now.
func replaceWithSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Remove(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("remove %s: %v", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s: %v", link, err)
	}
}

// worktreeLayout is a stamped primary checkout, a linked worktree of it, and the gitdir that the worktree's .git file
// names.
type worktreeLayout struct {
	home, worktree, gitdir string
}

// makeStampedWorktree builds the layout the link tests break: a primary checkout under a stamped folder, and a linked
// worktree of it outside that folder.
func makeStampedWorktree(t *testing.T) worktreeLayout {
	t.Helper()
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))
	checkout := makeCheckout(t, filepath.Join(stamped, "repo"))
	worktree := makeWorktree(t, checkout, filepath.Join(home, "repo-wt"))
	return worktreeLayout{home, worktree, filepath.Join(checkout, ".git", "worktrees", "repo-wt")}
}

func TestFindBrokenWorktreeLinkErrors(t *testing.T) {
	cases := []struct {
		name string
		// breakLink breaks one piece of the link and returns the path that Find's error must name.
		breakLink func(t *testing.T, l worktreeLayout) string
	}{
		{"commondir dangles", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			commondir := filepath.Join(l.gitdir, "commondir")
			replaceWithSymlink(t, filepath.Join(l.home, "nowhere"), commondir)
			return commondir
		}},
		{"commondir is mode 000", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			commondir := filepath.Join(l.gitdir, "commondir")
			if err := os.Chmod(commondir, 0); err != nil {
				t.Fatalf("chmod %s: %v", commondir, err)
			}
			return commondir
		}},
		{"commondir is empty", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			commondir := filepath.Join(l.gitdir, "commondir")
			writeFile(t, commondir, "")
			return commondir
		}},
		{"commondir names a missing common dir", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			missing := filepath.Join(l.home, "elsewhere", ".git")
			writeFile(t, filepath.Join(l.gitdir, "commondir"), missing+"\n")
			return missing
		}},
		{"commondir names a file", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			file := filepath.Join(l.home, "not-a-dir")
			writeFile(t, file, "x")
			writeFile(t, filepath.Join(l.gitdir, "commondir"), file+"\n")
			return file
		}},
		{"commondir absent from a worktree gitdir", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			if err := os.Remove(filepath.Join(l.gitdir, "commondir")); err != nil {
				t.Fatalf("remove commondir: %v", err)
			}
			return l.gitdir
		}},
		{"gitdir missing", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			if err := os.Rename(l.gitdir, l.gitdir+".gone"); err != nil {
				t.Fatalf("rename gitdir: %v", err)
			}
			return l.gitdir
		}},
		{"gitdir dangles", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			link := filepath.Join(l.home, "gitdir-link")
			replaceWithSymlink(t, filepath.Join(l.home, "nowhere"), link)
			writeFile(t, filepath.Join(l.worktree, ".git"), "gitdir: "+link+"\n")
			return link
		}},
		{"gitdir is a file", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			file := filepath.Join(l.home, "not-a-dir")
			writeFile(t, file, "x")
			writeFile(t, filepath.Join(l.worktree, ".git"), "gitdir: "+file+"\n")
			return file
		}},
		{".git names no gitdir", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			dotGit := filepath.Join(resolved(t, l.worktree), ".git")
			writeFile(t, dotGit, "garbage\n")
			return dotGit
		}},
		{".git dangles", func(t *testing.T, l worktreeLayout) string {
			t.Helper()
			dotGit := filepath.Join(resolved(t, l.worktree), ".git")
			replaceWithSymlink(t, filepath.Join(l.home, "nowhere"), dotGit)
			return dotGit
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := makeStampedWorktree(t)
			want := tc.breakLink(t, l)
			_, err := Find(l.home, l.worktree)
			assertErrorNames(t, err, want)
		})
	}
}

func TestFindWorktreeGitSymlinkedToFileGetsPrimaryStamp(t *testing.T) {
	l := makeStampedWorktree(t)
	target := filepath.Join(l.home, "worktree-git-file")
	writeFile(t, target, "gitdir: "+l.gitdir+"\n")
	replaceWithSymlink(t, target, filepath.Join(l.worktree, ".git"))

	assertStamp(t, findStamp(t, l.home, l.worktree), resolved(t, filepath.Join(l.home, "stamped")), urlOuter)
}

func TestFindGitSymlinkedToCheckoutDirHasNoStamp(t *testing.T) {
	home := t.TempDir()
	checkout := makeCheckout(t, filepath.Join(home, "plain"))
	folder := makeDir(t, filepath.Join(home, "linked"))
	replaceWithSymlink(t, filepath.Join(checkout, ".git"), filepath.Join(folder, ".git"))

	assertNoStamp(t, findStamp(t, home, folder))
}

func TestFindPlainCheckoutHasNoStamp(t *testing.T) {
	home := t.TempDir()
	assertNoStamp(t, findStamp(t, home, makeCheckout(t, filepath.Join(home, "plain"))))
}

func TestFindSubmoduleHasNoStamp(t *testing.T) {
	home := t.TempDir()
	source := makeCheckout(t, filepath.Join(home, "source"))
	parent := makeCheckout(t, filepath.Join(home, "parent"))
	gitIn(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", source, "sub")

	assertNoStamp(t, findStamp(t, home, filepath.Join(parent, "sub")))
}

func TestFindSeparateGitDirHasNoStamp(t *testing.T) {
	home := t.TempDir()
	work := makeDir(t, filepath.Join(home, "work"))
	gitIn(t, work, "init", "-q", "--separate-git-dir", filepath.Join(home, "sep.git"))

	assertNoStamp(t, findStamp(t, home, work))
}

func TestFindUnusableSettingsFileErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"not JSON", `{"env": `},
		{"env value not a string", `{"env": {"ANTHROPIC_BASE_URL": "` + urlOuter + `", "SYNTHETIC": 1}}`},
		{"base URL not a string", `{"env": {"ANTHROPIC_BASE_URL": 5}}`},
		{"base URL null", `{"env": {"ANTHROPIC_BASE_URL": null}}`},
		{"env not an object", `{"env": "x"}`},
		{"env null", `{"env": null}`},
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		for _, tc := range cases {
			t.Run(name+" "+tc.name, func(t *testing.T) {
				home := t.TempDir()
				parent := makeDir(t, filepath.Join(home, "parent"))
				child := makeDir(t, filepath.Join(parent, "child"))
				writeSettings(t, parent, name, tc.content)

				_, err := Find(home, child)
				assertErrorNames(t, err, filepath.Join(resolved(t, parent), ".claude", name))
			})
		}
	}
}

func TestFindDanglingSettingsSymlinkErrors(t *testing.T) {
	home := t.TempDir()
	parent := makeDir(t, filepath.Join(home, "parent"))
	child := makeDir(t, filepath.Join(parent, "child"))
	claude := makeDir(t, filepath.Join(parent, ".claude"))
	link := filepath.Join(claude, "settings.json")
	if err := os.Symlink(filepath.Join(home, "missing.json"), link); err != nil {
		t.Fatalf("symlink %s: %v", link, err)
	}

	_, err := Find(home, child)
	assertErrorNames(t, err, filepath.Join(resolved(t, parent), ".claude", "settings.json"))
}

// danglingClaudeLink makes folder's .claude a symlink to a path under home that does not exist.
func danglingClaudeLink(t *testing.T, home, folder string) {
	t.Helper()
	link := filepath.Join(folder, ".claude")
	if err := os.Symlink(filepath.Join(home, "missing-claude"), link); err != nil {
		t.Fatalf("symlink %s: %v", link, err)
	}
}

func TestFindDanglingClaudeSymlinkErrors(t *testing.T) {
	t.Run("folder itself", func(t *testing.T) {
		home := t.TempDir()
		parent := makeDir(t, filepath.Join(home, "parent"))
		child := makeDir(t, filepath.Join(parent, "child"))
		danglingClaudeLink(t, home, child)

		_, err := Find(home, child)
		assertErrorNames(t, err, filepath.Join(resolved(t, child), ".claude"))
	})
	t.Run("folder above dir", func(t *testing.T) {
		home := t.TempDir()
		parent := makeDir(t, filepath.Join(home, "parent"))
		child := makeDir(t, filepath.Join(parent, "child"))
		danglingClaudeLink(t, home, parent)

		_, err := Find(home, child)
		assertErrorNames(t, err, filepath.Join(resolved(t, parent), ".claude"))
	})
	t.Run("worktree primary", func(t *testing.T) {
		layout := makeStampedWorktree(t)
		primary := filepath.Join(layout.home, "stamped", "repo")
		danglingClaudeLink(t, layout.home, primary)

		_, err := Find(layout.home, layout.worktree)
		assertErrorNames(t, err, filepath.Join(resolved(t, primary), ".claude"))
	})
}

func TestFindDanglingLocalSettingsSymlinkErrors(t *testing.T) {
	home := t.TempDir()
	parent := makeDir(t, filepath.Join(home, "parent"))
	child := makeDir(t, filepath.Join(parent, "child"))
	claude := makeDir(t, filepath.Join(parent, ".claude"))
	link := filepath.Join(claude, "settings.local.json")
	if err := os.Symlink(filepath.Join(home, "missing.json"), link); err != nil {
		t.Fatalf("symlink %s: %v", link, err)
	}

	_, err := Find(home, child)
	assertErrorNames(t, err, filepath.Join(resolved(t, parent), ".claude", "settings.local.json"))
}

func TestFindModeZeroSettingsFileErrors(t *testing.T) {
	for _, name := range []string{"settings.json", "settings.local.json"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			parent := makeDir(t, filepath.Join(home, "parent"))
			child := makeDir(t, filepath.Join(parent, "child"))
			writeSettings(t, parent, name, stampJSON(urlOuter))
			path := filepath.Join(parent, ".claude", name)
			if err := os.Chmod(path, 0); err != nil {
				t.Fatalf("chmod %s: %v", path, err)
			}

			_, err := Find(home, child)
			assertErrorNames(t, err, filepath.Join(resolved(t, parent), ".claude", name))
		})
	}
}

func TestFindFolderWithoutClaudeHasNoStampOrError(t *testing.T) {
	cases := []struct {
		name string
		// claude makes parent's .claude, if the case has one.
		claude bool
	}{
		{"no .claude", false},
		{"empty .claude directory", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			parent := makeDir(t, filepath.Join(home, "parent"))
			child := makeDir(t, filepath.Join(parent, "child"))
			if tc.claude {
				makeDir(t, filepath.Join(parent, ".claude"))
			}

			assertNoStamp(t, findStamp(t, home, child))
		})
	}
}

func TestFindSkipsFileWithoutEnv(t *testing.T) {
	home := t.TempDir()
	stamped := makeDir(t, filepath.Join(home, "stamped"))
	writeSettings(t, stamped, "settings.json", stampJSON(urlOuter))
	child := makeDir(t, filepath.Join(stamped, "child"))
	writeSettings(t, child, "settings.local.json", `{"disableAllHooks": true}`)

	assertStamp(t, findStamp(t, home, child), resolved(t, stamped), urlOuter)
}

func TestFindRefusesDirOutsideHome(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	writeSettings(t, outside, "settings.json", stampJSON(urlOuter))
	inside := makeDir(t, filepath.Join(home, "session"))
	cases := []struct {
		name string
		home string
		dir  string
	}{
		{"folder outside home", home, outside},
		{"relative dir", home, "session"},
		{"empty dir", home, ""},
		{"empty home", "", inside},
		{"relative home", "relative-home", inside},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNoStamp(t, findStamp(t, tc.home, tc.dir))
		})
	}
}

func TestSettingsShapes(t *testing.T) {
	env := map[string]string{"ANTHROPIC_BASE_URL": urlOuter}
	envJSON := `{"ANTHROPIC_BASE_URL":"` + urlOuter + `"}`
	cases := []struct {
		name            string
		disableAllHooks bool
		env             map[string]string
		want            string
	}{
		{"nothing set", false, nil, `{}`},
		{"empty env", false, map[string]string{}, `{}`},
		{"hooks disabled", true, nil, `{"disableAllHooks":true}`},
		{"env only", false, env, `{"env":` + envJSON + `}`},
		{"hooks disabled with env", true, env, `{"disableAllHooks":true,"env":` + envJSON + `}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Settings(tc.disableAllHooks, tc.env); got != tc.want {
				t.Fatalf("Settings(%v, %v) = %s, want %s", tc.disableAllHooks, tc.env, got, tc.want)
			}
		})
	}
}

func TestBaseURLOfZeroStampIsEmpty(t *testing.T) {
	if got := (Stamp{}).BaseURL(); got != "" {
		t.Fatalf("zero stamp base URL = %q, want empty", got)
	}
	if got := (Stamp{Env: map[string]string{"ANTHROPIC_BASE_URL": urlOuter}}).BaseURL(); got != urlOuter {
		t.Fatalf("stamp base URL = %q, want %q", got, urlOuter)
	}
}
