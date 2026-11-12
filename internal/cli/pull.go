package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// ErrInputBusy is returned by PullQueued when no key is sent: the pane is in a tmux mode, its input box is not on
// screen, or the box holds text that is not Claude Code's dim placeholder.
var ErrInputBusy = errors.New("input busy")

// ErrNothingQueued is returned by PullQueued when Claude Code's queue gave back no message after Up.
var ErrNothingQueued = errors.New("nothing queued")

const (
	pullUpKey   = "Up"
	pullDownKey = "Down"
	pullBackKey = "BSpace"
	// pullBound bounds the wait for the first popAll record after Up.
	pullBound = 2 * time.Second
	// pullBatchSettle waits after the first popAll record for the rest of its batch.
	pullBatchSettle = 300 * time.Millisecond
	// pullPoll is how often the transcript is read while waiting for a popAll record.
	pullPoll = 20 * time.Millisecond
	// pullKeysBound bounds the key sends and reads that run after Up, which run even when the caller's context is done.
	pullKeysBound = 10 * time.Second
	// pullPaneFields is the number of fields in the display-message format pullBoxIdle reads.
	pullPaneFields = 2
	// pullPromptMark is the prompt mark at the start of the input box.
	pullPromptMark = "❯"
)

// pullSGR matches one SGR escape sequence in captured screen text.
var pullSGR = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// pullEscape matches any escape sequence in captured screen text, for the plain screen.
var pullEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// pullHistory matches Claude Code's history indicator on its own line.
var pullHistory = regexp.MustCompile(`(?m)^\s*History \d+/\d+`)

// PullQueued takes back the messages Claude Code's queue holds for pane, as Claude Code's own up-arrow does. It
// returns ErrInputBusy without sending a key unless the pane is idle in its input box and the box holds only dim
// placeholder text. It then sends Up and reads the popAll records that Up appends to transcript, which hold the
// queued messages oldest first. The returned text is those messages joined by line breaks, and the box is emptied
// with one Backspace per rune of that text. When no message is taken, PullQueued returns ErrNothingQueued, and it
// sends one Down first if Claude Code shows its history indicator. Once Up is sent, the call runs to the end even
// when ctx is done. If emptying the box fails, the taken text is returned with the error, so the caller can keep it.
func PullQueued(ctx context.Context, client *tmuxctl.Client, pane, transcript string) (string, error) {
	idle, err := pullBoxIdle(ctx, client, pane)
	if err != nil {
		return "", err
	}
	if !idle {
		return "", ErrInputBusy
	}
	start, err := pullSize(transcript)
	if err != nil {
		return "", err
	}

	keysCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pullKeysBound)
	defer cancel()
	if _, err = client.Run(keysCtx, sendKeysCmd, "-t", pane, pullUpKey); err != nil {
		return "", fmt.Errorf("pull: %w", err)
	}

	texts, err := pullPopped(keysCtx, transcript, start)
	if err != nil {
		return "", err
	}
	if len(texts) == 0 {
		return "", pullNothing(keysCtx, client, pane)
	}

	text := strings.Join(texts, "\n")
	if n := utf8.RuneCountInString(text); n > 0 {
		if _, err = client.Run(keysCtx, sendKeysCmd, "-t", pane, "-N", strconv.Itoa(n), pullBackKey); err != nil {
			return text, fmt.Errorf("pull: clear input box: %w", err)
		}
	}
	return text, nil
}

// pullNothing returns ErrNothingQueued after one Down when the pane shows Claude Code's history indicator, which
// means Up loaded a history entry into the box.
func pullNothing(ctx context.Context, client *tmuxctl.Client, pane string) error {
	screen, err := client.Run(ctx, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	if pullHistory.MatchString(screen) {
		if _, err = client.Run(ctx, sendKeysCmd, "-t", pane, pullDownKey); err != nil {
			return fmt.Errorf("pull: restore input box: %w", err)
		}
	}
	return ErrNothingQueued
}

// pullBoxIdle reports whether pane is not in a tmux mode, shows its input box at its width, and holds only dim
// placeholder text in that box.
func pullBoxIdle(ctx context.Context, client *tmuxctl.Client, pane string) (bool, error) {
	out, err := client.Run(ctx, "display-message", "-p", "-t", pane, "#{pane_in_mode}|#{pane_width}")
	if err != nil {
		return false, fmt.Errorf("pull: %w", err)
	}
	fields := strings.Split(strings.TrimSuffix(out, "\n"), "|")
	if len(fields) != pullPaneFields {
		return false, fmt.Errorf("pull: unexpected pane format %q", out)
	}
	width, err := strconv.Atoi(fields[1])
	if err != nil {
		return false, fmt.Errorf("pull: pane width %q: %w", fields[1], err)
	}
	if fields[0] != "0" {
		return false, nil
	}

	screen, err := client.Run(ctx, "capture-pane", "-p", "-e", "-t", pane)
	if err != nil {
		return false, fmt.Errorf("pull: %w", err)
	}
	plain := pullEscape.ReplaceAllString(screen, "")
	if !inputBoxVisible(plain, width) {
		return false, nil
	}
	first, last := pullBoxLines(strings.Split(plain, "\n"), width)
	styled := strings.Split(screen, "\n")
	for i := first; i < last; i++ {
		line := styled[i]
		if i == first {
			line = strings.Replace(line, pullPromptMark, "", 1)
		}
		if pullBrightText(line) {
			return false, nil
		}
	}
	return true, nil
}

// pullBoxLines returns the line indexes of the input box on plain: the prompt line and the lines after it, up to but
// not including the lower rule. It is called only on a screen that inputBoxVisible accepts.
func pullBoxLines(plain []string, width int) (int, int) {
	lower := len(plain) - 1
	for lower >= 0 && !isInputBoxRule(plain[lower], width) {
		lower--
	}
	upper := lower - 1
	for upper >= 0 && !isInputBoxRule(plain[upper], width) && !isInputBoxTitledRule(plain[upper], width) {
		upper--
	}
	return upper + 1, lower
}

// pullBrightText reports whether a styled screen line shows a visible, non-blank character outside dim text.
// Attributes carry from one escape sequence to the next, and a reset clears dim.
func pullBrightText(line string) bool {
	dim, last := false, 0
	for _, m := range pullSGR.FindAllStringSubmatchIndex(line, -1) {
		if !dim && pullVisible(line[last:m[0]]) {
			return true
		}
		dim = pullDimAfter(dim, line[m[2]:m[3]])
		last = m[1]
	}
	return !dim && pullVisible(line[last:])
}

// pullDimAfter returns whether dim text is on after the SGR parameters params, given that it was on or off before.
// Extended colors take their arguments from the same list, so their numbers are not read as attributes.
func pullDimAfter(dim bool, params string) bool {
	codes := strings.Split(params, ";")
	for i := 0; i < len(codes); i++ {
		switch codes[i] {
		case "", "0", "22":
			dim = false
		case "2":
			dim = true
		case "38", "48":
			if i+1 < len(codes) && codes[i+1] == "5" {
				i += 2
			} else if i+1 < len(codes) && codes[i+1] == "2" {
				i += 4
			}
		}
	}
	return dim
}

// pullVisible reports whether text has a character that is not white space.
func pullVisible(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return !unicode.IsSpace(r) }) >= 0
}

// pullSize returns the size of the transcript at path, which the popAll records after Up start at.
func pullSize(path string) (int64, error) {
	info, err := os.Stat(filepath.Clean(path))
	if err != nil {
		return 0, fmt.Errorf("pull: transcript: %w", err)
	}
	return info.Size(), nil
}

// pullPopped waits up to pullBound for the first popAll record after from, then waits pullBatchSettle for the rest
// of its batch. It returns the non-empty contents of the popAll records, oldest first, and nil when none came.
func pullPopped(ctx context.Context, transcript string, from int64) ([]string, error) {
	tail := pullTail{path: filepath.Clean(transcript), offset: from}
	deadline := time.Now().Add(pullBound)
	for {
		if err := tail.read(); err != nil {
			return nil, err
		}
		if len(tail.texts) > 0 {
			break
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
		if err := sendPause(ctx, pullPoll); err != nil {
			return nil, fmt.Errorf("pull: %w", err)
		}
	}
	if err := sendPause(ctx, pullBatchSettle); err != nil {
		return nil, fmt.Errorf("pull: %w", err)
	}
	if err := tail.read(); err != nil {
		return nil, err
	}
	return tail.texts, nil
}

// pullTail reads the popAll records that a transcript gains after offset.
type pullTail struct {
	path   string
	offset int64
	texts  []string
}

// pullRecord holds the fields of a queue-operation record that the pull reads.
type pullRecord struct {
	Operation string `json:"operation"`
	Content   string `json:"content"`
}

// read appends the non-empty contents of the popAll records in the complete lines after the offset, and moves the
// offset past them. A trailing line without its newline is left for a later read.
func (p *pullTail) read() error {
	file, err := os.Open(p.path)
	if err != nil {
		return fmt.Errorf("pull: transcript: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("pull: transcript: %w", err)
	}
	if info.Size() < p.offset {
		return fmt.Errorf("pull: transcript shrank from %d to %d bytes", p.offset, info.Size())
	}
	reader := bufio.NewReader(io.NewSectionReader(file, p.offset, info.Size()-p.offset))
	for {
		line, readErr := reader.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("pull: read transcript: %w", readErr)
		}
		p.offset += int64(len(line))
		if !bytes.Contains(line, []byte(`"popAll"`)) {
			continue
		}
		var record pullRecord
		if json.Unmarshal(line, &record) != nil || record.Operation != "popAll" || record.Content == "" {
			continue
		}
		p.texts = append(p.texts, record.Content)
	}
}
