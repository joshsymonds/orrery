// Package stamp finds the context stamp that a folder inherits from its settings files.
package stamp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Stamp is the context stamp in effect for a folder: the folder that carries it and the env its settings file sets.
// The zero value is no stamp.
type Stamp struct {
	Folder string
	Env    map[string]string
}

// BaseURL returns the stamp's ANTHROPIC_BASE_URL, or "" for no stamp.
func (s Stamp) BaseURL() string {
	return s.Env["ANTHROPIC_BASE_URL"]
}

// Find returns the stamp in effect for dir. dir is resolved through its symlinks, and only the folders strictly below
// home are searched: dir, then each folder above it up to home, and home's own settings never count. A folder is
// stamped when its .claude/settings.local.json or .claude/settings.json sets env.ANTHROPIC_BASE_URL to a non-empty
// string, and the nearest stamping file wins with its whole env. When no folder stamps and dir lies in a linked git
// worktree, the search continues from the worktree's primary checkout. An empty or relative home, an empty or relative
// dir, and a dir that is not below home have no stamp. A settings or git file that exists but cannot be read, including
// a dangling symlink, and a settings file that does not parse or whose env has the wrong shape, are errors that name the
// file. A worktree link that does not lead to its primary checkout is an error that names the broken path; see
// linkedWorktreePrimary.
func Find(home, dir string) (Stamp, error) {
	root, ok := resolveHome(home)
	if !ok || !filepath.IsAbs(dir) {
		return Stamp{}, nil
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Stamp{}, fmt.Errorf("stamp: resolve %s: %w", dir, err)
	}
	if !strings.HasPrefix(resolvedDir, root+string(filepath.Separator)) {
		return Stamp{}, nil
	}
	stamp, err := stampFrom(root, resolvedDir)
	if err != nil || stamp.Folder != "" {
		return stamp, err
	}
	primary, err := linkedWorktreePrimary(root, resolvedDir)
	if err != nil || primary == "" {
		return Stamp{}, err
	}
	resolvedPrimary, err := filepath.EvalSymlinks(primary)
	if err != nil {
		return Stamp{}, fmt.Errorf("stamp: resolve %s: %w", primary, err)
	}
	return stampFrom(root, resolvedPrimary)
}

// Settings returns the settings document, as JSON, that disables hooks when disableAllHooks is set and sets env. Either
// part is omitted when it is false or empty, so no settings yields "{}".
func Settings(disableAllHooks bool, env map[string]string) string {
	doc := struct {
		DisableAllHooks bool              `json:"disableAllHooks,omitempty"`
		Env             map[string]string `json:"env,omitempty"`
	}{disableAllHooks, env}
	data, err := json.Marshal(doc)
	if err != nil {
		// Unreachable: a bool and a map of strings always marshal. An empty result is not a settings document, so a
		// caller fails visibly rather than running with settings it did not mean to set.
		return ""
	}
	return string(data)
}

// resolveHome returns home with its symlinks resolved. It reports false for an empty or relative home, for the root
// directory, and for a home that does not resolve.
func resolveHome(home string) (string, bool) {
	if !filepath.IsAbs(home) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil || resolved == string(filepath.Separator) {
		return "", false
	}
	return resolved, true
}

// stampFrom returns the nearest stamp at folder or above it, below root. The walk stops at root, which never counts,
// and a folder outside root has no stamp.
func stampFrom(root, folder string) (Stamp, error) {
	below := root + string(filepath.Separator)
	for current := folder; strings.HasPrefix(current, below); current = filepath.Dir(current) {
		stamp, err := folderStamp(current)
		if err != nil || stamp.Folder != "" {
			return stamp, err
		}
	}
	return Stamp{}, nil
}

// folderStamp returns folder's stamp: its .claude/settings.local.json is read before its .claude/settings.json, and the
// first of them that sets env.ANTHROPIC_BASE_URL to a non-empty string stamps the folder. A folder with no .claude, or
// with a .claude that is not a directory, has no stamp and no error. A .claude symlink whose target is missing is an
// error naming .claude, so the folder refuses rather than being skipped. A settings path that Lstat does not find, or has
// a component above it that is not a directory, is skipped. One that Lstat finds, including a symlink whose target is
// missing, must be readable.
func folderStamp(folder string) (Stamp, error) {
	if err := danglingClaude(folder); err != nil {
		return Stamp{}, err
	}
	for _, name := range []string{"settings.local.json", "settings.json"} {
		path := filepath.Join(folder, ".claude", name)
		if _, err := os.Lstat(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
				continue
			}
			return Stamp{}, fmt.Errorf("stamp: read %s: %w", path, err)
		}
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return Stamp{}, fmt.Errorf("stamp: read %s: %w", path, err)
		}
		env, ok, err := settingsEnv(data)
		if err != nil {
			return Stamp{}, fmt.Errorf("stamp: parse %s: %w", path, err)
		}
		if ok {
			return Stamp{Folder: folder, Env: env}, nil
		}
	}
	return Stamp{}, nil
}

// danglingClaude returns an error naming folder's .claude when it is a symlink whose target cannot be stat'd, a dangling
// symlink included. A .claude that Lstat does not find, or that is not a directory, is nil, as is any other .claude: the
// settings reads in folderStamp decide those. Any other Lstat failure is an error naming .claude.
func danglingClaude(folder string) error {
	claude := filepath.Join(folder, ".claude")
	info, err := os.Lstat(claude)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil
		}
		return fmt.Errorf("stamp: read %s: %w", claude, err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return nil
	}
	if _, err = os.Stat(claude); err != nil {
		return fmt.Errorf("stamp: read %s: %w", claude, err)
	}
	return nil
}

// settingsEnv returns the env of a settings file's data and whether that file stamps: env.ANTHROPIC_BASE_URL is a
// non-empty string. Data that is not a JSON object is an error, and so is a stamping env with a value that is not a
// string, since the env is carried whole into the session. An absent env, or an env without ANTHROPIC_BASE_URL, is no
// stamp.
func settingsEnv(data []byte) (map[string]string, bool, error) {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, false, fmt.Errorf("settings: %w", err)
	}
	stamps, err := stampsEnv(settings["env"])
	if err != nil || !stamps {
		return nil, false, err
	}
	var values map[string]string
	if err = json.Unmarshal(settings["env"], &values); err != nil {
		return nil, false, fmt.Errorf("env: %w", err)
	}
	return values, true, nil
}

// stampsEnv reports whether raw, the env value of a settings file, stamps: it is an object whose ANTHROPIC_BASE_URL is a
// non-empty string. An absent env, or an env without ANTHROPIC_BASE_URL, is no stamp. An env that is present but not an
// object, and a base URL that is present but not a string, are errors, including JSON null.
func stampsEnv(raw json.RawMessage) (bool, error) {
	if raw == nil {
		return false, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("env: %w", err)
	}
	env, ok := value.(map[string]any)
	if !ok {
		return false, errors.New("env: not an object")
	}
	base, present := env["ANTHROPIC_BASE_URL"]
	if !present {
		return false, nil
	}
	baseURL, ok := base.(string)
	if !ok {
		return false, errors.New("env: ANTHROPIC_BASE_URL is not a string")
	}
	return baseURL != "", nil
}

// linkedWorktreePrimary returns the primary checkout of the linked worktree that dir lies in. It looks for a .git entry
// in dir or a folder above it below root, and the nearest one decides. The entry is followed through symlinks: a
// directory is an ordinary checkout, and any other kind that is not a regular file also ends the search with no
// worktree. A regular file is a worktree's .git file. An entry that cannot be followed, a dangling symlink included, is
// an error that names it. It returns "" when no linked worktree is named.
func linkedWorktreePrimary(root, dir string) (string, error) {
	below := root + string(filepath.Separator)
	for folder := dir; strings.HasPrefix(folder, below); folder = filepath.Dir(folder) {
		entry := filepath.Join(folder, ".git")
		_, err := os.Lstat(entry)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("stamp: read %s: %w", entry, err)
		}
		info, err := os.Stat(entry)
		if err != nil {
			return "", fmt.Errorf("stamp: read %s: %w", entry, err)
		}
		if !info.Mode().IsRegular() {
			return "", nil
		}
		return worktreePrimary(entry)
	}
	return "", nil
}

// worktreePrimary returns the primary checkout of the linked worktree whose .git file is entry: the parent of the
// common git directory that the gitdir line names. The gitdir must be a directory. When it has a commondir file, that
// file must be readable, non-empty and name a directory, and the parent of the directory it names is the primary. When
// it has no commondir, it names no worktree and returns "" if it holds an objects directory, as a submodule's or a
// separate git directory does; without one it is an error naming the gitdir. A file with no gitdir line is an error
// naming the file.
func worktreePrimary(entry string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(entry))
	if err != nil {
		return "", fmt.Errorf("stamp: read %s: %w", entry, err)
	}
	line, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	gitdir := strings.TrimSpace(line)
	if !ok || gitdir == "" {
		return "", fmt.Errorf("stamp: %s names no gitdir", entry)
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(entry), gitdir)
	}
	gitFS, err := dirFS(gitdir)
	if err != nil {
		return "", err
	}
	commonFile := filepath.Join(gitdir, "commondir")
	if _, err = fs.Lstat(gitFS, "commondir"); errors.Is(err, fs.ErrNotExist) {
		objects, statErr := fs.Stat(gitFS, "objects")
		if statErr != nil {
			return "", fmt.Errorf("stamp: gitdir %s has neither commondir nor objects: %w", gitdir, statErr)
		}
		if !objects.IsDir() {
			return "", fmt.Errorf("stamp: gitdir %s has neither commondir nor objects", gitdir)
		}
		return "", nil
	}
	common, err := fs.ReadFile(gitFS, "commondir")
	if err != nil {
		return "", fmt.Errorf("stamp: read %s: %w", commonFile, err)
	}
	shared := strings.TrimSpace(string(common))
	if shared == "" {
		return "", fmt.Errorf("stamp: %s is empty", commonFile)
	}
	if !filepath.IsAbs(shared) {
		shared = filepath.Join(gitdir, shared)
	}
	return parentOfDir(shared)
}

// parentOfDir returns the parent of shared, which must be a directory: a path that does not exist, or that lies below a
// file, is an error that names it.
func parentOfDir(shared string) (string, error) {
	if _, err := dirFS(shared); err != nil {
		return "", err
	}
	return filepath.Dir(shared), nil
}

// dirFS returns the file system rooted at path, which must be a directory once symlinks are followed. A path that does
// not exist, or that lies below a file, is an error that names it.
func dirFS(path string) (fs.FS, error) {
	dir := os.DirFS(path)
	info, err := fs.Stat(dir, ".")
	if err != nil {
		return nil, fmt.Errorf("stamp: stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("stamp: %s is not a directory", path)
	}
	return dir, nil
}
