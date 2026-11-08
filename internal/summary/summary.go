package summary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// DefaultTimeout bounds one summary run when Config.Timeout is zero.
const DefaultTimeout = 60 * time.Second

// killGrace bounds how long a run waits for its output pipes once the child has exited or been killed,
// so a descendant that outlives the child cannot hold the run open.
const killGrace = 2 * time.Second

const (
	anthropicBaseURLEnv = "ANTHROPIC_BASE_URL"
	summaryPrefix       = "summary:"
	labelPrefix         = "label:"
	maxSummaryRunes     = 100
	maxLabelWords       = 4
	summaryInstruction  = "Summarize the coding session excerpt below. " +
		"Reply with one line in the form\nsummary: <what the session is doing, at most 100 characters>\n"
	labelInstruction = "Then reply with a second line in the form\nlabel: <3 to 4 words naming the task>\n"
	excerptHeader    = "\nSession excerpt:\n"
)

// Config configures a Summarizer.
type Config struct {
	Claude  string            // path of the claude binary
	Models  map[string]string // patchbay context name to summary model, see protocol.SummaryModel
	Tmux    *tmuxctl.Client
	Timeout time.Duration // per-run limit; zero selects DefaultTimeout
}

// Summarizer runs the claude binary once per job and writes the reply to the job's pane.
type Summarizer struct {
	claude  string
	models  map[string]string
	tmux    *tmuxctl.Client
	timeout time.Duration
}

// New returns a Summarizer for cfg. It returns an error when cfg.Claude is empty or cfg.Tmux is nil.
func New(cfg Config) (*Summarizer, error) {
	if cfg.Claude == "" {
		return nil, errors.New("summary: Claude binary path is empty")
	}
	if cfg.Tmux == nil {
		return nil, errors.New("summary: Tmux client is nil")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Summarizer{claude: cfg.Claude, models: cfg.Models, tmux: cfg.Tmux, timeout: timeout}, nil
}

// Job is one summary run for one pane.
type Job struct {
	PaneID    string
	BaseURL   string // the session's ANTHROPIC_BASE_URL, "" when unset
	Input     string // transcript excerpt
	WantLabel bool
}

// Summarize runs the claude binary for job and writes the summary and, when WantLabel, the label to the pane.
// A failed, timed-out or unparseable run returns an error and writes neither option.
func (s *Summarizer) Summarize(ctx context.Context, job Job) error {
	reply, err := s.run(ctx, job)
	if err != nil {
		return err
	}
	summary, label, err := parseReply(reply, job.WantLabel)
	if err != nil {
		return err
	}
	if err = s.tmux.SetPaneOption(ctx, job.PaneID, protocol.OptionSummary, summary); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	if label == "" {
		return nil
	}
	if err = s.tmux.SetPaneOption(ctx, job.PaneID, protocol.OptionLabel, label); err != nil {
		return fmt.Errorf("write label: %w", err)
	}
	return nil
}

// run executes one claude summary call for job and returns its stdout.
func (s *Summarizer) run(ctx context.Context, job Job) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	model := protocol.SummaryModel(job.BaseURL, s.models)
	cmd := exec.CommandContext(runCtx, s.claude, argvFor(model)...)
	cmd.Dir = "/"
	cmd.Env = summaryEnv(os.Environ(), job.BaseURL)
	cmd.Stdin = strings.NewReader(stdinFor(job))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = killGrace

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude run: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// argvFor is the argv after the claude binary: hooks disabled, no session saved, no tools, one turn.
func argvFor(model string) []string {
	return []string{
		"-p", "--model", model,
		"--settings", `{"disableAllHooks":true}`,
		"--no-session-persistence",
		"--tools", "",
		"--max-turns", "1",
	}
}

// summaryEnv returns environ without ANTHROPIC_BASE_URL and ORRERY_SUMMARIZER, plus the session's base URL
// when it is set and the summarizer sentinel.
func summaryEnv(environ []string, baseURL string) []string {
	env := make([]string, 0, len(environ)+2)
	for _, entry := range environ {
		if strings.HasPrefix(entry, anthropicBaseURLEnv+"=") || strings.HasPrefix(entry, protocol.EnvSummarizer+"=") {
			continue
		}
		env = append(env, entry)
	}
	if baseURL != "" {
		env = append(env, anthropicBaseURLEnv+"="+baseURL)
	}
	return append(env, protocol.EnvSummarizer+"="+protocol.SummarizerValue)
}

// stdinFor is the instruction text followed by the job's transcript excerpt.
func stdinFor(job Job) string {
	instruction := summaryInstruction
	if job.WantLabel {
		instruction += labelInstruction
	}
	return instruction + excerptHeader + job.Input
}

// parseReply returns the summary from the first summary line and, when wantLabel, the label from the first
// label line. The label is empty when the reply has none, and the caller then leaves the option alone.
func parseReply(reply string, wantLabel bool) (string, string, error) {
	summary, found := replyValue(reply, summaryPrefix)
	if !found || summary == "" {
		return "", "", errors.New("summary reply: no summary line")
	}
	summary = truncateRunes(summary, maxSummaryRunes)
	if !wantLabel {
		return summary, "", nil
	}
	label, _ := replyValue(reply, labelPrefix)
	return summary, firstWords(label, maxLabelWords), nil
}

// replyValue returns the value of the first line that starts with prefix, ignoring case and surrounding
// whitespace, with its inner whitespace collapsed to single spaces. found is false when no line matches.
func replyValue(reply, prefix string) (string, bool) {
	for line := range strings.SplitSeq(reply, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
			continue
		}
		return strings.Join(strings.Fields(trimmed[len(prefix):]), " "), true
	}
	return "", false
}

// truncateRunes returns s cut to at most n runes.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

// firstWords returns the first maxLabelWords words of value, joined by single spaces.
func firstWords(value string, limit int) string {
	words := strings.Fields(value)
	if len(words) > limit {
		words = words[:limit]
	}
	return strings.Join(words, " ")
}
