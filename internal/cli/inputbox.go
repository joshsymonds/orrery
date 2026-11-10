package cli

import (
	"strings"
	"unicode/utf8"
)

// inputBoxMaxFooter is the most non-blank lines the screen may show below the input box.
const inputBoxMaxFooter = 3

// inputBoxVisible reports whether Claude Code's input box is on a captured pane screen.
// The box is the lowest full-width rule, the rule above it, and a prompt mark on the line
// between them, followed only by blank or indented continuation lines and at most three footer lines.
func inputBoxVisible(screen string, width int) bool {
	lines := strings.Split(screen, "\n")

	lower := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if isInputBoxRule(lines[i], width) {
			lower = i
			break
		}
	}
	if lower < 0 {
		return false
	}

	upper := -1
	for i := lower - 1; i >= 0; i-- {
		if isInputBoxRule(lines[i], width) {
			upper = i
			break
		}
	}
	if upper < 0 || !startsInputPrompt(lines[upper+1]) {
		return false
	}

	for _, line := range lines[upper+2 : lower] {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "  ") {
			return false
		}
	}

	footer := 0
	for _, line := range lines[lower+1:] {
		if strings.TrimSpace(line) != "" {
			footer++
		}
	}
	return footer <= inputBoxMaxFooter
}

// isInputBoxRule reports whether line is a non-empty run of U+2500 exactly width runes long.
func isInputBoxRule(line string, width int) bool {
	return line != "" && utf8.RuneCountInString(line) == width && strings.Trim(line, "─") == ""
}

// startsInputPrompt reports whether line begins with the U+276F prompt mark followed by a space,
// a no-break space, or nothing.
func startsInputPrompt(line string) bool {
	rest, ok := strings.CutPrefix(line, "❯")
	if !ok {
		return false
	}
	return rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, " ")
}
