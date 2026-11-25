package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// paneWakeCutLine is the line that paneWakeCut puts where the rest of a text was cut.
const paneWakeCutLine = "[... rest of the text cut to fit the pane ...]"

// paneWakeBreaks is the number of line breaks around paneWakeCutLine: the one before it and the one before the last
// line.
const paneWakeBreaks = 2

// WakePane types text into pane on the tmux server that server names, then presses Enter, when the pane is idle (see
// SendIdle) and its screen shows the input box. Otherwise it sends no key and returns false. server is the store's server
// column: "-L <name>", "-S <path>", or "" for the default server. A malformed column is an error that names pane,
// and nothing is sent. Its signature is enginehost's WakePane, so orreryd passes it straight in.
//
// WakePane cleans text first, as paneWakeText does, so text that SendIdle would refuse is typed instead: invalid
// UTF-8 is replaced, line ends become LF, other control characters become spaces, and text too long to type is cut
// in the middle.
func WakePane(ctx context.Context, server, pane, text string) (bool, error) {
	return wakePaneWithClock(ctx, rpcretry.SystemClock{}, server, pane, text)
}

// wakePaneWithClock is WakePane with the clock that times the typed send's pauses.
func wakePaneWithClock(ctx context.Context, clock rpcretry.Clock, server, pane, text string) (bool, error) {
	srv, err := tmuxctl.ServerOfColumn(server)
	if err != nil {
		return false, fmt.Errorf("wake pane %s: %w", pane, err)
	}
	client, err := tmuxctl.New(srv)
	if err != nil {
		return false, fmt.Errorf("wake pane %s: %w", pane, err)
	}
	return sendIdleWithClock(ctx, clock, client, pane, paneWakeText(pane, text))
}

// paneWakeText returns text that sendTypeable accepts for pane. Invalid UTF-8 is replaced, CRLF and lone CR become
// LF, and every other control character, a tab among them, becomes a space. Text that is then typeable is returned
// as it is. Text that still needs more than sendTypeLimit typed invocations is cut by paneWakeCut.
func paneWakeText(pane, text string) string {
	text = strings.Map(paneWakeSpace, normalizeLineEnds(strings.ToValidUTF8(text, "\uFFFD")))
	if sendTypeable(pane, text) {
		return text
	}
	return paneWakeCut(text)
}

// paneWakeSpace maps each control character other than the line feed to a space.
func paneWakeSpace(r rune) rune {
	if r != '\n' && unicode.IsControl(r) {
		return ' '
	}
	return r
}

// paneWakeCut cuts clean text to at most sendTypeLimit typed invocations. It keeps as many leading lines as fit,
// cutting the last of them by pieces when it does not fit whole, then holds paneWakeCutLine, then the text's last
// line, cut to fit beside the notice when it alone is too long.
func paneWakeCut(text string) string {
	lines := strings.Split(text, "\n")
	tail := lines[len(lines)-1]
	noticeSteps := len(sendPieces(paneWakeCutLine))
	// The leading lines may use what is left after the notice's invocations, the line breaks around the notice, and
	// the last line's own invocations.
	headBudget := sendTypeLimit - noticeSteps - paneWakeBreaks - len(sendPieces(tail))
	if headBudget < 0 {
		tail = paneWakeLinePrefix(tail, sendTypeLimit-noticeSteps-1)
		return paneWakeCutLine + "\n" + tail
	}
	head := paneWakeHead(lines[:len(lines)-1], headBudget)
	return strings.Join(append(head, paneWakeCutLine, tail), "\n")
}

// paneWakeHead returns the leading lines that fit in budget typed invocations. A line after the first costs a line
// break as well as its pieces. The first line that does not fit whole is cut by pieces, and it is the last line that
// paneWakeHead returns.
func paneWakeHead(lines []string, budget int) []string {
	var head []string
	for i, line := range lines {
		lineBreak := 0
		if i > 0 {
			lineBreak = 1
		}
		if cost := len(sendPieces(line)) + lineBreak; cost <= budget {
			head = append(head, line)
			budget -= cost
			continue
		}
		budget -= lineBreak
		if budget < 0 {
			break
		}
		if cut := paneWakeLinePrefix(line, budget); cut != "" {
			head = append(head, cut)
		}
		break
	}
	return head
}

// paneWakeLinePrefix returns the longest prefix of line, cut at a character boundary, that sendPieces splits into at
// most n pieces. n must not be negative. line must be valid UTF-8. The piece count of a prefix never shrinks as the
// prefix grows, so a search over its length finds the end of the n-th piece.
func paneWakeLinePrefix(line string, n int) string {
	runes := []rune(line)
	// over is the shortest prefix that needs more than n pieces. The empty prefix needs none, so over is at least 1.
	over := sort.Search(len(runes)+1, func(i int) bool {
		return len(sendPieces(string(runes[:i]))) > n
	})
	return string(runes[:over-1])
}
