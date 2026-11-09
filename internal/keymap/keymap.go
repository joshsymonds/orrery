// Package keymap holds the one table of key bindings that the tree, its overlays, the help output and the tmux
// configuration read.
package keymap

import "slices"

// Surface names where a binding applies.
type Surface string

const (
	// Tree is the keys the tree reads while it is open.
	Tree Surface = "tree"
	// Tmux is the prefix keys tmux binds to orrery.
	Tmux Surface = "tmux"
)

// Action names what a binding runs. Each constant is one action.
type Action string

// The actions a binding can run.
const (
	ActDown         Action = "down"
	ActUp           Action = "up"
	ActNextUnseen   Action = "next-unseen"
	ActFilter       Action = "filter"
	ActSwitch       Action = "switch"
	ActMarkSeen     Action = "mark-seen"
	ActNewClaude    Action = "new-claude"
	ActShell        Action = "shell"
	ActPicker       Action = "picker"
	ActClosePane    Action = "close-pane"
	ActCloseProject Action = "close-project"
	ActHelp         Action = "help"
	ActQuit         Action = "quit"
	ActTree         Action = "tree"
)

// Binding is one key binding: the keys that trigger it on a surface, the action it runs, and its help text.
type Binding struct {
	Surface Surface
	Keys    []string
	Action  Action
	Help    string
}

// table returns every binding in display order. Tree entries come first, then tmux entries. It builds a new slice on
// each call, so a caller that changes the result changes nothing else.
func table() []Binding {
	return []Binding{
		{Tree, []string{"j", "down"}, ActDown, "move the cursor one row down"},
		{Tree, []string{"k", "up"}, ActUp, "move the cursor one row up"},
		{Tree, []string{"n"}, ActNextUnseen, "move to the next unseen row after the cursor, wrapping around"},
		{Tree, []string{"/"}, ActFilter, "start filter input. Typed text sets the filter. Backspace deletes its last " +
			"character, Enter keeps the filter, and Esc clears it"},
		{
			Tree,
			[]string{"enter"},
			ActSwitch,
			"switch the client to the row's pane, mark the pane seen, and close the tree",
		},
		{Tree, []string{"x"}, ActMarkSeen, "mark the row's pane seen; the tree stays open"},
		{
			Tree,
			[]string{"c"},
			ActNewClaude,
			"add a window running `claude` to the row's session, started in the row's " +
				"directory; the tree stays open",
		},
		{Tree, []string{"s"}, ActShell, "open the shell popup for the row's directory, and close the tree"},
		{Tree, []string{"N"}, ActPicker, "close the tree and run the picker for the row's directory; the picker's " +
			"choice switches the client"},
		{
			Tree,
			[]string{"X"},
			ActClosePane,
			"close the row's pane after a y/N prompt, asking twice for a working Claude " +
				"pane",
		},
		{
			Tree,
			[]string{"D"},
			ActCloseProject,
			"close every session of the row's project and its shell session after a " +
				"prompt listing them",
		},
		{Tree, []string{"?"}, ActHelp, "show every key"},
		{Tree, []string{"esc", "q"}, ActQuit, "close the tree"},
		{Tmux, []string{"Space"}, ActTree, "open the tree in a popup"},
		{Tmux, []string{"s"}, ActShell, "open the shell popup for the current pane's directory"},
	}
}

// Bindings returns a copy of every binding in display order.
func Bindings() []Binding {
	return table()
}

// For returns the bindings on surface s in display order.
func For(s Surface) []Binding {
	var out []Binding
	for _, b := range table() {
		if b.Surface == s {
			out = append(out, b)
		}
	}
	return out
}

// Lookup returns the action bound to key on surface s, and false when no binding on s uses key.
func Lookup(s Surface, key string) (Action, bool) {
	for _, b := range table() {
		if b.Surface == s && slices.Contains(b.Keys, key) {
			return b.Action, true
		}
	}
	return "", false
}
