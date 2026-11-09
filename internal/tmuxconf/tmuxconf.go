// Package tmuxconf prints the orrery tmux plugin: the bindings that open the tree and a per-project shell, the
// status-line counts, the top pane border, and the hooks that mark panes seen while a client is attached.
package tmuxconf

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// shellGlob matches the names of shell sessions in a #{m:...} format.
const shellGlob = protocol.ShellSessionPrefix + "*"

// Print writes the orrery tmux plugin for the orrery binary at path orrery to w. It writes nothing and returns an
// error when orrery is empty or holds a byte outside [A-Za-z0-9/._+-], because the plugin embeds the path unquoted.
func Print(w io.Writer, orrery string) error {
	if err := checkPath(orrery); err != nil {
		return err
	}
	if _, err := io.WriteString(w, plugin(orrery)); err != nil {
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

// plugin returns the plugin text for the orrery binary at orrery.
func plugin(orrery string) string {
	seen := seenHook(orrery)
	lines := []string{
		`bind-key Space run-shell -b "tmux display-popup -c #{q:client_tty} -E ` + `\"` + orrery +
			` tree --client #{q:client_tty}` + `\""`,
		shellBinding(orrery),
		`set -ga status-right " orrery ` + paneCount(waitingPane()) + ` waiting ` + paneCount(newPane()) + ` new "`,
		`set -g pane-border-status top`,
		`set -g pane-border-format "#{?` + option(protocol.OptionState) + `,` + option(protocol.OptionState) + ` ` +
			option(protocol.OptionLabel) + ` ` + option(protocol.OptionSummary) + `,}"`,
		`set-hook -ga window-pane-changed "` + seen + `"`,
		`set-hook -ga session-window-changed "` + seen + `"`,
		`set-hook -ga client-session-changed "` + seen + `"`,
	}
	return strings.Join(lines, "\n") + "\n"
}

// option returns the format that expands to the value of the option name.
func option(name string) string {
	return "#{" + name + "}"
}

// shellBinding returns the prefix s binding. It runs `orrery shell` in the background with the pane's directory and
// the invoking client's tty, and that command opens the popup shell for the directory.
func shellBinding(orrery string) string {
	return `bind-key s run-shell -b "` + orrery + ` shell --dir #{q:pane_current_path} --client #{q:client_tty}"`
}

// seenHook returns the command a focus hook runs. It marks the hook's pane seen in the background, and only while a
// client is attached to the session.
func seenHook(orrery string) string {
	return `if-shell -F '#{session_attached}' 'run-shell -b \"` + orrery + ` seen --pane #{pane_id}\"'`
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
