package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/shell"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
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

// actionPane is one pane of a synthetic tmux layout. dir is a directory name under the
// test's temp dir, and changed is the @orrery_changed value.
type actionPane struct {
	session string
	dir     string
	state   protocol.State
	label   string
	changed string
}

// seenBefore is the @orrery_seen value every layout pane starts with. An action that marks a
// pane seen replaces it with the current time.
const seenBefore = "100"

// actionLayout is the layout the action tests run against. The invoking client is on s1 and a
// second client on s4. The tree shows s1 (waiting), then s2 (unseen) in alpha, then s3 and s4
// (working) in beta, so the rows are %A, %B, %C, %D in that order.
func actionLayout() []actionPane {
	return []actionPane{
		{session: "s1", dir: "alpha", state: protocol.StateWaiting, label: "alpha-review", changed: "100"},
		{session: "s2", dir: "alpha", state: protocol.StateDone, label: "alpha-tests", changed: "200"},
		{session: "s3", dir: "beta", state: protocol.StateWorking, label: "beta-build", changed: "100"},
		{session: "s4", dir: "beta", state: protocol.StateWorking, label: "beta-notes", changed: "100"},
	}
}

// actionTree is a private tmux server holding one pane per layout entry, with the invoking
// client attached to s1 and a second client to s4. panes maps each session to its pane ID.
type actionTree struct {
	t        *testing.T
	client   *tmuxctl.Client
	base     string
	labels   []string
	panes    map[string]string
	invoking string
	other    string
}

// newActionTree builds layout on a fresh private server and attaches the two clients.
func newActionTree(t *testing.T, layout []actionPane) *actionTree {
	t.Helper()
	a := &actionTree{t: t, client: tuiServer(t), base: t.TempDir(), panes: map[string]string{}}
	for _, p := range layout {
		a.addPane(p)
	}
	a.invoking = a.attach("s1", 80, 24)
	a.other = a.attach("s4", 200, 60)
	return a
}

// dir returns the path of the layout directory name.
func (a *actionTree) dir(name string) string {
	return filepath.Join(a.base, name)
}

// addPane creates the session for p, running a long-lived command in p's directory, and sets
// the @orrery options the tree reads.
func (a *actionTree) addPane(p actionPane) {
	a.t.Helper()
	dir := a.dir(p.dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.t.Fatalf("mkdir %s: %v", dir, err)
	}
	// The pane starts in the base directory and cds into dir: new-session -c expands formats in
	// its directory, so a directory whose name holds a format cannot be its start directory.
	command := "cd " + shellQuote(dir) + " && exec sleep 300"
	out, err := a.client.Run(a.t.Context(), "-f", tuiNoConfig, "new-session", "-d", "-P", "-F", "#{pane_id}",
		"-s", p.session, "-c", a.base, command)
	if err != nil {
		a.t.Fatalf("new-session %s: %v", p.session, err)
	}
	id := strings.TrimSpace(out)
	a.panes[p.session] = id
	a.labels = append(a.labels, p.label)
	options := map[string]string{
		protocol.OptionState:   string(p.state),
		protocol.OptionLabel:   p.label,
		protocol.OptionChanged: p.changed,
		protocol.OptionSeen:    seenBefore,
	}
	for name, value := range options {
		if err := a.client.SetPaneOption(a.t.Context(), id, name, value); err != nil {
			a.t.Fatalf("set %s on %s: %v", name, id, err)
		}
	}
}

// shellQuote returns value as one single-quoted word of a POSIX shell command.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// attach starts a client of cols by rows on session through script, with its stdin held open
// until cleanup, and returns its tty once tmux lists it.
func (a *actionTree) attach(session string, cols, rows int) string {
	a.t.Helper()
	socket, err := a.client.Run(a.t.Context(), "display-message", "-p", "#{socket_path}")
	if err != nil {
		a.t.Fatalf("socket path: %v", err)
	}
	command := fmt.Sprintf("stty cols %d rows %d; tmux -S %s attach -t %s",
		cols, rows, strings.TrimSpace(socket), session)
	cmd := exec.CommandContext(a.t.Context(), "script", "-qfc", command, "/dev/null")
	cmd.Env = withoutEnv(os.Environ(), "TMUX")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		a.t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		a.t.Fatalf("start client on %s: %v", session, err)
	}
	a.t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	var tty string
	waitFor(a.t, "client on "+session, func() error {
		clients, err := a.clients()
		if err != nil {
			return err
		}
		for clientTTY, clientSession := range clients {
			if clientSession == session {
				tty = clientTTY
				return nil
			}
		}
		return fmt.Errorf("no client on %s in %v", session, clients)
	})
	return tty
}

// withoutEnv returns env without the entries for name, so that a client started from a
// process inside tmux does not refuse to attach.
func withoutEnv(env []string, name string) []string {
	var kept []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			kept = append(kept, kv)
		}
	}
	return kept
}

// clientInfo is one attached client as list-clients reports it.
type clientInfo struct {
	tty     string
	session string
	width   int
	height  int
}

// listClients returns each attached client with the session it shows and its size.
func (a *actionTree) listClients() ([]clientInfo, error) {
	out, err := a.client.Run(a.t.Context(), "list-clients", "-F",
		"#{client_tty} #{client_session} #{client_width} #{client_height}")
	if err != nil {
		return nil, fmt.Errorf("list-clients: %w", err)
	}
	var got []clientInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("list-clients line %q: want 4 fields", line)
		}
		width, errWidth := strconv.Atoi(fields[2])
		height, errHeight := strconv.Atoi(fields[3])
		if err := errors.Join(errWidth, errHeight); err != nil {
			return nil, fmt.Errorf("list-clients size in %q: %w", line, err)
		}
		got = append(got, clientInfo{tty: fields[0], session: fields[1], width: width, height: height})
	}
	return got, nil
}

// clients returns the session each attached client shows, keyed by tty.
func (a *actionTree) clients() (map[string]string, error) {
	infos, err := a.listClients()
	if err != nil {
		return nil, err
	}
	got := map[string]string{}
	for _, c := range infos {
		got[c.tty] = c.session
	}
	return got, nil
}

// windowCount returns how many windows session has.
func (a *actionTree) windowCount(session string) (int, error) {
	out, err := a.client.Run(a.t.Context(), "list-windows", "-t", "="+session, "-F", "#{window_index}")
	if err != nil {
		return 0, fmt.Errorf("list-windows %s: %w", session, err)
	}
	return len(strings.Fields(out)), nil
}

// check returns nil when the invoking client shows session invokingSession, the second client
// still shows s4, and exactly the panes of the sessions in marked have had @orrery_seen replaced.
// Every layout session holds one pane, so a client shows the pane of its session.
func (a *actionTree) check(invokingSession string, marked ...string) error {
	clients, err := a.clients()
	if err != nil {
		return err
	}
	if got := clients[a.invoking]; got != invokingSession {
		return fmt.Errorf("invoking client shows %s, want %s", got, invokingSession)
	}
	if got := clients[a.other]; got != "s4" {
		return fmt.Errorf("second client shows %s, want s4", got)
	}
	for session, pane := range a.panes {
		seen, _, err := a.client.PaneOption(a.t.Context(), pane, protocol.OptionSeen)
		if err != nil {
			return fmt.Errorf("seen of %s: %w", pane, err)
		}
		isMarked := slices.Contains(marked, session)
		if isMarked != (seen != seenBefore) {
			return fmt.Errorf("session %s @orrery_seen = %q, marked = %v", session, seen, isMarked)
		}
	}
	return nil
}

// shellPopupErr returns an error unless the popup shell for dir is attached by a client other
// than the invoking one, that popup client is smaller than the invoking client, and the shell
// session holds @orrery_dir as dir. A popup is sized from the client it is drawn on, so a popup
// drawn on the wider second client is larger than one drawn on the invoking client.
func (a *actionTree) shellPopupErr(dir string) error {
	infos, err := a.listClients()
	if err != nil {
		return err
	}
	want := shell.Name(dir)
	var invoking, popup *clientInfo
	for i := range infos {
		switch {
		case infos[i].tty == a.invoking:
			invoking = &infos[i]
		case infos[i].session == want:
			popup = &infos[i]
		}
	}
	if invoking == nil {
		return fmt.Errorf("invoking client %s not listed in %v", a.invoking, infos)
	}
	if popup == nil {
		return fmt.Errorf("no client attached to %s in %v", want, infos)
	}
	gotDir, err := a.client.Run(a.t.Context(), "show-options", "-q", "-v", "-t", "="+want+":", protocol.OptionDir)
	if err != nil {
		return fmt.Errorf("%s of %s: %w", protocol.OptionDir, want, err)
	}
	if got := strings.TrimSuffix(gotDir, "\n"); got != dir {
		return fmt.Errorf("%s of %s = %q, want %q", protocol.OptionDir, want, got, dir)
	}
	if popup.width >= invoking.width || popup.height >= invoking.height {
		return fmt.Errorf(
			"popup client %s is %dx%d, not smaller than invoking client %dx%d: popup drawn on another client",
			popup.tty,
			popup.width,
			popup.height,
			invoking.width,
			invoking.height,
		)
	}
	return nil
}

// treeRun is one Run of the tree, driven through its Input.
type treeRun struct {
	t     *testing.T
	out   *syncBuffer
	input *io.PipeWriter
	done  chan runOutcome
}

// start runs the tree for the client on tty and returns once every pane label is rendered.
func (a *actionTree) start(tty string) *treeRun {
	a.t.Helper()
	pr, pw := io.Pipe()
	run := &treeRun{t: a.t, out: &syncBuffer{}, input: pw, done: make(chan runOutcome, 1)}
	a.t.Cleanup(func() { _ = pw.Close() })
	go func() {
		res, err := Run(a.t.Context(), Options{
			Client:    a.client,
			ClientTTY: tty,
			Panes:     a.client.ListPanes,
			Input:     pr,
			Output:    run.out,
		})
		run.done <- runOutcome{result: res, err: err}
	}()
	waitFor(a.t, "pane labels rendered", func() error {
		for _, label := range a.labels {
			if !strings.Contains(run.out.String(), label) {
				return fmt.Errorf("label %s not rendered", label)
			}
		}
		return nil
	})
	return run
}

// keyBytes returns the terminal bytes for a key name: a special key or one literal character.
func keyBytes(name string) string {
	switch name {
	case "down":
		return "\x1b[B"
	case "up":
		return "\x1b[A"
	case "esc":
		return "\x1b"
	case "enter":
		return "\r"
	}
	return name
}

// send writes each key to the tree's input in order.
func (r *treeRun) send(keys ...string) {
	r.t.Helper()
	for _, key := range keys {
		if _, err := r.input.Write([]byte(keyBytes(key))); err != nil {
			r.t.Fatalf("send %q: %v", key, err)
		}
	}
}

// assertOpen fails if Run has returned.
func (r *treeRun) assertOpen() {
	r.t.Helper()
	select {
	case got := <-r.done:
		r.t.Fatalf("tree closed (action %q, err %v), want it open", got.result.Action, got.err)
	default:
	}
}

// closed waits for Run to return and returns its result.
func (r *treeRun) closed() Result {
	r.t.Helper()
	select {
	case got := <-r.done:
		if got.err != nil {
			r.t.Fatalf("Run: %v", got.err)
		}
		return got.result
	case <-time.After(10 * time.Second):
		r.t.Fatalf("Run did not return; output:\n%s", r.out.String())
		return Result{}
	}
}

// waitFor polls cond until it returns nil, and fails the test with its last error at the deadline.
func waitFor(t *testing.T, what string, cond func() error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := cond()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %v", what, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEnterSwitchesOnlyInvokingClientAndMarksSeen(t *testing.T) {
	cases := []struct {
		name    string
		keys    []string
		session string
	}{
		{"j", []string{"j", "enter"}, "s2"},
		{"k", []string{"j", "j", "k", "enter"}, "s2"},
		{"down", []string{"down", "enter"}, "s2"},
		{"up", []string{"down", "down", "up", "enter"}, "s2"},
		{"n", []string{"n", "enter"}, "s2"},
		{"filter", []string{"/", "b", "e", "t", "a", "enter", "enter"}, "s3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newActionTree(t, actionLayout())
			run := a.start(a.invoking)
			run.send(tc.keys...)
			if got := run.closed(); got.Action != ActionClose {
				t.Fatalf("Run action = %q, want %q", got.Action, ActionClose)
			}
			waitFor(t, "enter switches and marks seen", func() error { return a.check(tc.session, tc.session) })
		})
	}
}

func TestXMarksCursorPaneSeenAndStaysOpen(t *testing.T) {
	a := newActionTree(t, actionLayout())
	run := a.start(a.invoking)
	run.send("j", "x")
	waitFor(t, "x marks the cursor pane seen", func() error { return a.check("s1", "s2") })
	run.assertOpen()
	run.send("q")
	run.closed()
}

func TestClaudeAddsWindowInCursorSessionAndDirectory(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\npwd > '" + marker + "'\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatalf("write synthetic claude: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	a := newActionTree(t, actionLayout())
	run := a.start(a.invoking)
	run.send("j", "c")
	waitFor(t, "claude window in s2 in the group directory", func() error {
		count, err := a.windowCount("s2")
		if err != nil {
			return err
		}
		if count != 2 {
			return fmt.Errorf("s2 has %d windows, want 2", count)
		}
		ran, err := os.ReadFile(marker)
		if err != nil {
			return fmt.Errorf("read marker: %w", err)
		}
		if got := strings.TrimSpace(string(ran)); got != a.dir("alpha") {
			return fmt.Errorf("claude ran in %q, want %q", got, a.dir("alpha"))
		}
		return nil
	})
	for _, session := range []string{"s1", "s3", "s4"} {
		count, err := a.windowCount(session)
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s has %d windows, want 1", session, count)
		}
	}
	waitFor(t, "invoking client unchanged", func() error { return a.check("s1") })
	run.assertOpen()
	run.send("q")
	run.closed()
}

func TestShellOpensPopupShellForGroupAndAttachesIt(t *testing.T) {
	a := newActionTree(t, actionLayout())
	run := a.start(a.invoking)
	run.send("j", "s")
	if got := run.closed(); got.Action != ActionClose {
		t.Fatalf("Run action = %q, want %q", got.Action, ActionClose)
	}
	waitFor(t, "popup shell attached on invoking client", func() error { return a.shellPopupErr(a.dir("alpha")) })
	waitFor(t, "invoking client unchanged", func() error { return a.check("s1") })
}

func TestShellPopupAttachesWithQuotedDirectory(t *testing.T) {
	quoted := `it's a "dir"`
	a := newActionTree(t, []actionPane{
		{session: "s1", dir: quoted, state: protocol.StateWaiting, label: "quoted-dir", changed: "100"},
		{session: "s4", dir: "beta", state: protocol.StateWorking, label: "beta-notes", changed: "100"},
	})
	run := a.start(a.invoking)
	run.send("s")
	if got := run.closed(); got.Action != ActionClose {
		t.Fatalf("Run action = %q, want %q", got.Action, ActionClose)
	}
	waitFor(t, "popup shell attached for quoted directory", func() error { return a.shellPopupErr(a.dir(quoted)) })
}

func TestShellOpensSessionForDirectoryWithBraceFormat(t *testing.T) {
	name := "x#{session_name}y"
	a := newActionTree(t, []actionPane{
		{session: "s1", dir: name, state: protocol.StateWaiting, label: "brace-dir", changed: "100"},
		{session: "s4", dir: "beta", state: protocol.StateWorking, label: "beta-notes", changed: "100"},
	})
	run := a.start(a.invoking)
	run.send("s")
	if got := run.closed(); got.Action != ActionClose {
		t.Fatalf("Run action = %q, want %q", got.Action, ActionClose)
	}
	waitFor(t, "popup shell attached for directory with brace format", func() error {
		return a.shellPopupErr(a.dir(name))
	})
	waitFor(t, "invoking client unchanged", func() error { return a.check("s1") })
}

func TestShellDoesNotExpandFormatsInGroupDirectory(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	// The name is a tmux format job, "#(...)", written with its '#' doubled. A directory name
	// holds no slash, so the marker path is spelled with @ and tr rebuilds it inside the job.
	// The job runs only if a format expansion turns "##" into a single "#".
	name := `pwn##(touch "$(echo ` + strings.ReplaceAll(marker, "/", "@") + ` | tr @ /)")`
	a := newActionTree(t, []actionPane{
		{session: "s1", dir: name, state: protocol.StateWaiting, label: "injected-dir", changed: "100"},
		{session: "s4", dir: "beta", state: protocol.StateWorking, label: "beta-notes", changed: "100"},
	})
	run := a.start(a.invoking)
	run.send("s")
	if got := run.closed(); got.Action != ActionClose {
		t.Fatalf("Run action = %q, want %q", got.Action, ActionClose)
	}
	waitFor(t, "popup shell attached for directory with formats", func() error { return a.shellPopupErr(a.dir(name)) })
	// A format job runs asynchronously, so the marker is checked after a short settle.
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command in group directory ran: stat %s = %v, want not exist", marker, err)
	}
	waitFor(t, "invoking client unchanged", func() error { return a.check("s1") })
}

func TestNHandsOffIntentWithGroupDirectory(t *testing.T) {
	a := newActionTree(t, actionLayout())
	run := a.start(a.invoking)
	run.send("j", "N")
	got := run.closed()
	if got.Action != ActionIntent {
		t.Fatalf("Run action = %q, want %q", got.Action, ActionIntent)
	}
	if got.Dir != a.dir("alpha") {
		t.Fatalf("Run dir = %q, want %q", got.Dir, a.dir("alpha"))
	}
	waitFor(t, "invoking client unchanged", func() error { return a.check("s1") })
}

func TestEscAndQCloseWithoutMovingClientsOrMarkingSeen(t *testing.T) {
	for _, key := range []string{"esc", "q"} {
		t.Run(key, func(t *testing.T) {
			a := newActionTree(t, actionLayout())
			run := a.start(a.invoking)
			run.send("j", key)
			if got := run.closed(); got.Action != ActionClose {
				t.Fatalf("Run action = %q, want %q", got.Action, ActionClose)
			}
			waitFor(t, "clients and seen unchanged", func() error { return a.check("s1") })
		})
	}
}

func TestFailedActionShowsErrorAndStaysOpen(t *testing.T) {
	a := newActionTree(t, actionLayout())
	run := a.start("/dev/does-not-exist")
	run.send("enter")
	waitFor(t, "error on status line", func() error {
		if !strings.Contains(run.out.String(), "error: ") {
			return errors.New("no error line rendered")
		}
		return nil
	})
	run.assertOpen()
	run.send("q")
	run.closed()
}
