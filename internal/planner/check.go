package planner

import (
	"fmt"
	"path"
	"strings"
)

// The reasons a refusal gives after its field. Each says what the field must be.
const (
	reasonFileForm    = "not a normalized repository-relative file path"
	reasonFileTwice   = "named twice in the plan"
	reasonFileOwned   = "owned by an unfinished task"
	reasonGitDir      = "inside a .git directory"
	reasonRequirement = "not a Requirement of the epic"
	reasonEmpty       = "empty"
	reasonNoFiles     = "no files"
)

// Check returns one refusal per bad task field of ret, in plan order, or nil when the plan passes. Each refusal names
// the task by index and title, then the field, then the reason. requirements are the epic's Requirement ids, and owned
// are the files owned by the epic's unfinished tasks.
func Check(ret Return, requirements []string, owned []string) []string {
	known := stringSet(requirements)
	taken := stringSet(owned)
	named := make(map[string]bool)
	refusals := make([]string, 0, len(ret.Tasks))
	for i, task := range ret.Tasks {
		refusals = append(refusals, checkTask(i, task, known, taken, named)...)
	}
	if len(refusals) == 0 {
		return nil
	}
	return refusals
}

// checkTask returns the refusals of one task's fields, in the order title, deliverable, files, requirements, check.
// named records each well-formed file the plan names, so a later naming of the same file is refused.
func checkTask(i int, task Task, known, taken, named map[string]bool) []string {
	prefix := fmt.Sprintf(`tasks[%d] "%s"`, i, task.Title)
	var refusals []string
	if strings.TrimSpace(task.Title) == "" {
		refusals = append(refusals, prefix+" title: "+reasonEmpty)
	}
	if strings.TrimSpace(task.Deliverable) == "" {
		refusals = append(refusals, prefix+" deliverable: "+reasonEmpty)
	}
	if len(task.Files) == 0 {
		refusals = append(refusals, prefix+" files: "+reasonNoFiles)
	}
	for j, file := range task.Files {
		if reason := fileReason(file, taken, named); reason != "" {
			refusals = append(refusals, fmt.Sprintf("%s files[%d]: %s", prefix, j, reason))
		}
	}
	for k, id := range task.Requirements {
		if !known[id] {
			refusals = append(refusals, fmt.Sprintf("%s requirements[%d]: %s", prefix, k, reasonRequirement))
		}
	}
	if strings.TrimSpace(task.Check) == "" {
		refusals = append(refusals, prefix+" check: "+reasonEmpty)
	}
	return refusals
}

// fileReason returns the reason file is refused, or "" when it passes. It records file as named before it checks
// ownership, so the plan's own naming is counted whether or not the file is owned.
func fileReason(file string, taken, named map[string]bool) string {
	if !fileFormOK(file) {
		return reasonFileForm
	}
	if inGitDir(file) {
		return reasonGitDir
	}
	if named[file] {
		return reasonFileTwice
	}
	named[file] = true
	if taken[file] {
		return reasonFileOwned
	}
	return ""
}

// fileFormOK reports whether file is a normalized repository-relative file path: non-empty, no surrounding white
// space, no leading slash or backslash, already clean, and no . or .. component.
func fileFormOK(file string) bool {
	if file == "" || strings.TrimSpace(file) != file || strings.HasPrefix(file, "/") || strings.Contains(file, `\`) {
		return false
	}
	if path.Clean(file) != file {
		return false
	}
	for part := range strings.SplitSeq(file, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// inGitDir reports whether file is a .git directory or lies inside one, at any depth and in any letter case. A task
// that owned a file there, such as a hook, could run code on later git operations.
func inGitDir(file string) bool {
	for part := range strings.SplitSeq(file, "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

// stringSet returns the set of items.
func stringSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}
