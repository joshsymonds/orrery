package stamp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStepMemoryExcluded checks the user CLAUDE.md path that an agent's settings exclude: the config directory's when
// CLAUDE_CONFIG_DIR is set, home's .claude folder otherwise, and never the target of a symlink.
func TestStepMemoryExcluded(t *testing.T) {
	t.Parallel()
	t.Run("the path comes from CLAUDE_CONFIG_DIR when set", func(t *testing.T) {
		t.Parallel()
		home, configDir := t.TempDir(), t.TempDir()
		got, err := UserMemory(home, configDir)
		if err != nil {
			t.Fatalf("UserMemory: %v", err)
		}
		if want := filepath.Join(configDir, "CLAUDE.md"); got != want {
			t.Fatalf("UserMemory = %q, want %q", got, want)
		}
	})

	t.Run("the path is the home directory's .claude/CLAUDE.md otherwise", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		got, err := UserMemory(home, "")
		if err != nil {
			t.Fatalf("UserMemory: %v", err)
		}
		if want := filepath.Join(home, ".claude", "CLAUDE.md"); got != want {
			t.Fatalf("UserMemory = %q, want %q", got, want)
		}
	})

	t.Run("a symlinked CLAUDE.md is named by its unresolved path", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		shared := filepath.Join(root, "shared", "rules.md")
		if err := os.Mkdir(filepath.Dir(shared), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, shared, "shared rules\n")
		configDir := filepath.Join(root, "config")
		if err := os.Mkdir(configDir, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(configDir, "CLAUDE.md")
		if err := os.Symlink(shared, link); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(link)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("CLAUDE.md is not a symlink: %v", err)
		}
		got, err := UserMemory(root, configDir)
		if err != nil {
			t.Fatalf("UserMemory: %v", err)
		}
		if got != link {
			t.Fatalf("UserMemory = %q, want the unresolved %q", got, link)
		}
	})

	t.Run("a relative CLAUDE_CONFIG_DIR is made absolute", func(t *testing.T) {
		t.Parallel()
		got, err := UserMemory(t.TempDir(), filepath.Join("rel", "config"))
		if err != nil {
			t.Fatalf("UserMemory: %v", err)
		}
		if !filepath.IsAbs(got) || !strings.HasSuffix(got, filepath.Join("rel", "config", "CLAUDE.md")) {
			t.Fatalf("UserMemory = %q, want an absolute path ending rel/config/CLAUDE.md", got)
		}
	})

	t.Run("an empty or relative home without CLAUDE_CONFIG_DIR is an error", func(t *testing.T) {
		t.Parallel()
		for _, home := range []string{"", "relative-home"} {
			if got, err := UserMemory(home, ""); err == nil {
				t.Fatalf("UserMemory(%q, \"\") = %q, want an error", home, got)
			}
		}
	})
}
