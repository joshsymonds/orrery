// Package shell owns the per-directory popup shell session: its name, and the act of opening it on
// one tmux client.
package shell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Name returns the shell session name for dir: the shell prefix and the first twelve hex digits of
// the SHA-256 of the cleaned dir.
func Name(dir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return protocol.ShellSessionPrefix + hex.EncodeToString(sum[:])[:12]
}

// Open shows the popup shell for dir on the client at clientTTY. The shell session for dir is created
// detached in dir, with @orrery_dir set to dir, unless it already exists. The popup attaches that
// session, with the server's socket and TMUX unset so the attach is not nested inside the session the
// popup is drawn over. The session starts in dir, so the popup takes no directory, and dir stays out
// of the run-shell command. A popup already shown on clientTTY, such as the tree that calls Open, is
// closed first, because tmux ignores a new popup on a client that shows one.
func Open(ctx context.Context, client *tmuxctl.Client, dir, clientTTY string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("shell: directory %q is not absolute", dir)
	}
	if clientTTY == "" {
		return errors.New("shell: empty client tty")
	}
	clean := filepath.Clean(dir)
	name := Name(clean)
	if err := ensureSession(ctx, client, name, clean); err != nil {
		return err
	}
	socket, err := client.Run(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil {
		return fmt.Errorf("shell: socket path: %w", err)
	}
	closePopup := tmuxCommand("display-popup", "-C", "-c", clientTTY)
	popup := tmuxCommand("display-popup", "-c", clientTTY, "-E",
		"env", "-u", "TMUX", "tmux", "-S", strings.TrimSpace(socket), "attach-session", "-t", "="+name)
	if _, err = client.Run(ctx, "run-shell", "-b", "-C", closePopup+" ; "+popup); err != nil {
		return fmt.Errorf("shell: open popup: %w", err)
	}
	return nil
}

// ensureSession creates the detached session name in dir, with @orrery_dir set to dir, unless it
// already exists. The option is set in the same tmux call as the session starts.
func ensureSession(ctx context.Context, client *tmuxctl.Client, name, dir string) error {
	if _, err := client.Run(ctx, "has-session", "-t", "="+name); err == nil {
		return nil
	}
	_, err := client.Run(ctx, "new-session", "-d", "-s", name, "-c", TmuxDirArg(dir),
		";", "set-option", "-t", "="+name+":", protocol.OptionDir, tmuxLiteral(dir))
	if err != nil {
		return fmt.Errorf("shell: create session %q: %w", name, err)
	}
	return nil
}

// tmuxLiteral returns value as an argument that tmux reads back as value. tmux ends a command at an
// argument ending in ";", so that final ";" is written as "\;".
func tmuxLiteral(value string) string {
	if before, ok := strings.CutSuffix(value, ";"); ok {
		return before + `\;`
	}
	return value
}

// TmuxDirArg returns dir as a -c argument. tmux expands formats in -c, so every "#" is doubled, then
// the argument is escaped as tmuxLiteral escapes it.
func TmuxDirArg(dir string) string {
	return tmuxLiteral(strings.ReplaceAll(dir, "#", "##"))
}

// tmuxCommand returns a tmux command string: name, then each argument as one quoted word, so that a
// space, a quote, or a $ in an argument cannot split or expand inside the command.
func tmuxCommand(name string, args ...string) string {
	words := make([]string, 0, 1+len(args))
	words = append(words, name)
	for _, arg := range args {
		words = append(words, tmuxWord(arg))
	}
	return strings.Join(words, " ")
}

// tmuxWord quotes value as one word of a tmux command string. Single quotes are literal in tmux, so a
// single quote inside value is written as '"'"', which joins to one word. Every '#' is doubled, because
// run-shell -C format-expands its command and "##" expands to "#", so the word reaches the command as
// written and no format in it runs.
func tmuxWord(value string) string {
	escaped := strings.ReplaceAll(value, "#", "##")
	return "'" + strings.ReplaceAll(escaped, "'", `'"'"'`) + "'"
}
