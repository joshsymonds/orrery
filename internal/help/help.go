// Package help renders the one-screen help card from the keymap table.
package help

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/joshsymonds/orrery/internal/keymap"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// labelWidth is the column where a row's text starts, counted after its two-space indent.
const labelWidth = 14

// Card writes the help card to out. prefix is tmux's prefix key; "" means it is unknown.
func Card(out io.Writer, prefix string) error {
	var card strings.Builder

	card.WriteString("orrery daily use\n\n")

	card.WriteString("p\n")
	writeRow(&card, "p", "attach the most recently active session, then open the tree")
	writeRow(&card, "p .", "the session for the current directory, created if missing")
	writeRow(&card, "p WORD", "a session named WORD, else a zoxide match, else the picker")
	writeRow(&card, "p new NAME", "create project directory NAME and a session for it (--root DIR sets the parent)")
	writeRow(&card, "p WORDS...", "open the picker with WORDS as the intent")
	card.WriteString("\n")

	if prefix == "" {
		card.WriteString("tmux\n")
	} else {
		fmt.Fprintf(&card, "tmux (prefix is %s)\n", prefix)
	}
	for _, binding := range keymap.For(keymap.Tmux) {
		for _, key := range binding.Keys {
			writeRow(&card, "prefix "+key, binding.Help)
		}
	}
	card.WriteString("\n")

	card.WriteString("kitty\n")
	writeRow(&card, "kitty_mod+p", "run p; nix-config's kitty binding, not set by orrery")
	card.WriteString("\n")

	card.WriteString("tree\n")
	for _, binding := range keymap.For(keymap.Tree) {
		writeRow(&card, strings.Join(binding.Keys, ", "), binding.Help)
	}

	if _, err := io.WriteString(out, card.String()); err != nil {
		return fmt.Errorf("write help card: %w", err)
	}
	return nil
}

// writeRow appends one indented row: the label left-justified to labelWidth, then the text.
func writeRow(card *strings.Builder, label, text string) {
	fmt.Fprintf(card, "  %-*s%s\n", labelWidth, label, text)
}

// Prefix returns tmux's global prefix key on c's server, trimmed. It returns "" when the server cannot be read.
func Prefix(ctx context.Context, c *tmuxctl.Client) string {
	out, err := c.Run(ctx, "show-options", "-gv", "prefix")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
