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
)

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

// DefaultSummaryModel is the summary model used when the patchbay context has no mapping.
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

// HookFrame is the single frame a sender writes to orreryd per connection. A Claude Code hook frame carries Hook.
// A seen report carries Seen and no Hook.
type HookFrame struct {
	Hook             json.RawMessage `json:"hook,omitempty"`
	TmuxPane         string          `json:"tmuxPane"`
	Tmux             string          `json:"tmux"`
	Cwd              string          `json:"cwd"`
	AnthropicBaseURL string          `json:"anthropicBaseUrl"`
	Seen             int64           `json:"seen,omitempty"`
}

// WriteFrame writes frame to w as one JSON object terminated by a newline.
func WriteFrame(w io.Writer, frame HookFrame) error {
	if err := json.NewEncoder(w).Encode(frame); err != nil {
		return fmt.Errorf("write hook frame: %w", err)
	}
	return nil
}

// ReadFrame decodes one frame from r with no line-length limit. It accepts a hook frame, whose hook is a JSON
// object, and a seen report, which has a positive seen and no hook. It rejects malformed JSON, a hook that is null
// or not a JSON object, a frame with neither a hook nor a positive seen, and a frame with both a hook and a seen.
func ReadFrame(r io.Reader) (HookFrame, error) {
	var frame HookFrame
	if err := json.NewDecoder(r).Decode(&frame); err != nil {
		return HookFrame{}, fmt.Errorf("read hook frame: %w", err)
	}
	if frame.Hook == nil {
		if frame.Seen > 0 {
			return frame, nil
		}
		return HookFrame{}, errors.New("read hook frame: hook must be a JSON object")
	}
	hook := bytes.TrimSpace(frame.Hook)
	if len(hook) == 0 || hook[0] != '{' {
		return HookFrame{}, errors.New("read hook frame: hook must be a JSON object")
	}
	if frame.Seen != 0 {
		return HookFrame{}, errors.New("read hook frame: a frame carries a hook or a seen report, not both")
	}
	return frame, nil
}
