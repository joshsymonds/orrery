package keymap_test

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/keymap"
)

// treeOrder returns the expected display order of the tree surface, one key per entry, with the action each key runs.
func treeOrder() []struct {
	key    string
	action keymap.Action
} {
	return []struct {
		key    string
		action keymap.Action
	}{
		{"j", keymap.ActDown},
		{"down", keymap.ActDown},
		{"k", keymap.ActUp},
		{"up", keymap.ActUp},
		{"n", keymap.ActNextUnseen},
		{"/", keymap.ActFilter},
		{"enter", keymap.ActSwitch},
		{"x", keymap.ActMarkSeen},
		{"c", keymap.ActNewClaude},
		{"s", keymap.ActShell},
		{"N", keymap.ActPicker},
		{"p", keymap.ActPools},
		{"X", keymap.ActClosePane},
		{"D", keymap.ActCloseProject},
		{"?", keymap.ActHelp},
		{"esc", keymap.ActQuit},
		{"q", keymap.ActQuit},
	}
}

func TestEveryEntryHasKeysActionAndHelp(t *testing.T) {
	for _, b := range keymap.Bindings() {
		if len(b.Keys) == 0 {
			t.Errorf("%s entry %q has no keys", b.Surface, b.Action)
		}
		for _, key := range b.Keys {
			if key == "" {
				t.Errorf("%s entry %q has an empty key", b.Surface, b.Action)
			}
		}
		if b.Action == "" {
			t.Errorf("%s entry %v has no action", b.Surface, b.Keys)
		}
		if b.Help == "" {
			t.Errorf("%s entry %v has no help text", b.Surface, b.Keys)
		}
	}
}

func TestKeysUniquePerSurface(t *testing.T) {
	for _, s := range []keymap.Surface{keymap.Tree, keymap.Tmux} {
		seen := map[string]bool{}
		for _, b := range keymap.For(s) {
			for _, key := range b.Keys {
				if seen[key] {
					t.Errorf("surface %s binds key %q twice", s, key)
				}
				seen[key] = true
			}
		}
	}
}

func TestTreeSurfaceHoldsEveryKeyInDisplayOrder(t *testing.T) {
	var got []string
	for _, b := range keymap.For(keymap.Tree) {
		got = append(got, b.Keys...)
	}
	entries := treeOrder()
	want := make([]string, 0, len(entries))
	for _, entry := range entries {
		want = append(want, entry.key)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tree keys = %v, want %v", got, want)
	}
}

func TestTreeKeysLookUpToTheirActions(t *testing.T) {
	for _, entry := range treeOrder() {
		action, ok := keymap.Lookup(keymap.Tree, entry.key)
		if !ok {
			t.Errorf("Lookup(tree, %q) not bound, want %s", entry.key, entry.action)
			continue
		}
		if action != entry.action {
			t.Errorf("Lookup(tree, %q) = %s, want %s", entry.key, action, entry.action)
		}
	}
}

func TestTmuxSurfaceHoldsExactlySpaceAndS(t *testing.T) {
	var keys []string
	for _, b := range keymap.For(keymap.Tmux) {
		keys = append(keys, b.Keys...)
	}
	if len(keys) != 2 || keys[0] != "Space" || keys[1] != "s" {
		t.Fatalf("tmux keys = %v, want [Space s]", keys)
	}
	if action, ok := keymap.Lookup(keymap.Tmux, "Space"); !ok || action != keymap.ActTree {
		t.Errorf("Lookup(tmux, Space) = %s, %v, want tree, true", action, ok)
	}
	if action, ok := keymap.Lookup(keymap.Tmux, "s"); !ok || action != keymap.ActShell {
		t.Errorf("Lookup(tmux, s) = %s, %v, want shell, true", action, ok)
	}
}

func TestLookupUnboundKeyReportsFalse(t *testing.T) {
	cases := []struct {
		surface keymap.Surface
		key     string
	}{
		{keymap.Tree, "z"},
		{keymap.Tree, "Space"},
		{keymap.Tmux, "j"},
		{keymap.Tmux, "enter"},
	}
	for _, c := range cases {
		action, ok := keymap.Lookup(c.surface, c.key)
		if ok || action != "" {
			t.Errorf("Lookup(%s, %q) = %q, %v, want \"\", false", c.surface, c.key, action, ok)
		}
	}
}

func TestBindingsReturnsACopy(t *testing.T) {
	first := keymap.Bindings()
	first[0].Keys[0] = "mutated"
	first[0].Action = ""
	first[0].Help = ""
	first[len(first)-1] = keymap.Binding{}

	second := keymap.Bindings()
	if second[0].Keys[0] != "j" || second[0].Action != keymap.ActDown || second[0].Help == "" {
		t.Errorf("mutating Bindings() changed the table: first entry = %+v", second[0])
	}
	if second[len(second)-1].Action == "" {
		t.Errorf("mutating Bindings() changed the table: last entry is empty")
	}
}

func TestForReturnsACopy(t *testing.T) {
	first := keymap.For(keymap.Tree)
	first[0].Keys[0] = "mutated"
	first[0].Action = ""

	second := keymap.For(keymap.Tree)
	if second[0].Keys[0] != "j" || second[0].Action != keymap.ActDown {
		t.Errorf("mutating For() changed the table: first entry = %+v", second[0])
	}
	if action, ok := keymap.Lookup(keymap.Tree, "j"); !ok || action != keymap.ActDown {
		t.Errorf("mutating For() changed Lookup: Lookup(tree, j) = %s, %v", action, ok)
	}
}

func TestBindingsOrderPutsTreeBeforeTmux(t *testing.T) {
	all := keymap.Bindings()
	treeCount := len(keymap.For(keymap.Tree))
	if len(all) != treeCount+len(keymap.For(keymap.Tmux)) {
		t.Fatalf("Bindings() has %d entries, want tree and tmux entries only", len(all))
	}
	for i, b := range all {
		want := keymap.Tree
		if i >= treeCount {
			want = keymap.Tmux
		}
		if b.Surface != want {
			t.Errorf("Bindings()[%d].Surface = %s, want %s", i, b.Surface, want)
		}
	}
}
