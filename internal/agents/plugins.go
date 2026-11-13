package agents

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// installedPlugins is the shape of <config>/plugins/installed_plugins.json, which lists each plugin key, name@marketplace,
// with its installs.
type installedPlugins struct {
	Plugins map[string][]pluginInstall `json:"plugins"`
}

// pluginInstall is one install of a plugin, which is at InstallPath and has the given Scope.
type pluginInstall struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
}

// enabledFile is the part of a settings file that enables plugins: enabledPlugins maps each plugin key to whether the
// plugin is on.
type enabledFile struct {
	EnabledPlugins map[string]bool `json:"enabledPlugins"`
}

// decision is how the first settings file that names a plugin key sets it: on, and the index of that file in settings.
type decision struct {
	on   bool
	file int
}

// pluginAgents returns the scoped agent names of the Claude Code plugins that are installed under the config directory
// config and enabled by the settings files settings, highest precedence first. The installs are listed in
// <config>/plugins/installed_plugins.json; only user and managed installs count, and a missing file lists none. A plugin
// is enabled by the first settings file, in settings order, that names its key in enabledPlugins. A plugin that no file
// names is disabled, and a missing settings file is skipped. Plugins are listed by the index of the file that decides
// them, then by key. Each plugin's agents are the .md files under its install's agents folder, named by frontmatter name
// or else by filename, and prefixed by the plugin and its subfolders: <plugin>:<sub>:<name>. An unparsable file is an
// error that names it.
func pluginAgents(config string, settings []string) ([]string, error) {
	var installed installedPlugins
	if err := readJSON(filepath.Join(config, "plugins", "installed_plugins.json"), &installed); err != nil {
		return nil, err
	}
	decided, err := decide(settings)
	if err != nil {
		return nil, err
	}
	var keys []string
	for key := range installed.Plugins {
		if decided[key].on {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b string) int {
		if c := cmp.Compare(decided[a].file, decided[b].file); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	var names []string
	for _, key := range keys {
		plugin, _, _ := strings.Cut(key, "@")
		for _, install := range installed.Plugins[key] {
			if install.Scope != "user" && install.Scope != "managed" {
				continue
			}
			var found []string
			found, err = pluginDirAgents(plugin, filepath.Join(install.InstallPath, "agents"))
			if err != nil {
				return nil, err
			}
			names = append(names, found...)
		}
	}
	return names, nil
}

// decide returns, for each plugin key that a settings file names, the decision of the first file in settings that names
// it.
func decide(settings []string) (map[string]decision, error) {
	decided := make(map[string]decision)
	for i, file := range settings {
		var enabled enabledFile
		if err := readJSON(file, &enabled); err != nil {
			return nil, err
		}
		for key, on := range enabled.EnabledPlugins {
			if _, named := decided[key]; !named {
				decided[key] = decision{on: on, file: i}
			}
		}
	}
	return decided, nil
}

// pluginDirAgents returns the scoped names of the agents in dir, the agents folder of plugin. A .md file is named by its
// frontmatter name, or else by its filename without .md, and is prefixed by plugin and by the folders between dir and
// the file.
func pluginDirAgents(plugin, dir string) ([]string, error) {
	var names []string
	err := walkMarkdown(dir, func(rel string, data []byte) error {
		name, _ := frontmatter(data)
		if name == "" {
			name = strings.TrimSuffix(path.Base(rel), ".md")
		}
		prefix := plugin
		if sub := path.Dir(rel); sub != "." {
			prefix += ":" + strings.ReplaceAll(sub, "/", ":")
		}
		names = append(names, prefix+":"+name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// readJSON decodes the JSON file into target. A file that does not exist leaves target as it was and is not an error.
// Any other read or parse failure is an error that names the file.
func readJSON(file string, target any) error {
	data, err := os.ReadFile(filepath.Clean(file))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agents: read %s: %w", file, err)
	}
	if err = json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("agents: parse %s: %w", file, err)
	}
	return nil
}
