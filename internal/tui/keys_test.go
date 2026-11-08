package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// keyPanes is synthetic panes in two directories. Display order is %1, %2, %4, %3: alpha's
// waiting pane first, then its unseen done pane, then beta's unseen done pane, then beta's
// working pane. %2 and %4 are unseen.
func keyPanes() []protocol.Pane {
	return []protocol.Pane{
		{ID: "%1", Session: "s1", Cwd: "/home/user/src/alpha", State: protocol.StateWaiting,
			Label: "alpha-review", Summary: "needs approval", Changed: 1699999880, Seen: 1699999880},
		{ID: "%2", Session: "s2", Cwd: "/home/user/src/alpha", State: protocol.StateDone,
			Label: "alpha-tests", Summary: "ran 14 tests", Changed: 1699999970, Seen: 1699999700},
		{ID: "%3", Session: "s3", Cwd: "/home/user/src/beta", State: protocol.StateWorking,
			Label: "beta-build", Summary: "compiling", Changed: 1699996400, Seen: 1699999000},
		{ID: "%4", Session: "s4", Cwd: "/home/user/src/beta", State: protocol.StateDone,
			Label: "beta-notes", Summary: "wrote notes", Changed: 1699999900, Seen: 1699999000},
	}
}

// keyMsg returns the key press named by name: a single character, or one of the special
// names down, up, esc, backspace, and enter.
func keyMsg(name string) tea.KeyPressMsg {
	special := map[string]rune{
		"down":      tea.KeyDown,
		"up":        tea.KeyUp,
		"esc":       tea.KeyEscape,
		"backspace": tea.KeyBackspace,
		"enter":     tea.KeyEnter,
	}
	if code, ok := special[name]; ok {
		return tea.KeyPressMsg{Code: code}
	}
	code := []rune(name)[0]
	return tea.KeyPressMsg{Code: code, Text: name}
}

// loadedModel returns a model at the golden size with panes loaded from source and every
// command settled.
func loadedModel(t *testing.T, source func() ([]protocol.Pane, error)) model {
	t.Helper()
	m := newModel(source,
		func(id string) (string, error) { return "preview " + id, nil },
		func() time.Time { return time.Unix(testNowUnix, 0) },
	)
	next, _ := m.Update(tea.WindowSizeMsg{Width: treeWidth, Height: treeHeight})
	return modelOf(t, settle(t, next, m.loadPanes()))
}

// modelOf asserts that m is a model and returns it.
func modelOf(t *testing.T, m tea.Model) model {
	t.Helper()
	got, ok := m.(model)
	if !ok {
		t.Fatalf("model type = %T, want tui.model", m)
	}
	return got
}

// press sends the named key to m and settles the commands it returns.
func press(t *testing.T, m model, name string) model {
	t.Helper()
	next, cmd := m.Update(keyMsg(name))
	return modelOf(t, settle(t, next, cmd))
}

// pressAll sends each named key to m in order.
func pressAll(t *testing.T, m model, names ...string) model {
	t.Helper()
	for _, name := range names {
		m = press(t, m, name)
	}
	return m
}

// typeText sends each character of text to m as a key press.
func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		m = press(t, m, string(r))
	}
	return m
}

// ids returns the pane IDs in order.
func ids(panes []protocol.Pane) []string {
	out := make([]string, 0, len(panes))
	for _, p := range panes {
		out = append(out, p.ID)
	}
	return out
}

// sameIDs reports whether got lists want in order.
func sameIDs(got []protocol.Pane, want []string) bool {
	return strings.Join(ids(got), ",") == strings.Join(want, ",")
}

// staticSource returns keyPanes on every load.
func staticSource() ([]protocol.Pane, error) {
	return keyPanes(), nil
}

// assertCursor fails unless the cursor is on paneID and the preview captured paneID.
func assertCursor(t *testing.T, m model, paneID string) {
	t.Helper()
	cur, ok := m.current()
	if !ok || cur.ID != paneID {
		t.Fatalf("cursor on %q (ok=%v), want %q", cur.ID, ok, paneID)
	}
	if m.previewID != paneID || m.preview != "preview "+paneID {
		t.Fatalf("preview = %q for %q, want %q for %q", m.preview, m.previewID, "preview "+paneID, paneID)
	}
}

// quits reports whether cmd produces a quit message.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// assertQuit fails unless cmd is a quit command.
func assertQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if !quits(cmd) {
		t.Fatal("key did not quit, want quit")
	}
}

func TestKeyJMovesCursorAndRecapturesPreview(t *testing.T) {
	m := pressAll(t, loadedModel(t, staticSource), "j")
	assertCursor(t, m, "%2")
}

func TestKeyJClampsAtLastRow(t *testing.T) {
	m := pressAll(t, loadedModel(t, staticSource), "j", "j", "j", "j", "j")
	assertCursor(t, m, "%3")
}

func TestKeyKMovesCursorUpAndClampsAtFirstRow(t *testing.T) {
	m := pressAll(t, loadedModel(t, staticSource), "j", "j", "k")
	assertCursor(t, m, "%2")
	m = pressAll(t, m, "k", "k", "k")
	assertCursor(t, m, "%1")
}

func TestKeyDownMovesCursorLikeJ(t *testing.T) {
	m := pressAll(t, loadedModel(t, staticSource), "down")
	assertCursor(t, m, "%2")
	m = pressAll(t, m, "down", "down", "down", "down")
	assertCursor(t, m, "%3")
}

func TestKeyUpMovesCursorLikeK(t *testing.T) {
	m := pressAll(t, loadedModel(t, staticSource), "down", "down", "up")
	assertCursor(t, m, "%2")
	m = pressAll(t, m, "up", "up", "up")
	assertCursor(t, m, "%1")
}

func TestKeyNJumpsToNextUnseenAndWraps(t *testing.T) {
	m := loadedModel(t, staticSource)
	m = press(t, m, "n")
	assertCursor(t, m, "%2")
	m = press(t, m, "n")
	assertCursor(t, m, "%4")
	m = press(t, m, "n")
	assertCursor(t, m, "%2")
}

func TestKeyNStaysWhenNoUnseenRow(t *testing.T) {
	seenPanes := func() ([]protocol.Pane, error) {
		panes := keyPanes()
		for i := range panes {
			panes[i].Seen = panes[i].Changed
		}
		return panes, nil
	}
	m := pressAll(t, loadedModel(t, seenPanes), "j", "n")
	assertCursor(t, m, "%2")
}

func TestKeySlashTypingFiltersRows(t *testing.T) {
	m := pressAll(t, loadedModel(t, staticSource), "/")
	m = typeText(t, m, "ALPHA")
	if !sameIDs(m.rows(), []string{"%1", "%2"}) {
		t.Fatalf("visible rows = %v, want [%%1 %%2]", ids(m.rows()))
	}
	if first := strings.Split(m.View().Content, "\n")[0]; first != "/ALPHA" {
		t.Fatalf("filter line = %q, want %q", first, "/ALPHA")
	}
}

func TestFilterMatchesLabelSummarySessionAndDir(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"dir case-insensitive", "SRC/BETA", []string{"%4", "%3"}},
		{"label case-insensitive", "BETA-B", []string{"%3"}},
		{"summary", "compiling", []string{"%3"}},
		{"session", "s4", []string{"%4"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := typeText(t, pressAll(t, loadedModel(t, staticSource), "/"), tc.query)
			if !sameIDs(m.rows(), tc.want) {
				t.Fatalf("visible rows = %v, want %v", ids(m.rows()), tc.want)
			}
		})
	}
}

func TestFilterHidesEmptyGroups(t *testing.T) {
	m := typeText(t, pressAll(t, loadedModel(t, staticSource), "/"), "alpha")
	frame := m.View().Content
	if strings.Contains(frame, "/home/user/src/beta") {
		t.Fatalf("empty beta group shown:\n%s", frame)
	}
	if !strings.Contains(frame, "/home/user/src/alpha") {
		t.Fatalf("alpha group hidden:\n%s", frame)
	}
}

func TestKeyBackspaceDeletesLastCharacter(t *testing.T) {
	m := typeText(t, pressAll(t, loadedModel(t, staticSource), "/"), "beta")
	m = press(t, m, "backspace")
	if first := strings.Split(m.View().Content, "\n")[0]; first != "/bet" {
		t.Fatalf("filter line = %q, want %q", first, "/bet")
	}
	if !sameIDs(m.rows(), []string{"%4", "%3"}) {
		t.Fatalf("visible rows = %v, want [%%4 %%3]", ids(m.rows()))
	}
}

func TestKeyEnterKeepsFilterAndLeavesInput(t *testing.T) {
	m := pressAll(t, loadedModel(t, staticSource), "/")
	m = typeText(t, m, "beta")
	m = press(t, m, "enter")
	if first := strings.Split(m.View().Content, "\n")[0]; first != "/beta" {
		t.Fatalf("filter line = %q, want %q", first, "/beta")
	}
	m = press(t, m, "j")
	if first := strings.Split(m.View().Content, "\n")[0]; first != "/beta" {
		t.Fatalf("filter line after j = %q, want %q; j must move, not type", first, "/beta")
	}
	assertCursor(t, m, "%3")
}

func TestKeyEscClearsFilter(t *testing.T) {
	m := typeText(t, pressAll(t, loadedModel(t, staticSource), "/"), "beta")
	next, cmd := m.Update(keyMsg("esc"))
	if quits(cmd) {
		t.Fatal("esc in filter input quit, want it to clear the filter only")
	}
	m = modelOf(t, settle(t, next, cmd))
	if !sameIDs(m.rows(), []string{"%1", "%2", "%4", "%3"}) {
		t.Fatalf("visible rows = %v after esc, want all rows", ids(m.rows()))
	}
	if first := strings.Split(m.View().Content, "\n")[0]; first != "/home/user/src/alpha" {
		t.Fatalf("first line = %q after esc, want the alpha group header", first)
	}
}

func TestFilterTypesQAsText(t *testing.T) {
	m := pressAll(t, loadedModel(t, staticSource), "/")
	next, cmd := m.Update(keyMsg("q"))
	if quits(cmd) {
		t.Fatal("q in filter input quit, want it typed into the filter")
	}
	m = modelOf(t, settle(t, next, cmd))
	if first := strings.Split(m.View().Content, "\n")[0]; first != "/q" {
		t.Fatalf("filter line = %q, want %q", first, "/q")
	}
}

func TestKeyEscCloses(t *testing.T) {
	_, cmd := loadedModel(t, staticSource).Update(keyMsg("esc"))
	assertQuit(t, cmd)
}

func TestKeyQCloses(t *testing.T) {
	_, cmd := loadedModel(t, staticSource).Update(keyMsg("q"))
	assertQuit(t, cmd)
}

func TestRunQCloses(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan runOutcome, 1)
	go func() {
		res, err := Run(ctx, Options{
			Panes:  func(context.Context) ([]protocol.Pane, error) { return nil, nil },
			Input:  strings.NewReader("q"),
			Output: io.Discard,
		})
		done <- runOutcome{result: res, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Run: %v", got.err)
		}
		if got.result.Action != ActionClose {
			t.Fatalf("Run action = %q, want %q", got.result.Action, ActionClose)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after q")
	}
}

func TestReloadKeepsCursorOnSamePane(t *testing.T) {
	panes := keyPanes()
	m := pressAll(t, loadedModel(t, func() ([]protocol.Pane, error) { return panes, nil }), "j")
	assertCursor(t, m, "%2")

	reordered := keyPanes()
	reordered[0].State = protocol.StateDone
	panes = reordered
	m = modelOf(t, settle(t, m, m.loadPanes()))
	if !sameIDs(m.rows(), []string{"%2", "%1", "%4", "%3"}) {
		t.Fatalf("visible rows = %v after reorder", ids(m.rows()))
	}
	assertCursor(t, m, "%2")
}

func TestReloadClampsWhenCursorPaneIsGone(t *testing.T) {
	panes := keyPanes()
	m := pressAll(t, loadedModel(t, func() ([]protocol.Pane, error) { return panes, nil }), "j", "j", "j")
	assertCursor(t, m, "%3")

	var kept []protocol.Pane
	for _, p := range keyPanes() {
		if p.ID != "%3" {
			kept = append(kept, p)
		}
	}
	panes = kept
	m = modelOf(t, settle(t, m, m.loadPanes()))
	assertCursor(t, m, "%4")
}
