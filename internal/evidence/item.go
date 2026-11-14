// Package evidence holds the evidence item a Requirement or task names, and how a run of it passes (O3d2).
package evidence

import (
	"fmt"
	"math"
	"path"
	"strings"
	"time"
)

// Item is the evidence a Requirement or task names: the capture a run executes, the limit it runs under in seconds,
// the artifacts it leaves, the files a person supplies in place of a capture, the conclusion the evidence supports,
// and the judge that reads it.
type Item struct {
	Capture    []string `json:"capture"`
	Limit      int64    `json:"limit"`
	Artifacts  []string `json:"artifacts"`
	Supplied   []string `json:"supplied"`
	Conclusion string   `json:"conclusion"`
	Judge      string   `json:"judge"`
}

// maxLimitSeconds is the largest limit in whole seconds whose duration fits in a time.Duration.
const maxLimitSeconds = math.MaxInt64 / int64(time.Second)

// Check returns one line per refusal of item, in field order capture, limit, artifacts, supplied, conclusion, judge,
// and nil when item is well formed. A line is `<field>: <reason>`, or `<field>[<j>]: <reason>` for one element of a
// list.
func Check(item Item) []string {
	var lines []string
	lines = append(lines, captureLines(item)...)
	lines = append(lines, limitLines(item)...)
	lines = append(lines, artifactLines(item)...)
	lines = append(lines, suppliedLines(item)...)
	if strings.TrimSpace(item.Conclusion) == "" {
		lines = append(lines, "conclusion: empty or blank")
	}
	if strings.TrimSpace(item.Judge) == "" {
		lines = append(lines, "judge: empty or blank")
	}
	return lines
}

// captureLines returns the refusals of the capture field: neither a capture nor supplied files, an empty element, and
// a first program without a slash.
func captureLines(item Item) []string {
	var lines []string
	if len(item.Capture) == 0 && len(item.Supplied) == 0 {
		lines = append(lines, "capture: none, name a capture or supplied files")
	}
	for j, arg := range item.Capture {
		switch {
		case arg == "":
			lines = append(lines, fmt.Sprintf("capture[%d]: empty", j))
		case j == 0 && !strings.Contains(arg, "/"):
			lines = append(lines, "capture[0]: not a path with a / in it")
		}
	}
	return lines
}

// limitLines returns the refusal of the limit field: a limit outside 1 to maxLimitSeconds with a capture, or any
// limit without one.
func limitLines(item Item) []string {
	switch {
	case len(item.Capture) == 0 && item.Limit != 0:
		return []string{"limit: set without a capture"}
	case len(item.Capture) > 0 && (item.Limit < 1 || item.Limit > maxLimitSeconds):
		return []string{fmt.Sprintf("limit: %d is outside 1 to %d seconds", item.Limit, maxLimitSeconds)}
	}
	return nil
}

// artifactLines returns the refusals of the artifacts field: artifacts without a capture, and each bad path.
func artifactLines(item Item) []string {
	var lines []string
	if len(item.Capture) == 0 && len(item.Artifacts) > 0 {
		lines = append(lines, "artifacts: set without a capture")
	}
	return append(lines, pathLines("artifacts", item.Artifacts)...)
}

// suppliedLines returns the refusals of the supplied field: supplied files beside a capture, and each bad path.
func suppliedLines(item Item) []string {
	var lines []string
	if len(item.Capture) > 0 && len(item.Supplied) > 0 {
		lines = append(lines, "supplied: set beside a capture")
	}
	return append(lines, pathLines("supplied", item.Supplied)...)
}

// pathLines returns one line per element of files that is not a usable repository file path, naming the element by
// its index in field. An element gets at most one line: the first rule it breaks.
func pathLines(field string, files []string) []string {
	var lines []string
	seen := make(map[string]bool, len(files))
	for j, file := range files {
		var reason string
		switch {
		case !fileFormOK(file):
			reason = "not a normalized repository-relative file path"
		case inGitDir(file):
			reason = "inside a .git directory"
		case seen[file]:
			reason = "named twice within the list"
		}
		seen[file] = true
		if reason != "" {
			lines = append(lines, fmt.Sprintf("%s[%d]: %s", field, j, reason))
		}
	}
	return lines
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

// inGitDir reports whether file is a .git directory or lies inside one, at any depth and in any letter case. A file
// there could run code on later git operations, such as a hook.
func inGitDir(file string) bool {
	for part := range strings.SplitSeq(file, "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}
