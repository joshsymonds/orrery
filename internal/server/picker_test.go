package server_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// pickerAskPath is the route POST /v1/picker/ask answers.
const pickerAskPath = "/v1/picker/ask"

// pickerLabel is the label set on the synthetic pane a jump row names.
const pickerLabel = "example-label-a"

// wireRow is one row of a 200 answer from POST /v1/picker/ask.
type wireRow struct {
	Token  string `json:"token"`
	Kind   string `json:"kind"`
	Dir    string `json:"dir"`
	Pane   string `json:"pane"`
	Label  string `json:"label"`
	Note   string `json:"note"`
	Prompt bool   `json:"prompt"`
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
func startPickerPane(t *testing.T, tmux *tmuxctl.Client, name, dir, label string) string {
	t.Helper()
	args := []string{"-f", noConfig, "new-session", "-d", "-s", name, "-c", dir, longCommand}
	if _, err := tmux.Run(t.Context(), args...); err != nil {
		t.Fatalf("new-session %s: %v", name, err)
	}
	id := paneID(t, tmux, name)
	options := map[string]string{
		protocol.OptionState:   "waiting",
		protocol.OptionLabel:   label,
		protocol.OptionContext: "",
	}
	for option, value := range options {
		if _, err := tmux.Run(t.Context(), "set-option", "-p", "-t", id, option, value); err != nil {
			t.Fatalf("set-option %s: %v", option, err)
		}
	}
	return id
}

// askRows decodes the rows of a 200 answer from POST /v1/picker/ask.
func askRows(t *testing.T, rec *httptest.ResponseRecorder) []wireRow {
	t.Helper()
	assertStatus(t, rec, http.StatusOK)
	assertJSON(t, rec)
	var body struct {
		Rows []wireRow `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body.Rows
}

// isPickerToken reports whether token is 128 random bits as hex.
func isPickerToken(token string) bool {
	raw, err := hex.DecodeString(token)
	return err == nil && len(raw) == 16
}

// assertRow fails the test unless got equals want, once got's token is checked for shape and set aside.
func assertRow(t *testing.T, got, want wireRow) {
	t.Helper()
	if !isPickerToken(got.Token) {
		t.Errorf("token %q is not 128 random bits as hex", got.Token)
	}
	got.Token = ""
	if got != want {
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
	pane := startPickerPane(t, tmux, "example-session-a", paneDir, pickerLabel)
	claude, _ := installPickerClaude(t, "jump "+pane+"\npane d1\ntask: yes\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(filepath.Join(root, "dir-01"))), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, postJSON(t, handler, pickerAskPath, jsonBody(t, map[string]any{"text": "example text"})))
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want 3 rows", rows)
	}
	assertRow(t, rows[0], wireRow{Kind: "jump", Dir: paneDir, Pane: pane, Label: pickerLabel})
	assertRow(t, rows[1], wireRow{Kind: "pane", Dir: filepath.Join(root, "dir-01"), Prompt: true})
	assertRow(t, rows[2], wireRow{Kind: "pane", Dir: root, Prompt: true})
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.Token] {
			t.Errorf("token %q repeats across rows", row.Token)
		}
		seen[row.Token] = true
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

	rows := askRows(t, postJSON(t, handler, pickerAskPath, jsonBody(t, map[string]any{"text": "example-proj"})))
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2 rows", rows)
	}
	assertRow(t, rows[0], wireRow{Kind: "resume", Dir: proj, Note: "summary a"})
	assertRow(t, rows[1], wireRow{Kind: "fresh", Dir: proj})
}

func TestPickerAskPickerErrorAnswers502WithReason(t *testing.T) {
	tmux := newTmuxClient(t)
	handler := pickerHandler(t, tmux, func() (open.Config, error) {
		return open.Config{}, errors.New("example picker failure")
	})

	rec := postJSON(t, handler, pickerAskPath, jsonBody(t, map[string]any{"text": "example text"}))
	assertStatus(t, rec, http.StatusBadGateway)
	if got := strings.TrimSpace(rec.Body.String()); got != "example picker failure" {
		t.Errorf("body = %q, want the picker reason", got)
	}
}

func TestPickerAskFailedClaudeAnswers502WithReason(t *testing.T) {
	tmux := newTmuxClient(t)
	root := t.TempDir()
	claude, runs := installPickerClaude(t, "", true)
	cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rec := postJSON(t, handler, pickerAskPath, jsonBody(t, map[string]any{"text": "example text"}))
	assertStatus(t, rec, http.StatusBadGateway)
	if !strings.Contains(rec.Body.String(), "intent: claude run") {
		t.Errorf("body = %q, want the claude failure reason", rec.Body.String())
	}
	if !claudeRan(t, runs) {
		t.Errorf("claude did not run; the failure must come from its run")
	}
}

func TestPickerAskBlankOrMalformedTextAnswers400AndRunsNothing(t *testing.T) {
	bodies := map[string]string{
		"empty text":       `{"text": ""}`,
		"blank text":       `{"text": " \t\n "}`,
		"missing text":     `{}`,
		"non-string text":  `{"text": 3}`,
		"array body":       `[]`,
		"null body":        `null`,
		"unparseable body": `not json`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			tmux := newTmuxClient(t)
			claude, runs := installPickerClaude(t, "pane d1\n", false)
			asks := 0
			handler := pickerHandler(t, tmux, func() (open.Config, error) {
				asks++
				return pickerConfig(tmux, installPickerZoxide(t, ""), claude, t.TempDir()), nil
			})

			assertStatus(t, postJSON(t, handler, pickerAskPath, body), http.StatusBadRequest)
			if asks != 0 {
				t.Errorf("Picker was called %d times, want 0", asks)
			}
			if claudeRan(t, runs) {
				t.Errorf("claude ran for a body that must be refused")
			}
		})
	}
}

func TestPickerAskRefusedWithoutLoginClientHeaderOrJSONContentType(t *testing.T) {
	refused := map[string]func(http.Header){
		"without login":         func(h http.Header) { h.Del(server.HeaderLogin) },
		"other login":           func(h http.Header) { h.Set(server.HeaderLogin, otherLogin) },
		"without client header": func(h http.Header) { h.Del(server.HeaderClient) },
		"text content type":     func(h http.Header) { h.Set("Content-Type", textType) },
		"without content type":  func(h http.Header) { h.Del("Content-Type") },
	}
	body := jsonBody(t, map[string]any{"text": "example text"})
	for name, mutate := range refused {
		t.Run(name, func(t *testing.T) {
			tmux := newTmuxClient(t)
			claude, runs := installPickerClaude(t, "pane d1\n", false)
			cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, t.TempDir())
			handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

			header := writeHeader()
			mutate(header)
			assertStatus(t, serve(t, handler, http.MethodPost, pickerAskPath, header, body), http.StatusForbidden)
			if claudeRan(t, runs) {
				t.Errorf("claude ran for a refused ask")
			}
		})
	}

	t.Run("admitted with every header", func(t *testing.T) {
		tmux := newTmuxClient(t)
		root := t.TempDir()
		claude, _ := installPickerClaude(t, "pane d1\n", false)
		cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(filepath.Join(root, "dir-01"))), claude, root)
		handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

		assertStatus(t, postJSON(t, handler, pickerAskPath, body), http.StatusOK)
	})
}

func TestPickerAskIsNotAnsweredOnGET(t *testing.T) {
	tmux := newTmuxClient(t)
	claude, _ := installPickerClaude(t, "", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, t.TempDir())
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	header := http.Header{}
	header.Set(server.HeaderLogin, allowedLogin)
	assertStatus(t, serve(t, handler, http.MethodGet, pickerAskPath, header, ""), http.StatusMethodNotAllowed)
}

func TestPickerAskWithoutPickerAnswers404(t *testing.T) {
	tmux := newTmuxClient(t)
	handler := pickerHandler(t, tmux, nil)

	body := jsonBody(t, map[string]any{"text": "example text"})
	assertStatus(t, postJSON(t, handler, pickerAskPath, body), http.StatusNotFound)
}
