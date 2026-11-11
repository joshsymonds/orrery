// Package tmuxconf prints the orrery tmux plugin: the bindings that open the tree and a per-project shell, the
// status-line counts, the top pane border, and the hooks that mark panes seen while a client is attached.
package tmuxconf

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/keymap"
	"github.com/joshsymonds/orrery/internal/palette"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// shellGlob matches the names of shell sessions in a #{m:...} format.
const shellGlob = protocol.ShellSessionPrefix + "*"

// Print writes the orrery tmux plugin for the orrery binary at path orrery to out. It writes nothing and returns an
// error when orrery is empty or holds a byte outside [A-Za-z0-9/._+-], because the plugin embeds the path unquoted.
func Print(out io.Writer, orrery string) error {
	if err := checkPath(orrery); err != nil {
		return err
	}
	text, err := plugin(orrery)
	if err != nil {
		return err
	}
	if _, err = io.WriteString(out, text); err != nil {
		return fmt.Errorf("write tmux plugin: %w", err)
	}
	return nil
}

// checkPath returns an error when orrery is empty or holds a byte outside [A-Za-z0-9/._+-].
func checkPath(orrery string) error {
	if orrery == "" {
		return errors.New("orrery path is empty")
	}
	for i := range len(orrery) {
		if !isPathByte(orrery[i]) {
			return fmt.Errorf("orrery path %q holds byte %q outside [A-Za-z0-9/._+-]", orrery, orrery[i])
		}
	}
	return nil
}

// isPathByte reports whether char may appear unquoted in the plugin.
func isPathByte(char byte) bool {
	return ('A' <= char && char <= 'Z') || ('a' <= char && char <= 'z') || ('0' <= char && char <= '9') ||
		strings.ContainsRune("/._+-", rune(char))
}

// plugin returns the plugin text for the orrery binary at orrery. It returns an error when a tmux binding names an
// action with no command.
func plugin(orrery string) (string, error) {
	binds, err := prefixBindings(orrery)
	if err != nil {
		return "", err
	}
	seen := seenHook(orrery)
	lines := slices.Concat(binds, []string{
		`set -ga status-right " orrery ` + palette.Tmux(palette.Peach, paneCount(waitingPane())) + ` waiting ` +
			palette.Tmux(palette.Mauve, paneCount(newPane())) + ` new "`,
		`set -g pane-border-status top`,
		`set -g pane-border-format "#{?` + option(protocol.OptionState) + `,` + stateStyles() +
			option(protocol.OptionState) + `#[default] ` + palette.Tmux(palette.Text, option(protocol.OptionLabel)) +
			` ` + palette.Tmux(palette.Overlay0, option(protocol.OptionSummary)) + `,}"`,
		`set -g popup-border-lines rounded`,
		`set -g popup-border-style "fg=` + string(palette.Lavender) + `"`,
		`set -g popup-style "bg=` + string(palette.Base) + `"`,
		`set-hook -ga window-pane-changed "` + seen + `"`,
		`set-hook -ga session-window-changed "` + seen + `"`,
		`set-hook -ga client-session-changed "` + seen + `"`,
	})
	return strings.Join(lines, "\n") + "\n", nil
}

// prefixBindings returns one bind-key line for each key of each tmux binding in keymap order. The line runs the
// command for the binding's action.
func prefixBindings(orrery string) ([]string, error) {
	var lines []string
	for _, b := range keymap.For(keymap.Tmux) {
		command, err := prefixCommand(b.Action, orrery)
		if err != nil {
			return nil, err
		}
		for _, key := range b.Keys {
			lines = append(lines, "bind-key "+key+" "+command)
		}
	}
	return lines, nil
}

// prefixCommand returns the command a prefix binding for action runs. It returns an error when action has no command.
func prefixCommand(action keymap.Action, orrery string) (string, error) {
	switch action {
	case keymap.ActTree:
		return treeCommand(orrery), nil
	case keymap.ActShell:
		return shellCommand(orrery), nil
	default:
		return "", fmt.Errorf("tmux binding action %q has no command", action)
	}
}

// option returns the format that expands to the value of the option name.
func option(name string) string {
	return "#{" + name + "}"
}

// stateStyles returns the formats that style the state word of a pane border. Each format expands to the style of one
// state when the pane is in that state, and to nothing otherwise.
func stateStyles() string {
	var b strings.Builder
	for _, state := range []protocol.State{protocol.StateWorking, protocol.StateWaiting, protocol.StateDone} {
		b.WriteString("#{?#{==:" + option(protocol.OptionState) + "," + string(state) + "},#[fg=" +
			string(palette.State(state)) + "],}")
	}
	return b.String()
}

// treeCommand returns the command the tree binding runs. It opens `orrery tree` in a popup on the invoking client.
func treeCommand(orrery string) string {
	return `run-shell -b "tmux display-popup -c #{q:client_tty} -E \"` + orrery + ` tree --client #{q:client_tty}\""`
}

// shellCommand returns the command the shell binding runs. It runs `orrery shell` in the background with the pane's
// directory and the invoking client's tty, and that command opens the popup shell for the directory.
func shellCommand(orrery string) string {
	return `run-shell -b "` + orrery + ` shell --dir #{q:pane_current_path} --client #{q:client_tty}"`
}

// seenHook returns the command a focus hook runs. It marks the hook's pane seen in the background, and only while a
// client is attached to the session and a recently active client shows the pane (seen --focus).
func seenHook(orrery string) string {
	return `if-shell -F '#{session_attached}' 'run-shell -b \"` + orrery + ` seen --pane #{pane_id} --focus\"'`
}

// waitingPane expands to "w" for a waiting pane and to nothing otherwise.
func waitingPane() string {
	return "#{?#{==:" + option(protocol.OptionState) + ",waiting},w,}"
}

// newPane expands to "n" for a pane that is unseen and not waiting, and to nothing otherwise. A pane is unseen
// when it has a state and @orrery_changed is greater than @orrery_seen.
func newPane() string {
	return "#{?#{==:" + option(protocol.OptionState) + ",waiting},,#{?" + option(protocol.OptionState) +
		",#{?#{e|>|:" + option(protocol.OptionChanged) + "," + option(protocol.OptionSeen) + "},n,},}}"
}

// paneCount returns a format whose length is the number of panes, outside shell sessions, for which body expands
// to one character.
func paneCount(body string) string {
	return "#{n:#{S:#{?#{m:" + shellGlob + ",#{session_name}},,#{W:#{P:" + body + "}}}}}"
}
