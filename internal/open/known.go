package open

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/store"
)

// historyDirs returns the directories orrery's history names, in order: the @orrery_dir of each non-shell session in
// sessions, the archive's record directories newest first, then the store's engine and step directories. Each is
// cleaned and listed once, and kept only when os.Stat reports a directory. An empty cfg.Archive names none, and a
// store the reader cannot reach names none; any other store error fails the lookup.
func historyDirs(ctx context.Context, cfg Config, sessions []session) ([]string, error) {
	var candidates []string
	for _, s := range sessions {
		if !isShell(s.name) && s.dir != "" {
			candidates = append(candidates, s.dir)
		}
	}
	if cfg.Archive != "" {
		archived, err := archive.Dirs(cfg.Archive)
		if err != nil {
			return nil, fmt.Errorf("open: archive dirs: %w", err)
		}
		candidates = append(candidates, archived...)
	}
	if cfg.Store != nil {
		stored, err := cfg.Store.Dirs(ctx)
		if err != nil && !errors.Is(err, store.ErrUnavailable) {
			return nil, fmt.Errorf("open: store dirs: %w", err)
		}
		candidates = append(candidates, stored...)
	}
	var dirs []string
	for _, candidate := range candidates {
		dir := filepath.Clean(candidate)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return uniqueDirs(dirs), nil
}

// knownDirs returns the directories a bare word may name: the history directories, then the children of cfg.Root,
// then each root's directories, each listed once.
func knownDirs(ctx context.Context, cfg Config, sessions []session) ([]string, error) {
	history, err := historyDirs(ctx, cfg, sessions)
	if err != nil {
		return nil, err
	}
	return uniqueDirs(slices.Concat(history, rootChildren(cfg), rootsDirs(cfg.Roots))), nil
}

// rootChildren returns the directories directly inside cfg.Root when it is absolute, and none otherwise.
func rootChildren(cfg Config) []string {
	if !filepath.IsAbs(cfg.Root) {
		return nil
	}
	return childDirs([]string{cfg.Root})
}

// rootsDirs returns the directories of each root in roots, in the order of roots.
func rootsDirs(roots []Root) []string {
	dirs := make([]string, 0, len(roots))
	for _, root := range roots {
		dirs = append(dirs, rootDirs(root)...)
	}
	return dirs
}

// uniqueDirs returns dirs without repeats, each at its first position.
func uniqueDirs(dirs []string) []string {
	seen := make(map[string]bool, len(dirs))
	var out []string
	for _, dir := range dirs {
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}
