package summary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// DefaultTimeout bounds one summary run when Config.Timeout is zero.
const DefaultTimeout = 60 * time.Second

// Scheduling limits for Request.
const (
	Throttle   = 60 * time.Second // minimum gap between runs for one pane on working events
	MaxRuns    = 2                // summary runs in flight at once
	LabelEvery = 4                // exchanges between label refreshes
)

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
	Timeout time.Duration    // per-run limit; zero selects DefaultTimeout
	Now     func() time.Time // clock for the throttle; nil selects time.Now
	Logger  *slog.Logger     // receives failed runs; nil discards
}

// Request is one summary trigger for one pane.
type Request struct {
	PaneID               string
	BaseURL              string // the session's ANTHROPIC_BASE_URL
	TranscriptPath       string
	LastAssistantMessage string // Stop's last_assistant_message, "" otherwise
	Stop                 bool   // true on Stop, false while working
}

// Summarizer runs the claude binary once per job and writes the reply to the job's pane.
type Summarizer struct {
	claude  string
	models  map[string]string
	tmux    *tmuxctl.Client
	timeout time.Duration
	now     func() time.Time
	logger  *slog.Logger
	runs    sync.WaitGroup // one count per run in flight
	writeMu sync.Mutex     // held around each option write and Forget's unset, so a write never lands after Forget

	mu              sync.Mutex
	running         map[string]bool      // panes with a run in flight
	started         map[string]time.Time // start of each pane's latest run
	waiting         map[string]Request   // each pane's latest request that could not start yet
	labeledExchange map[string]int       // exchange count of each pane's latest label run
	epoch           map[string]int       // Forget count of each pane; a run writes only while its pane's epoch is unchanged
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
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Summarizer{
		claude:          cfg.Claude,
		models:          cfg.Models,
		tmux:            cfg.Tmux,
		timeout:         timeout,
		now:             now,
		logger:          logger,
		running:         make(map[string]bool),
		started:         make(map[string]time.Time),
		waiting:         make(map[string]Request),
		labeledExchange: make(map[string]int),
		epoch:           make(map[string]int),
	}, nil
}

// Job is one summary run for one pane.
type Job struct {
	PaneID    string
	BaseURL   string // the session's ANTHROPIC_BASE_URL, "" when unset
	Input     string // transcript excerpt
	WantLabel bool
}

// Summarize runs the claude binary for job and writes the summary and, when WantLabel, the label to the pane.
// A failed, timed-out or unparseable run returns an error and writes neither option. A run whose pane is
// forgotten before it writes returns nil and writes neither option.
func (s *Summarizer) Summarize(ctx context.Context, job Job) error {
	return s.summarize(ctx, job, s.paneEpoch(job.PaneID))
}

// summarize runs job and writes its reply unless its pane was forgotten since epoch was read.
func (s *Summarizer) summarize(ctx context.Context, job Job, epoch int) error {
	reply, err := s.run(ctx, job)
	if err != nil {
		return err
	}
	summary, label, err := parseReply(reply, job.WantLabel)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.paneEpoch(job.PaneID) != epoch {
		return nil
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

// Forget ends pane's session. Its waiting request is dropped, its options are unset, and its next label run is
// asked for a label whatever its exchange count. A run already in flight writes no option when it finishes; Forget
// does not wait for it. The throttle and the run cap are unchanged.
func (s *Summarizer) Forget(pane string) {
	ctx := context.Background()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	s.epoch[pane]++
	delete(s.waiting, pane)
	delete(s.labeledExchange, pane)
	s.mu.Unlock()

	for _, name := range []string{protocol.OptionSummary, protocol.OptionLabel} {
		if err := s.tmux.UnsetPaneOption(ctx, pane, name); err != nil {
			s.logger.WarnContext(ctx, "unset pane option failed", "pane", pane, "option", name, "error", err)
		}
	}
}

// Request triggers a summary for req.PaneID and returns without waiting for the run. A working request is dropped
// when the pane's last run started less than Throttle ago. Otherwise the request starts at once when its pane has
// no run in flight and fewer than MaxRuns runs are in flight; if not, it replaces the pane's waiting request.
// ctx bounds the runs that Request starts, including the waiting requests they start in turn.
func (s *Summarizer) Request(ctx context.Context, req Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if !req.Stop {
		if last, ok := s.started[req.PaneID]; ok && now.Sub(last) < Throttle {
			return
		}
	}
	if s.running[req.PaneID] || len(s.running) >= MaxRuns {
		s.waiting[req.PaneID] = req
		return
	}
	s.startLocked(ctx, req, now)
}

// Wait returns once no run is in flight or waiting.
func (s *Summarizer) Wait() {
	s.runs.Wait()
}

// paneEpoch returns pane's Forget count.
func (s *Summarizer) paneEpoch(pane string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch[pane]
}

// startLocked starts the run for req and records its start. The caller holds s.mu.
func (s *Summarizer) startLocked(ctx context.Context, req Request, now time.Time) {
	s.running[req.PaneID] = true
	s.started[req.PaneID] = now
	s.runs.Add(1)
	go s.execute(ctx, req, s.epoch[req.PaneID])
}

// execute runs req, whose pane had Forget count epoch when req started, then starts each waiting request whose
// pane is free while a run slot is open.
func (s *Summarizer) execute(ctx context.Context, req Request, epoch int) {
	defer s.runs.Done()
	s.summarizeRequest(ctx, req, epoch)

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, req.PaneID)
	for pane, next := range s.waiting {
		if len(s.running) >= MaxRuns {
			break
		}
		if s.running[pane] {
			continue
		}
		delete(s.waiting, pane)
		s.startLocked(ctx, next, s.now())
	}
}

// summarizeRequest reads req's transcript excerpt and runs one summary over it. A failure is logged and leaves
// the pane's previous options in place. The label exchange is recorded only while the pane's epoch is unchanged.
func (s *Summarizer) summarizeRequest(ctx context.Context, req Request, epoch int) {
	excerpt, err := ReadExcerpt(req.TranscriptPath, req.LastAssistantMessage)
	if err != nil {
		s.logger.WarnContext(ctx, "read transcript excerpt failed", "pane", req.PaneID, "error", err)
		return
	}
	wantLabel := s.wantLabel(req.PaneID, excerpt.Exchanges)
	job := Job{PaneID: req.PaneID, BaseURL: req.BaseURL, Input: excerpt.Text, WantLabel: wantLabel}
	if err = s.summarize(ctx, job, epoch); err != nil {
		s.logger.WarnContext(ctx, "summary run failed", "pane", req.PaneID, "error", err)
		return
	}
	if wantLabel {
		s.mu.Lock()
		if s.epoch[req.PaneID] == epoch {
			s.labeledExchange[req.PaneID] = excerpt.Exchanges
		}
		s.mu.Unlock()
	}
}

// wantLabel reports whether pane has no label yet, or its last label run is LabelEvery or more exchanges old.
func (s *Summarizer) wantLabel(pane string, exchanges int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	labeled, ok := s.labeledExchange[pane]
	return !ok || exchanges >= labeled+LabelEvery
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
