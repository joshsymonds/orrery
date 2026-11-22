package protocol

// OptionCommand holds the pane's foreground command, #{pane_current_command}, as tmux read it when orreryd applied the
// pane's last session registry update. The registry binds a pane only while its Claude lives, and Claude holds its
// pane's foreground, so the value is the name Claude runs under, whatever its install calls it. It is a per-pane option
// like the others, and an unbound update unsets it.
const OptionCommand = "@orrery_command"

// IsShell reports whether command, a pane's #{pane_current_command}, is the base name of a shell a pane's Claude Code
// is started from. A pane whose foreground is one is at its shell's prompt, where typed text runs as a command.
func IsShell(command string) bool {
	switch command {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh", "fish", "tcsh", "csh", "nu", "elvish", "xonsh":
		return true
	}
	return false
}

// ClaudeGone reports whether a pane's Claude Code no longer runs in its foreground. current is the pane's
// #{pane_current_command} now, and recorded its OptionCommand. A recorded command that is not a shell is Claude's own
// name, and Claude is gone once current differs from it. With no such record, as on a pane whose Claude has not written
// one, Claude is gone when current is a shell. A shell is never taken for Claude's name, since a state write that lands
// after Claude has died reads the shell that took the pane back.
func ClaudeGone(current, recorded string) bool {
	if recorded != "" && !IsShell(recorded) {
		return current != recorded
	}
	return IsShell(current)
}
