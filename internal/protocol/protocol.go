// Package protocol defines the names and wire types shared by the orrery CLI,
// the orreryd daemon, and the Claude Code hook.
package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
)

// State is the state of a Claude Code pane.
type State string

// Pane states.
const (
	StateWorking State = "working"
	StateWaiting State = "waiting"
	StateDone    State = "done"
)

// ParseState returns the State named by s, or an error when s is not one of the three states.
func ParseState(s string) (State, error) {
	switch State(s) {
	case StateWorking, StateWaiting, StateDone:
		return State(s), nil
	default:
		return "", fmt.Errorf("unknown pane state %q", s)
	}
}

// Per-pane tmux option names, set at pane scope. Values are single-line.
const (
	OptionID            = "@orrery_id"
	OptionState         = "@orrery_state"
	OptionChanged       = "@orrery_changed"
	OptionSeen          = "@orrery_seen"
	OptionQuestion      = "@orrery_question"
	OptionSummary       = "@orrery_summary"
	OptionLabel         = "@orrery_label"
	OptionContext       = "@orrery_context"
	OptionDir           = "@orrery_dir"
	OptionClaudeSession = "@orrery_claude_session"
	OptionTranscript    = "@orrery_transcript"
	OptionTool          = "@orrery_tool"
	// OptionTurnStart holds the unix seconds of the newest UserPromptSubmit. The state options move only when the
	// state changes, so a prompt sent to a pane that is already working leaves OptionChanged at the earlier turn's
	// start. This option moves on every prompt.
	OptionTurnStart = "@orrery_turn_start"
	// OptionHeld holds the unix seconds of the transcript line that ended the pane's last turn while its Claude Code
	// session registry entry still reads busy. Claude Code keeps a session busy while a background agent, workflow or
	// teammate it started runs, so the pane stays working, not done, with no turn running: the task's end starts the
	// turn whose end makes the pane done. Every registry update that leaves the pane otherwise unsets it.
	OptionHeld = "@orrery_held"
)

// Idle reports whether a pane in state, with held saying whether its @orrery_held is set, runs no turn: it is done, or
// working and held (see OptionHeld).
func Idle(state State, held bool) bool {
	return state == StateDone || state == StateWorking && held
}

// ShellSessionPrefix is the fixed prefix of shell session names.
const ShellSessionPrefix = "_orrery-shell-"

// claudeSessionKey is the wire key of Pane.ClaudeSession. It is a constant rather than a
// struct tag, so the camel-case rule of the tag linter does not apply to it.
const claudeSessionKey = "claude_session"

// Pane is one element of the `orrery list --json` array.
type Pane struct {
	ID            string `json:"id"`
	Host          string `json:"host"`
	Session       string `json:"session"`
	WindowIndex   int    `json:"window"`
	PaneIndex     int    `json:"pane"`
	Cwd           string `json:"cwd"`
	Command       string `json:"command"`
	State         State  `json:"state"`
	Summary       string `json:"summary"`
	Label         string `json:"label"`
	Changed       int64  `json:"changed"`
	Seen          int64  `json:"seen"`
	Question      string `json:"question"`
	Context       string `json:"context"`
	ClaudeSession string `json:"-"`
	Activity      int64  `json:"activity"`
	Tool          string `json:"tool"`
	Held          bool   `json:"held"`
}

// paneFields is Pane without its methods, so encoding and decoding it uses the field tags.
type paneFields Pane

// MarshalJSON encodes the tagged fields in declaration order, then claude_session as the last key.
func (p Pane) MarshalJSON() ([]byte, error) {
	fields, err := json.Marshal(paneFields(p))
	if err != nil {
		return nil, fmt.Errorf("encode pane: %w", err)
	}
	session, err := json.Marshal(p.ClaudeSession)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", claudeSessionKey, err)
	}
	// fields is a non-empty JSON object, so its closing brace is the last byte.
	out := bytes.Clone(fields[:len(fields)-1])
	out = append(out, `,"`+claudeSessionKey+`":`...)
	out = append(out, session...)
	return append(out, '}'), nil
}

// UnmarshalJSON decodes the tagged fields, then reads claude_session by its constant key. An
// absent key leaves ClaudeSession empty. A key whose value is not a JSON string is an error.
func (p *Pane) UnmarshalJSON(data []byte) error {
	var fields paneFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode pane: %w", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("decode pane keys: %w", err)
	}
	*p = Pane(fields)
	raw, ok := keys[claudeSessionKey]
	if !ok {
		return nil
	}
	if bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("decode %s: value is null, want a string", claudeSessionKey)
	}
	var session string
	if err := json.Unmarshal(raw, &session); err != nil {
		return fmt.Errorf("decode %s: %w", claudeSessionKey, err)
	}
	p.ClaudeSession = session
	return nil
}

// Environment variable names and the value that marks a summarizer process.
const (
	EnvSocket       = "ORRERY_SOCKET"
	EnvSummarizer   = "ORRERY_SUMMARIZER"
	SummarizerValue = "1"
)

// EnvSession is the marker that names an orrery-started headless session.
const EnvSession = "ORRERY_SESSION"

// Request headers that name the session a client call runs in: HeaderSession carries EnvSession, and HeaderTmux and
// HeaderTmuxPane carry the TMUX and TMUX_PANE of a session in tmux.
const (
	HeaderSession  = "Orrery-Session"
	HeaderTmux     = "Orrery-Tmux"
	HeaderTmuxPane = "Orrery-Tmux-Pane"
)

// HeaderHost is the orrery name of the host that a session-service call comes from. The listener that serves the call
// sets it over any value the caller sent, so the name it carries is the one the listener admitted.
const HeaderHost = "Orrery-Host"

// SocketPath resolves the daemon socket: flagValue when non-empty, else ORRERY_SOCKET,
// else $XDG_RUNTIME_DIR/orrery/orreryd.sock. It returns an error when none of them is set.
func SocketPath(flagValue string, getenv func(string) string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if sock := getenv(EnvSocket); sock != "" {
		return sock, nil
	}
	if runtimeDir := getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "orrery", "orreryd.sock"), nil
	}
	return "", fmt.Errorf("no daemon socket: pass --socket, set %s, or set XDG_RUNTIME_DIR", EnvSocket)
}

// SessionSocketPath names the session control socket: sessions.sock beside the daemon socket,
// so orreryd and `orrery session` derive the same path from the daemon socket each resolves.
func SessionSocketPath(daemonSocket string) string {
	return filepath.Join(filepath.Dir(daemonSocket), "sessions.sock")
}

// IsSummarizer reports whether this process is the summarizer's own claude subprocess.
func IsSummarizer(getenv func(string) string) bool {
	return getenv(EnvSummarizer) == SummarizerValue
}

// QuestionHash returns the lowercase hex SHA-256 of question's raw bytes, exactly as stored
// in the @orrery_question option. The empty string hashes as an unset question.
func QuestionHash(question string) string {
	sum := sha256.Sum256([]byte(question))
	return hex.EncodeToString(sum[:])
}

// DefaultSummaryModel is the summary model used when the proxy context has no mapping.
const DefaultSummaryModel = "claude-haiku-5-5"

// SummaryContext returns the name in the first /ctx/<name> path segment pair of baseURL,
// or "" when there is none or baseURL does not parse.
func SummaryContext(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	segments := strings.Split(parsed.Path, "/")
	for i := range len(segments) - 1 {
		if segments[i] == "ctx" {
			return segments[i+1]
		}
	}
	return ""
}

// SummaryModel returns models[SummaryContext(baseURL)] when that context is non-empty and
// maps to a non-empty model, else DefaultSummaryModel.
func SummaryModel(baseURL string, models map[string]string) string {
	if name := SummaryContext(baseURL); name != "" {
		if model := models[name]; model != "" {
			return model
		}
	}
	return DefaultSummaryModel
}

// ViewKey names what the web view views: the pane, session or epic a notice is keyed by. At most one field is set,
// and the zero ViewKey names nothing.
type ViewKey struct {
	Pane    string `json:"pane,omitempty"`
	Session string `json:"session,omitempty"`
	Epic    string `json:"epic,omitempty"`
}

// fields returns how many of v's fields are set.
func (v ViewKey) fields() int {
	count := 0
	for _, field := range []string{v.Pane, v.Session, v.Epic} {
		if field != "" {
			count++
		}
	}
	return count
}

// HookFrame is the single frame a sender writes to orreryd per connection. A seen report carries Seen. A read report
// carries Read and not Seen. An input report carries Input and neither Seen nor Read, and carries View when the web
// view views a key. A sessions question carries Ask and none of Seen, Read, Input or View.
type HookFrame struct {
	TmuxPane         string `json:"tmuxPane"`
	Tmux             string `json:"tmux"`
	Cwd              string `json:"cwd"`
	AnthropicBaseURL string `json:"anthropicBaseUrl"`
	Seen             int64  `json:"seen,omitempty"`
	// Read is the time of a read report: serve's word that the phone read the pane at that time.
	Read int64 `json:"read,omitempty"`
	// Input is the time of an input report: serve's word that the web view had input at that time.
	Input int64 `json:"input,omitempty"`
	// View is the key the web view views, as its input report carries it. Only an input report carries a View, and a
	// zero View is omitted.
	View ViewKey `json:"view,omitzero"`
	// Ask names a question the sender puts to orreryd. A question carries Ask and none of Seen, Read, Input or View.
	Ask string `json:"ask,omitempty"`
}

// SessionsAsk is the Ask of the question that asks orreryd whether sessions are on.
const SessionsAsk = "sessions"

// WriteFrame writes frame to w as one JSON object terminated by a newline.
func WriteFrame(w io.Writer, frame HookFrame) error {
	if err := json.NewEncoder(w).Encode(frame); err != nil {
		return fmt.Errorf("write hook frame: %w", err)
	}
	return nil
}

// checkInputReport rejects an input report that carries a seen or a read, or a negative input.
func checkInputReport(frame HookFrame) error {
	if frame.Seen != 0 || frame.Read != 0 {
		return errors.New("read hook frame: an input report carries no seen or read")
	}
	if frame.Input < 0 {
		return errors.New("read hook frame: input must be positive")
	}
	return nil
}

// checkReadReport rejects a read report that carries a seen, or a negative read.
func checkReadReport(frame HookFrame) error {
	if frame.Seen != 0 {
		return errors.New("read hook frame: a read report carries no seen")
	}
	if frame.Read < 0 {
		return errors.New("read hook frame: read must be positive")
	}
	return nil
}

// checkView rejects a view that rides on no input report, or that names more than one key. A frame with no view passes.
func checkView(frame HookFrame) error {
	if frame.View.fields() == 0 {
		return nil
	}
	if frame.Input == 0 {
		return errors.New("read hook frame: a viewing key rides only an input report")
	}
	if frame.View.fields() > 1 {
		return errors.New("read hook frame: a viewing key names one pane, session or epic")
	}
	return nil
}

// checkSessionsQuestion rejects a question whose ask is not SessionsAsk, and a question that carries a seen, a read, an
// input or a view.
func checkSessionsQuestion(frame HookFrame) error {
	if frame.Ask != SessionsAsk {
		return fmt.Errorf("read hook frame: unknown ask %q", frame.Ask)
	}
	if frame.Seen != 0 || frame.Read != 0 || frame.Input != 0 || frame.View.fields() != 0 {
		return errors.New("read hook frame: a sessions question carries no seen, read, input or view")
	}
	return nil
}

// ReadFrame decodes one frame from r with no line-length limit. It accepts a seen report, which has a positive seen,
// a read report, which has a positive read and no seen, an input report, which has a positive input and neither a
// seen nor a read, and which may carry a view naming one key, and a sessions question, which has ask set to
// SessionsAsk and none of seen, read, input or view.
// It rejects malformed JSON, a frame that carries none of seen, read, input or ask, a frame that carries read together
// with a seen, or with a negative read, a frame that carries input together with a seen or a read, or with a negative
// input, a frame that carries a view without an input, or a view that names more than one key, a frame whose ask is
// not SessionsAsk, and a frame that carries ask together with a seen, a read, an input or a view.
func ReadFrame(r io.Reader) (HookFrame, error) {
	var frame HookFrame
	if err := json.NewDecoder(r).Decode(&frame); err != nil {
		return HookFrame{}, fmt.Errorf("read hook frame: %w", err)
	}
	if err := checkView(frame); err != nil {
		return HookFrame{}, err
	}
	if frame.Ask != "" {
		if err := checkSessionsQuestion(frame); err != nil {
			return HookFrame{}, err
		}
		return frame, nil
	}
	if frame.Input != 0 {
		if err := checkInputReport(frame); err != nil {
			return HookFrame{}, err
		}
		return frame, nil
	}
	if frame.Read != 0 {
		if err := checkReadReport(frame); err != nil {
			return HookFrame{}, err
		}
		return frame, nil
	}
	if frame.Seen > 0 {
		return frame, nil
	}
	return HookFrame{}, errors.New("read hook frame: a frame carries none of seen, read, input or ask")
}

// sessionsAnswer is the answer to the sessions question: one JSON object whose sessions key is a boolean.
type sessionsAnswer struct {
	Sessions *bool `json:"sessions"`
}

// WriteSessionsAnswer writes the answer to the sessions question to w as one JSON object terminated by a newline.
func WriteSessionsAnswer(w io.Writer, on bool) error {
	if err := json.NewEncoder(w).Encode(sessionsAnswer{Sessions: &on}); err != nil {
		return fmt.Errorf("write sessions answer: %w", err)
	}
	return nil
}

// ReadSessionsAnswer decodes one answer to the sessions question from r. It returns an error on end of input,
// on malformed JSON, and on an object without a boolean sessions key.
func ReadSessionsAnswer(r io.Reader) (bool, error) {
	var answer sessionsAnswer
	if err := json.NewDecoder(r).Decode(&answer); err != nil {
		return false, fmt.Errorf("read sessions answer: %w", err)
	}
	if answer.Sessions == nil {
		return false, errors.New("read sessions answer: answer has no boolean sessions key")
	}
	return *answer.Sessions, nil
}
