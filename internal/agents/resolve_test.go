package agents

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gitHead is the HEAD file that makes a folder's .git a git directory, so that the folder is a work-tree root.
const gitHead = "ref: refs/heads/main\n"

// userInstall lists one user-scope install of plugin p, whose files are under ROOT/cache/p.
const userInstall = `{"version":2,"plugins":{"p@m":[{"scope":"user","installPath":"ROOT/cache/p"}]}}`

// projectInstall lists the same plugin installed at project scope, which Resolve does not count.
const projectInstall = `{"version":2,"plugins":{"p@m":[{"scope":"project","installPath":"ROOT/cache/p"}]}}`

// agentMD is an agent file that defines an agent named name.
func agentMD(name string) string {
	return "---\nname: " + name + "\ndescription: Reviews code\n---\n"
}

// pluginFiles is the tree of one user-scope install of plugin p, with agent a and nested agent review/sec, merged with
// extra. The install is listed under HOME's default config directory.
func pluginFiles(extra map[string]string) map[string]string {
	files := map[string]string{
		"home/.claude/plugins/installed_plugins.json": userInstall,
		"cache/p/agents/a.md":                         "body\n",
		"cache/p/agents/review/sec.md":                "body\n",
	}
	maps.Copy(files, extra)
	return files
}

// place names the folders of a Where relative to the test tree. An empty field is unset.
type place struct {
	dir, home, config, managed string
}

// under joins rel to root, and leaves an empty rel empty, so that the Where field it fills stays unset.
func under(root, rel string) string {
	if rel == "" {
		return ""
	}
	return filepath.Join(root, rel)
}

// setUp writes files under a new temp tree, replacing ROOT in each file's content with the tree's path, creates the
// step directory and HOME, and returns the tree with its symlinks resolved and the Where that p names under it.
func setUp(t *testing.T, files map[string]string, p place) (string, Where) {
	t.Helper()
	root, err := filepath.EvalSymlinks(pluginRoot(t, files))
	if err != nil {
		t.Fatalf("resolve tree: %v", err)
	}
	for _, rel := range []string{p.dir, p.home} {
		if err = os.MkdirAll(under(root, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	return root, Where{
		Dir:       under(root, p.dir),
		Home:      under(root, p.home),
		ConfigDir: under(root, p.config),
		Managed:   under(root, p.managed),
	}
}

// resolveCase is one call to Resolve over a tree. A refused case must return an error wrapping ErrUnresolved that
// names the agent and each checked path, which is relative to the tree. A resolved case must return nil.
type resolveCase struct {
	name    string
	files   map[string]string
	place   place
	agent   string
	refused bool
	checked []string
}

// runResolveCases runs each case as a subtest.
func runResolveCases(t *testing.T, tests []resolveCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, where := setUp(t, tt.files, tt.place)
			err := Resolve(tt.agent, where)
			if !tt.refused {
				if err != nil {
					t.Errorf("Resolve(%q) = %v; want nil", tt.agent, err)
				}
				return
			}
			if !errors.Is(err, ErrUnresolved) {
				t.Fatalf("Resolve(%q) = %v; want an error wrapping ErrUnresolved", tt.agent, err)
			}
			if !strings.Contains(err.Error(), strconv.Quote(tt.agent)) {
				t.Errorf("Resolve(%q) = %v; want the error to name the agent", tt.agent, err)
			}
			for _, rel := range tt.checked {
				if want := filepath.Join(root, rel); !strings.Contains(err.Error(), want) {
					t.Errorf("Resolve(%q) = %v; want the error to name %s", tt.agent, err, want)
				}
			}
		})
	}
}

func TestResolveLocations(t *testing.T) {
	runResolveCases(t, []resolveCase{
		{
			name:  "user directory under HOME",
			files: map[string]string{"home/.claude/agents/u.md": agentMD("u")},
			place: place{dir: "home/proj", home: "home"},
			agent: "u",
		},
		{
			name:  "user directory under CLAUDE_CONFIG_DIR",
			files: map[string]string{"cfg/agents/c.md": agentMD("c")},
			place: place{dir: "home/proj", home: "home", config: "cfg"},
			agent: "c",
		},
		{
			name: "project directory at the git root",
			files: map[string]string{
				"home/proj/.git/HEAD":           gitHead,
				"home/proj/.claude/agents/p.md": agentMD("p"),
			},
			place: place{dir: "home/proj", home: "home"},
			agent: "p",
		},
		{
			name: "project directory above a nested step directory",
			files: map[string]string{
				"home/proj/.git/HEAD":           gitHead,
				"home/proj/.claude/agents/p.md": agentMD("p"),
			},
			place: place{dir: "home/proj/step/deep", home: "home"},
			agent: "p",
		},
		{
			name:  "managed directory",
			files: map[string]string{"managed/.claude/agents/m.md": agentMD("m")},
			place: place{dir: "home/proj", home: "home", managed: "managed"},
			agent: "m",
		},
	})
}

func TestResolveBuiltins(t *testing.T) {
	names := []string{"claude", "Explore", "general-purpose", "Plan", "statusline-setup"}
	tests := make([]resolveCase, 0, len(names))
	for _, name := range names {
		tests = append(tests, resolveCase{name: name, place: place{dir: "home/proj", home: "home"}, agent: name})
	}
	runResolveCases(t, tests)
}

func TestResolveRefuses(t *testing.T) {
	home := place{dir: "home/proj", home: "home"}
	runResolveCases(t, []resolveCase{
		{
			name: "name nothing defines names every place checked",
			files: map[string]string{
				"home/proj/.git/HEAD":      gitHead,
				"home/.claude/agents/u.md": agentMD("u"),
			},
			place:   place{dir: "home/proj", home: "home", managed: "managed"},
			agent:   "missing",
			refused: true,
			checked: []string{
				"home/proj/.claude/agents",
				"home/.claude/agents",
				"managed/.claude/agents",
				"managed/managed-settings.json",
				"home/proj/.claude/settings.local.json",
				"home/proj/.claude/settings.json",
				"home/.claude/settings.json",
				"home/.claude/plugins/installed_plugins.json",
			},
		},
		{
			name:    "CLAUDE_CONFIG_DIR replaces HOME's agents directory",
			files:   map[string]string{"home/.claude/agents/u.md": agentMD("u")},
			place:   place{dir: "home/proj", home: "home", config: "cfg"},
			agent:   "u",
			refused: true,
			checked: []string{"cfg/agents"},
		},
		{
			name: "project directory above the git root",
			files: map[string]string{
				"home/outer/.claude/agents/o.md": agentMD("o"),
				"home/outer/repo/.git/HEAD":      gitHead,
			},
			place:   place{dir: "home/outer/repo/step", home: "home"},
			agent:   "o",
			refused: true,
			checked: []string{"home/outer/repo/.claude/agents"},
		},
		{
			name:    "project directory above HOME",
			files:   map[string]string{"above/.claude/agents/z.md": agentMD("z")},
			place:   home,
			agent:   "z",
			refused: true,
			checked: []string{"home/proj/.claude/agents"},
		},
		{
			name:    "agent name is matched exactly",
			files:   map[string]string{"home/.claude/agents/u.md": agentMD("reviewer")},
			place:   home,
			agent:   "Reviewer",
			refused: true,
		},
		{
			name:    "web-fetch with no file",
			place:   home,
			agent:   "web-fetch",
			refused: true,
		},
		{
			name:    "web-fetch defined by a user file",
			files:   map[string]string{"home/.claude/agents/w.md": agentMD("web-fetch")},
			place:   home,
			agent:   "web-fetch",
			refused: true,
		},
	})
}

func TestResolvePlugins(t *testing.T) {
	home := place{dir: "home/proj", home: "home"}
	enabled := `{"enabledPlugins":{"p@m":true}}`
	disabled := `{"enabledPlugins":{"p@m":false}}`
	runResolveCases(t, []resolveCase{
		{
			name:  "scoped name resolves",
			files: pluginFiles(map[string]string{"home/.claude/settings.json": enabled}),
			place: home,
			agent: "p:a",
		},
		{
			name:  "bare name resolves",
			files: pluginFiles(map[string]string{"home/.claude/settings.json": enabled}),
			place: home,
			agent: "a",
		},
		{
			name:  "nested scoped name resolves",
			files: pluginFiles(map[string]string{"home/.claude/settings.json": enabled}),
			place: home,
			agent: "p:review:sec",
		},
		{
			name:  "bare name of a nested agent resolves",
			files: pluginFiles(map[string]string{"home/.claude/settings.json": enabled}),
			place: home,
			agent: "sec",
		},
		{
			name: "plugin enabled by the work-tree root's settings.local.json resolves",
			files: pluginFiles(map[string]string{
				"home/proj/.git/HEAD":                   gitHead,
				"home/proj/.claude/settings.local.json": enabled,
			}),
			place: home,
			agent: "a",
		},
		{
			name: "plugin enabled by the work-tree root's settings.json resolves",
			files: pluginFiles(map[string]string{
				"home/proj/.git/HEAD":             gitHead,
				"home/proj/.claude/settings.json": enabled,
			}),
			place: home,
			agent: "a",
		},
		{
			name:    "disabled plugin's scoped name is refused",
			files:   pluginFiles(map[string]string{"home/.claude/settings.json": disabled}),
			place:   home,
			agent:   "p:a",
			refused: true,
			checked: []string{"home/.claude/settings.json", "home/.claude/plugins/installed_plugins.json"},
		},
		{
			name:    "disabled plugin's bare name is refused",
			files:   pluginFiles(map[string]string{"home/.claude/settings.json": disabled}),
			place:   home,
			agent:   "a",
			refused: true,
		},
		{
			name: "managed settings disable a plugin the user enables",
			files: pluginFiles(map[string]string{
				"home/.claude/settings.json":    enabled,
				"managed/managed-settings.json": disabled,
			}),
			place:   place{dir: "home/proj", home: "home", managed: "managed"},
			agent:   "a",
			refused: true,
		},
		{
			name: "project-scope install is refused",
			files: pluginFiles(map[string]string{
				"home/.claude/settings.json":                  enabled,
				"home/.claude/plugins/installed_plugins.json": projectInstall,
			}),
			place:   home,
			agent:   "a",
			refused: true,
		},
		{
			name:    "plugin agent name is matched exactly",
			files:   pluginFiles(map[string]string{"home/.claude/settings.json": enabled}),
			place:   home,
			agent:   "P:a",
			refused: true,
		},
	})
}

func TestResolveLinkedWorktree(t *testing.T) {
	enabled := `{"enabledPlugins":{"p@m":true}}`
	disabled := `{"enabledPlugins":{"p@m":false}}`
	// files builds a linked worktree at home/wt whose primary checkout is home/main: the primary's settings.local.json
	// and the worktree's settings.json are the two files that could decide plugin p.
	files := func(primaryLocal, worktreeShared string) map[string]string {
		return pluginFiles(map[string]string{
			"home/main/.git/HEAD":                   gitHead,
			"home/main/.git/worktrees/wt/commondir": "../..\n",
			"home/wt/.git":                          "gitdir: ROOT/home/main/.git/worktrees/wt\n",
			"home/main/.claude/settings.local.json": primaryLocal,
			"home/wt/.claude/settings.json":         worktreeShared,
		})
	}
	home := place{dir: "home/wt", home: "home"}
	runResolveCases(t, []resolveCase{
		{
			name:  "primary's settings.local.json enables over the worktree's settings.json",
			files: files(enabled, disabled),
			place: home,
			agent: "a",
		},
		{
			name:    "primary's settings.local.json disables over the worktree's settings.json",
			files:   files(disabled, enabled),
			place:   home,
			agent:   "a",
			refused: true,
		},
	})
}

func TestResolveBadWhere(t *testing.T) {
	root, where := setUp(t, nil, place{dir: "home/proj", home: "home"})
	tests := []struct {
		name  string
		where Where
	}{
		{name: "empty home", where: Where{Dir: where.Dir}},
		{name: "relative home", where: Where{Dir: where.Dir, Home: "home"}},
		{name: "relative dir", where: Where{Dir: "proj", Home: where.Home}},
		{name: "missing dir", where: Where{Dir: filepath.Join(root, "absent"), Home: where.Home}},
		{name: "relative config directory", where: Where{Dir: where.Dir, Home: where.Home, ConfigDir: "cfg"}},
		{name: "relative managed directory", where: Where{Dir: where.Dir, Home: where.Home, Managed: "managed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Resolve("reviewer", tt.where); err == nil {
				t.Errorf("Resolve(reviewer) = nil; want an error")
			}
		})
	}
}

// assertReadError fails unless err is a read or parse error that names path and does not wrap ErrUnresolved.
func assertReadError(t *testing.T, err error, path string) {
	t.Helper()
	if err == nil || errors.Is(err, ErrUnresolved) || !strings.Contains(err.Error(), path) {
		t.Errorf("Resolve() = %v; want a read error naming %s that does not wrap ErrUnresolved", err, path)
	}
}

func TestResolveReadErrors(t *testing.T) {
	home := place{dir: "home/proj", home: "home"}
	t.Run("unparsable settings file names the file", func(t *testing.T) {
		root, where := setUp(t, map[string]string{"home/.claude/settings.json": "{not json"}, home)
		assertReadError(t, Resolve("x", where), filepath.Join(root, "home/.claude/settings.json"))
	})
	t.Run("unreadable agent file names the file", func(t *testing.T) {
		root, where := setUp(t, map[string]string{"home/.claude/agents/locked.md": agentMD("x")}, home)
		locked := filepath.Join(root, "home/.claude/agents/locked.md")
		lock(t, locked)
		assertReadError(t, Resolve("x", where), locked)
	})
}
