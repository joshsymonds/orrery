package open

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Root is one declared directory tree: the intent lookup lists its subdirectories down to Depth levels.
type Root struct {
	Dir   string // absolute path of the root directory
	Depth int    // levels below Dir to list, at least 1
}

// RootsPath resolves the roots file: $XDG_CONFIG_HOME/orrery/roots when XDG_CONFIG_HOME is set, else
// $HOME/.config/orrery/roots. It returns "" when both are unset.
func RootsPath(getenv func(string) string) string {
	if configHome := getenv("XDG_CONFIG_HOME"); configHome != "" {
		return filepath.Join(configHome, "orrery", "roots")
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".config", "orrery", "roots")
	}
	return ""
}

// ReadRoots parses the roots file at path, resolving each PATH against home. Each non-blank line is
// "DEPTH PATH": DEPTH a positive decimal integer, PATH a local path that may contain spaces. An empty path
// or a missing file declares no roots.
func ReadRoots(path, home string) ([]Root, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read roots file %s: %w", path, err)
	}
	var roots []Root
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		root, parseErr := parseRootLine(line, home)
		if parseErr != nil {
			return nil, fmt.Errorf("roots file %s line %d: %w", path, i+1, parseErr)
		}
		roots = append(roots, root)
	}
	return roots, nil
}

// parseRootLine parses one "DEPTH PATH" line of the roots file.
func parseRootLine(line, home string) (Root, error) {
	depthText, rel, ok := strings.Cut(line, " ")
	if !ok {
		return Root{}, fmt.Errorf("want DEPTH PATH, got %q", line)
	}
	depth, err := parseDepth(depthText)
	if err != nil {
		return Root{}, err
	}
	if !filepath.IsLocal(rel) {
		return Root{}, fmt.Errorf("path %q is not local to home", rel)
	}
	if !filepath.IsAbs(home) {
		return Root{}, fmt.Errorf("home %q is not absolute", home)
	}
	return Root{Dir: filepath.Join(home, rel), Depth: depth}, nil
}

// parseDepth parses a positive decimal integer, rejecting signs and anything else.
func parseDepth(text string) (int, error) {
	if text == "" || strings.TrimLeft(text, "0123456789") != "" {
		return 0, fmt.Errorf("depth %q is not a positive integer", text)
	}
	depth, err := strconv.Atoi(text)
	if err != nil || depth < 1 {
		return 0, fmt.Errorf("depth %q is not a positive integer", text)
	}
	return depth, nil
}
