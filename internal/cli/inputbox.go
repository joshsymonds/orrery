package cli

import (
	"strings"
	"unicode/utf8"
)

// inputBoxMaxFooter is the most non-blank footer lines the screen may show below the input box,
// not counting the background-agent list.
const inputBoxMaxFooter = 3

// inputBoxVisible reports whether Claude Code's input box is on a captured pane screen.
// The box is the lowest full-width rule, the rule above it, and a prompt mark on the line
// between them, followed only by blank or indented continuation lines. The rule above may be
// a titled rule (see isInputBoxTitledRule). Below the lower rule
// the footer is at most three non-blank lines with no agent row. Background agents may follow
// the footer: a single blank line, then unselected agent rows that end the screen, optionally
// followed by one overflow row (see isOverflowRow). Anything else below the box is not the box.
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
		if isInputBoxRule(lines[i], width) || isInputBoxTitledRule(lines[i], width) {
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

	footer, ok := footerBelowBox(lines[lower+1:])
	return ok && footerWithinLimit(footer)
}

// footerBelowBox returns the footer lines under the input box, leaving out a trailing agent list.
// A final overflow row directly under the last agent row is left out before the agent list is
// judged. It reports false when the lines end in agent rows without a blank line and a footer above them.
func footerBelowBox(below []string) ([]string, bool) {
	end := len(below)
	for end > 0 && strings.TrimSpace(below[end-1]) == "" {
		end--
	}
	body := below[:end]
	if end >= 2 && isOverflowRow(body[end-1]) && isAgentRow(body[end-2]) {
		body = body[:end-1]
		end--
	}
	if end == 0 || !isAgentRow(body[end-1]) {
		return body, true
	}

	start := end - 1
	for start > 0 && isAgentRow(body[start-1]) {
		start--
	}
	if start < 2 || strings.TrimSpace(body[start-1]) != "" || strings.TrimSpace(body[start-2]) == "" {
		return nil, false
	}
	return body[:start-1], true
}

// footerWithinLimit reports whether footer has at most inputBoxMaxFooter non-blank lines and no
// agent, selected, or overflow row.
func footerWithinLimit(footer []string) bool {
	count := 0
	for _, line := range footer {
		if isAgentRow(line) || isSelectedAgentRow(line) || isOverflowRow(line) {
			return false
		}
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count <= inputBoxMaxFooter
}

// isInputBoxRule reports whether line is a non-empty run of U+2500 exactly width runes long.
func isInputBoxRule(line string, width int) bool {
	return line != "" && utf8.RuneCountInString(line) == width && strings.Trim(line, "─") == ""
}

// isInputBoxTitledRule reports whether line is a titled rule exactly width runes long: one or
// more U+2500, a space, text with no U+2500, a space, and exactly one U+2500.
func isInputBoxTitledRule(line string, width int) bool {
	runes := []rune(line)
	n := len(runes)
	if n != width {
		return false
	}
	lead := 0
	for lead < n && runes[lead] == '─' {
		lead++
	}
	if lead == 0 || lead >= n || runes[lead] != ' ' {
		return false
	}
	if lead+1 >= n-2 || runes[n-2] != ' ' || runes[n-1] != '─' {
		return false
	}
	return !strings.ContainsRune(string(runes[lead+1:n-2]), '─')
}

// isAgentRow reports whether line is an unselected background-agent row.
func isAgentRow(line string) bool {
	return hasAgentDot(line, "  ")
}

// isOverflowRow reports whether line is the agent-list overflow row: two spaces, a down arrow,
// a space, one or more ASCII digits, then " more".
func isOverflowRow(line string) bool {
	rest, ok := strings.CutPrefix(line, "  ↓ ")
	if !ok {
		return false
	}
	digits, ok := strings.CutSuffix(rest, " more")
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isSelectedAgentRow reports whether line is a background-agent row marked as selected.
func isSelectedAgentRow(line string) bool {
	return hasAgentDot(line, "❯ ")
}

// hasAgentDot reports whether line begins with lead, a filled or hollow dot, a space, and at least one more character.
func hasAgentDot(line, lead string) bool {
	rest, ok := strings.CutPrefix(line, lead)
	if !ok {
		return false
	}
	for _, dot := range []string{"●", "◯"} {
		if text, found := strings.CutPrefix(rest, dot+" "); found && text != "" {
			return true
		}
	}
	return false
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
