package releasestep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// machine is what a step reaches on the machine other than git: the commands it runs, such as nix, systemctl and an
// update step's run argv, and the files and links it reads outside a repository, such as a process's exe link and the
// commit file of its build.
type machine interface {
	// Run runs argv in dir, in the process's environment, with argv's stdout and stderr written to stdout and stderr.
	// A command that does not exit 0 returns an error that names its exit.
	Run(ctx context.Context, dir string, argv []string, stdout, stderr io.Writer) error
	// ReadFile reads the file at path.
	ReadFile(path string) ([]byte, error)
	// Readlink returns the target of the symbolic link at path.
	Readlink(path string) (string, error)
}

// osMachine is the machine this process runs on: commands run with os/exec, and files and links are read with os.
type osMachine struct{}

// Run runs argv[0] from PATH with the rest of argv, and no shell, in dir.
func (osMachine) Run(ctx context.Context, dir string, argv []string, stdout, stderr io.Writer) error {
	if len(argv) == 0 {
		return errors.New("run: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return nil
}

// ReadFile reads the file at path.
func (osMachine) ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// Readlink returns the target of the symbolic link at path.
func (osMachine) Readlink(path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("readlink %s: %w", path, err)
	}
	return target, nil
}
