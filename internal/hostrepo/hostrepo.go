// Package hostrepo makes a repository's checkout available on a host that engine work runs on, and reads the origin of
// a checkout there. It fetches only. Ensure never checks out, resets or changes the branch of a checkout on a host: it
// fetches into the checkout, so a checkout the owner works in keeps its branch, its index and its files.
package hostrepo

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// RootLister lists a host's roots: the absolute directories whose folders the host's agent reports, under which a
// checkout may be found. *node.Client satisfies it.
type RootLister interface {
	Roots(ctx context.Context) ([]string, error)
}

// Request describes the checkout that Ensure makes available. Host is the orrery name of the host the checkout is on.
// Origin is the URL of the repository's origin, which the checkout fetches from. Preferred is the directory of a
// checkout the work should use, when that directory is under one of the roots. Base is the commit the checkout must
// hold after the fetch.
type Request struct {
	Host, Origin, Preferred, Base string
}

// OriginURL returns the URL of the origin remote of the checkout at repository on host, with the surrounding space
// trimmed. It fails when git fails, as it does for a repository with no origin.
func OriginURL(ctx context.Context, host hostexec.Host, repository string) (string, error) {
	out, err := host.Git(ctx, hostexec.GitCall{Dir: repository, Args: []string{"remote", "get-url", "origin"}})
	if err != nil {
		return "", fmt.Errorf("origin of %s: %w", repository, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Ensure makes the checkout that req describes available on host, and returns the path of that checkout. It fetches
// into a checkout under one of roots, and never checks out, resets or changes the branch of a checkout. When no
// checkout has req.Origin, it clones the origin into the first root, under the base name of req.Preferred. A failure
// returns an error that names req.Host and carries git's error.
func Ensure(ctx context.Context, host hostexec.Host, roots RootLister, req Request) (string, error) {
	checkout, err := ensure(ctx, host, roots, req)
	if err != nil {
		return "", fmt.Errorf("hostrepo: %s: %w", req.Host, err)
	}
	return checkout, nil
}

// ensure does the work of Ensure, and returns its errors unnamed for Ensure to name the host in.
func ensure(ctx context.Context, host hostexec.Host, roots RootLister, req Request) (string, error) {
	rootDirs, err := roots.Roots(ctx)
	if err != nil {
		return "", fmt.Errorf("list the agent's roots: %w", err)
	}
	if len(rootDirs) == 0 {
		return "", fmt.Errorf("no root to hold a checkout of %s", req.Origin)
	}
	checkout := findCheckout(ctx, host, rootDirs, req)
	if checkout == "" {
		checkout, err = cloneCheckout(ctx, host, rootDirs[0], req)
	} else {
		err = fetchOrigin(ctx, host, checkout)
	}
	if err != nil {
		return "", err
	}
	if req.Base != "" {
		args := []string{"cat-file", "-e", req.Base + "^{commit}"}
		if _, err = host.Git(ctx, hostexec.GitCall{Dir: checkout, Args: args}); err != nil {
			return "", fmt.Errorf("commit %s is missing from %s after the fetch: %w", req.Base, checkout, err)
		}
	}
	return checkout, nil
}

// findCheckout returns the first candidate checkout whose origin matches req.Origin, or "" when none does. A candidate
// that git refuses, because it is not a checkout the agent admits, is skipped.
func findCheckout(ctx context.Context, host hostexec.Host, rootDirs []string, req Request) string {
	want := normalizeOrigin(req.Origin)
	for _, dir := range candidates(ctx, host, rootDirs, req.Preferred) {
		origin, err := OriginURL(ctx, host, dir)
		if err != nil {
			continue
		}
		if normalizeOrigin(origin) == want {
			return dir
		}
	}
	return ""
}

// candidates returns the directories that a checkout may be at, in the order Ensure tries them: preferred first, when
// it is set, then the folders at levels 1 and 2 under each root, sorted by path. A directory that host cannot list
// contributes no folders.
func candidates(ctx context.Context, host hostexec.Host, rootDirs []string, preferred string) []string {
	var folders []string
	for _, root := range rootDirs {
		for _, dir := range subdirs(ctx, host, root) {
			folders = append(folders, dir)
			folders = append(folders, subdirs(ctx, host, dir)...)
		}
	}
	slices.Sort(folders)
	folders = slices.Compact(folders)
	if preferred != "" {
		folders = append([]string{preferred}, folders...)
	}
	return folders
}

// subdirs returns the directories directly inside dir, as paths under dir. A name that begins with "." is left out,
// as is a symlink, since a symlink's type is not a directory's. A dir that host cannot list has none.
func subdirs(ctx context.Context, host hostexec.Host, dir string) []string {
	entries, err := host.ReadDir(ctx, dir)
	if err != nil {
		return nil
	}
	var children []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !entry.Type().IsDir() {
			continue
		}
		children = append(children, filepath.Join(dir, entry.Name()))
	}
	return children
}

// normalizeOrigin returns url with a trailing "/" and then a trailing ".git" trimmed, so that the two spellings of one
// origin compare equal.
func normalizeOrigin(url string) string {
	return strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
}

// fetchOrigin fetches origin into the checkout at checkout. It moves no ref of the checkout's branches, index or
// working tree.
func fetchOrigin(ctx context.Context, host hostexec.Host, checkout string) error {
	call := hostexec.GitCall{Dir: checkout, Args: []string{"fetch", "origin"}, Remote: true}
	if _, err := host.Git(ctx, call); err != nil {
		return fmt.Errorf("fetch origin in %s: %w", checkout, err)
	}
	return nil
}

// cloneCheckout clones req.Origin into root under the base name of req.Preferred, and returns the path of the clone.
// Preferred names the clone, so its base name must be a directory name of its own: a name that is "." or ".." would
// clone outside root.
func cloneCheckout(ctx context.Context, host hostexec.Host, root string, req Request) (string, error) {
	name := filepath.Base(req.Preferred)
	if req.Preferred == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		return "", fmt.Errorf("clone %s: preferred %q names no directory to clone into", req.Origin, req.Preferred)
	}
	args := []string{"clone", "--", req.Origin, name}
	if _, err := host.Git(ctx, hostexec.GitCall{Dir: root, Args: args, Remote: true}); err != nil {
		return "", fmt.Errorf("clone %s into %s: %w", req.Origin, root, err)
	}
	return filepath.Join(root, name), nil
}
