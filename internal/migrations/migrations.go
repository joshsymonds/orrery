// Package migrations reads the goose SQL migrations of a directory a project declares, at a commit, and finds the new
// migrations of one side whose versions origin's main already uses for a different file, with the numbers each new
// migration is renumbered to. It reads the trees with git plumbing, so it needs no worktree and reads the same at every
// run.
package migrations

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// sqlSuffix is the extension of a goose SQL migration.
const sqlSuffix = ".sql"

// File is one goose SQL migration: its path from the repository's root, its version, and the digits its name opens
// with, whose count a renumbered name keeps.
type File struct {
	Path    string
	Version int64
	digits  string
}

// Name returns the file's name without its directory.
func (f File) Name() string {
	return path.Base(f.Path)
}

// Collision is a new migration of one side whose version origin's main uses for a different file: the new migration's
// path, the path of origin's file, and the version both have.
type Collision struct {
	Path, Origin string
	Version      int64
}

// Rename is the move of a new migration to its free number: From is its path, To the path it takes.
type Rename struct {
	From, To string
}

// Plan is what Check found: each collision, and the renames that move every new migration, in version order, to the
// versions after the newest one origin and the other sides use. A plan with no collision renames nothing.
type Plan struct {
	Collisions []Collision
	Renames    []Rename
}

// Parse returns the goose SQL migrations among paths, each a path from the repository's root, in version order and
// then path order. A path is a migration when its name ends in .sql and opens with the digits of a version of 1 or more
// and then an underscore, as goose numbers a migration; any other path is left out.
func Parse(paths []string) []File {
	var files []File
	for _, full := range paths {
		name := path.Base(full)
		if !strings.HasSuffix(name, sqlSuffix) {
			continue
		}
		digits, _, found := strings.Cut(name, "_")
		if !found || digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
			continue
		}
		version, err := strconv.ParseInt(digits, 10, 64)
		if err != nil || version < 1 {
			continue
		}
		files = append(files, File{Path: full, Version: version, digits: digits})
	}
	slices.SortFunc(files, func(a, b File) int {
		return cmp.Or(cmp.Compare(a.Version, b.Version), strings.Compare(a.Path, b.Path))
	})
	return files
}

// List returns the goose SQL migrations directly in dir, a directory from the repository's root, at commit in repo on
// host (Parse). It reads the commit's tree with git ls-tree, so nothing is checked out. A commit that has no such
// directory has none.
func List(ctx context.Context, host hostexec.Host, repo, commit, dir string) ([]File, error) {
	tree := strings.TrimSuffix(dir, "/") + "/"
	out, err := host.Git(ctx, hostexec.GitCall{
		Dir: repo, Args: []string{"--literal-pathspecs", "ls-tree", "-z", "--full-tree", commit, "--", tree},
	})
	if err != nil {
		return nil, fmt.Errorf("list the migrations in %s at %s: %w", dir, commit, err)
	}
	var paths []string
	for _, record := range strings.Split(string(out), "\x00") {
		meta, full, found := strings.Cut(record, "\t")
		if !found {
			continue
		}
		if fields := strings.Fields(meta); len(fields) == 3 && fields[1] == "blob" {
			paths = append(paths, full)
		}
	}
	return Parse(paths), nil
}

// Check returns what own's new migrations collide with on origin. A new migration is one of own whose name neither
// origin nor also holds: also is the other tree the new migrations land in, such as the candidate a task merges into,
// whose own migrations stay where they are. A new migration collides when origin holds a different file of its
// version. When one does, every new migration is renamed, in version order, to the versions after the newest that
// origin, also and the rest of own use, so the new migrations keep their order and all run after origin's. Each
// rename keeps its name past its digits and the count of its digits. A plan with no collision renames nothing.
func Check(own, origin, also []File) Plan {
	held := make(map[string]bool, len(origin)+len(also))
	for _, file := range slices.Concat(origin, also) {
		held[file.Name()] = true
	}
	var plan Plan
	var moving []File
	newest := int64(0)
	for _, file := range own {
		if held[file.Name()] {
			newest = max(newest, file.Version)
			continue
		}
		moving = append(moving, file)
		for _, theirs := range origin {
			if theirs.Version == file.Version {
				plan.Collisions = append(plan.Collisions, Collision{
					Path: file.Path, Origin: theirs.Path, Version: file.Version,
				})
			}
		}
	}
	if len(plan.Collisions) == 0 {
		return Plan{}
	}
	for _, file := range slices.Concat(origin, also) {
		newest = max(newest, file.Version)
	}
	for i, file := range moving {
		_, rest, _ := strings.Cut(file.Name(), "_")
		name := fmt.Sprintf("%0*d_%s", len(file.digits), newest+int64(i)+1, rest)
		plan.Renames = append(plan.Renames, Rename{From: file.Path, To: path.Join(path.Dir(file.Path), name)})
	}
	return plan
}

// Summary returns the plan in one line, for a reason or a log: each collision as `<name> collides with origin's
// <name>`, joined with `, `, then `; renumber <side>'s new migrations to <versions> in order`, where side names whose
// migrations they are, such as "the candidate". A plan with no collision returns "".
func (p Plan) Summary(side string) string {
	if len(p.Collisions) == 0 {
		return ""
	}
	collisions := make([]string, 0, len(p.Collisions))
	for _, collision := range p.Collisions {
		collisions = append(collisions, fmt.Sprintf("%s collides with origin's %s", path.Base(collision.Path),
			path.Base(collision.Origin)))
	}
	versions := make([]string, 0, len(p.Renames))
	for _, rename := range p.Renames {
		digits, _, _ := strings.Cut(path.Base(rename.To), "_")
		versions = append(versions, digits)
	}
	return fmt.Sprintf("%s; renumber %s's new migrations to %s in order", strings.Join(collisions, ", "), side,
		strings.Join(versions, ", "))
}

// Lines returns the plan as the lines of an integrator's prompt: each collision as `- <path> collides with origin's
// <path>: both are version <n>`, then `Renumber <side>'s new migrations, in order:` and each rename as
// `- <path> to <path>`, and last what a renumber carries with it. A plan with no collision returns none.
func (p Plan) Lines(side string) []string {
	if len(p.Collisions) == 0 {
		return nil
	}
	lines := make([]string, 0, len(p.Collisions)+len(p.Renames)+2)
	for _, collision := range p.Collisions {
		lines = append(lines, fmt.Sprintf("- %s collides with origin's %s: both are version %d", collision.Path,
			collision.Origin, collision.Version))
	}
	lines = append(lines, fmt.Sprintf("Renumber %s's new migrations, in order:", side))
	for _, rename := range p.Renames {
		lines = append(lines, fmt.Sprintf("- %s to %s", rename.From, rename.To))
	}
	return append(lines, "Rename each file with git mv, change every reference to its old name or version, make "+
		"again what the repository generates from its migrations, and list each changed path in other_changes.")
}

// Failure returns the line a commit whose own migrations still collide with origin's fails on, as an integrator's return
// check names it, or "" when none collides.
func (p Plan) Failure() string {
	if len(p.Collisions) == 0 {
		return ""
	}
	collisions := make([]string, 0, len(p.Collisions))
	for _, collision := range p.Collisions {
		collisions = append(collisions, fmt.Sprintf("%s collides with origin's %s", collision.Path, collision.Origin))
	}
	return "migrations: " + strings.Join(collisions, ", ") + "; renumber them as the prompt's Migration collisions says"
}
