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
// dir, and a dir that is not below home have no stamp. A settings or git file that exists but cannot be read, and a
// settings file that does not parse, are errors that name the file.
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
// first of them that sets env.ANTHROPIC_BASE_URL to a non-empty string stamps the folder. A settings path that does not
// exist, or has a component above it that is not a directory, is skipped.
func folderStamp(folder string) (Stamp, error) {
	for _, name := range []string{"settings.local.json", "settings.json"} {
		path := filepath.Join(folder, ".claude", name)
		data, err := os.ReadFile(filepath.Clean(path))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
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

// settingsEnv returns the env of a settings file's data and whether that file stamps: env.ANTHROPIC_BASE_URL is a
// non-empty string. Data that is not a JSON object is an error. An env that is not an object, or a base URL that is
// absent or not a non-empty string, is no stamp. A stamping file whose env holds a value that is not a string is an
// error, since the env is carried whole into the session.
func settingsEnv(data []byte) (map[string]string, bool, error) {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, false, fmt.Errorf("settings: %w", err)
	}
	if !stampsEnv(settings["env"]) {
		return nil, false, nil
	}
	var values map[string]string
	if err := json.Unmarshal(settings["env"], &values); err != nil {
		return nil, false, fmt.Errorf("env: %w", err)
	}
	return values, true, nil
}

// stampsEnv reports whether raw, the env value of a settings file, is an object whose ANTHROPIC_BASE_URL is a non-empty
// string. An absent env is raw nil, which is no stamp.
func stampsEnv(raw json.RawMessage) bool {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return false
	}
	var baseURL string
	if err := json.Unmarshal(env["ANTHROPIC_BASE_URL"], &baseURL); err != nil {
		return false
	}
	return baseURL != ""
}

// linkedWorktreePrimary returns the primary checkout of the linked worktree that dir lies in. It looks for a .git entry
// in dir or a folder above it below root, and the nearest one decides: a .git directory is an ordinary checkout and
// ends the search with no worktree. It returns "" when no linked worktree is named.
func linkedWorktreePrimary(root, dir string) (string, error) {
	below := root + string(filepath.Separator)
	for folder := dir; strings.HasPrefix(folder, below); folder = filepath.Dir(folder) {
		entry := filepath.Join(folder, ".git")
		info, err := os.Lstat(entry)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
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
// common git directory that the gitdir line names. A file that names no gitdir, or a gitdir with no commondir, names no
// worktree and returns "".
func worktreePrimary(entry string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(entry))
	if err != nil {
		return "", fmt.Errorf("stamp: read %s: %w", entry, err)
	}
	line, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	gitdir := strings.TrimSpace(line)
	if !ok || gitdir == "" {
		return "", nil
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(entry), gitdir)
	}
	common, err := fs.ReadFile(os.DirFS(gitdir), "commondir")
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stamp: read %s: %w", filepath.Join(gitdir, "commondir"), err)
	}
	shared := strings.TrimSpace(string(common))
	if shared == "" {
		return "", nil
	}
	if !filepath.IsAbs(shared) {
		shared = filepath.Join(gitdir, shared)
	}
	return filepath.Dir(shared), nil
}
