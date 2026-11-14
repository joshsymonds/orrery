// Package build runs each planned task of an epic: red at its base, one implementer, green at its commit and the
// epic's review gates (O3d3).
package build

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runGit runs git in dir and returns its trimmed stdout. On failure the error carries git's stderr. Like stepGit, it
// builds the argument vector itself: git runs with argv and no shell.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-C", dir)
	cmd.Args = append(cmd.Args, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// Slug returns the part of an epic id after its "/".
func Slug(epic string) string {
	_, slug, _ := strings.Cut(epic, "/")
	return slug
}

// BranchRef returns the full name of the branch an epic's work is committed to.
func BranchRef(epic string) string {
	return "refs/heads/epic/" + Slug(epic)
}
