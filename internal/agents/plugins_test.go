package agents

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// pluginRoot writes files under a new temp root, replacing ROOT in each file's content with the root's path, and
// returns the root.
func pluginRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	replaced := make(map[string]string, len(files))
	for rel, content := range files {
		replaced[rel] = strings.ReplaceAll(content, "ROOT", root)
	}
	return writeTree(t, root, replaced)
}

func TestPluginAgents(t *testing.T) {
	const (
		installedP  = `{"version":2,"plugins":{"p@m":[{"scope":"user","installPath":"ROOT/cache/p"}]}}`
		installedPQ = `{"version":2,"plugins":{"p@m":[{"scope":"user","installPath":"ROOT/cache/p"}],` +
			`"q@m":[{"scope":"user","installPath":"ROOT/cache/q"}]}}`
		enabledP = `{"enabledPlugins":{"p@m":true}}`
	)
	tests := []struct {
		name     string
		files    map[string]string
		settings []string
		want     []string
	}{
		{
			name: "enabled user install",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedP,
				"cache/p/agents/a.md":                   "body\n",
				"settings.json":                         enabledP,
			},
			settings: []string{"settings.json"},
			want:     []string{"p:a"},
		},
		{
			name: "nested agent joins its folders",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedP,
				"cache/p/agents/review/sec.md":          "body\n",
				"settings.json":                         enabledP,
			},
			settings: []string{"settings.json"},
			want:     []string{"p:review:sec"},
		},
		{
			name: "frontmatter name replaces filename",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedP,
				"cache/p/agents/good.md":                "---\nname: renamed\ndescription: Reviews code\n---\n",
				"settings.json":                         enabledP,
			},
			settings: []string{"settings.json"},
			want:     []string{"p:renamed"},
		},
		{
			name: "file without a name is named by its filename",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedP,
				"cache/p/agents/fallback.md":            "---\ndescription: Reviews code\n---\n",
				"settings.json":                         enabledP,
			},
			settings: []string{"settings.json"},
			want:     []string{"p:fallback"},
		},
		{
			name: "managed install counts",
			files: map[string]string{
				"config/plugins/installed_plugins.json": `{"version":2,"plugins":{"p@m":[{"scope":"managed",` +
					`"installPath":"ROOT/cache/p"}]}}`,
				"cache/p/agents/a.md": "body\n",
				"settings.json":       enabledP,
			},
			settings: []string{"settings.json"},
			want:     []string{"p:a"},
		},
		{
			name: "project and local installs are ignored",
			files: map[string]string{
				"config/plugins/installed_plugins.json": `{"version":2,"plugins":{` +
					`"p@m":[{"scope":"project","installPath":"ROOT/cache/p"}],` +
					`"q@m":[{"scope":"local","installPath":"ROOT/cache/q"}]}}`,
				"cache/p/agents/a.md": "body\n",
				"cache/q/agents/a.md": "body\n",
				"settings.json":       `{"enabledPlugins":{"p@m":true,"q@m":true}}`,
			},
			settings: []string{"settings.json"},
			want:     nil,
		},
		{
			name: "plugin set to false gives nothing",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedP,
				"cache/p/agents/a.md":                   "body\n",
				"settings.json":                         `{"enabledPlugins":{"p@m":false}}`,
			},
			settings: []string{"settings.json"},
			want:     nil,
		},
		{
			name: "plugin no settings file names gives nothing",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedP,
				"cache/p/agents/a.md":                   "body\n",
				"settings.json":                         `{"enabledPlugins":{"other@m":true}}`,
			},
			settings: []string{"settings.json"},
			want:     nil,
		},
		{
			name: "missing settings file is skipped",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedP,
				"cache/p/agents/a.md":                   "body\n",
				"settings.json":                         enabledP,
			},
			settings: []string{"missing.json", "settings.json"},
			want:     []string{"p:a"},
		},
		{
			name: "higher false overrides lower true",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedP,
				"cache/p/agents/a.md":                   "body\n",
				"local.json":                            `{"enabledPlugins":{"p@m":false}}`,
				"settings.json":                         enabledP,
			},
			settings: []string{"local.json", "settings.json"},
			want:     nil,
		},
		{
			name: "higher true overrides lower false",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedP,
				"cache/p/agents/a.md":                   "body\n",
				"local.json":                            enabledP,
				"settings.json":                         `{"enabledPlugins":{"p@m":false}}`,
			},
			settings: []string{"local.json", "settings.json"},
			want:     []string{"p:a"},
		},
		{
			name: "missing installed file gives none",
			files: map[string]string{
				"cache/p/agents/a.md": "body\n",
				"settings.json":       enabledP,
			},
			settings: []string{"settings.json"},
			want:     nil,
		},
		{
			name: "earlier settings file lists its plugin first",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedPQ,
				"cache/p/agents/a.md":                   "body\n",
				"cache/q/agents/a.md":                   "body\n",
				"higher.json":                           `{"enabledPlugins":{"q@m":true}}`,
				"lower.json":                            enabledP,
			},
			settings: []string{"higher.json", "lower.json"},
			want:     []string{"q:a", "p:a"},
		},
		{
			name: "plugins from one settings file list by key",
			files: map[string]string{
				"config/plugins/installed_plugins.json": installedPQ,
				"cache/p/agents/a.md":                   "body\n",
				"cache/q/agents/a.md":                   "body\n",
				"settings.json":                         `{"enabledPlugins":{"q@m":true,"p@m":true}}`,
			},
			settings: []string{"settings.json"},
			want:     []string{"p:a", "q:a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := pluginRoot(t, tt.files)
			settings := make([]string, len(tt.settings))
			for i, s := range tt.settings {
				settings[i] = filepath.Join(root, s)
			}
			got, err := pluginAgents(filepath.Join(root, "config"), settings)
			if err != nil {
				t.Fatalf("pluginAgents: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("pluginAgents = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPluginAgentsErrors(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		settings []string
		wantPath string
	}{
		{
			name: "unparsable installed file names it",
			files: map[string]string{
				"config/plugins/installed_plugins.json": "{not json",
				"settings.json":                         `{"enabledPlugins":{"p@m":true}}`,
			},
			settings: []string{"settings.json"},
			wantPath: "config/plugins/installed_plugins.json",
		},
		{
			name: "unparsable settings file names it",
			files: map[string]string{
				"config/plugins/installed_plugins.json": `{"version":2,"plugins":{"p@m":[]}}`,
				"bad.json":                              "{not json",
			},
			settings: []string{"bad.json"},
			wantPath: "bad.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := pluginRoot(t, tt.files)
			settings := make([]string, len(tt.settings))
			for i, s := range tt.settings {
				settings[i] = filepath.Join(root, s)
			}
			_, err := pluginAgents(filepath.Join(root, "config"), settings)
			if err == nil {
				t.Fatal("pluginAgents returned no error")
			}
			if !strings.HasPrefix(err.Error(), "agents:") {
				t.Errorf("error %q does not start with agents:", err)
			}
			if !strings.Contains(err.Error(), filepath.Join(root, tt.wantPath)) {
				t.Errorf("error %q does not name %s", err, filepath.Join(root, tt.wantPath))
			}
		})
	}
}
