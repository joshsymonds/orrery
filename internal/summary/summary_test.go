package summary_test

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Every test tmux server starts with -f /dev/null so the user's tmux config never applies.
const (
	noConfig        = "/dev/null"
	testCommand     = "sleep 300"
	exampleContext  = "example-context"
	exampleBaseURL  = "https://patchbay.example.com/ctx/example-context"
	exampleModel    = "example-model"
	exampleInput    = "example transcript excerpt"
	validReply      = "summary: example session summary\n"
	previousSummary = "previous summary"
	previousLabel   = "previous label"
	shortTimeout    = 200 * time.Millisecond
	failureBound    = 10 * time.Second
	pollInterval    = 10 * time.Millisecond
	settleTime      = 200 * time.Millisecond
	labelMarker     = "label: <3 to 4 words"
)

// hooksOnlySettings is the --settings document of a run with no env.
const hooksOnlySettings = `{"disableAllHooks":true}`

// baseURLSettings is the --settings document of a run whose env sets ANTHROPIC_BASE_URL to baseURL.
func baseURLSettings(baseURL string) string {
	return `{"disableAllHooks":true,"env":{"ANTHROPIC_BASE_URL":"` + baseURL + `"}}`
}

// argvFor is the exact argv after the binary for model and settings.
func argvFor(model, settings string) []string {
	return []string{
		"-p", "--model", model,
		"--settings", settings,
		"--no-session-persistence",
		"--tools", "",
		"--max-turns", "1",
	}
}

// call is what one fake claude run recorded.
type call struct {
	args  []string
	env   []string
	cwd   string
	stdin string
}

// installFake copies the scripted fake into a fresh directory and returns the path of the copy, named claude.
func installFake(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "fake-claude.sh"))
	if err != nil {
		t.Fatalf("read fake claude: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, src, 0o755); err != nil {
		t.Fatalf("install fake claude: %v", err)
	}
	return bin
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// readLines splits a record file into lines, keeping empty lines such as an empty argument.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	text := readFile(t, path)
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// countRuns returns how many runs the fake beside bin has started and how many of those have not written done.
func countRuns(t *testing.T, bin string) (started, running int) {
	t.Helper()
	runs, err := filepath.Glob(filepath.Join(filepath.Dir(bin), "calls", "*"))
	if err != nil {
		t.Fatalf("glob calls: %v", err)
	}
	for _, run := range runs {
		_, err := os.Stat(filepath.Join(run, "done"))
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist):
			running++
		default:
			t.Fatalf("stat done: %v", err)
		}
	}
	return len(runs), running
}

// waitRunning polls until want fake runs are in flight, failing the test when that takes longer than failureBound.
func waitRunning(t *testing.T, bin string, want int) {
	t.Helper()
	deadline := time.After(failureBound)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		_, running := countRuns(t, bin)
		if running == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("runs in flight = %d, want %d", running, want)
		case <-ticker.C:
		}
	}
}

// waitIdle waits for s to finish every run in flight or waiting, failing the test when that takes longer than failureBound.
func waitIdle(t *testing.T, s *summary.Summarizer) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		s.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(failureBound):
		t.Fatalf("Wait did not return within %v", failureBound)
	}
}

// fakeClock is a settable clock for Config.Now.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// jsonLine marshals one transcript entry.
func jsonLine(t *testing.T, entry map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal transcript entry: %v", err)
	}
	return string(raw)
}

// exampleTranscript writes a session transcript of exchanges synthetic exchanges and returns its path.
func exampleTranscript(t *testing.T, exchanges int) string {
	t.Helper()
	lines := make([]string, 0, 2*exchanges)
	for i := 1; i <= exchanges; i++ {
		lines = append(lines,
			jsonLine(t, map[string]any{
				"type":    "user",
				"message": map[string]any{"role": "user", "content": fmt.Sprintf("Example prompt %d", i)},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant",
				"message": map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "text", "text": fmt.Sprintf("Example reply %d", i)},
				}},
			}),
		)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write example transcript: %v", err)
	}
	return path
}

// exampleRequest is a summary request for pane that reads transcript and carries last as the last message.
func exampleRequest(pane string, stop bool, transcript, last string) summary.Request {
	return summary.Request{
		PaneID:               pane,
		BaseURL:              exampleBaseURL,
		TranscriptPath:       transcript,
		LastAssistantMessage: last,
		Stop:                 stop,
	}
}

// recordedCalls returns one call per run the fake beside bin has recorded.
func recordedCalls(t *testing.T, bin string) []call {
	t.Helper()
	runs, err := filepath.Glob(filepath.Join(filepath.Dir(bin), "calls", "*"))
	if err != nil {
		t.Fatalf("glob calls: %v", err)
	}
	calls := make([]call, 0, len(runs))
	for _, run := range runs {
		calls = append(calls, call{
			args:  readLines(t, filepath.Join(run, "args")),
			env:   readLines(t, filepath.Join(run, "env")),
			cwd:   strings.TrimSuffix(readFile(t, filepath.Join(run, "cwd")), "\n"),
			stdin: readFile(t, filepath.Join(run, "stdin")),
		})
	}
	return calls
}

// onlyCall returns the single call the fake beside bin recorded.
func onlyCall(t *testing.T, bin string) call {
	t.Helper()
	calls := recordedCalls(t, bin)
	if len(calls) != 1 {
		t.Fatalf("claude runs = %d, want 1", len(calls))
	}
	return calls[0]
}

// newServer returns a client for a unique private tmux server and kills that server at cleanup.
func newServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-summary-test-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, tmuxctl.Server{Name: name}); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return client
}

func mustRun(t *testing.T, client *tmuxctl.Client, args ...string) string {
	t.Helper()
	out, err := client.Run(t.Context(), args...)
	if err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
	return out
}

// rig is one summarizer wired to a fake claude and to a private tmux server holding one pane.
type rig struct {
	bin    string
	client *tmuxctl.Client
	pane   string
	s      *summary.Summarizer
}

func newRig(t *testing.T, models map[string]string, timeout time.Duration) rig {
	t.Helper()
	return newRigWith(t, summary.Config{Models: models, Timeout: timeout})
}

// newRigWith wires a summarizer built from cfg to a fake claude and to a private tmux server holding one pane.
func newRigWith(t *testing.T, cfg summary.Config) rig {
	t.Helper()
	client := newServer(t)
	pane := newPane(t, client, "s1")
	bin := installFake(t)
	cfg.Claude = bin
	cfg.Tmux = client
	s, err := summary.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return rig{bin: bin, client: client, pane: pane, s: s}
}

// newPane adds a detached session named session to client, holding one pane, and returns the pane id.
func newPane(t *testing.T, client *tmuxctl.Client, session string) string {
	t.Helper()
	mustRun(t, client, "-f", noConfig, "new-session", "-d", "-s", session, "-c", t.TempDir(), testCommand)
	return strings.Fields(mustRun(t, client, "list-panes", "-t", session, "-F", "#{pane_id}"))[0]
}

func (r rig) job(wantLabel bool) summary.Job {
	return summary.Job{PaneID: r.pane, BaseURL: exampleBaseURL, Input: exampleInput, WantLabel: wantLabel}
}

// place writes content to name beside the fake: reply, exit or hold.
func (r rig) place(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(r.bin), name), []byte(content), 0o600); err != nil {
		t.Fatalf("place %s: %v", name, err)
	}
}

// hold makes every fake run wait until release.
func (r rig) hold(t *testing.T) {
	t.Helper()
	r.place(t, "hold", "")
}

// release removes the hold so the waiting fake runs finish.
func (r rig) release(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(filepath.Dir(r.bin), "hold")); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func (r rig) setOption(t *testing.T, name, value string) {
	t.Helper()
	if err := r.client.SetPaneOption(t.Context(), r.pane, name, value); err != nil {
		t.Fatalf("SetPaneOption(%s, %q): %v", name, value, err)
	}
}

func (r rig) option(t *testing.T, name string) string {
	t.Helper()
	value, _, err := r.client.PaneOption(t.Context(), r.pane, name)
	if err != nil {
		t.Fatalf("PaneOption(%s): %v", name, err)
	}
	return value
}

func (r rig) setPrevious(t *testing.T) {
	t.Helper()
	r.setOption(t, protocol.OptionSummary, previousSummary)
	r.setOption(t, protocol.OptionLabel, previousLabel)
}

func (r rig) assertPrevious(t *testing.T) {
	t.Helper()
	if got := r.option(t, protocol.OptionSummary); got != previousSummary {
		t.Errorf("%s = %q, want previous %q", protocol.OptionSummary, got, previousSummary)
	}
	if got := r.option(t, protocol.OptionLabel); got != previousLabel {
		t.Errorf("%s = %q, want previous %q", protocol.OptionLabel, got, previousLabel)
	}
}

func TestSummarizerFlags(t *testing.T) {
	r := newRig(t, map[string]string{exampleContext: exampleModel}, 0)
	r.place(t, "reply", validReply)
	if err := r.s.Summarize(t.Context(), r.job(false)); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	got := onlyCall(t, r.bin)
	if want := argvFor(exampleModel, baseURLSettings(exampleBaseURL)); !slices.Equal(got.args, want) {
		t.Fatalf("argv = %q, want %q", got.args, want)
	}
	if got.cwd != "/" {
		t.Fatalf("cwd = %q, want %q", got.cwd, "/")
	}
	if !strings.HasSuffix(got.stdin, exampleInput) {
		t.Fatalf("stdin = %q, want it to end with the job input", got.stdin)
	}
}

func TestSummarizerEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://patchbay.example.com/ctx/leftover")

	t.Run("base url set", func(t *testing.T) {
		r := newRig(t, nil, 0)
		r.place(t, "reply", validReply)
		if err := r.s.Summarize(t.Context(), r.job(false)); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		want := argvFor(protocol.DefaultSummaryModel, baseURLSettings(exampleBaseURL))
		if got := onlyCall(t, r.bin).args; !slices.Equal(got, want) {
			t.Fatalf("argv = %q, want %q", got, want)
		}
		assertEnv(t, r.bin, []string{"ORRERY_SUMMARIZER=1", "ANTHROPIC_BASE_URL=" + exampleBaseURL})
	})

	t.Run("base url empty", func(t *testing.T) {
		r := newRig(t, nil, 0)
		r.place(t, "reply", validReply)
		job := r.job(false)
		job.BaseURL = ""
		if err := r.s.Summarize(t.Context(), job); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		want := argvFor(protocol.DefaultSummaryModel, hooksOnlySettings)
		if got := onlyCall(t, r.bin).args; !slices.Equal(got, want) {
			t.Fatalf("argv = %q, want %q", got, want)
		}
		assertEnv(t, r.bin, []string{"ORRERY_SUMMARIZER=1"})
	})
}

// assertEnv checks the one recorded run's ANTHROPIC_BASE_URL and ORRERY_SUMMARIZER lines, ignoring order.
func assertEnv(t *testing.T, bin string, want []string) {
	t.Helper()
	got := onlyCall(t, bin).env
	if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Fatalf("env = %q, want %q", got, want)
	}
}

func TestSummarizerModel(t *testing.T) {
	models := map[string]string{exampleContext: exampleModel}

	t.Run("mapped context", func(t *testing.T) {
		r := newRig(t, models, 0)
		r.place(t, "reply", validReply)
		if err := r.s.Summarize(t.Context(), r.job(false)); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		if got := onlyCall(t, r.bin).args; !slices.Equal(got, argvFor(exampleModel, baseURLSettings(exampleBaseURL))) {
			t.Fatalf("argv = %q, want model %q", got, exampleModel)
		}
	})

	t.Run("unmapped context", func(t *testing.T) {
		r := newRig(t, models, 0)
		r.place(t, "reply", validReply)
		const otherURL = "https://patchbay.example.com/ctx/other-context"
		job := r.job(false)
		job.BaseURL = otherURL
		if err := r.s.Summarize(t.Context(), job); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		want := argvFor(protocol.DefaultSummaryModel, baseURLSettings(otherURL))
		if got := onlyCall(t, r.bin).args; !slices.Equal(got, want) {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	})
}

// writeSettings writes body as the settings file of folder, creating the folder's .claude directory.
func writeSettings(t *testing.T, folder, body string) {
	t.Helper()
	dir := filepath.Join(folder, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

// makeFolder creates folder and returns it.
func makeFolder(t *testing.T, folder string) string {
	t.Helper()
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatalf("make folder: %v", err)
	}
	return folder
}

func TestSummarizerStamp(t *testing.T) {
	// The stamped context and the job's base URL name different contexts, each with its own model.
	const stampURL = "http://127.0.0.1:9/ctx/example"
	models := map[string]string{"example": "example-stamp-model", exampleContext: exampleModel}
	stampBody := `{"env":{"ANTHROPIC_BASE_URL":"` + stampURL + `","EXAMPLE_SETTING":"example-value"}}`
	stampSettings := `{"disableAllHooks":true,"env":{"ANTHROPIC_BASE_URL":"` + stampURL + `","EXAMPLE_SETTING":"example-value"}}`
	t.Setenv("ANTHROPIC_BASE_URL", "https://patchbay.example.com/ctx/leftover")

	t.Run("stamp above dir wins over base url", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, filepath.Join(home, "work"), stampBody)
		dir := makeFolder(t, filepath.Join(home, "work", "project"))
		r := newRigWith(t, summary.Config{Models: models, Home: home})
		r.place(t, "reply", validReply)
		job := r.job(false)
		job.Dir = dir
		if err := r.s.Summarize(t.Context(), job); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		want := argvFor("example-stamp-model", stampSettings)
		if got := onlyCall(t, r.bin).args; !slices.Equal(got, want) {
			t.Fatalf("argv = %q, want %q", got, want)
		}
		assertEnv(t, r.bin, []string{"ORRERY_SUMMARIZER=1", "ANTHROPIC_BASE_URL=" + stampURL})
	})

	t.Run("request dir reaches the stamp", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, filepath.Join(home, "work"), stampBody)
		dir := makeFolder(t, filepath.Join(home, "work", "project"))
		r := newRigWith(t, summary.Config{Models: models, Home: home})
		r.place(t, "reply", validReply)
		req := exampleRequest(r.pane, true, exampleTranscript(t, 1), "Example last message")
		req.Dir = dir
		r.s.Request(t.Context(), req)
		waitIdle(t, r.s)
		want := argvFor("example-stamp-model", stampSettings)
		if got := onlyCall(t, r.bin).args; !slices.Equal(got, want) {
			t.Fatalf("argv = %q, want %q", got, want)
		}
		assertEnv(t, r.bin, []string{"ORRERY_SUMMARIZER=1", "ANTHROPIC_BASE_URL=" + stampURL})
	})

	t.Run("no stamp carries base url in settings", func(t *testing.T) {
		home := t.TempDir()
		dir := makeFolder(t, filepath.Join(home, "plain", "project"))
		r := newRigWith(t, summary.Config{Models: models, Home: home})
		r.place(t, "reply", validReply)
		job := r.job(false)
		job.Dir = dir
		if err := r.s.Summarize(t.Context(), job); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		want := argvFor(exampleModel, baseURLSettings(exampleBaseURL))
		if got := onlyCall(t, r.bin).args; !slices.Equal(got, want) {
			t.Fatalf("argv = %q, want %q", got, want)
		}
		assertEnv(t, r.bin, []string{"ORRERY_SUMMARIZER=1", "ANTHROPIC_BASE_URL=" + exampleBaseURL})
	})

	t.Run("unparseable stamp runs nothing", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, filepath.Join(home, "broken"), `{"env": `)
		dir := makeFolder(t, filepath.Join(home, "broken", "project"))
		r := newRigWith(t, summary.Config{Models: models, Home: home})
		r.setPrevious(t)
		r.place(t, "reply", validReply)
		job := r.job(true)
		job.Dir = dir
		if err := r.s.Summarize(t.Context(), job); err == nil {
			t.Fatal("Summarize: want error for unparseable stamp, got nil")
		}
		r.assertPrevious(t)
		if got := recordedCalls(t, r.bin); len(got) != 0 {
			t.Fatalf("claude runs = %d, want 0 below an unparseable stamp", len(got))
		}
	})
}

func TestSummarizerWritesOptions(t *testing.T) {
	t.Run("multi-line reply yields single-line values", func(t *testing.T) {
		r := newRig(t, nil, 0)
		r.setPrevious(t)
		r.place(t, "reply", "preamble line\n"+
			"summary:\t Example   session \tdoing  work\n"+
			"label:  alpha beta\tgamma   delta epsilon\n"+
			"trailing line\n")
		if err := r.s.Summarize(t.Context(), r.job(true)); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		if got, want := r.option(t, protocol.OptionSummary), "Example session doing work"; got != want {
			t.Errorf("%s = %q, want %q", protocol.OptionSummary, got, want)
		}
		if got, want := r.option(t, protocol.OptionLabel), "alpha beta gamma delta"; got != want {
			t.Errorf("%s = %q, want %q", protocol.OptionLabel, got, want)
		}
	})

	t.Run("summary cut to 100 runes and label to 4 words", func(t *testing.T) {
		r := newRig(t, nil, 0)
		r.setPrevious(t)
		long := strings.Repeat("é", 150)
		r.place(t, "reply", "summary: "+long+"\nlabel: one two three four five\n")
		if err := r.s.Summarize(t.Context(), r.job(true)); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		if got, want := r.option(t, protocol.OptionSummary), strings.Repeat("é", 100); got != want {
			t.Errorf("%s = %q (%d runes), want %d runes", protocol.OptionSummary, got, len([]rune(got)), 100)
		}
		if got, want := r.option(t, protocol.OptionLabel), "one two three four"; got != want {
			t.Errorf("%s = %q, want %q", protocol.OptionLabel, got, want)
		}
	})

	t.Run("label untouched without WantLabel", func(t *testing.T) {
		r := newRig(t, nil, 0)
		r.setPrevious(t)
		r.place(t, "reply", "summary: fresh summary\nlabel: other words here\n")
		if err := r.s.Summarize(t.Context(), r.job(false)); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		if got, want := r.option(t, protocol.OptionSummary), "fresh summary"; got != want {
			t.Errorf("%s = %q, want %q", protocol.OptionSummary, got, want)
		}
		if got := r.option(t, protocol.OptionLabel); got != previousLabel {
			t.Errorf("%s = %q, want previous %q", protocol.OptionLabel, got, previousLabel)
		}
	})
}

func TestSummaryFailureKeepsPrevious(t *testing.T) {
	t.Run("non-zero exit", func(t *testing.T) {
		r := newRig(t, nil, 0)
		r.setPrevious(t)
		r.place(t, "exit", "3\n")
		if err := r.s.Summarize(t.Context(), r.job(true)); err == nil {
			t.Fatal("Summarize: want error for non-zero exit, got nil")
		}
		r.assertPrevious(t)
	})

	t.Run("reply without summary line", func(t *testing.T) {
		r := newRig(t, nil, 0)
		r.setPrevious(t)
		r.place(t, "reply", "label: example words\nno summary line here\n")
		if err := r.s.Summarize(t.Context(), r.job(true)); err == nil {
			t.Fatal("Summarize: want error for reply without summary, got nil")
		}
		r.assertPrevious(t)
	})

	t.Run("held run past timeout", func(t *testing.T) {
		r := newRig(t, nil, shortTimeout)
		r.setPrevious(t)
		r.place(t, "hold", "")
		start := time.Now()
		if err := r.s.Summarize(t.Context(), r.job(true)); err == nil {
			t.Fatal("Summarize: want error for timed-out run, got nil")
		}
		if elapsed := time.Since(start); elapsed > failureBound {
			t.Fatalf("Summarize returned after %v, want within %v", elapsed, failureBound)
		}
		r.assertPrevious(t)
	})

	t.Run("missing transcript on request path", func(t *testing.T) {
		r := newRig(t, nil, 0)
		r.setPrevious(t)
		r.place(t, "reply", validReply)
		missing := filepath.Join(t.TempDir(), "missing.jsonl")
		r.s.Request(t.Context(), exampleRequest(r.pane, true, missing, "Example last message"))
		waitIdle(t, r.s)
		r.assertPrevious(t)
		if got := recordedCalls(t, r.bin); len(got) != 0 {
			t.Fatalf("claude runs = %d, want 0 for a missing transcript", len(got))
		}
	})
}

func TestSummarizerThrottle(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	r := newRigWith(t, summary.Config{Now: clock.Now})
	r.place(t, "reply", validReply)
	transcript := exampleTranscript(t, 1)
	base := clock.Now()

	// run sets the clock to base+at, sends one request, waits for it, and checks the run count.
	run := func(at time.Duration, stop bool, wantRuns int) {
		t.Helper()
		clock.Set(base.Add(at))
		r.s.Request(t.Context(), exampleRequest(r.pane, stop, transcript, "Example last message"))
		waitIdle(t, r.s)
		if got := len(recordedCalls(t, r.bin)); got != wantRuns {
			t.Fatalf("after request at +%v (stop=%t): claude runs = %d, want %d", at, stop, got, wantRuns)
		}
	}

	run(0, false, 1)
	run(30*time.Second, false, 1)
	run(61*time.Second, false, 2)
	run(62*time.Second, true, 3)
}

func TestSummarizerCap(t *testing.T) {
	r := newRig(t, nil, 0)
	r.place(t, "reply", validReply)
	r.hold(t)
	panes := []string{r.pane, newPane(t, r.client, "s2"), newPane(t, r.client, "s3")}
	transcript := exampleTranscript(t, 1)

	start := time.Now()
	for _, pane := range panes {
		r.s.Request(t.Context(), exampleRequest(pane, true, transcript, "Example last message"))
	}
	if elapsed := time.Since(start); elapsed > failureBound {
		t.Fatalf("Request calls took %v while runs were held, want within %v", elapsed, failureBound)
	}

	waitRunning(t, r.bin, summary.MaxRuns)
	<-time.After(settleTime)
	if started, _ := countRuns(t, r.bin); started != summary.MaxRuns {
		t.Fatalf("runs started while held = %d, want %d", started, summary.MaxRuns)
	}

	r.release(t)
	waitIdle(t, r.s)
	if got := len(recordedCalls(t, r.bin)); got != len(panes) {
		t.Fatalf("claude runs = %d, want %d once released", got, len(panes))
	}
	for _, pane := range panes {
		value, _, err := r.client.PaneOption(t.Context(), pane, protocol.OptionSummary)
		if err != nil {
			t.Fatalf("PaneOption(%s): %v", pane, err)
		}
		if want := "example session summary"; value != want {
			t.Errorf("pane %s %s = %q, want %q", pane, protocol.OptionSummary, value, want)
		}
	}
}

func TestSummarizerLatestWaitingWins(t *testing.T) {
	r := newRig(t, nil, 0)
	r.place(t, "reply", validReply)
	r.hold(t)
	transcript := exampleTranscript(t, 1)

	r.s.Request(t.Context(), exampleRequest(r.pane, true, transcript, "Example message A"))
	waitRunning(t, r.bin, 1)
	r.s.Request(t.Context(), exampleRequest(r.pane, true, transcript, "Example message B"))
	r.s.Request(t.Context(), exampleRequest(r.pane, true, transcript, "Example message C"))

	r.release(t)
	waitIdle(t, r.s)
	calls := recordedCalls(t, r.bin)
	if len(calls) != 2 {
		t.Fatalf("claude runs = %d, want 2: the held run and one run for the latest waiting request", len(calls))
	}
	for _, message := range []string{"Example message A", "Example message C"} {
		if n := countStdin(calls, message); n != 1 {
			t.Errorf("runs with stdin holding %q = %d, want 1", message, n)
		}
	}
	if n := countStdin(calls, "Example message B"); n != 0 {
		t.Errorf("runs with stdin holding superseded %q = %d, want 0", "Example message B", n)
	}
}

// countStdin returns how many of calls received stdin holding text.
func countStdin(calls []call, text string) int {
	n := 0
	for _, c := range calls {
		if strings.Contains(c.stdin, text) {
			n++
		}
	}
	return n
}

func TestSummarizerInput(t *testing.T) {
	r := newRig(t, nil, 0)
	r.place(t, "reply", validReply)
	transcript := exampleTranscript(t, 2)
	const last = "Example final message"
	excerpt, err := summary.ReadExcerpt(transcript, last)
	if err != nil {
		t.Fatalf("ReadExcerpt: %v", err)
	}

	r.s.Request(t.Context(), exampleRequest(r.pane, true, transcript, last))
	waitIdle(t, r.s)
	got := onlyCall(t, r.bin).stdin
	if !strings.HasSuffix(got, excerpt.Text) {
		t.Fatalf("stdin = %q, want it to end with the excerpt %q", got, excerpt.Text)
	}
	for _, want := range []string{"User: Example prompt 1", "Assistant: Example reply 2", "Assistant: " + last} {
		if !strings.Contains(got, want) {
			t.Errorf("stdin = %q, want it to hold %q", got, want)
		}
	}
}

func TestSummarizerLabelCadence(t *testing.T) {
	r := newRig(t, nil, 0)
	r.place(t, "reply", validReply)

	// Exchange counts 1, 2, 4 and 5 are requested in order; labels are wanted for 1 and 5 only.
	cadence := []struct {
		exchanges int
		wantLabel bool
	}{{1, true}, {2, false}, {4, false}, {5, true}}
	for _, step := range cadence {
		last := fmt.Sprintf("Example message %d", step.exchanges)
		r.s.Request(t.Context(), exampleRequest(r.pane, true, exampleTranscript(t, step.exchanges), last))
		waitIdle(t, r.s)
	}

	calls := recordedCalls(t, r.bin)
	for _, step := range cadence {
		message := fmt.Sprintf("Example message %d", step.exchanges)
		var matched []call
		for _, c := range calls {
			if strings.Contains(c.stdin, message) {
				matched = append(matched, c)
			}
		}
		if len(matched) != 1 {
			t.Fatalf("runs with stdin holding %q = %d, want 1", message, len(matched))
		}
		if got := strings.Contains(matched[0].stdin, labelMarker); got != step.wantLabel {
			t.Errorf("%d exchanges: label instruction present = %t, want %t", step.exchanges, got, step.wantLabel)
		}
	}
}

// callWith returns the one call whose stdin holds text, failing the test unless exactly one does.
func callWith(t *testing.T, calls []call, text string) call {
	t.Helper()
	var matched []call
	for _, c := range calls {
		if strings.Contains(c.stdin, text) {
			matched = append(matched, c)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("runs with stdin holding %q = %d, want 1", text, len(matched))
	}
	return matched[0]
}

func TestSummarizerForgetRestartsLabelCadence(t *testing.T) {
	r := newRig(t, nil, 0)
	r.place(t, "reply", validReply)
	r.s.Request(t.Context(), exampleRequest(r.pane, true, exampleTranscript(t, 9), "Example message first"))
	waitIdle(t, r.s)

	r.s.Forget(r.pane)
	r.s.Request(t.Context(), exampleRequest(r.pane, true, exampleTranscript(t, 1), "Example message second"))
	waitIdle(t, r.s)

	got := callWith(t, recordedCalls(t, r.bin), "Example message second")
	if !strings.Contains(got.stdin, labelMarker) {
		t.Errorf("run after Forget (1 exchange) stdin lacks label marker; stdin = %q", got.stdin)
	}
}

func TestSummarizerForgetEndsInFlightRun(t *testing.T) {
	r := newRig(t, nil, 0)
	r.place(t, "reply", validReply)
	r.setPrevious(t)
	r.hold(t)
	transcript := exampleTranscript(t, 9)

	r.s.Request(t.Context(), exampleRequest(r.pane, true, transcript, "Example message in flight"))
	waitRunning(t, r.bin, 1)
	r.s.Request(t.Context(), exampleRequest(r.pane, true, transcript, "Example message waiting"))

	start := time.Now()
	r.s.Forget(r.pane)
	if elapsed := time.Since(start); elapsed > failureBound {
		t.Fatalf("Forget took %v while a run was held, want within %v", elapsed, failureBound)
	}

	r.release(t)
	waitIdle(t, r.s)

	for _, name := range []string{protocol.OptionSummary, protocol.OptionLabel} {
		value, ok, err := r.client.PaneOption(t.Context(), r.pane, name)
		if err != nil {
			t.Fatalf("PaneOption(%s): %v", name, err)
		}
		if ok {
			t.Errorf("%s = %q after Forget, want unset", name, value)
		}
	}
	calls := recordedCalls(t, r.bin)
	if len(calls) != 1 {
		t.Fatalf("claude runs = %d, want 1: the waiting request must not start", len(calls))
	}
	callWith(t, calls, "Example message in flight")

	r.s.Request(t.Context(), exampleRequest(r.pane, true, exampleTranscript(t, 1), "Example message after"))
	waitIdle(t, r.s)
	got := callWith(t, recordedCalls(t, r.bin), "Example message after")
	if !strings.Contains(got.stdin, labelMarker) {
		t.Errorf("run after Forget (1 exchange) stdin lacks label marker; stdin = %q", got.stdin)
	}
}
