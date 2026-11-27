package node

import (
	"os"
	"path/filepath"
	"testing"
)

// readAboveRootsFixture is an agent whose one root lies two folders below a home, with a managed settings directory
// beside the home. Each folder above the root, and the managed directory, holds Claude settings a controller reads when
// it resolves a step's agents and stamp.
type readAboveRootsFixture struct {
	addr    string
	home    string
	mid     string // the folder between home and the root
	root    string
	managed string
}

// startReadAboveRoots makes the folders of a readAboveRootsFixture, with a settings file in each .claude and in the
// managed directory, and a private file beside each, and serves its agent until the test ends.
func startReadAboveRoots(t *testing.T) readAboveRootsFixture {
	t.Helper()
	base := t.TempDir()
	f := readAboveRootsFixture{
		home:    filepath.Join(base, "home"),
		mid:     filepath.Join(base, "home", "Personal"),
		root:    filepath.Join(base, "home", "Personal", "repos"),
		managed: filepath.Join(base, "etc", "claude-code"),
	}
	makeDirs(t, base, "home/.claude", "home/Personal/.claude", "home/Personal/repos", "etc/claude-code/.claude/agents")
	for path, data := range map[string]string{
		filepath.Join(f.home, ".claude", "settings.json"):                    "{}",
		filepath.Join(f.mid, ".claude", "settings.local.json"):               "{}",
		filepath.Join(f.managed, "managed-settings.json"):                    "{}",
		filepath.Join(f.managed, ".claude", "agents", "planner.md"):          "agent",
		filepath.Join(f.home, "private"):                                     "secret",
		filepath.Join(f.mid, "private"):                                      "secret",
		filepath.Join(filepath.Dir(f.managed), "elsewhere-managed-settings"): "secret",
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.addr = startConfiguredAgent(t, AgentConfig{Roots: []string{f.root}, ManagedDir: f.managed})
	return f
}

// TestNodeV2OpsReadsClaudeSettingsAboveRoots shows that the read-only file ops may name what Claude Code reads for a
// process under the agent's root: each folder above the root, to stat or resolve; anything in such a folder's .claude;
// and anything under the managed directory. Those are what a controller reads to resolve a step's agents and stamp on
// this host.
func TestNodeV2OpsReadsClaudeSettingsAboveRoots(t *testing.T) {
	f := startReadAboveRoots(t)
	for _, spec := range []FileSpec{
		{Op: FileEvalSymlinks, Path: f.home},
		{Op: FileStat, Path: f.home},
		{Op: FileLstat, Path: f.mid},
		{Op: FileLstat, Path: filepath.Join(f.home, ".claude", "settings.json")},
		{Op: FileRead, Path: filepath.Join(f.home, ".claude", "settings.json")},
		{Op: FileRead, Path: filepath.Join(f.mid, ".claude", "settings.local.json")},
		{Op: FileReadDir, Path: filepath.Join(f.mid, ".claude")},
		{Op: FileStat, Path: filepath.Join(f.mid, ".claude", "agents")},
		{Op: FileRead, Path: filepath.Join(f.managed, "managed-settings.json")},
		{Op: FileReadDir, Path: filepath.Join(f.managed, ".claude", "agents")},
		{Op: FileReadRooted, Root: filepath.Join(f.managed, ".claude", "agents"), Name: "planner.md"},
	} {
		t.Run(string(spec.Op)+" "+spec.Path+spec.Root, func(t *testing.T) {
			if resp := v2File(t, f.addr, spec); resp.Code != "" {
				t.Fatalf("%s answered %+v, want no code", spec.Op, resp)
			}
		})
	}
}

// TestNodeV2OpsRefusesAboveRootsBeyondClaudeSettings shows what stays refused above the agent's root: reading or listing
// a folder above it, or a file in one outside its .claude, reading beside the managed directory, and every op that writes,
// in a .claude above the root or in the managed directory alike. Nothing a refused write names is made.
func TestNodeV2OpsRefusesAboveRootsBeyondClaudeSettings(t *testing.T) {
	f := startReadAboveRoots(t)
	for _, spec := range []FileSpec{
		{Op: FileReadDir, Path: f.home},
		{Op: FileReadDir, Path: f.mid},
		{Op: FileRead, Path: filepath.Join(f.home, "private")},
		{Op: FileStat, Path: filepath.Join(f.mid, "private")},
		{Op: FileReadRooted, Root: f.home, Name: "private"},
		{Op: FileRead, Path: filepath.Join(filepath.Dir(f.managed), "elsewhere-managed-settings")},
	} {
		t.Run(string(spec.Op)+" "+spec.Path+spec.Root, func(t *testing.T) {
			v2Refused(t, v2File(t, f.addr, spec))
		})
	}
	for _, path := range []string{
		filepath.Join(f.home, ".claude", "made.json"),
		filepath.Join(f.mid, ".claude", "made.json"),
		filepath.Join(f.managed, "made.json"),
	} {
		t.Run("write "+path, func(t *testing.T) {
			v2Refused(t, v2File(t, f.addr, FileSpec{Op: FileWrite, Path: path, Data: []byte("x"), Perm: 0o600}))
			v2Absent(t, path)
		})
		t.Run("mkdir-all "+path, func(t *testing.T) {
			v2Refused(t, v2File(t, f.addr, FileSpec{Op: FileMkdirAll, Path: path, Perm: 0o700}))
			v2Absent(t, path)
		})
	}
	settings := filepath.Join(f.mid, ".claude", "settings.local.json")
	v2Refused(t, v2File(t, f.addr, FileSpec{Op: FileRemove, Path: settings}))
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("refused remove took %s: %v", settings, err)
	}
}
