package enginehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// haikuDefaultCap and haikuDefaultTimeout apply when HaikuConfig leaves Cap or Timeout zero.
const (
	haikuDefaultCap     = 2
	haikuDefaultTimeout = 60 * time.Second
)

// haikuKillGrace bounds how long a killed claude may hold its output pipes open before the run returns.
const haikuKillGrace = 2 * time.Second

// haikuBaseURLPrefix names the environment entry the call drops so claude reaches the API directly.
const haikuBaseURLPrefix = "ANTHROPIC_BASE_URL="

// HaikuConfig configures a Haiku caller. Cap 0 means 2 concurrent calls, and Timeout 0 means 60 seconds.
type HaikuConfig struct {
	Claude  string
	Environ []string
	Cap     int
	Timeout time.Duration
}

// Haiku runs short structured Haiku calls through the claude binary.
type Haiku struct {
	claude  string
	environ []string
	timeout time.Duration
	slots   chan struct{}
}

// haikuCaller is the one short structured call the engine makes.
type haikuCaller interface {
	Call(ctx context.Context, schema, input string) (json.RawMessage, error)
}

// NewHaiku returns a Haiku caller for cfg with the defaults applied.
func NewHaiku(cfg HaikuConfig) *Haiku {
	capacity := cfg.Cap
	if capacity == 0 {
		capacity = haikuDefaultCap
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = haikuDefaultTimeout
	}
	return &Haiku{
		claude:  cfg.Claude,
		environ: cfg.Environ,
		timeout: timeout,
		slots:   make(chan struct{}, capacity),
	}
}

// Call runs claude once against schema with input on stdin, with the default summary model and no base URL, and
// returns the result's structured_output. The timeout covers the wait for a call slot and the run together.
func (h *Haiku) Call(ctx context.Context, schema, input string) (json.RawMessage, error) {
	return h.CallVia(ctx, "", protocol.DefaultSummaryModel, schema, input)
}

// CallVia runs claude once against schema with input on stdin, using model (the default summary model when
// empty) and, when baseURL is non-empty, claude's API base URL set to baseURL. It returns the result's
// structured_output. The timeout covers the wait for a call slot and the run together.
func (h *Haiku) CallVia(ctx context.Context, baseURL, model, schema, input string) (json.RawMessage, error) {
	if model == "" {
		model = protocol.DefaultSummaryModel
	}
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	select {
	case h.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("haiku: waiting for a call slot: %w", ctx.Err())
	}
	defer func() { <-h.slots }()

	return h.run(ctx, baseURL, model, schema, input)
}

// run executes claude once and parses its result line.
func (h *Haiku) run(ctx context.Context, baseURL, model, schema, input string) (json.RawMessage, error) {
	path, err := exec.LookPath(h.claude)
	if err != nil {
		return nil, fmt.Errorf("haiku: find claude: %w", err)
	}
	var stdout, stderr bytes.Buffer
	cmd := &exec.Cmd{
		Path:        path,
		Args:        append([]string{path}, haikuArgv(model, schema)...),
		Dir:         "/",
		Env:         haikuEnv(h.environ, baseURL),
		Stdin:       strings.NewReader(input),
		Stdout:      &stdout,
		Stderr:      &stderr,
		WaitDelay:   haikuKillGrace,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("haiku: start claude: %w", err)
	}
	pid := cmd.Process.Pid
	var waitErr error
	waited := make(chan struct{})
	go func() {
		waitErr = cmd.Wait()
		close(waited)
	}()

	select {
	case <-waited:
	case <-ctx.Done():
		killErr := gateKillGroup(pid)
		<-waited
		return nil, errors.Join(fmt.Errorf("haiku: claude run: %w", ctx.Err()), killErr)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("haiku: claude run: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}

	return haikuResultOutput(stdout.Bytes())
}

// haikuResultOutput returns the structured_output of the one JSON result line claude -p --output-format json
// prints in stdout, or an error when that line is not a successful result carrying structured output.
func haikuResultOutput(stdout []byte) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(stdout, &fields); err != nil {
		return nil, fmt.Errorf("haiku: stdout is not a JSON result: %w", err)
	}
	var typ, subtype string
	var isError bool
	for _, field := range []struct {
		key string
		dst any
	}{{"type", &typ}, {"subtype", &subtype}, {"is_error", &isError}} {
		if err := haikuDecodeField(fields, field.key, field.dst); err != nil {
			return nil, err
		}
	}
	if typ != "result" {
		return nil, fmt.Errorf("haiku: stdout is a %q message, not a result", typ)
	}
	if isError {
		return nil, errors.New("haiku: result reported is_error")
	}
	if subtype != "success" {
		return nil, fmt.Errorf("haiku: result subtype is %q, not success", subtype)
	}
	output := fields["structured_output"]
	if len(output) == 0 || string(output) == "null" {
		return nil, errors.New("haiku: result has no structured_output")
	}
	return output, nil
}

// haikuDecodeField decodes the field key of fields into dst. A missing field leaves dst at its zero value.
func haikuDecodeField(fields map[string]json.RawMessage, key string, dst any) error {
	raw, ok := fields[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("haiku: result field %s: %w", key, err)
	}
	return nil
}

// haikuArgv is the argv after the claude binary: one structured turn on model with hooks disabled, no tools, no
// saved session.
func haikuArgv(model, schema string) []string {
	return []string{
		"-p", "--model", model,
		"--settings", `{"disableAllHooks":true}`,
		"--no-session-persistence",
		"--tools", "",
		"--max-turns", "1",
		"--output-format", "json",
		"--json-schema", schema,
	}
}

// haikuEnv returns environ without ANTHROPIC_BASE_URL and ORRERY_SUMMARIZER, plus baseURL as ANTHROPIC_BASE_URL
// when it is set and the summarizer sentinel.
func haikuEnv(environ []string, baseURL string) []string {
	env := make([]string, 0, len(environ)+2)
	for _, entry := range environ {
		if strings.HasPrefix(entry, haikuBaseURLPrefix) || strings.HasPrefix(entry, protocol.EnvSummarizer+"=") {
			continue
		}
		env = append(env, entry)
	}
	if baseURL != "" {
		env = append(env, haikuBaseURLPrefix+baseURL)
	}
	return append(env, protocol.EnvSummarizer+"="+protocol.SummarizerValue)
}
