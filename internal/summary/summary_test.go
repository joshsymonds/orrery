package summary_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
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
)

// argvFor is the exact argv after the binary for model.
func argvFor(model string) []string {
	return []string{
		"-p", "--model", model,
		"--settings", `{"disableAllHooks":true}`,
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
		if _, err := client.Run(context.Background(), "kill-server"); err != nil {
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
	client := newServer(t)
	mustRun(t, client, "-f", noConfig, "new-session", "-d", "-s", "s1", "-c", t.TempDir(), testCommand)
	pane := strings.Fields(mustRun(t, client, "list-panes", "-t", "s1", "-F", "#{pane_id}"))[0]
	bin := installFake(t)
	s, err := summary.New(summary.Config{Claude: bin, Models: models, Tmux: client, Timeout: timeout})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return rig{bin: bin, client: client, pane: pane, s: s}
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
	if !slices.Equal(got.args, argvFor(exampleModel)) {
		t.Fatalf("argv = %q, want %q", got.args, argvFor(exampleModel))
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
		want := []string{"ANTHROPIC_BASE_URL=" + exampleBaseURL, "ORRERY_SUMMARIZER=1"}
		assertEnv(t, r.bin, want)
	})

	t.Run("base url empty", func(t *testing.T) {
		r := newRig(t, nil, 0)
		r.place(t, "reply", validReply)
		job := r.job(false)
		job.BaseURL = ""
		if err := r.s.Summarize(t.Context(), job); err != nil {
			t.Fatalf("Summarize: %v", err)
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
		if got := onlyCall(t, r.bin).args; !slices.Equal(got, argvFor(exampleModel)) {
			t.Fatalf("argv = %q, want model %q", got, exampleModel)
		}
	})

	t.Run("unmapped context", func(t *testing.T) {
		r := newRig(t, models, 0)
		r.place(t, "reply", validReply)
		job := r.job(false)
		job.BaseURL = "https://patchbay.example.com/ctx/other-context"
		if err := r.s.Summarize(t.Context(), job); err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		if got := onlyCall(t, r.bin).args; !slices.Equal(got, argvFor(protocol.DefaultSummaryModel)) {
			t.Fatalf("argv = %q, want model %q", got, protocol.DefaultSummaryModel)
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
}
