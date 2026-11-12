package pools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// revParseFields is the number of lines git rev-parse prints for the common directory and the work tree's top.
const revParseFields = 2

// Files names the pool files that apply to one directory and the project they belong to. Project is the directory
// that names the project. ProjectFile and MachineFile are the paths of the project's and the machine's pool files;
// a field is empty when its file does not exist.
type Files struct {
	Project     string
	ProjectFile string
	MachineFile string
}

// Discover finds the pool files for dir. Inside a git work tree, the project is the parent of the repository's
// common directory, so every worktree of one repository names the same project, and its file is at the work tree's
// top. Outside git, the project is the directory of the nearest project file at or above dir, else dir itself.
// getenv reads the environment for the machine file's location.
func Discover(ctx context.Context, dir string, getenv func(string) string) (Files, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Files{}, fmt.Errorf("resolve %s: %w", dir, err)
	}
	// git exits nonzero for a missing directory as it does outside a work tree, so the directory is checked here,
	// or a typo would name an ancestor's project.
	if _, err = os.Stat(abs); err != nil {
		return Files{}, fmt.Errorf("stat %s: %w", abs, err)
	}
	project, projectFile, err := locateProject(ctx, abs)
	if err != nil {
		return Files{}, err
	}
	machineFile, err := machinePoolFile(getenv)
	if err != nil {
		return Files{}, err
	}
	return Files{Project: project, ProjectFile: projectFile, MachineFile: machineFile}, nil
}

// Load discovers the pool files for dir, parses the ones that exist, and merges their pools. It returns the
// project's name and the merged pools sorted by name.
func Load(ctx context.Context, dir string, getenv func(string) string) (project string, pools []Pool, err error) {
	files, err := Discover(ctx, dir, getenv)
	if err != nil {
		return "", nil, err
	}
	var sets [][]Pool
	for _, path := range []string{files.ProjectFile, files.MachineFile} {
		if path == "" {
			continue
		}
		set, readErr := readPools(path)
		if readErr != nil {
			return "", nil, readErr
		}
		sets = append(sets, set)
	}
	merged, err := Merge(sets...)
	if err != nil {
		return "", nil, err
	}
	return files.Project, merged, nil
}

// readPools reads and parses one pool file.
func readPools(path string) ([]Pool, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(path, data)
}

// locateProject returns the project directory for abs and the path of its project file, or "" when none exists.
func locateProject(ctx context.Context, abs string) (project, projectFile string, err error) {
	commonDir, top, inTree, err := gitWorkTree(ctx, abs)
	if err != nil {
		return "", "", err
	}
	if inTree {
		projectFile, err = existing(projectFileIn(top))
		if err != nil {
			return "", "", err
		}
		return filepath.Dir(commonDir), projectFile, nil
	}
	root := abs
	for {
		projectFile, err = existing(projectFileIn(root))
		if err != nil {
			return "", "", err
		}
		if projectFile != "" {
			return root, projectFile, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return abs, "", nil
		}
		root = parent
	}
}

// gitWorkTree asks git for the common directory and the work tree's top of dir. inTree is false when git exits
// nonzero because dir is not inside a work tree. A failure to run git, or a canceled context, is an error.
func gitWorkTree(ctx context.Context, dir string) (commonDir, top string, inTree bool, err error) {
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel")
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && ctx.Err() == nil {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("git in %s: %w", dir, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != revParseFields {
		return "", "", false, fmt.Errorf("git in %s: want a common directory and a top, got %q", dir, out)
	}
	return lines[0], lines[1], true, nil
}

// projectFileIn returns the path of the project file under root.
func projectFileIn(root string) string {
	return filepath.Join(root, ".orrery", "project.toml")
}

// machinePoolFile returns the machine's pool file, or "" when it does not exist. The file is
// $XDG_CONFIG_HOME/orrery/pools.toml, else $HOME/.config/orrery/pools.toml.
func machinePoolFile(getenv func(string) string) (string, error) {
	config := getenv("XDG_CONFIG_HOME")
	if config == "" {
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("neither XDG_CONFIG_HOME nor HOME is set")
		}
		config = filepath.Join(home, ".config")
	}
	return existing(filepath.Join(config, "orrery", "pools.toml"))
}

// existing returns path when a file exists there, "" when none does, and an error when the check itself fails.
func existing(path string) (string, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return path, nil
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	default:
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
}
