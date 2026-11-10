package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutsideRepository(t *testing.T) {
	t.Run("plain nested directory is outside", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "a", "b")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := OutsideRepository(dir); err != nil {
			t.Fatalf("OutsideRepository(%q) = %v, want nil", dir, err)
		}
	})

	t.Run("git directory in a parent is refused and named", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		err := OutsideRepository(dir)
		if err == nil {
			t.Fatalf("OutsideRepository(%q) = nil, want error naming %q", dir, root)
		}
		resolved, err2 := filepath.EvalSymlinks(root)
		if err2 != nil {
			t.Fatal(err2)
		}
		if want := filepath.Join(resolved, ".git"); !strings.Contains(err.Error(), want) {
			t.Fatalf("OutsideRepository(%q) = %v, want error naming the parent entry %q", dir, err, want)
		}
	})

	t.Run("symlink into a repository is refused", func(t *testing.T) {
		root := t.TempDir()
		repo := filepath.Join(root, "repo")
		sub := filepath.Join(repo, "sub")
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(sub, link); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(link, "x")
		if err := os.Mkdir(filepath.Join(sub, "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := OutsideRepository(dir); err == nil {
			t.Fatalf("OutsideRepository(%q) = nil, want error: the directory is inside a repository", dir)
		}
	})

	t.Run("git file in the directory itself is refused", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := OutsideRepository(dir)
		if err == nil {
			t.Fatalf("OutsideRepository(%q) = nil, want error", dir)
		}
		resolved, err2 := filepath.EvalSymlinks(dir)
		if err2 != nil {
			t.Fatal(err2)
		}
		if want := filepath.Join(resolved, ".git"); !strings.Contains(err.Error(), want) {
			t.Fatalf("OutsideRepository(%q) = %v, want error naming the entry %q", dir, err, want)
		}
	})

	t.Run("unreadable parent is an error, not absence", func(t *testing.T) {
		root := t.TempDir()
		locked := filepath.Join(root, "locked")
		dir := filepath.Join(locked, "child")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(locked, 0o755)
		})
		if err := OutsideRepository(dir); err == nil {
			t.Fatalf("OutsideRepository(%q) = nil, want error for unreadable parent", dir)
		}
	})
}
