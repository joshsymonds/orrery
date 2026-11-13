package agents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/stamp"
)

// ErrUnresolved is wrapped by the error that Resolve returns for an agent name that Claude Code would not resolve.
var ErrUnresolved = errors.New("agent name does not resolve")

// Where is the environment of a step's claude process: its working directory Dir, its HOME, its CLAUDE_CONFIG_DIR
// ("" when unset) and the managed settings root Managed ("" reads none). Each non-empty field must be absolute.
type Where struct {
	Dir, Home, ConfigDir, Managed string
}

// claudeFolder is the .claude folder that holds agents and settings under a home, a project and the managed root.
const claudeFolder = ".claude"

// webFetch is a name that Claude Code does not resolve for --agent, so no file may define it.
const webFetch = "web-fetch"

// Resolve returns nil exactly when Claude Code 2.1.321 resolves --agent name for a process with where. A built-in
// agent resolves. Otherwise the agent folders of the project, the user and the managed root are searched, then the
// agents of the enabled plugins. A name that none of them defines returns an error wrapping ErrUnresolved that names
// the agent and each place checked. A file or settings file that cannot be read or parsed returns its own error.
func Resolve(name string, where Where) error {
	places, err := locate(where)
	if err != nil {
		return err
	}
	if slices.Contains([]string{"claude", "Explore", "general-purpose", "Plan", "statusline-setup"}, name) {
		return nil
	}
	defined, err := places.defines(name)
	if err != nil {
		return err
	}
	if defined {
		return nil
	}
	return fmt.Errorf("agents: %w: %q; checked %s", ErrUnresolved, name, strings.Join(places.checked(), ", "))
}

// locations are the places that Claude Code reads --agent names from for one Where.
type locations struct {
	agentDirs []string // agent folders, highest precedence first: managed, the project folders nearest first, then user
	config    string   // the config directory, which lists its plugins under plugins/
	settings  []string // the settings files that enable plugins, highest precedence first
}

// locate returns the locations of where. Home and dir are resolved through their symlinks, so both must exist.
func locate(where Where) (locations, error) {
	if !filepath.IsAbs(where.Home) || !filepath.IsAbs(where.Dir) {
		return locations{}, fmt.Errorf("agents: home %q and dir %q must be absolute", where.Home, where.Dir)
	}
	for _, optional := range []string{where.ConfigDir, where.Managed} {
		if optional != "" && !filepath.IsAbs(optional) {
			return locations{}, fmt.Errorf("agents: %q must be absolute", optional)
		}
	}
	home, err := filepath.EvalSymlinks(where.Home)
	if err != nil {
		return locations{}, fmt.Errorf("agents: resolve %s: %w", where.Home, err)
	}
	dir, err := filepath.EvalSymlinks(where.Dir)
	if err != nil {
		return locations{}, fmt.Errorf("agents: resolve %s: %w", where.Dir, err)
	}
	folders, root, err := projectFolders(home, dir)
	if err != nil {
		return locations{}, err
	}
	places := locations{config: where.ConfigDir}
	if places.config == "" {
		places.config = filepath.Join(home, claudeFolder)
	}
	if where.Managed != "" {
		places.agentDirs = append(places.agentDirs, filepath.Join(where.Managed, claudeFolder, "agents"))
		places.settings = append(places.settings, filepath.Join(where.Managed, "managed-settings.json"))
	}
	for _, folder := range folders {
		places.agentDirs = append(places.agentDirs, filepath.Join(folder, claudeFolder, "agents"))
	}
	places.agentDirs = append(places.agentDirs, filepath.Join(places.config, "agents"))
	project, err := projectSettings(where, root)
	if err != nil {
		return locations{}, err
	}
	places.settings = append(places.settings, project...)
	places.settings = append(places.settings, filepath.Join(places.config, "settings.json"))
	return places, nil
}

// projectFolders returns the folders whose .claude/agents Claude Code reads for a process in dir, nearest first: dir
// and each folder above it, up to the work-tree root, which is the nearest folder with a .git entry. The walk takes
// only the folders strictly below home. root is the work-tree root, or "" when no such folder has a .git entry, and
// then the folders run up to home.
func projectFolders(home, dir string) ([]string, string, error) {
	below := home + string(filepath.Separator)
	var folders []string
	for folder := dir; strings.HasPrefix(folder, below); folder = filepath.Dir(folder) {
		folders = append(folders, folder)
		hasGit, err := hasGitEntry(folder)
		if err != nil {
			return nil, "", err
		}
		if hasGit {
			return folders, folder, nil
		}
	}
	return folders, "", nil
}

// hasGitEntry reports whether folder has a .git entry, a directory or a file. An entry that cannot be read is an error
// that names it.
func hasGitEntry(folder string) (bool, error) {
	entry := filepath.Join(folder, ".git")
	_, err := os.Lstat(entry)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("agents: read %s: %w", entry, err)
	}
	return true, nil
}

// projectSettings returns the settings files that a project with work-tree root enables plugins from, highest first:
// the local file at the main checkout's root, which is the primary of a linked worktree and else root, then the
// shared file at root. With no work-tree root there are none.
func projectSettings(where Where, root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	primary, err := stamp.LinkedWorktreePrimary(where.Home, where.Dir)
	if err != nil {
		return nil, fmt.Errorf("agents: find the primary checkout of %s: %w", where.Dir, err)
	}
	if primary == "" {
		primary = root
	}
	return []string{
		filepath.Join(primary, claudeFolder, "settings.local.json"),
		filepath.Join(root, claudeFolder, "settings.json"),
	}, nil
}

// defines reports whether a location defines name: an agent file in one of agentDirs, or an enabled plugin's agent
// whose scoped name is name or ends in ":"+name. web-fetch is never defined.
func (l locations) defines(name string) (bool, error) {
	if name == webFetch {
		return false, nil
	}
	for _, dir := range l.agentDirs {
		names, err := dirAgents(dir)
		if err != nil {
			return false, err
		}
		if slices.Contains(names, name) {
			return true, nil
		}
	}
	scoped, err := pluginAgents(l.config, l.settings)
	if err != nil {
		return false, err
	}
	for _, agent := range scoped {
		if agent == name || strings.HasSuffix(agent, ":"+name) {
			return true, nil
		}
	}
	return false, nil
}

// checked lists every place that Resolve reads, for the error that names them.
func (l locations) checked() []string {
	installed := filepath.Join(l.config, "plugins", "installed_plugins.json")
	return append(slices.Concat(l.agentDirs, l.settings), installed)
}
