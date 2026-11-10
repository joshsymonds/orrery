package capture

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// OutsideRepository returns nil when neither dir nor any of its parents holds an entry named .git. A
// .git file counts as well as a directory, since a linked worktree uses a file. Otherwise it returns
// an error naming the .git entry that was found. The walk starts from the symlink-resolved path, the
// one git itself searches from, so a directory reached through a symlink into a repository is refused.
// A stat failure other than not-exist is returned as an error, so an unreadable parent is never taken
// for absence.
func OutsideRepository(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("capture: resolve %s: %w", dir, err)
	}
	physical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("capture: resolve %s: %w", dir, err)
	}
	for current := physical; ; {
		gitPath := filepath.Join(current, ".git")
		_, err = os.Lstat(gitPath)
		switch {
		case err == nil:
			return fmt.Errorf("capture: %s is inside a git repository: %s exists", dir, gitPath)
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("capture: stat %s: %w", gitPath, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}
