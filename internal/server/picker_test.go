package server_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/archive"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// pickerLabel is the label set on the synthetic pane a jump row names.
const pickerLabel = "example-label-a"

// pickerRow is the content of one row of a Picker.Ask answer other than its token.
type pickerRow struct {
	Kind   string
	Dir    string
	Pane   string
	Label  string
	Note   string
	Prompt bool
}

// rowOf returns the content of row other than its token.
func rowOf(row *orreryv1.PickerRow) pickerRow {
	return pickerRow{
		Kind:   row.GetKind(),
		Dir:    row.GetDir(),
		Pane:   row.GetPane(),
		Label:  row.GetLabel(),
		Note:   row.GetNote(),
		Prompt: row.GetPrompt(),
	}
}

// installPickerClaude installs a fake claude that prints reply, or exits 1 when fail is set, and returns its path
// and the runs file. Each run appends a line to the runs file, so a test can see whether claude ran.
func installPickerClaude(t *testing.T, reply string, fail bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
D=$(dirname "$0")
echo run >> "$D/runs"
cat > /dev/null
if [ -e "$D/fail" ]; then
	echo "example claude failure" >&2
	exit 1
fi
cat "$D/reply"
`
	writePickerFile(t, bin, script)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod fake claude: %v", err)
	}
	writePickerFile(t, filepath.Join(dir, "reply"), reply)
	if fail {
		writePickerFile(t, filepath.Join(dir, "fail"), "")
	}
	return bin, filepath.Join(dir, "runs")
}

// installPickerZoxide installs a fake zoxide that prints listing for every query and returns its path.
func installPickerZoxide(t *testing.T, listing string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "zoxide")
	writePickerFile(t, bin, "#!/bin/sh\ncat \"$(dirname \"$0\")/listing\"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod fake zoxide: %v", err)
	}
	writePickerFile(t, filepath.Join(dir, "listing"), listing)
	return bin
}

// writePickerFile writes content to path.
func writePickerFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// scoredLine is one line of zoxide output for path.
func scoredLine(path string) string {
	return "  9.0 " + path + "\n"
}

// pickerConfig returns the open configuration a picker test asks with.
func pickerConfig(tmux *tmuxctl.Client, zoxide, claude, root string) open.Config {
	return open.Config{Tmux: tmux, Zoxide: zoxide, Claude: claude, Root: root, Home: root}
}

// pickerHandler builds the guarded handler that admits only allowedLogin, routes to tmux, and asks picker.
func pickerHandler(t *testing.T, tmux *tmuxctl.Client, picker func() (open.Config, error)) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins: []string{allowedLogin},
		Tmux:   tmux,
		Push:   newPushStore(t),
		Picker: picker,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// startPickerPane starts a session named name in dir and sets the options that make its pane an eligible
// default-context pane labeled label. It returns the pane ID.
func startPickerPane(t *testing.T, tmux *tmuxctl.Client, name, dir string) string {
	t.Helper()
	args := []string{"-f", noConfig, "new-session", "-d", "-s", name, "-c", dir, longCommand}
	if _, err := tmux.Run(t.Context(), args...); err != nil {
		t.Fatalf("new-session %s: %v", name, err)
	}
	id := paneID(t, tmux, name)
	options := map[string]string{
		protocol.OptionState:   "waiting",
		protocol.OptionLabel:   pickerLabel,
		protocol.OptionContext: "",
	}
	for option, value := range options {
		if _, err := tmux.Run(t.Context(), "set-option", "-p", "-t", id, option, value); err != nil {
			t.Fatalf("set-option %s: %v", option, err)
		}
	}
	return id
}

// askRows asks text through Picker.Ask on handler and returns the rows of the answer. It fails the test on error.
func askRows(t *testing.T, handler http.Handler, text string) []*orreryv1.PickerRow {
	t.Helper()
	answer, err := pickerClient(t, handler).Ask(t.Context(), connect.NewRequest(&orreryv1.AskRequest{Text: text}))
	if err != nil {
		t.Fatalf("Picker.Ask %q: %v", text, err)
	}
	return answer.Msg.GetRows()
}

// runToken runs token through Picker.Run on handler and returns the pane of the answer and the error.
func runToken(t *testing.T, handler http.Handler, token string) (string, error) {
	t.Helper()
	answer, err := pickerClient(t, handler).Run(t.Context(), connect.NewRequest(&orreryv1.RunRequest{Token: token}))
	if err != nil {
		return "", err
	}
	return answer.Msg.GetPane(), nil
}

// runPane runs token through Picker.Run on handler and returns the pane of the answer. It fails the test on error.
func runPane(t *testing.T, handler http.Handler, token string) string {
	t.Helper()
	pane, err := runToken(t, handler, token)
	if err != nil {
		t.Fatalf("Picker.Run: %v", err)
	}
	return pane
}

// isPickerToken reports whether token is 128 random bits as hex.
func isPickerToken(token string) bool {
	raw, err := hex.DecodeString(token)
	return err == nil && len(raw) == 16
}

// assertRow fails the test unless row's token is 128 random bits as hex and the rest of row equals want.
func assertRow(t *testing.T, row *orreryv1.PickerRow, want pickerRow) {
	t.Helper()
	if !isPickerToken(row.GetToken()) {
		t.Errorf("token %q is not 128 random bits as hex", row.GetToken())
	}
	if got := rowOf(row); got != want {
		t.Errorf("row = %+v, want %+v", got, want)
	}
}

// claudeRan reports whether the fake claude recorded any run.
func claudeRan(t *testing.T, runs string) bool {
	t.Helper()
	_, err := os.Stat(runs)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatalf("stat %s: %v", runs, err)
	}
	return true
}

func TestPickerAskMultiWordRowsInRankOrderWithDistinctTokens(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	pane := startPickerPane(t, tmux, "example-session-a", paneDir)
	claude, _ := installPickerClaude(t, "jump "+pane+"\npane d1\ntask: yes\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(filepath.Join(root, "dir-01"))), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	assertRow(t, rows[0], pickerRow{Kind: "jump", Dir: paneDir, Pane: pane, Label: pickerLabel})
	assertRow(t, rows[1], pickerRow{Kind: "pane", Dir: filepath.Join(root, "dir-01"), Prompt: true})
	assertRow(t, rows[2], pickerRow{Kind: "pane", Dir: root, Prompt: true})
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.GetToken()] {
			t.Errorf("token %q repeats across rows", row.GetToken())
		}
		seen[row.GetToken()] = true
	}
}

func TestPickerAskOneWordFastMatchWithoutSessionGivesResumeRowsWithNotes(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "example-proj")
	transcript := filepath.Join(t.TempDir(), "a.jsonl")
	writePickerFile(t, transcript, "{}\n")
	archivePath := filepath.Join(t.TempDir(), "archive.jsonl")
	if err := archive.Append(archivePath, archive.Record{
		Dir: proj, ClaudeSession: "sess-a", Transcript: transcript, Summary: "summary a", Closed: 100,
	}); err != nil {
		t.Fatalf("append archive record: %v", err)
	}
	claude, _ := installPickerClaude(t, "", false)
	cfg := pickerConfig(newTmuxClient(t), installPickerZoxide(t, scoredLine(proj)), claude, root)
	cfg.Archive = archivePath
	handler := pickerHandler(t, cfg.Tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example-proj")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	assertRow(t, rows[0], pickerRow{Kind: "resume", Dir: proj, Note: "summary a"})
	assertRow(t, rows[1], pickerRow{Kind: "fresh", Dir: proj})
}

func TestPickerAskPickerErrorAnswers502WithReason(t *testing.T) {
	tmux := newTmuxClient(t)
	handler := pickerHandler(t, tmux, func() (open.Config, error) {
		return open.Config{}, errors.New("example picker failure")
	})

	_, err := pickerClient(t, handler).Ask(t.Context(), connect.NewRequest(&orreryv1.AskRequest{Text: "example text"}))
	assertConnectError(t, err, connect.CodeUnavailable, "example picker failure")
}

func TestPickerAskFailedClaudeAnswers502WithReason(t *testing.T) {
	tmux := newTmuxClient(t)
	root := t.TempDir()
	claude, runs := installPickerClaude(t, "", true)
	cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	_, err := pickerClient(t, handler).Ask(t.Context(), connect.NewRequest(&orreryv1.AskRequest{Text: "example text"}))
	assertConnectError(t, err, connect.CodeUnavailable, "intent: claude run: exit status 1: example claude failure")
	if !claudeRan(t, runs) {
		t.Errorf("claude did not run; the failure must come from its run")
	}
}

func TestPickerAskBlankOrMalformedTextAnswers400AndRunsNothing(t *testing.T) {
	texts := map[string]string{
		"empty text": "",
		"blank text": " \t\n ",
	}
	for name, text := range texts {
		t.Run(name, func(t *testing.T) {
			tmux := newTmuxClient(t)
			claude, runs := installPickerClaude(t, "pane d1\n", false)
			asks := 0
			handler := pickerHandler(t, tmux, func() (open.Config, error) {
				asks++
				return pickerConfig(tmux, installPickerZoxide(t, ""), claude, t.TempDir()), nil
			})

			_, err := pickerClient(t, handler).Ask(t.Context(), connect.NewRequest(&orreryv1.AskRequest{Text: text}))
			assertConnectError(t, err, connect.CodeInvalidArgument, "bad request")
			if asks != 0 {
				t.Errorf("Picker was called %d times, want 0", asks)
			}
			if claudeRan(t, runs) {
				t.Errorf("claude ran for text that must be refused")
			}
		})
	}
}

func TestPickerAskWithoutPickerAnswers404(t *testing.T) {
	tmux := newTmuxClient(t)
	handler := pickerHandler(t, tmux, nil)

	_, err := pickerClient(t, handler).Ask(t.Context(), connect.NewRequest(&orreryv1.AskRequest{Text: "example text"}))
	assertConnectError(t, err, connect.CodeNotFound, "404 page not found")
}

// pickerHandlerAt builds the guarded handler that admits only allowedLogin, routes to tmux, asks picker, and reads
// its clock from now.
func pickerHandlerAt(
	t *testing.T,
	tmux *tmuxctl.Client,
	picker func() (open.Config, error),
	now func() time.Time,
) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins: []string{allowedLogin},
		Tmux:   tmux,
		Push:   newPushStore(t),
		Picker: picker,
		Now:    now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// serverPaneIDs returns the IDs of every pane on the test server.
func serverPaneIDs(t *testing.T, tmux *tmuxctl.Client) []string {
	t.Helper()
	out, err := tmux.Run(t.Context(), "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes -a: %v", err)
	}
	return strings.Fields(out)
}

// sessionPaneIDs returns the IDs of every pane in session, in every window.
func sessionPaneIDs(t *testing.T, tmux *tmuxctl.Client, session string) []string {
	t.Helper()
	out, err := tmux.Run(t.Context(), "list-panes", "-s", "-t", session, "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes -s -t %s: %v", session, err)
	}
	return strings.Fields(out)
}

// paneSession returns the name of the session pane is in.
func paneSession(t *testing.T, tmux *tmuxctl.Client, pane string) string {
	t.Helper()
	out, err := tmux.Run(t.Context(), "display-message", "-p", "-t", pane, "#{session_name}")
	if err != nil {
		t.Fatalf("session of pane %s: %v", pane, err)
	}
	return strings.TrimSpace(out)
}

// pickerDirSession is the session startPickerDirSession starts.
const pickerDirSession = "example-session-a"

// startPickerDirSession starts pickerDirSession in dir with its @orrery_dir set to dir, the mark createSession
// sets, so the open package finds the session for dir.
func startPickerDirSession(t *testing.T, tmux *tmuxctl.Client, dir string) {
	t.Helper()
	startPickerPane(t, tmux, pickerDirSession, dir)
	target := "=" + pickerDirSession + ":"
	if _, err := tmux.Run(t.Context(), "set-option", "-t", target, protocol.OptionDir, dir); err != nil {
		t.Fatalf("set %s on %s: %v", protocol.OptionDir, pickerDirSession, err)
	}
}

// claudeRuns returns how many times the fake claude has run, one line per run.
func claudeRuns(t *testing.T, runs string) int {
	t.Helper()
	data, err := os.ReadFile(runs)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read %s: %v", runs, err)
	}
	return strings.Count(string(data), "\n")
}

// awaitClaudeRuns waits until the fake claude has run want times, and fails at the screenWait deadline
// naming the count it reached.
func awaitClaudeRuns(t *testing.T, runs string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	for {
		got := claudeRuns(t, runs)
		if got == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("claude ran %d times, want %d", got, want)
		case <-ticker.C:
		}
	}
}

func TestPickerRunPaneRowOpensWindowInItsSessionAndAnswersThePane(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	startPickerDirSession(t, tmux, paneDir)
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	if len(rows) == 0 || rows[0].GetKind() != "pane" || rows[0].GetDir() != paneDir {
		t.Fatalf("rows = %d, want a pane row for %s first", len(rows), paneDir)
	}
	before := serverPaneIDs(t, tmux)
	pane := runPane(t, handler, rows[0].GetToken())
	awaitClaudeRuns(t, runs, 2)
	after := serverPaneIDs(t, tmux)
	if !slices.Contains(after, pane) {
		t.Fatalf("answered pane %s is not on the server: %v", pane, after)
	}
	if got := paneSession(t, tmux, pane); got != pickerDirSession {
		t.Errorf("answered pane %s is in session %q, want %q", pane, got, pickerDirSession)
	}
	if len(after) != len(before)+1 {
		t.Errorf("session has %d panes, want %d after one window", len(after), len(before)+1)
	}
}

func TestPickerRunRowForFolderWithoutSessionCreatesOneAndAnswersItsPane(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	startPickerPane(t, tmux, "example-session-b", t.TempDir())
	folder := filepath.Join(t.TempDir(), "example-dir-c")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", folder, err)
	}
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(folder)), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	if len(rows) == 0 || rows[0].GetKind() != "pane" || rows[0].GetDir() != folder {
		t.Fatalf("rows = %d, want a pane row for %s first", len(rows), folder)
	}
	pane := runPane(t, handler, rows[0].GetToken())
	awaitClaudeRuns(t, runs, 2)
	if got := paneSession(t, tmux, pane); got != "example-dir-c" {
		t.Errorf("answered pane %s is in session %q, want the new session example-dir-c", pane, got)
	}
}

// failPickerLookupOnce puts a tmux first on PATH that fails the first pane lookup of session, and passes every other
// call to the real tmux. tmuxctl resolves tmux from PATH at each call, so the failure reaches the picker run.
func failPickerLookupOnce(t *testing.T, session string) {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("find tmux: %v", err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "failed")
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
*"display-message -p -t =%s: #{pane_id}")
	if [ ! -e '%s' ]; then
		: > '%s'
		echo "example lookup failure" >&2
		exit 1
	fi
	;;
esac
exec '%s' "$@"
`, session, marker, marker, tmuxPath)
	bin := filepath.Join(dir, "tmux")
	writePickerFile(t, bin, script)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPickerRunRetryAfterFailedLookupAnswersCreatedPaneWithoutRunningAgain(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	startPickerPane(t, tmux, "example-session-b", t.TempDir())
	folder := filepath.Join(t.TempDir(), "example-dir-c")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", folder, err)
	}
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(folder)), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	if len(rows) == 0 || rows[0].GetKind() != "pane" || rows[0].GetDir() != folder {
		t.Fatalf("rows = %d, want a pane row for %s first", len(rows), folder)
	}
	failPickerLookupOnce(t, "example-dir-c")
	_, err := runToken(t, handler, rows[0].GetToken())
	assertConnectError(
		t,
		err,
		connect.CodeUnavailable,
		`server: session "example-dir-c" pane: tmux: exit status 1: example lookup failure`,
	)
	created := sessionPaneIDs(t, tmux, "example-dir-c")
	if len(created) != 1 {
		t.Fatalf("after the failed lookup session example-dir-c has panes %v, want the one the action created", created)
	}
	pane := runPane(t, handler, rows[0].GetToken())
	awaitClaudeRuns(t, runs, 2)
	if pane != created[0] {
		t.Errorf("retry answered pane %s, want the pane the first run created, %s", pane, created[0])
	}
	if after := sessionPaneIDs(t, tmux, "example-dir-c"); !slices.Equal(after, created) {
		t.Errorf("retry left session example-dir-c with panes %v, want %v: the action ran again", after, created)
	}
}

func TestPickerRunJumpRowAnswersItsPaneAndOpensNoWindow(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	pane := startPickerPane(t, tmux, "example-session-a", t.TempDir())
	claude, _ := installPickerClaude(t, "jump "+pane+"\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	if len(rows) == 0 || rows[0].GetKind() != "jump" || rows[0].GetPane() != pane {
		t.Fatalf("rows = %d, want a jump row to %s first", len(rows), pane)
	}
	before := serverPaneIDs(t, tmux)
	if got := runPane(t, handler, rows[0].GetToken()); got != pane {
		t.Errorf("pane = %q, want the jumped-to pane %q", got, pane)
	}
	if after := serverPaneIDs(t, tmux); len(after) != len(before) {
		t.Errorf("jump changed the pane count from %d to %d", len(before), len(after))
	}
}

func TestPickerRunRepeatAnswersSamePaneAndOpensNoSecondWindow(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	startPickerDirSession(t, tmux, paneDir)
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	token := rows[0].GetToken()
	first := runPane(t, handler, token)
	awaitClaudeRuns(t, runs, 2)
	afterFirst := serverPaneIDs(t, tmux)
	second := runPane(t, handler, token)
	afterSecond := serverPaneIDs(t, tmux)
	if second != first {
		t.Errorf("repeat pane = %q, want %q", second, first)
	}
	if len(afterSecond) != len(afterFirst) {
		t.Errorf("repeat changed the pane count from %d to %d", len(afterFirst), len(afterSecond))
	}
}

func TestPickerRunConcurrentRunsOfOneTokenOpenOneWindow(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	startPickerDirSession(t, tmux, paneDir)
	claude, runsFile := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	token := rows[0].GetToken()
	before := serverPaneIDs(t, tmux)
	client := pickerClient(t, handler)
	const runs = 2
	panes := make([]string, runs)
	errs := make([]error, runs)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			answer, err := client.Run(t.Context(), connect.NewRequest(&orreryv1.RunRequest{Token: token}))
			if err != nil {
				errs[i] = err
				return
			}
			panes[i] = answer.Msg.GetPane()
		}()
	}
	close(start)
	wg.Wait()
	awaitClaudeRuns(t, runsFile, 2)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Picker.Run %d: %v", i, err)
		}
	}
	if panes[0] != panes[1] {
		t.Errorf("concurrent runs answered %q and %q, want one pane", panes[0], panes[1])
	}
	if after := serverPaneIDs(t, tmux); len(after) != len(before)+1 {
		t.Errorf("concurrent runs left %d panes, want %d: the row ran more than once", len(after), len(before)+1)
	}
}

// unissuedToken is 128 bits as hex, the shape of a token no ask issued.
const unissuedToken = "00000000000000000000000000000000"

func TestPickerRunUnknownTokenAnswers404(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	startPickerDirSession(t, tmux, paneDir)
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	runPane(t, handler, rows[0].GetToken())
	awaitClaudeRuns(t, runs, 2)
	_, err := runToken(t, handler, unissuedToken)
	assertConnectError(t, err, connect.CodeNotFound, "unknown token")
}

func TestPickerRunTokenFromAnotherHandlerAnswers404(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	startPickerDirSession(t, tmux, paneDir)
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, root)
	asked := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })
	restarted := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, asked, "example text")
	_, err := runToken(t, restarted, rows[0].GetToken())
	assertConnectError(t, err, connect.CodeNotFound, "unknown token")
	runPane(t, asked, rows[0].GetToken())
	awaitClaudeRuns(t, runs, 2)
}

func TestPickerRunExpiredTokenAnswers410AndRunsNothing(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	startPickerDirSession(t, tmux, paneDir)
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, root)
	issued := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	clock := issued
	picker := func() (open.Config, error) { return cfg, nil }
	handler := pickerHandlerAt(t, tmux, picker, func() time.Time { return clock })

	live := askRows(t, handler, "example text")
	expired := askRows(t, handler, "example text")

	clock = issued.Add(5*time.Minute - time.Second)
	runPane(t, handler, live[0].GetToken())
	awaitClaudeRuns(t, runs, 3)

	before := serverPaneIDs(t, tmux)
	clock = issued.Add(5 * time.Minute)
	_, err := runToken(t, handler, expired[0].GetToken())
	assertConnectError(t, err, connect.CodeFailedPrecondition, "expired token")
	if after := serverPaneIDs(t, tmux); len(after) != len(before) {
		t.Errorf("expired run changed the pane count from %d to %d", len(before), len(after))
	}
}

// noServerMessage is the message of a run against the tmux server named name while no server runs on its socket.
func noServerMessage(name string) string {
	return "server: list panes: tmux: exit status 1: error connecting to " + tmuxtest.SocketPath(name) +
		" (No such file or directory)"
}

func TestPickerRunWithoutTmuxServerAnswers409AndStartsNothing(t *testing.T) {
	name, tmux := newNamedTmuxClient(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	asked := claudeRuns(t, runs)
	_, err := runToken(t, handler, rows[0].GetToken())
	assertConnectError(t, err, connect.CodeFailedPrecondition, noServerMessage(name))
	if got := claudeRuns(t, runs); got != asked {
		t.Errorf("claude ran %d times for a run with no tmux server, want none", got-asked)
	}
	if _, err := tmux.ListPanes(t.Context()); err == nil {
		t.Errorf("a tmux server is running after a run that must start nothing")
	}
}

func TestPickerRunRootRowOfNewWordOpensNoFolder(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	startPickerPane(t, tmux, "example-session-a", t.TempDir())
	claude, runs := installPickerClaude(t, "pane d1\ntask: yes\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(t.TempDir())), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "new foo")
	var rootRow *orreryv1.PickerRow
	for _, row := range rows {
		if row.GetKind() == "pane" && row.GetDir() == root {
			rootRow = row
		}
	}
	if rootRow == nil {
		t.Fatalf("rows = %d, want a pane row for the root %s", len(rows), root)
	}
	runPane(t, handler, rootRow.GetToken())
	awaitClaudeRuns(t, runs, 2)
	if _, err := os.Stat(filepath.Join(root, "foo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat foo in root: %v, want no such folder", err)
	}
}

func TestPickerRunFailedActionAnswers502WithReasonAndRecordsNothing(t *testing.T) {
	tmux := newTmuxServer(t)
	pane := startPickerPane(t, tmux, "example-session-a", t.TempDir())
	startPickerPane(t, tmux, "example-session-b", t.TempDir())
	claude, _ := installPickerClaude(t, "jump "+pane+"\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, t.TempDir())
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	if len(rows) == 0 || rows[0].GetKind() != "jump" {
		t.Fatalf("rows = %d, want a jump row first", len(rows))
	}
	if _, err := tmux.Run(t.Context(), "kill-pane", "-t", pane); err != nil {
		t.Fatalf("kill-pane %s: %v", pane, err)
	}
	for range 2 {
		_, err := runToken(t, handler, rows[0].GetToken())
		assertConnectError(t, err, connect.CodeUnavailable, `server: picker run: intent: pane "%0" does not exist`)
	}
}

func TestPickerRunBadBodyAnswers400(t *testing.T) {
	tokens := map[string]string{
		"missing token": "",
		"blank token":   " \t\n ",
	}
	handler := pickerHandler(t, newTmuxClient(t), nil)
	for name, token := range tokens {
		t.Run(name, func(t *testing.T) {
			_, err := runToken(t, handler, token)
			assertConnectError(t, err, connect.CodeInvalidArgument, "bad request")
		})
	}
}
