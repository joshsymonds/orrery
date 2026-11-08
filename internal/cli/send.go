package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// ErrQuestionRefused is returned by Send when --expect-question does not match the pane's question.
var ErrQuestionRefused = errors.New("question refused")

// Send implements `orrery send --pane ID (--text T | --keys K...) [--expect-question H]`.
// --text pastes T with bracketed paste and then presses Enter as a separate key. --keys sends each
// positional argument as one key. With --expect-question, nothing is sent unless the pane's question
// hashes to H and is visible on the pane. Send prints nothing.
func Send(ctx context.Context, client *tmuxctl.Client, args []string, _ io.Writer) error {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	pane := flags.String("pane", "", "pane ID to send to")
	text := flags.String("text", "", "text to paste, then press Enter")
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
// and appears in the pane's captured screen.
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
	screen, err := client.Run(ctx, "capture-pane", "-p", "-J", "-t", pane)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if !strings.Contains(screen, question) {
		return fmt.Errorf("send: %w: question is not visible on pane", ErrQuestionRefused)
	}
	return nil
}

// sendText pastes text into pane through a buffer that only this call uses, then presses Enter.
func sendText(ctx context.Context, client *tmuxctl.Client, pane, text string) error {
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
	if _, err := client.Run(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// sendKeys sends each of keys to pane as its own key argument.
func sendKeys(ctx context.Context, client *tmuxctl.Client, pane string, keys []string) error {
	argv := append([]string{"send-keys", "-t", pane, "--"}, keys...)
	if _, err := client.Run(ctx, argv...); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}
