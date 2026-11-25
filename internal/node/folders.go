package node

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// rootFolders returns the directories of root at levels 1 and 2 below root, sorted by path. A name beginning with "." is
// left out with everything under it, as is a symlink and any directory os.ReadDir cannot read.
func rootFolders(root string) []string {
	level1 := childFolders(root)
	folders := slices.Clone(level1)
	for _, dir := range level1 {
		folders = append(folders, childFolders(dir)...)
	}
	slices.Sort(folders)
	return folders
}

// childFolders returns the visible, readable directories directly inside parent, in the order os.ReadDir gives them. A
// parent that os.ReadDir cannot read contributes none.
func childFolders(parent string) []string {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	var children []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		child := filepath.Join(parent, entry.Name())
		if _, readErr := os.ReadDir(child); readErr != nil {
			continue
		}
		children = append(children, child)
	}
	return children
}

// checkFolder returns the resolved path of dir, when dir is an absolute path to an existing directory that is root or lies
// under it. Both are resolved with their symlinks followed, so a symlink that leads out of root is refused. Any other dir is
// refused with the reason it fails.
func checkFolder(root, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("node: folder %s is not an absolute path", dir)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("node: resolve the agent's root %s: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("node: resolve folder %s: %w", dir, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("node: inspect folder %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("node: folder %s is not a directory", dir)
	}
	if !within(realRoot, resolved) {
		return "", fmt.Errorf("node: folder %s is not under the agent's root %s", dir, root)
	}
	return resolved, nil
}

// within reports whether path is root or lies under it. Both are absolute paths that are clean, as EvalSymlinks returns them.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
