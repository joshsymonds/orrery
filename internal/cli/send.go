package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// ErrQuestionRefused is returned by Send when --expect-question does not match the pane's question.
var ErrQuestionRefused = errors.New("question refused")

// questionFragmentRunes bounds a question's display fragment so it fits inside the dialog box of an
// 80-column pane.
const questionFragmentRunes = 40

// Typed text is sent in pieces of at most sendPieceBytes bytes, with sendTypeGap between tmux calls
// and sendSettle before Enter, so that Claude Code reads each piece and submits the whole text.
const (
	sendPieceBytes = 400
	sendTypeGap    = 20 * time.Millisecond
	sendSettle     = 300 * time.Millisecond
	// sendPaneFields is the number of fields in the display-message format sendTypable reads.
	sendPaneFields = 3
	sendKeysCmd    = "send-keys"
	// sendTypeLimit is the most typed invocations, before Enter, that sendTypeable accepts.
	sendTypeLimit = 200
)

// Send implements `orrery send --pane ID (--text T | --keys K...) [--expect-question H]`.
// --text types T into the pane when its input box is on screen and it is idle or working, else pastes
// T with bracketed paste; either way it then presses Enter as a separate key. --keys sends each
// positional argument as one key. With --expect-question, nothing is sent unless the pane's question
// hashes to H and its display fragment is visible on the pane. Send prints nothing.
func Send(ctx context.Context, client *tmuxctl.Client, args []string, _ io.Writer) error {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	pane := flags.String("pane", "", "pane ID to send to")
	text := flags.String("text", "", "text to type or paste, then press Enter")
	keysMode := flags.Bool("keys", false, "send the positional arguments as keys")
	expect := flags.String("expect-question", "", "send only if the pane's question hashes to this")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	given := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })

	keys := flags.Args()
	if err := sendValidate(*pane, given["text"], *keysMode, keys); err != nil {
		return err
	}

	if given["expect-question"] {
		if err := sendCheckQuestion(ctx, client, *pane, *expect); err != nil {
			return err
		}
	}
	if given["text"] {
		return sendText(ctx, client, *pane, *text)
	}
	return sendKeys(ctx, client, *pane, keys)
}

// sendValidate rejects every argument combination Send cannot act on, before any tmux call.
func sendValidate(pane string, hasText, keysMode bool, keys []string) error {
	switch {
	case pane == "":
		return errors.New("send: --pane is required")
	case hasText && keysMode:
		return errors.New("send: --text and --keys are mutually exclusive")
	case !hasText && !keysMode:
		return errors.New("send: one of --text or --keys is required")
	case keysMode && len(keys) == 0:
		return errors.New("send: --keys needs at least one key")
	case !keysMode && len(keys) > 0:
		return fmt.Errorf("send: unexpected argument %q", keys[0])
	}
	return nil
}

// sendCheckQuestion returns ErrQuestionRefused unless the pane's question is set, hashes to want,
// and its display fragment appears in the pane's captured screen.
func sendCheckQuestion(ctx context.Context, client *tmuxctl.Client, pane, want string) error {
	question, ok, err := client.PaneOption(ctx, pane, protocol.OptionQuestion)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if !ok || question == "" {
		return fmt.Errorf("send: %w: pane has no question", ErrQuestionRefused)
	}
	if protocol.QuestionHash(question) != want {
		return fmt.Errorf("send: %w: question hash does not match --expect-question", ErrQuestionRefused)
	}
	fragment := questionFragment(question)
	if fragment == "" {
		return fmt.Errorf("send: %w: question has no text to find on pane", ErrQuestionRefused)
	}
	screen, err := client.Run(ctx, "capture-pane", "-p", "-J", "-t", pane)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if !strings.Contains(collapseSpace(screen), fragment) {
		return fmt.Errorf("send: %w: question is not visible on pane", ErrQuestionRefused)
	}
	return nil
}

// questionFragment returns the text a stored @orrery_question value shows on the pane: the question of
// an AskUserQuestion, else the first of command, file_path, or url in input, else the first string
// field of input, else the tool. A value that is not a JSON object is its own fragment. It returns ""
// when the value cannot be decoded or has no text.
func questionFragment(stored string) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stored), &fields); err != nil || fields == nil {
		return displayFragment(stored)
	}
	if raw, ok := fields["questions"]; ok {
		var questions []struct {
			Question string `json:"question"`
		}
		if err := json.Unmarshal(raw, &questions); err != nil || len(questions) == 0 {
			return ""
		}
		return displayFragment(questions[0].Question)
	}
	if value := inputFragment(fields["input"]); value != "" {
		return displayFragment(value)
	}
	return displayFragment(nonEmptyString(fields["tool"]))
}

// inputFragment returns the first non-empty of command, file_path, or url in input, else its first
// string field in document order. It returns "" when input is not a JSON object or has no string.
func inputFragment(input json.RawMessage) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil || object == nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "url"} {
		if value := nonEmptyString(object[key]); value != "" {
			return value
		}
	}
	return firstStringField(input)
}

// nonEmptyString returns the string in raw, or "" when raw is absent or is not a non-empty string.
func nonEmptyString(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

// firstStringField returns the first non-empty string member of the JSON object in document order.
func firstStringField(object json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(object))
	if _, err := dec.Token(); err != nil {
		return ""
	}
	for dec.More() {
		if _, err := dec.Token(); err != nil {
			return ""
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return ""
		}
		if s := nonEmptyString(value); s != "" {
			return s
		}
	}
	return ""
}

// displayFragment returns the first non-blank line of text, whitespace collapsed and cut to
// questionFragmentRunes runes. It returns "" when text has no non-blank line.
func displayFragment(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if collapsed := collapseSpace(line); collapsed != "" {
			runes := []rune(collapsed)
			if len(runes) > questionFragmentRunes {
				runes = runes[:questionFragmentRunes]
			}
			return strings.TrimSpace(string(runes))
		}
	}
	return ""
}

// collapseSpace replaces each whitespace run in s with one space and trims both ends.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// sendText types text into pane when sendTypable and sendTypeable allow it, else pastes it, then presses
// Enter.
func sendText(ctx context.Context, client *tmuxctl.Client, pane, text string) error {
	typable, err := sendTypable(ctx, client, pane)
	if err != nil {
		return err
	}
	if typable && sendTypeable(pane, text) {
		return sendTyped(ctx, client, pane, text)
	}
	return sendPaste(ctx, client, pane, text)
}

// sendTypeable reports whether typing text delivers it exactly: it is valid UTF-8, holds no control
// character other than the line feed once CRLF and CR become LF, and needs at most sendTypeLimit typed
// invocations before Enter.
func sendTypeable(pane, text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	for _, r := range normalizeLineEnds(text) {
		if r != '\n' && unicode.IsControl(r) {
			return false
		}
	}
	return len(typedSteps(pane, text)) <= sendTypeLimit
}

// sendTypable reports whether pane may take typed text: it is not in a tmux mode, its orrery state is
// working or done, and its captured screen shows Claude Code's input box at the pane's width.
func sendTypable(ctx context.Context, client *tmuxctl.Client, pane string) (bool, error) {
	out, err := client.Run(ctx, "display-message", "-p", "-t", pane,
		"#{pane_in_mode}|#{pane_width}|#{@orrery_state}")
	if err != nil {
		return false, fmt.Errorf("send: %w", err)
	}
	fields := strings.SplitN(strings.TrimSuffix(out, "\n"), "|", sendPaneFields)
	if len(fields) != sendPaneFields {
		return false, fmt.Errorf("send: unexpected pane format %q", out)
	}
	width, err := strconv.Atoi(fields[1])
	if err != nil {
		return false, fmt.Errorf("send: pane width %q: %w", fields[1], err)
	}
	state := protocol.State(fields[2])
	if fields[0] != "0" || (state != protocol.StateWorking && state != protocol.StateDone) {
		return false, nil
	}
	screen, err := client.Run(ctx, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return false, fmt.Errorf("send: %w", err)
	}
	return inputBoxVisible(screen, width), nil
}

// sendTyped types text into pane one key, line break, or piece at a time, pauses, then presses Enter.
func sendTyped(ctx context.Context, client *tmuxctl.Client, pane, text string) error {
	for i, argv := range typedSteps(pane, text) {
		if i > 0 {
			if err := sendPause(ctx, sendTypeGap); err != nil {
				return err
			}
		}
		if _, err := client.Run(ctx, argv...); err != nil {
			return fmt.Errorf("send: %w", err)
		}
	}
	if err := sendPause(ctx, sendSettle); err != nil {
		return err
	}
	if _, err := client.Run(ctx, sendKeysCmd, "-t", pane, "Enter"); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// normalizeLineEnds returns text with CRLF and lone CR replaced by LF.
func normalizeLineEnds(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// typedSteps returns the tmux invocations that type text into pane, in order. CRLF and CR become LF.
// Each line break is the C-j key, and each piece of a line is one literal send-keys argument.
func typedSteps(pane, text string) [][]string {
	text = normalizeLineEnds(text)
	var steps [][]string
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			steps = append(steps, []string{sendKeysCmd, "-t", pane, "C-j"})
		}
		for _, piece := range sendPieces(line) {
			steps = append(steps, []string{sendKeysCmd, "-t", pane, "-l", "--", piece})
		}
	}
	return steps
}

// sendPieces cuts line into pieces of at most sendPieceBytes bytes that end on UTF-8 character
// boundaries. A piece ending in ";" gets a backslash before it, because tmux drops a trailing ";" from
// an argument and turns a trailing "\;" into ";".
func sendPieces(line string) []string {
	var pieces []string
	for start := 0; start < len(line); {
		end := start
		for end < len(line) {
			_, size := utf8.DecodeRuneInString(line[end:])
			if end+size-start > sendPieceBytes {
				break
			}
			end += size
		}
		piece := line[start:end]
		if strings.HasSuffix(piece, ";") {
			piece = piece[:len(piece)-1] + `\;`
		}
		pieces = append(pieces, piece)
		start = end
	}
	return pieces
}

// sendPause waits for d or until ctx is done.
func sendPause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("send: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// sendPaste pastes text into pane through a buffer that only this call uses, then presses Enter.
func sendPaste(ctx context.Context, client *tmuxctl.Client, pane, text string) error {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("send: buffer name: %w", err)
	}
	buffer := fmt.Sprintf("orrery-send-%x", suffix)

	if _, err := client.Run(ctx, "set-buffer", "-b", buffer, "--", text); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if _, err := client.Run(ctx, "paste-buffer", "-p", "-d", "-b", buffer, "-t", pane); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if _, err := client.Run(ctx, sendKeysCmd, "-t", pane, "Enter"); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// sendKeys sends each of keys to pane as its own key argument.
func sendKeys(ctx context.Context, client *tmuxctl.Client, pane string, keys []string) error {
	argv := append([]string{sendKeysCmd, "-t", pane, "--"}, keys...)
	if _, err := client.Run(ctx, argv...); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}
