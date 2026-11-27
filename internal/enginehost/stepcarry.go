package enginehost

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// carryDirMode and carryFileMode are the modes a carried file, and any folder made for it, get on the step's host.
const (
	carryDirMode  = 0o700
	carryFileMode = 0o600
)

// carryFiles copies each of paths, an absolute path of a file it reads through from, the controller's own host, to the
// same path on host, making its folder there when it is missing. A step that runs on host then reads at those paths what
// its prompt names. A path that is not absolute, or a file that cannot be read from from or written on host, is an error
// naming it.
func carryFiles(ctx context.Context, from, host hostexec.Files, paths []string) error {
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("carry %q: not an absolute path", path)
		}
		data, err := from.ReadFile(ctx, filepath.Clean(path))
		if err != nil {
			return fmt.Errorf("carry %s: %w", path, err)
		}
		if err = host.MkdirAll(ctx, filepath.Dir(path), carryDirMode); err != nil {
			return fmt.Errorf("carry %s: %w", path, err)
		}
		if err = host.WriteFile(ctx, path, data, carryFileMode); err != nil {
			return fmt.Errorf("carry %s: %w", path, err)
		}
	}
	return nil
}
