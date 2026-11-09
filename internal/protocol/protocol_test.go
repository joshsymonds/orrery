package protocol_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

const hookPayloadBytes = 1 << 20

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func sortedKeys(t *testing.T, data []byte) []string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	return slices.Sorted(maps.Keys(obj))
}

type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestFrozenNames(t *testing.T) {
	cases := []struct {
		name, got, want string
	}{
		{"StateWorking", string(protocol.StateWorking), "working"},
		{"StateWaiting", string(protocol.StateWaiting), "waiting"},
		{"StateDone", string(protocol.StateDone), "done"},
		{"OptionState", protocol.OptionState, "@orrery_state"},
		{"OptionChanged", protocol.OptionChanged, "@orrery_changed"},
		{"OptionSeen", protocol.OptionSeen, "@orrery_seen"},
		{"OptionQuestion", protocol.OptionQuestion, "@orrery_question"},
		{"OptionSummary", protocol.OptionSummary, "@orrery_summary"},
		{"OptionLabel", protocol.OptionLabel, "@orrery_label"},
		{"OptionContext", protocol.OptionContext, "@orrery_context"},
		{"OptionDir", protocol.OptionDir, "@orrery_dir"},
		{"OptionClaudeSession", protocol.OptionClaudeSession, "@orrery_claude_session"},
		{"OptionTranscript", protocol.OptionTranscript, "@orrery_transcript"},
		{"ShellSessionPrefix", protocol.ShellSessionPrefix, "_orrery-shell-"},
		{"EnvSocket", protocol.EnvSocket, "ORRERY_SOCKET"},
		{"EnvNtfyURLFile", protocol.EnvNtfyURLFile, "ORRERY_NTFY_URL_FILE"},
		{"EnvNtfyAuthFile", protocol.EnvNtfyAuthFile, "ORRERY_NTFY_TOKEN_FILE"},
		{"EnvSummarizer", protocol.EnvSummarizer, "ORRERY_SUMMARIZER"},
		{"SummarizerValue", protocol.SummarizerValue, "1"},
		{"DefaultSummaryModel", protocol.DefaultSummaryModel, "claude-haiku-5-5"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestParseState(t *testing.T) {
	valid := []string{"working", "waiting", "done"}
	for _, input := range valid {
		got, err := protocol.ParseState(input)
		if err != nil {
			t.Fatalf("ParseState(%q) error: %v", input, err)
		}
		if string(got) != input {
			t.Errorf("ParseState(%q) = %q", input, got)
		}
	}

	invalid := []string{"", "Working", "DONE", "done ", "idle"}
	for _, input := range invalid {
		if _, err := protocol.ParseState(input); err == nil {
			t.Errorf("ParseState(%q) returned no error", input)
		}
	}
}

func TestPaneJSONKeys(t *testing.T) {
	data, err := json.Marshal(protocol.Pane{})
	if err != nil {
		t.Fatalf("marshal zero Pane: %v", err)
	}
	want := []string{
		"activity", "changed", "claude_session", "command", "context", "cwd", "host", "id",
		"label", "pane", "question", "seen", "session", "state", "summary", "window",
	}
	if got := sortedKeys(t, data); !slices.Equal(got, want) {
		t.Errorf("Pane keys = %v, want %v", got, want)
	}
}

func TestPaneWireFormat(t *testing.T) {
	pane := protocol.Pane{
		ID:            "%3",
		Host:          "host-a",
		Session:       "work",
		WindowIndex:   1,
		PaneIndex:     2,
		Cwd:           "/home/u/proj",
		Command:       "claude",
		State:         protocol.StateWaiting,
		Summary:       "waiting on review",
		Label:         "proj",
		Changed:       100,
		Seen:          200,
		Question:      "ship it?",
		Context:       "example-context",
		ClaudeSession: "example-claude-session",
		Activity:      1700000300,
	}
	data, err := json.Marshal(pane)
	if err != nil {
		t.Fatalf("marshal Pane: %v", err)
	}
	want := `{"id":"%3","host":"host-a","session":"work","window":1,"pane":2,` +
		`"cwd":"/home/u/proj","command":"claude","state":"waiting",` +
		`"summary":"waiting on review","label":"proj","changed":100,"seen":200,` +
		`"question":"ship it?","context":"example-context",` +
		`"activity":1700000300,"claude_session":"example-claude-session"}`
	if string(data) != want {
		t.Errorf("Pane JSON =\n%s\nwant\n%s", data, want)
	}
}

func TestPaneJSONRoundTrip(t *testing.T) {
	want := protocol.Pane{
		ID:            "%3",
		Host:          "host-a",
		Session:       "work",
		WindowIndex:   1,
		PaneIndex:     2,
		Cwd:           "/home/u/proj",
		Command:       "claude",
		State:         protocol.StateWaiting,
		Summary:       "waiting on review",
		Label:         "proj",
		Changed:       100,
		Seen:          200,
		Question:      "ship it?",
		Context:       "example-context",
		ClaudeSession: `example "quoted" session`,
		Activity:      1700000300,
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal Pane: %v", err)
	}
	var got protocol.Pane
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal Pane %s: %v", data, err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestPaneUnmarshalMissingClaudeSession(t *testing.T) {
	var pane protocol.Pane
	if err := json.Unmarshal([]byte(`{"id":"%3","activity":5}`), &pane); err != nil {
		t.Fatalf("unmarshal Pane without claude_session: %v", err)
	}
	if pane.ClaudeSession != "" || pane.ID != "%3" || pane.Activity != 5 {
		t.Errorf("Pane = %+v, want ID %%3, Activity 5, empty ClaudeSession", pane)
	}
}

func TestPaneUnmarshalRejectsNonStringClaudeSession(t *testing.T) {
	inputs := []string{
		`{"claude_session":1}`,
		`{"claude_session":null}`,
		`{"claude_session":["example"]}`,
	}
	for _, input := range inputs {
		var pane protocol.Pane
		if err := json.Unmarshal([]byte(input), &pane); err == nil {
			t.Errorf("Unmarshal(%s) returned no error", input)
		}
	}
}

func TestHookFrameJSONKeys(t *testing.T) {
	data, err := json.Marshal(protocol.HookFrame{Hook: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("marshal HookFrame: %v", err)
	}
	want := []string{"anthropicBaseUrl", "cwd", "hook", "tmux", "tmuxPane"}
	if got := sortedKeys(t, data); !slices.Equal(got, want) {
		t.Errorf("HookFrame keys = %v, want %v", got, want)
	}
}

func TestWriteFrameWireFormat(t *testing.T) {
	frame := protocol.HookFrame{
		Hook:             json.RawMessage(`{"hook_event_name":"Stop"}`),
		TmuxPane:         "%1",
		Tmux:             "/tmp/tmux-1000/default,123,0",
		Cwd:              "/home/u/proj",
		AnthropicBaseURL: "https://patchbay.example/ctx/example",
	}
	var buf bytes.Buffer
	if err := protocol.WriteFrame(&buf, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	want := `{"hook":{"hook_event_name":"Stop"},"tmuxPane":"%1",` +
		`"tmux":"/tmp/tmux-1000/default,123,0","cwd":"/home/u/proj",` +
		`"anthropicBaseUrl":"https://patchbay.example/ctx/example"}` + "\n"
	if buf.String() != want {
		t.Errorf("WriteFrame wrote\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestWriteFrameReportsWriterError(t *testing.T) {
	frame := protocol.HookFrame{Hook: json.RawMessage(`{}`)}
	if err := protocol.WriteFrame(failingWriter{}, frame); err == nil {
		t.Error("WriteFrame returned no error for a failing writer")
	}
}

func TestWriteFrameRejectsInvalidHook(t *testing.T) {
	frame := protocol.HookFrame{Hook: json.RawMessage(`{bad`)}
	var buf bytes.Buffer
	if err := protocol.WriteFrame(&buf, frame); err == nil {
		t.Error("WriteFrame returned no error for invalid hook JSON")
	}
}

func TestFrameRoundTrip(t *testing.T) {
	want := protocol.HookFrame{
		Hook:             json.RawMessage(`{"hook_event_name":"Stop","session_id":"s1"}`),
		TmuxPane:         "%3",
		Tmux:             "/tmp/tmux-1000/default,123,0",
		Cwd:              "/home/u/proj",
		AnthropicBaseURL: "https://patchbay.example/ctx/example",
	}
	var buf bytes.Buffer
	if err := protocol.WriteFrame(&buf, want); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := protocol.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got.Hook, want.Hook) {
		t.Errorf("Hook = %s, want %s", got.Hook, want.Hook)
	}
	if got.TmuxPane != want.TmuxPane || got.Tmux != want.Tmux ||
		got.Cwd != want.Cwd || got.AnthropicBaseURL != want.AnthropicBaseURL {
		t.Errorf("round trip changed fields: got %+v, want %+v", got, want)
	}
}

func TestFrameIsOneLine(t *testing.T) {
	frame := protocol.HookFrame{Hook: json.RawMessage(`{"message":"line one\nline two"}`)}
	var buf bytes.Buffer
	if err := protocol.WriteFrame(&buf, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if n := strings.Count(buf.String(), "\n"); n != 1 || !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("frame has %d newlines, want exactly one trailing newline: %q", n, buf.String())
	}
}

func TestReadFrameAcceptsLargePayload(t *testing.T) {
	large := strings.Repeat("a", hookPayloadBytes)
	frame := protocol.HookFrame{Hook: json.RawMessage(`{"text":"` + large + `"}`)}
	var buf bytes.Buffer
	if err := protocol.WriteFrame(&buf, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := protocol.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got.Hook, frame.Hook) {
		t.Errorf("large hook payload changed in round trip (%d bytes)", len(got.Hook))
	}
}

func TestReadFrameRejects(t *testing.T) {
	cases := map[string]string{
		"empty input":      ``,
		"malformed json":   `{"hook":`,
		"missing hook":     `{"tmuxPane":"%1","cwd":"/c"}`,
		"null hook":        `{"hook":null,"tmuxPane":"%1"}`,
		"string hook":      `{"hook":"stop"}`,
		"array hook":       `{"hook":[1,2]}`,
		"number hook":      `{"hook":1}`,
		"boolean hook":     `{"hook":true}`,
		"non-object root":  `["hook"]`,
		"wrong field type": `{"hook":{},"tmuxPane":7}`,
	}
	for name, input := range cases {
		if _, err := protocol.ReadFrame(strings.NewReader(input)); err == nil {
			t.Errorf("%s: ReadFrame(%q) returned no error", name, input)
		}
	}
}

func TestReadFrameAcceptsObjectHook(t *testing.T) {
	input := `{"hook":{"hook_event_name":"Notification"},"tmuxPane":"%2","tmux":"t","cwd":"/c","anthropicBaseUrl":"u"}`
	got, err := protocol.ReadFrame(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got.Hook) != `{"hook_event_name":"Notification"}` {
		t.Errorf("Hook = %s", got.Hook)
	}
	if got.TmuxPane != "%2" || got.Tmux != "t" || got.Cwd != "/c" || got.AnthropicBaseURL != "u" {
		t.Errorf("fields = %+v", got)
	}
}

func TestSocketPath(t *testing.T) {
	cases := []struct {
		name string
		flag string
		env  map[string]string
		want string
	}{
		{
			name: "flag wins over env and XDG",
			flag: "/flag.sock",
			env:  map[string]string{"ORRERY_SOCKET": "/env.sock", "XDG_RUNTIME_DIR": "/run/user/1000"},
			want: "/flag.sock",
		},
		{
			name: "env wins over XDG",
			env:  map[string]string{"ORRERY_SOCKET": "/env.sock", "XDG_RUNTIME_DIR": "/run/user/1000"},
			want: "/env.sock",
		},
		{
			name: "XDG runtime dir fallback",
			env:  map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"},
			want: "/run/user/1000/orrery/orreryd.sock",
		},
	}
	for _, tc := range cases {
		got, err := protocol.SocketPath(tc.flag, env(tc.env))
		if err != nil {
			t.Errorf("%s: error %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSocketPathErrorsWhenUnset(t *testing.T) {
	if got, err := protocol.SocketPath("", env(nil)); err == nil {
		t.Errorf("SocketPath with nothing set = %q, want error", got)
	}
}

func TestIsSummarizer(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"", false},
		{"0", false},
		{"true", false},
		{" 1", false},
	}
	for _, tc := range cases {
		got := protocol.IsSummarizer(env(map[string]string{"ORRERY_SUMMARIZER": tc.value}))
		if got != tc.want {
			t.Errorf("IsSummarizer(%q) = %t, want %t", tc.value, got, tc.want)
		}
	}
}

func TestSummaryContext(t *testing.T) {
	cases := map[string]string{
		"https://patchbay.example/ctx/example":      "example",
		"https://patchbay.example/ctx/example/v1/m": "example",
		"https://patchbay.example/api":              "",
		"https://patchbay.example/ctx":              "",
		"https://patchbay.example/ctx/":             "",
		"https://patchbay.example/xctx/example":     "",
		"https://patchbay.example/a/ctx/work":       "work",
		"http://[::1":                               "",
		"":                                          "",
	}
	for input, want := range cases {
		if got := protocol.SummaryContext(input); got != want {
			t.Errorf("SummaryContext(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestQuestionHash(t *testing.T) {
	cases := []struct {
		name, question, want string
	}{
		{
			"fixed vector",
			"Example question for the user?",
			"7f593eeb8a031e6a99b02a9b65b8e03a17a82a20ca4862ecda37bd06c479a5eb",
		},
		{
			"unset question",
			"",
			"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}
	for _, tc := range cases {
		if got := protocol.QuestionHash(tc.question); got != tc.want {
			t.Errorf("%s: QuestionHash(%q) = %q, want %q", tc.name, tc.question, got, tc.want)
		}
	}
}

func TestQuestionHashIsSHA256OfRawBytes(t *testing.T) {
	// Surrounding whitespace is part of the stored value and must not be trimmed.
	for _, question := range []string{" padded question? ", "line one\nline two", "Ünïcode?"} {
		sum := sha256.Sum256([]byte(question))
		want := hex.EncodeToString(sum[:])
		if got := protocol.QuestionHash(question); got != want {
			t.Errorf("QuestionHash(%q) = %q, want %q", question, got, want)
		}
	}
}

func TestSummaryModel(t *testing.T) {
	models := map[string]string{
		"example": "model-example",
		"blank":   "",
		"":        "model-empty-key",
	}
	cases := []struct {
		name    string
		baseURL string
		models  map[string]string
		want    string
	}{
		{"mapped context", "https://p.example/ctx/example/v1", models, "model-example"},
		{"unmapped context", "https://p.example/ctx/other", models, protocol.DefaultSummaryModel},
		{"mapped to empty", "https://p.example/ctx/blank", models, protocol.DefaultSummaryModel},
		{"no context", "https://p.example/api", models, protocol.DefaultSummaryModel},
		{
			"empty context key ignored",
			"https://p.example/api",
			map[string]string{"": "x"},
			protocol.DefaultSummaryModel,
		},
		{"nil map", "https://p.example/ctx/example", nil, protocol.DefaultSummaryModel},
	}
	for _, tc := range cases {
		if got := protocol.SummaryModel(tc.baseURL, tc.models); got != tc.want {
			t.Errorf("%s: SummaryModel = %q, want %q", tc.name, got, tc.want)
		}
	}
}
