package guard

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
)

// Spec names what a task's commit is judged against.
type Spec struct {
	// Guarded are the tests files of the epic's Requirements' items and of every task not failed.
	Guarded []string
	// CaptureArgs are the raw argv elements of the captures of the epic's Requirements' items and of every task not
	// failed. Check keeps those that name a file at the base.
	CaptureArgs []string
	// Own are the task's own item's tests files.
	Own []string
	// Owned are the task's test_changes.
	Owned []string
}

// Check returns the refusals of the changes from base to commit under spec, in diff order. It reads the changes with
// git, so an unknown base or commit is an error.
func Check(ctx context.Context, repo, base, commit string, spec Spec) ([]Refusal, error) {
	captures, err := blobPaths(ctx, repo, base, repoPaths(spec.CaptureArgs))
	if err != nil {
		return nil, fmt.Errorf("list captures at base: %w", err)
	}
	set := Set{
		Guarded:  spec.Guarded,
		Captures: slices.Collect(maps.Keys(captures)),
		Own:      spec.Own,
		Owned:    spec.Owned,
	}
	out, err := runGit(ctx, repo, nil, "diff", "--name-only", "-z", "--no-renames", "--end-of-options", base, commit)
	if err != nil {
		return nil, fmt.Errorf("list changed paths: %w", err)
	}
	watched := watchedPaths(set, splitNUL(out))
	atBase, err := readSide(ctx, repo, base, watched)
	if err != nil {
		return nil, fmt.Errorf("read changed paths at base: %w", err)
	}
	atHead, err := readSide(ctx, repo, commit, watched)
	if err != nil {
		return nil, fmt.Errorf("read changed paths at commit: %w", err)
	}
	changes := make([]Change, 0, len(watched))
	for _, changed := range watched {
		baseSrc, present := atBase[changed]
		headSrc, presentHead := atHead[changed]
		changes = append(changes, Change{
			Path:   changed,
			Base:   baseSrc,
			AtBase: present,
			Head:   headSrc,
			AtHead: presentHead,
		})
	}
	return Judge(set, changes), nil
}

// repoPaths returns the repository paths the argv elements name, cleaned. Elements that name no repository path, being
// empty, absolute, or outside the repository, are dropped.
func repoPaths(elements []string) []string {
	paths := make([]string, 0, len(elements))
	for _, element := range elements {
		if path.IsAbs(element) {
			continue
		}
		clean := path.Clean(element)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			continue
		}
		paths = append(paths, clean)
	}
	return paths
}

// blobPaths returns the paths among paths that are blobs at rev. The paths are matched literally. ls-tree recurses into
// a directory that leads any pathspec, so it can list entries under a path that was not asked for; only an entry named
// exactly as asked is returned.
func blobPaths(ctx context.Context, repo, rev string, paths []string) (map[string]bool, error) {
	blobs := make(map[string]bool, len(paths))
	if len(paths) == 0 {
		return blobs, nil
	}
	asked := members(paths)
	args := append([]string{"ls-tree", "-z", "--full-tree", rev, "--"}, paths...)
	out, err := runGit(ctx, repo, []string{"GIT_LITERAL_PATHSPECS=1"}, args...)
	if err != nil {
		return nil, err
	}
	for _, record := range splitNUL(out) {
		meta, name, found := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if found && len(fields) == 3 && fields[1] == "blob" && asked[name] {
			blobs[name] = true
		}
	}
	return blobs, nil
}

// readSide returns the content at rev of each of paths that is a blob there, keyed by path. A path absent at rev is
// not in the map.
func readSide(ctx context.Context, repo, rev string, paths []string) (map[string][]byte, error) {
	blobs, err := blobPaths(ctx, repo, rev, paths)
	if err != nil {
		return nil, err
	}
	content := make(map[string][]byte, len(blobs))
	for name := range blobs {
		var src []byte
		if src, err = runGit(ctx, repo, nil, "cat-file", "blob", rev+":"+name); err != nil {
			return nil, err
		}
		content[name] = src
	}
	return content, nil
}

// watchedPaths returns the changed paths a guard names, in change order.
func watchedPaths(set Set, changed []string) []string {
	guarded := members(set.Guarded)
	captures := members(set.Captures)
	watched := make([]string, 0, len(changed))
	for _, name := range changed {
		if guarded[name] || captures[name] {
			watched = append(watched, name)
		}
	}
	return watched
}

// splitNUL returns the NUL-terminated records of out, without the empty ones.
func splitNUL(out []byte) []string {
	return strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 })
}

// runGit runs git in repo with args and no shell. env is added to its environment. It returns git's stdout, and on
// failure an error that carries git's stderr.
func runGit(ctx context.Context, repo string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-C", repo)
	cmd.Args = append(cmd.Args, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
