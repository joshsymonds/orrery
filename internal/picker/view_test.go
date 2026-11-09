package picker

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/palette"
)

// barWidth is the window width the cursor-bar tests send.
const barWidth = 40

// barStart returns the Surface0 background SGR that opens the cursor bar.
func barStart() string {
	return strings.TrimSuffix(palette.Bg(palette.Surface0, "x"), "x\x1b[49m")
}

// barActions returns the synthetic rows the cursor-bar tests draw: a jump and a pane.
func barActions() []open.Action {
	return []open.Action{
		{Kind: open.ActionJump, Pane: "%example"},
		{Kind: open.ActionPane, Dir: "/example/dir"},
	}
}

// viewRows returns the rows m draws, one per line of its view.
func viewRows(m model) []string {
	return strings.Split(m.View().Content, "\n")
}

// sized returns the model after a window of width barWidth.
func sized(t *testing.T, m model) model {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: barWidth, Height: 10})
	done, ok := next.(model)
	if !ok {
		t.Fatalf("update returned %T, want model", next)
	}
	return done
}

func TestSelectedRowIsFullWidthBar(t *testing.T) {
	actions := barActions()
	m := sized(t, newModel(actions, nil))
	selected := viewRows(m)[0]

	if !strings.HasPrefix(selected, barStart()) {
		t.Fatalf("selected row does not start with the Surface0 background: %q", selected)
	}
	if got := ansi.StringWidth(selected); got != barWidth {
		t.Errorf("selected row width = %d, want %d: %q", got, barWidth, selected)
	}
	want := "> " + describe(actions[0])
	if got := strings.TrimRight(ansi.Strip(selected), " "); got != want {
		t.Errorf("selected row text = %q, want %q", got, want)
	}
}

func TestUnselectedRowIsPlain(t *testing.T) {
	actions := barActions()
	m := sized(t, newModel(actions, nil))
	want := "  " + describe(actions[1])
	if got := viewRows(m)[1]; got != want {
		t.Errorf("unselected row = %q, want plain %q", got, want)
	}
}

func TestCursorMoveMovesBar(t *testing.T) {
	actions := barActions()
	m := sized(t, newModel(actions, nil))
	next, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	moved, ok := next.(model)
	if !ok {
		t.Fatalf("update returned %T, want model", next)
	}
	rows := viewRows(moved)

	if rows[0] != "  "+describe(actions[0]) {
		t.Errorf("row 0 after moving down = %q, want plain", rows[0])
	}
	if !strings.HasPrefix(rows[1], barStart()) {
		t.Errorf("row 1 after moving down does not start with the bar: %q", rows[1])
	}
	if got := ansi.StringWidth(rows[1]); got != barWidth {
		t.Errorf("row 1 width = %d, want %d", got, barWidth)
	}
}

func TestSelectedRowWithoutSizeHasNoPadding(t *testing.T) {
	actions := barActions()
	m := newModel(actions, nil)
	selected := viewRows(m)[0]
	want := "> " + describe(actions[0])

	if !strings.HasPrefix(selected, barStart()) {
		t.Fatalf("selected row does not start with the Surface0 background: %q", selected)
	}
	if got := ansi.Strip(selected); got != want {
		t.Errorf("selected row text = %q, want %q (no padding before a size message)", got, want)
	}
}
