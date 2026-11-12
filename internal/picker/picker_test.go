package picker

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

const (
	// childEnv names the spec file a picker pane reads. Only TestPickerPane reads it.
	childEnv = "ORRERY_PICKER_SPEC"
	// noConfig keeps the user's tmux configuration out of the test servers.
	noConfig = "/dev/null"
	// paneWidth and paneHeight keep a list row on one line of the captured pane.
	paneWidth  = "200"
	paneHeight = "50"
	// settle is how long a check waits for the picker to act on a key it must ignore.
	settle = 500 * time.Millisecond
	// waitLimit bounds every wait for the picker pane.
	waitLimit = 20 * time.Second
	// testText is the synthetic TEXT every test sends.
	testText = "example text"
)

// pickerSpec is what a picker pane reads: the server and fakes Run uses, TEXT, DIR, and the file the outcome goes
// to. A non-empty Gate is a file the pane waits for before Run, so keys sent before it are queued. A non-empty
// Actions is the list the pane passes to Choose instead of calling Run.
type pickerSpec struct {
	Server  string
	Zoxide  string
	Claude  string
	Root    string
	Text    string
	Dir     string
	Gate    string
	Result  string
	Actions []open.Action
}

// pickerOutcome is what the picker pane's Run returned.
type pickerOutcome struct {
	Session string
	Pane    string
	Chosen  bool
	Err     string
}

// TestPickerPane is the program of a picker pane. The parent test runs the test binary with only this test
// selected and childEnv set; without childEnv the test does nothing.
func TestPickerPane(t *testing.T) {
	specPath := os.Getenv(childEnv)
	if specPath == "" {
		t.Skip("runs only as a picker pane")
	}
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var spec pickerSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	client, err := tmuxctl.New(tmuxctl.Server{Name: spec.Server})
	if err != nil {
		t.Fatalf("tmux client: %v", err)
	}
	if spec.Gate != "" {
		waitUntil(t, "the gate", func() bool {
			_, err := os.Stat(spec.Gate)
			return err == nil
		})
	}
	options := Options{
		Config: open.Config{Tmux: client, Zoxide: spec.Zoxide, Claude: spec.Claude, Root: spec.Root},
		Text:   spec.Text,
		Dir:    spec.Dir,
		Input:  os.Stdin,
		Output: os.Stdout,
	}
	var target open.Target
	var chosen bool
	var runErr error
	if len(spec.Actions) > 0 {
		target, chosen, runErr = Choose(t.Context(), options, spec.Actions)
	} else {
		target, chosen, runErr = Run(t.Context(), options)
	}
	outcome := pickerOutcome{Session: target.Session, Pane: target.Pane, Chosen: chosen}
	if runErr != nil {
		outcome.Err = runErr.Error()
	}
	out, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("encode outcome: %v", err)
	}
	// The parent waits for the result file, so it is renamed into place whole.
	tmp := spec.Result + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		t.Fatalf("write outcome: %v", err)
	}
	if err := os.Rename(tmp, spec.Result); err != nil {
		t.Fatalf("publish outcome: %v", err)
	}
}

// stopPickerServer kills the private server name, accepting one already gone. Cleanups run last first, so a
// fixture calls it again after any temp dir a held fake writes into, and the server stops before that dir goes.
func stopPickerServer(t *testing.T, name string) {
	t.Helper()
	err := tmuxtest.Stop(t, tmuxctl.Server{Name: name})
	if err == nil {
		return
	}
	msg := err.Error()
	if !strings.Contains(msg, "no server running") && !strings.Contains(msg, "error connecting to") {
		t.Errorf("kill-server: %v", err)
	}
}

// newPickerServer returns a client and name for a unique private tmux server, killed at cleanup.
func newPickerServer(t *testing.T) (*tmuxctl.Client, string) {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-test-picker-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() { stopPickerServer(t, name) })
	return client, name
}

// fixture is one picker run: a private tmux server with a target pane that has a state, the fake zoxide and
// fake claude, and the directories zoxide ranks.
type fixture struct {
	client        *tmuxctl.Client
	server        string
	root          string // absolute: the default project root, and the parent of the ranked directories
	dir1          string // ranked d1
	zoxide        string
	claude        string
	hold          string // while this file exists, every fake claude run blocks
	targetSession string
	targetPane    string
	pane          string // the picker's pane
	text          string // the picker's TEXT; "" asks for it
	dir           string // the picker's DIR; "" has none
	gated         bool   // the picker pane waits for gate before Run
	gate          string
	actions       []open.Action // non-empty: the picker pane calls Choose with these instead of Run
	spec          string
	result        string
}

// newFixture starts the target pane and installs the fakes. The fake zoxide exits with zoxideStatus; the fake
// claude replies with one pane action, one jump to the target pane, and task: yes.
func newFixture(t *testing.T, zoxideStatus int) *fixture {
	t.Helper()
	root := t.TempDir()
	client, server := newPickerServer(t)
	f := &fixture{
		client:        client,
		server:        server,
		root:          root,
		dir1:          filepath.Join(root, "dir-01"),
		targetSession: "target",
		text:          testText,
	}
	dir2 := filepath.Join(root, "dir-02")
	for _, dir := range []string{f.dir1, dir2} {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mustRun(t, client, "-f", noConfig, "new-session", "-d", "-s", f.targetSession, "-c", root, "sleep 300")
	f.targetPane = trimLine(mustRun(t, client, "display-message", "-p", "-t", f.targetSession, "#{pane_id}"))
	mustRun(t, client, "set-option", "-p", "-t", f.targetPane, protocol.OptionState, "waiting")
	mustRun(t, client, "set-option", "-p", "-t", f.targetPane, protocol.OptionContext, "")

	zoxideListing := zoxideLine(f.dir1) + zoxideLine(dir2)
	f.zoxide = installFake(t, "fake-zoxide", "zoxide", zoxideListing, zoxideStatus)
	reply := fmt.Sprintf("pane d1\njump %s\ntask: yes\n", f.targetPane)
	f.claude = installFake(t, "fake-claude", "claude", reply, 0)
	f.hold = filepath.Join(filepath.Dir(f.claude), "hold")
	// The fakes' dirs were made after the server, so stop it again first: a held fake claude writes into its dir.
	t.Cleanup(func() { stopPickerServer(t, server) })
	return f
}

// setReply replaces the fake claude's reply with reply.
func (f *fixture) setReply(t *testing.T, reply string) {
	t.Helper()
	writeFile(t, filepath.Join(filepath.Dir(f.claude), "reply"), reply)
}

// startPicker runs the test binary as the picker pane's program, with the fixture's text and dir, and returns
// once the pane exists.
func (f *fixture) startPicker(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// The picker writes its result here, so its server stops before this dir is removed.
	t.Cleanup(func() { stopPickerServer(t, f.server) })
	f.spec = filepath.Join(dir, "spec.json")
	f.result = filepath.Join(dir, "result.json")
	if f.gated {
		f.gate = filepath.Join(dir, "gate")
	}
	data, err := json.Marshal(pickerSpec{
		Server:  f.server,
		Zoxide:  f.zoxide,
		Claude:  f.claude,
		Root:    f.root,
		Text:    f.text,
		Dir:     f.dir,
		Gate:    f.gate,
		Result:  f.result,
		Actions: f.actions,
	})
	if err != nil {
		t.Fatalf("encode spec: %v", err)
	}
	writeFile(t, f.spec, string(data))
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("test binary: %v", err)
	}
	// The pane runs this test binary, then sleeps so the pane and its session outlive Run.
	command := fmt.Sprintf("%s=%s %s %s; sleep 300",
		childEnv, shellQuote(f.spec), shellQuote(exe), shellQuote("-test.run=^TestPickerPane$"))
	mustRun(t, f.client, "new-session", "-d", "-s", "picker", "-x", paneWidth, "-y", paneHeight, "-c", f.root, command)
	f.pane = trimLine(mustRun(t, f.client, "display-message", "-p", "-t", "picker", "#{pane_id}"))
}

// frame returns the picker pane's visible text.
func (f *fixture) frame(t *testing.T) string {
	t.Helper()
	return mustRun(t, f.client, "capture-pane", "-p", "-t", f.pane)
}

// waitForFrame waits until the picker pane shows want.
func (f *fixture) waitForFrame(t *testing.T, want string) {
	t.Helper()
	waitUntil(t, fmt.Sprintf("the picker to show %q", want), func() bool {
		return strings.Contains(f.frame(t), want)
	})
}

// sendKeys writes keys to the picker pane as key presses.
func (f *fixture) sendKeys(t *testing.T, keys ...string) {
	t.Helper()
	mustRun(t, f.client, slices.Concat([]string{"send-keys", "-t", f.pane}, keys)...)
}

// typeText writes s to the picker pane as literal characters, not key names.
func (f *fixture) typeText(t *testing.T, s string) {
	t.Helper()
	mustRun(t, f.client, "send-keys", "-t", f.pane, "-l", s)
}

// hasLine reports whether a line of frame is want, ignoring trailing spaces.
func hasLine(frame, want string) bool {
	return slices.ContainsFunc(strings.Split(frame, "\n"), func(line string) bool {
		return strings.TrimRight(line, " ") == want
	})
}

// claudeRunFiles returns the content of the file name in each fake claude run's directory.
func claudeRunFiles(t *testing.T, claude, name string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(claude), "calls", "run.*", name))
	if err != nil {
		t.Fatalf("glob claude %s: %v", name, err)
	}
	contents := make([]string, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		contents = append(contents, string(data))
	}
	return contents
}

// awaitOutcome waits for the picker's Run to return and reads what it returned.
func (f *fixture) awaitOutcome(t *testing.T) pickerOutcome {
	t.Helper()
	var outcome pickerOutcome
	waitUntil(t, "the picker outcome", func() bool {
		data, err := os.ReadFile(f.result)
		if err != nil {
			return false
		}
		return json.Unmarshal(data, &outcome) == nil
	})
	return outcome
}

// assertNoOutcome fails when Run has returned, after giving the picker time to return.
func (f *fixture) assertNoOutcome(t *testing.T) {
	t.Helper()
	time.Sleep(settle)
	if _, err := os.Stat(f.result); err == nil {
		t.Fatal("Run returned before Enter on a shown choice")
	}
}

// state is the sessions, the windows, and the entries under the root. A test asserts it is unchanged to show
// that nothing was created.
func (f *fixture) state(t *testing.T) string {
	t.Helper()
	sessions := strings.TrimSpace(mustRun(t, f.client, "list-sessions", "-F", "#{session_name}"))
	windows := strings.TrimSpace(mustRun(t, f.client, "list-windows", "-a", "-F", "#{window_id}"))
	entries, err := os.ReadDir(f.root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return strings.Join([]string{sessions, windows, strings.Join(names, " ")}, "\n")
}

// sessionDir returns the @orrery_dir option of session.
func (f *fixture) sessionDir(t *testing.T, session string) string {
	t.Helper()
	return trimLine(mustRun(t, f.client, "show-options", "-v", "-t", "="+session+":", protocol.OptionDir))
}

// claudeRuns counts the fake claude runs that have started.
func claudeRuns(t *testing.T, claude string) int {
	t.Helper()
	runs, err := filepath.Glob(filepath.Join(filepath.Dir(claude), "calls", "run.*"))
	if err != nil {
		t.Fatalf("glob claude runs: %v", err)
	}
	return len(runs)
}

func TestPickerDiscardsKeysTypedBeforeRender(t *testing.T) {
	f := newFixture(t, 0)
	writeFile(t, f.hold, "")
	f.startPicker(t)
	baseline := f.state(t)

	// Intent is held in the fake claude, so the picker has not rendered. The j moves the cursor if it reaches the
	// list. The Enter reaches the terminal as a newline (ICRNL), which the picker reads as ctrl+j, so the j is what
	// shows whether the queued keys were discarded.
	waitUntil(t, "the fake claude run to start", func() bool { return claudeRuns(t, f.claude) > 0 })
	f.sendKeys(t, "j")
	f.sendKeys(t, "Enter")
	time.Sleep(settle)
	if err := os.Remove(f.hold); err != nil {
		t.Fatalf("release the fake claude run: %v", err)
	}

	f.waitForFrame(t, "> pane "+f.dir1+" (prompt)")
	f.assertNoOutcome(t)
	if got := f.state(t); got != baseline {
		t.Fatalf("state changed before Enter on a shown choice:\nbefore:\n%s\nafter:\n%s", baseline, got)
	}

	// The pane action's fake claude run holds too, so its session outlives the check.
	writeFile(t, f.hold, "")
	f.sendKeys(t, "Enter")
	outcome := f.awaitOutcome(t)
	if !outcome.Chosen || outcome.Err != "" {
		t.Fatalf("outcome = %+v, want the first action run with no error", outcome)
	}
	if outcome.Pane != "" || outcome.Session == "" || outcome.Session == f.targetSession {
		t.Fatalf("outcome = %+v, want a new session with no pane", outcome)
	}
	if got := f.sessionDir(t, outcome.Session); got != f.dir1 {
		t.Fatalf("new session dir = %q, want %q", got, f.dir1)
	}
}

func TestPickerDownAndUpMoveSelectionAndEnterRunsIt(t *testing.T) {
	f := newFixture(t, 0)
	f.startPicker(t)
	baseline := f.state(t)
	f.waitForFrame(t, "> pane "+f.dir1)

	// Up at the first row stays there, down stops at the last row, and up returns to the jump row.
	for _, key := range []string{"k", "j", "j", "j", "k"} {
		f.sendKeys(t, key)
	}
	f.waitForFrame(t, "> jump "+f.targetPane)
	f.sendKeys(t, "Enter")

	outcome := f.awaitOutcome(t)
	if !outcome.Chosen || outcome.Err != "" {
		t.Fatalf("outcome = %+v, want the jump run with no error", outcome)
	}
	if outcome.Pane != f.targetPane || outcome.Session != f.targetSession {
		t.Fatalf("outcome = %+v, want pane %s in session %s", outcome, f.targetPane, f.targetSession)
	}
	if got := f.state(t); got != baseline {
		t.Fatalf("jump created state:\nbefore:\n%s\nafter:\n%s", baseline, got)
	}
}

func TestPickerQuitExecutesNothing(t *testing.T) {
	for _, key := range []string{"q", "Escape"} {
		t.Run(key, func(t *testing.T) {
			f := newFixture(t, 0)
			f.startPicker(t)
			baseline := f.state(t)
			f.waitForFrame(t, "> pane "+f.dir1)
			f.sendKeys(t, key)

			outcome := f.awaitOutcome(t)
			if outcome.Chosen || outcome.Err != "" || outcome.Session != "" || outcome.Pane != "" {
				t.Fatalf("outcome = %+v, want no choice, no error, and no target", outcome)
			}
			if got := f.state(t); got != baseline {
				t.Fatalf("quit created state:\nbefore:\n%s\nafter:\n%s", baseline, got)
			}
		})
	}
}

func TestPickerReturnsIntentError(t *testing.T) {
	f := newFixture(t, 2)
	f.startPicker(t)
	baseline := f.state(t)

	outcome := f.awaitOutcome(t)
	if outcome.Chosen || !strings.Contains(outcome.Err, "zoxide query") {
		t.Fatalf("outcome = %+v, want an Intent error and no choice", outcome)
	}
	if strings.Contains(f.frame(t), "> pane") {
		t.Fatal("the picker showed a list after Intent failed")
	}
	if got := f.state(t); got != baseline {
		t.Fatalf("failed Intent created state:\nbefore:\n%s\nafter:\n%s", baseline, got)
	}
}

func TestPickerTextDiscardsKeysTypedBeforePrompt(t *testing.T) {
	f := newFixture(t, 0)
	f.text, f.gated = "", true
	f.startPicker(t)
	baseline := f.state(t)

	// The pane waits at the gate, so the j and Enter are queued before the prompt draws. Read, the j would start
	// the text.
	f.sendKeys(t, "j", "Enter")
	time.Sleep(settle)
	writeFile(t, f.gate, "")

	f.waitForFrame(t, "text:")
	f.typeText(t, "examplx")
	f.sendKeys(t, "BSpace")
	f.typeText(t, "e text")
	f.waitForFrame(t, "text: example text")
	f.sendKeys(t, "Enter")
	f.waitForFrame(t, "> pane "+f.dir1+" (prompt)")

	stdins := claudeRunFiles(t, f.claude, "stdin")
	if len(stdins) != 1 || !strings.Contains(stdins[0], "\nTEXT:\nexample text\n") {
		t.Fatalf("fake claude stdin = %q, want one run with TEXT %q", stdins, "example text")
	}
	f.sendKeys(t, "Escape")
	outcome := f.awaitOutcome(t)
	if outcome.Chosen || outcome.Err != "" {
		t.Fatalf("outcome = %+v, want no choice and no error", outcome)
	}
	if got := f.state(t); got != baseline {
		t.Fatalf("state changed:\nbefore:\n%s\nafter:\n%s", baseline, got)
	}
}

func TestPickerTextEscapeRunsNothing(t *testing.T) {
	f := newFixture(t, 0)
	f.text = ""
	f.startPicker(t)
	baseline := f.state(t)
	f.waitForFrame(t, "text:")
	f.sendKeys(t, "Escape")

	outcome := f.awaitOutcome(t)
	if outcome.Chosen || outcome.Err != "" || outcome.Session != "" || outcome.Pane != "" {
		t.Fatalf("outcome = %+v, want no choice, no error, and no target", outcome)
	}
	if n := claudeRuns(t, f.claude); n != 0 {
		t.Fatalf("fake claude ran %d times, want none", n)
	}
	if got := f.state(t); got != baseline {
		t.Fatalf("Esc at the prompt created state:\nbefore:\n%s\nafter:\n%s", baseline, got)
	}
}

func TestPickerDirFirstRowOpensPaneWithPromptOnTask(t *testing.T) {
	f := newFixture(t, 0)
	// The model ranks d2 first and names Dir (d1) below it: Dir must come first and appear once.
	f.setReply(t, fmt.Sprintf("pane d2\npane d1\njump %s\ntask: yes\n", f.targetPane))
	f.dir = f.dir1
	f.startPicker(t)

	f.waitForFrame(t, "> pane "+f.dir1+" (prompt)")
	if n := strings.Count(f.frame(t), "pane "+f.dir1); n != 1 {
		t.Fatalf("the frame names %s %d times, want once", f.dir1, n)
	}
	f.sendKeys(t, "Enter")

	outcome := f.awaitOutcome(t)
	if !outcome.Chosen || outcome.Err != "" {
		t.Fatalf("outcome = %+v, want the Dir pane run with no error", outcome)
	}
	if outcome.Pane != "" || outcome.Session == "" || outcome.Session == f.targetSession {
		t.Fatalf("outcome = %+v, want a new session with no pane", outcome)
	}
	if got := f.sessionDir(t, outcome.Session); got != f.dir1 {
		t.Fatalf("new session dir = %q, want %q", got, f.dir1)
	}
	waitUntil(t, "the Claude pane to start with the prompt", func() bool {
		for _, args := range claudeRunFiles(t, f.claude, "args") {
			if strings.Contains(args, "\n"+testText+"\n") {
				return true
			}
		}
		return false
	})
}

func TestPickerDirFirstRowHasNoPromptWithoutTask(t *testing.T) {
	f := newFixture(t, 0)
	f.setReply(t, fmt.Sprintf("pane d2\npane d1\njump %s\ntask: no\n", f.targetPane))
	f.dir = f.dir1
	f.startPicker(t)

	f.waitForFrame(t, "> pane "+f.dir1)
	if frame := f.frame(t); !hasLine(frame, "> pane "+f.dir1) {
		t.Fatalf("first row is not the bare Dir pane row:\n%s", frame)
	}
}

func TestPickerNewReplyShowsOnlyRootPaneRow(t *testing.T) {
	f := newFixture(t, 0)
	f.setReply(t, "new example-project default\ntask: yes\n")
	f.startPicker(t)
	rootRow := "> pane " + f.root + " (prompt)"
	f.waitForFrame(t, rootRow)

	var rows []string
	for line := range strings.SplitSeq(f.frame(t), "\n") {
		if line = strings.TrimRight(line, " "); line != "" {
			rows = append(rows, line)
		}
	}
	if want := []string{rootRow}; !slices.Equal(rows, want) {
		t.Fatalf("rows = %q, want %q", rows, want)
	}

	f.sendKeys(t, "Enter")
	outcome := f.awaitOutcome(t)
	if !outcome.Chosen || outcome.Err != "" || outcome.Pane != "" || outcome.Session == "" {
		t.Fatalf("outcome = %+v, want the root pane run with no error and a new session", outcome)
	}
	if got := f.sessionDir(t, outcome.Session); got != f.root {
		t.Fatalf("new session dir = %q, want %q", got, f.root)
	}
	entries, err := os.ReadDir(f.root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"dir-01", "dir-02"}; !slices.Equal(names, want) {
		t.Fatalf("root entries = %q, want %q", names, want)
	}
	waitUntil(t, "the Claude pane to start with the prompt", func() bool {
		for _, args := range claudeRunFiles(t, f.claude, "args") {
			if strings.Contains(args, "\n"+testText+"\n") {
				return true
			}
		}
		return false
	})
}

// chooseActions makes two new directories under the fixture's root and returns a pane action for each, the first
// action first.
func chooseActions(t *testing.T, f *fixture) []open.Action {
	t.Helper()
	dirs := []string{filepath.Join(f.root, "choose-a"), filepath.Join(f.root, "choose-b")}
	for _, dir := range dirs {
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	return []open.Action{{Kind: open.ActionPane, Dir: dirs[0]}, {Kind: open.ActionPane, Dir: dirs[1]}}
}

func TestChooseShowsActionsAndRunsNothingBeforeInput(t *testing.T) {
	f := newFixture(t, 0)
	f.actions = chooseActions(t, f)
	f.startPicker(t)
	baseline := f.state(t)
	f.waitForFrame(t, "> pane "+f.actions[0].Dir)
	if frame := f.frame(t); !hasLine(frame, "  pane "+f.actions[1].Dir) {
		t.Fatalf("the second action is not shown:\n%s", frame)
	}
	if n := claudeRuns(t, f.claude); n != 0 {
		t.Fatalf("fake claude ran %d times before input, want none", n)
	}
	f.assertNoOutcome(t)
	if got := f.state(t); got != baseline {
		t.Fatalf("state changed before Enter on a shown choice:\nbefore:\n%s\nafter:\n%s", baseline, got)
	}
}

func TestChooseDownAndEnterRunsSecondAction(t *testing.T) {
	f := newFixture(t, 0)
	f.actions = chooseActions(t, f)
	// The pane action's fake claude run holds, so its session outlives the check.
	writeFile(t, f.hold, "")
	f.startPicker(t)
	f.waitForFrame(t, "> pane "+f.actions[0].Dir)
	f.sendKeys(t, "j")
	f.waitForFrame(t, "> pane "+f.actions[1].Dir)
	f.sendKeys(t, "Enter")

	outcome := f.awaitOutcome(t)
	if !outcome.Chosen || outcome.Err != "" {
		t.Fatalf("outcome = %+v, want the second action run with no error", outcome)
	}
	if outcome.Pane != "" || outcome.Session == "" || outcome.Session == f.targetSession {
		t.Fatalf("outcome = %+v, want a new session with no pane", outcome)
	}
	if got := f.sessionDir(t, outcome.Session); got != f.actions[1].Dir {
		t.Fatalf("new session dir = %q, want %q", got, f.actions[1].Dir)
	}
}

func TestChooseEscRunsNothing(t *testing.T) {
	f := newFixture(t, 0)
	f.actions = chooseActions(t, f)
	f.startPicker(t)
	baseline := f.state(t)
	f.waitForFrame(t, "> pane "+f.actions[0].Dir)
	f.sendKeys(t, "Escape")

	outcome := f.awaitOutcome(t)
	if outcome.Chosen || outcome.Err != "" || outcome.Session != "" || outcome.Pane != "" {
		t.Fatalf("outcome = %+v, want no choice, no error, and no target", outcome)
	}
	if n := claudeRuns(t, f.claude); n != 0 {
		t.Fatalf("fake claude ran %d times, want none", n)
	}
	if got := f.state(t); got != baseline {
		t.Fatalf("Esc created state:\nbefore:\n%s\nafter:\n%s", baseline, got)
	}
}

func TestChooseWithoutActionsErrors(t *testing.T) {
	var out bytes.Buffer
	_, chosen, err := Choose(t.Context(), Options{Output: &out}, nil)
	if err == nil || chosen {
		t.Fatalf("Choose with no actions: chosen = %v, err = %v; want an error and no choice", chosen, err)
	}
	if out.Len() != 0 {
		t.Fatalf("Choose with no actions drew %q, want nothing", out.String())
	}
}

func TestChooseResumeRowsShowAndEnterRunsResume(t *testing.T) {
	f := newFixture(t, 0)
	dir := filepath.Join(f.root, "resume-dir")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	f.actions = []open.Action{
		{Kind: open.ActionResume, Dir: dir, Session: "example-claude-session", Note: "example summary"},
		{Kind: open.ActionResume, Dir: dir, Session: "example-claude-session"},
		{Kind: open.ActionContinue, Dir: dir},
		{Kind: open.ActionFresh, Dir: dir},
	}
	// The resume run holds, so its session outlives the check.
	writeFile(t, f.hold, "")
	f.startPicker(t)
	f.waitForFrame(t, "> resume example-claude-session: example summary")
	var rows []string
	for line := range strings.SplitSeq(f.frame(t), "\n") {
		if line = strings.TrimRight(line, " "); line != "" {
			rows = append(rows, line)
		}
	}
	want := []string{
		"> resume example-claude-session: example summary",
		"  resume example-claude-session",
		"  continue latest in " + dir,
		"  start fresh in " + dir,
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("rows = %q, want %q", rows, want)
	}
	if n := claudeRuns(t, f.claude); n != 0 {
		t.Fatalf("fake claude ran %d times before input, want none", n)
	}
	f.sendKeys(t, "Enter")

	outcome := f.awaitOutcome(t)
	if !outcome.Chosen || outcome.Err != "" || outcome.Pane != "" || outcome.Session == "" {
		t.Fatalf("outcome = %+v, want the resume run with no error and a new session", outcome)
	}
	if got := f.sessionDir(t, outcome.Session); got != dir {
		t.Fatalf("new session dir = %q, want %q", got, dir)
	}
	// The Claude pane starts after tmux creates the session, so its cwd is waited for; the fake writes cwd after args.
	waitUntil(t, "the Claude pane to start with its cwd", func() bool {
		return slices.ContainsFunc(claudeRunFiles(t, f.claude, "cwd"), func(cwd string) bool { return cwd != "" })
	})
	if n := claudeRuns(t, f.claude); n != 1 {
		t.Fatalf("fake claude ran %d times, want exactly one", n)
	}
	args := claudeRunFiles(t, f.claude, "args")
	if want := []string{"--resume\nexample-claude-session\n"}; !slices.Equal(args, want) {
		t.Fatalf("claude args = %q, want %q", args, want)
	}
}

func TestChooseArchiveResumeOffersKeptSessionAndRunsIt(t *testing.T) {
	f := newFixture(t, 0)
	dir := filepath.Join(f.root, "archive-dir")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	transcripts := t.TempDir()
	kept := filepath.Join(transcripts, "kept.jsonl")
	writeFile(t, kept, "")
	archivePath := filepath.Join(t.TempDir(), "archive.jsonl")
	// The gone record is newer, so ResumeActions reads it first; its transcript file is never created.
	records := []archive.Record{
		{
			Dir:           dir,
			Session:       "example-session",
			ClaudeSession: "example-claude-session-kept",
			Transcript:    kept,
			Summary:       "example summary",
			Closed:        1,
		},
		{
			Dir:           dir,
			Session:       "example-session",
			ClaudeSession: "example-claude-session-gone",
			Transcript:    filepath.Join(transcripts, "gone.jsonl"),
			Summary:       "example gone summary",
			Closed:        2,
		},
	}
	for _, record := range records {
		if err := archive.Append(archivePath, record); err != nil {
			t.Fatalf("append archive record: %v", err)
		}
	}
	actions, err := open.ResumeActions(open.Config{Archive: archivePath}, dir)
	if err != nil {
		t.Fatalf("ResumeActions: %v", err)
	}
	f.actions = actions
	// The resume run holds, so its session outlives the check.
	writeFile(t, f.hold, "")
	f.startPicker(t)
	f.waitForFrame(t, "> resume example-claude-session-kept: example summary")
	var rows []string
	for line := range strings.SplitSeq(f.frame(t), "\n") {
		if line = strings.TrimRight(line, " "); line != "" {
			rows = append(rows, line)
		}
	}
	want := []string{
		"> resume example-claude-session-kept: example summary",
		"  start fresh in " + dir,
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("rows = %q, want %q", rows, want)
	}
	if n := claudeRuns(t, f.claude); n != 0 {
		t.Fatalf("fake claude ran %d times before input, want none", n)
	}
	f.sendKeys(t, "Enter")

	outcome := f.awaitOutcome(t)
	if !outcome.Chosen || outcome.Err != "" || outcome.Pane != "" || outcome.Session == "" {
		t.Fatalf("outcome = %+v, want the resume run with no error and a new session", outcome)
	}
	if got := f.sessionDir(t, outcome.Session); got != dir {
		t.Fatalf("new session dir = %q, want %q", got, dir)
	}
	// The Claude pane starts after tmux creates the session, so its cwd is waited for; the fake writes cwd after args.
	waitUntil(t, "the Claude pane to start with its cwd", func() bool {
		return slices.ContainsFunc(claudeRunFiles(t, f.claude, "cwd"), func(cwd string) bool { return cwd != "" })
	})
	if n := claudeRuns(t, f.claude); n != 1 {
		t.Fatalf("fake claude ran %d times, want exactly one", n)
	}
	args := claudeRunFiles(t, f.claude, "args")
	if want := []string{"--resume\nexample-claude-session-kept\n"}; !slices.Equal(args, want) {
		t.Fatalf("claude args = %q, want %q", args, want)
	}
}

// installFake copies testdata/<src>.sh into a fresh directory as name and returns the copy's path. A non-empty
// reply is printed on each run; a non-zero status is the run's exit status.
func installFake(t *testing.T, src, name, reply string, status int) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "open", "testdata", src+".sh"))
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, body, 0o755); err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
	if reply != "" {
		writeFile(t, filepath.Join(dir, "reply"), reply)
	}
	if status != 0 {
		writeFile(t, filepath.Join(dir, "exit"), strconv.Itoa(status))
	}
	return bin
}

// zoxideLine is one line of `zoxide query -l -s` output for path.
func zoxideLine(path string) string {
	return "  9.0 " + path + "\n"
}

// waitUntil polls cond until it holds, failing the test when waitLimit passes.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// mustRun runs tmux with args against the test's server and returns stdout.
func mustRun(t *testing.T, client *tmuxctl.Client, args ...string) string {
	t.Helper()
	out, err := client.Run(t.Context(), args...)
	if err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// shellQuote quotes s for the shell that runs a pane command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// trimLine removes the newline tmux prints after a value.
func trimLine(s string) string {
	return strings.TrimSuffix(s, "\n")
}
