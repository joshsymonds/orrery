package protocol

import (
	"encoding/json"
	"strings"
)

// patchResult is the part of a toolUseResult object that carries a file change.
// Content stays raw because it is only read for a create result.
type patchResult struct {
	Type            string          `json:"type"`
	StructuredPatch []patchHunk     `json:"structuredPatch"`
	Content         json.RawMessage `json:"content"`
}

// patchHunk is one hunk of a structuredPatch; Lines carries "+", "-" and " " prefixed lines.
type patchHunk struct {
	Lines []string `json:"lines"`
}

// PatchLineCounts returns how many lines a tool result added and removed, read
// from Claude Code's toolUseResult object. A non-empty structuredPatch counts
// its "+" and "-" lines across all hunks. An empty or absent structuredPatch on
// a create result counts the lines of its content, all as added. Anything else,
// including a string error, a Bash or Read result, null and malformed JSON, gives 0, 0.
func PatchLineCounts(toolUseResult json.RawMessage) (added, removed int64) {
	var result patchResult
	if err := json.Unmarshal(toolUseResult, &result); err != nil {
		return 0, 0
	}
	if len(result.StructuredPatch) > 0 {
		for _, hunk := range result.StructuredPatch {
			for _, line := range hunk.Lines {
				switch {
				case strings.HasPrefix(line, "+"):
					added++
				case strings.HasPrefix(line, "-"):
					removed++
				}
			}
		}
		return added, removed
	}
	if result.Type != "create" {
		return 0, 0
	}
	var content string
	if err := json.Unmarshal(result.Content, &content); err != nil {
		return 0, 0
	}
	return countLines(content), 0
}

// countLines returns the number of newline-separated lines in text. A final line
// without a trailing newline counts, and empty text has no lines.
func countLines(text string) int64 {
	if text == "" {
		return 0
	}
	lines := int64(strings.Count(text, "\n"))
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return lines
}
