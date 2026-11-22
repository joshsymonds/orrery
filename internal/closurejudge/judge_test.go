package closurejudge

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Synthetic values only; no test reaches the real claude binary.
const (
	exampleContext = "example-context"
	exampleBaseURL = "https://proxy.example.com/ctx/example-context"
	exampleModel   = "example-model"
	exampleRecent  = "example recent text"
	judgeErrPrefix = "closure judge: "
	judgeSchema    = `{"type":"object","additionalProperties":false,"required":["closed","why"],"properties":{"closed":{"type":"boolean"},"why":{"type":"string"}}}`
)

// errExample is the failure a fake caller returns.
var errExample = errors.New("example caller failure")

// call is what one fake CallVia call recorded.
type call struct {
	baseURL, model string
	env            map[string]string
	schema, input  string
}

// fakeCaller is a Caller that records its calls and returns a canned reply and error.
type fakeCaller struct {
	reply json.RawMessage
	err   error
	calls []call
}

func (f *fakeCaller) CallVia(
	_ context.Context, baseURL, model string, env map[string]string, schema, input string,
) (json.RawMessage, error) {
	f.calls = append(f.calls, call{baseURL: baseURL, model: model, env: env, schema: schema, input: input})
	return f.reply, f.err
}

func TestInboxClosureJudgePassesBaseURLEnvModelSchemaAndInput(t *testing.T) {
	t.Parallel()
	env := map[string]string{"ANTHROPIC_BASE_URL": exampleBaseURL, "CTX_EXAMPLE": exampleContext}
	models := map[string]string{exampleContext: exampleModel}
	tests := []struct {
		name      string
		baseURL   string
		wantModel string
	}{
		{"mapped context", exampleBaseURL, exampleModel},
		{"unmapped context", "https://proxy.example.com/ctx/other-context", protocol.DefaultSummaryModel},
		{"no context", "https://api.example.com", protocol.DefaultSummaryModel},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{reply: json.RawMessage(`{"closed":false,"why":"unfinished"}`)}
			if _, err := New(caller, models).Closed(t.Context(), tc.baseURL, env, exampleRecent); err != nil {
				t.Fatalf("Closed: %v", err)
			}
			if len(caller.calls) != 1 {
				t.Fatalf("CallVia calls = %d, want 1", len(caller.calls))
			}
			got := caller.calls[0]
			if got.baseURL != tc.baseURL {
				t.Errorf("baseURL = %q, want %q", got.baseURL, tc.baseURL)
			}
			if !maps.Equal(got.env, env) {
				t.Errorf("env = %v, want %v", got.env, env)
			}
			if got.model != tc.wantModel {
				t.Errorf("model = %q, want %q", got.model, tc.wantModel)
			}
			if got.schema != judgeSchema {
				t.Errorf("schema = %q, want %q", got.schema, judgeSchema)
			}
			if want := Instruction + "\n\n" + exampleRecent; got.input != want {
				t.Errorf("input = %q, want %q", got.input, want)
			}
		})
	}
}

func TestInboxClosureJudgeInstructionIsNotTheDoneInstruction(t *testing.T) {
	t.Parallel()
	if strings.HasPrefix(Instruction, "Judge whether a coding agent's final message") {
		t.Errorf("Instruction = %q, must differ from the done judge's instruction", Instruction)
	}
}

func TestInboxClosureJudgeReturnsVerdict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reply string
		want  Verdict
	}{
		{
			name:  "closed book needs nothing",
			reply: `{"closed":true,"why":"user ended the conversation"}`,
			want:  Verdict{Closed: true, Why: "user ended the conversation"},
		},
		{
			name:  "open conversation",
			reply: `{"closed":false,"why":"asks which branch to merge"}`,
			want:  Verdict{Closed: false, Why: "asks which branch to merge"},
		},
		{
			name:  "why is trimmed with newlines turned into spaces",
			reply: `{"closed":false,"why":"  asks which branch\nto merge \n"}`,
			want:  Verdict{Closed: false, Why: "asks which branch to merge"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{reply: json.RawMessage(tc.reply)}
			got, err := New(caller, nil).Closed(t.Context(), exampleBaseURL, nil, exampleRecent)
			if err != nil {
				t.Fatalf("Closed: %v", err)
			}
			if got != tc.want {
				t.Errorf("Closed = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestInboxClosureJudgeBlankRecentErrorsWithoutCall(t *testing.T) {
	t.Parallel()
	for _, recent := range []string{"", " \n\t "} {
		caller := &fakeCaller{reply: json.RawMessage(`{"closed":true,"why":"unused"}`)}
		_, err := New(caller, nil).Closed(t.Context(), exampleBaseURL, nil, recent)
		if err == nil {
			t.Fatalf("Closed(%q): want error, got nil", recent)
		}
		if !strings.HasPrefix(err.Error(), judgeErrPrefix) {
			t.Errorf("Closed(%q) error = %q, want prefix %q", recent, err, judgeErrPrefix)
		}
		if len(caller.calls) != 0 {
			t.Errorf("Closed(%q) made %d calls, want none", recent, len(caller.calls))
		}
	}
}

func TestInboxClosureJudgeCallerErrorIsWrapped(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{err: errExample}
	_, err := New(caller, nil).Closed(t.Context(), exampleBaseURL, nil, exampleRecent)
	if !errors.Is(err, errExample) {
		t.Fatalf("Closed error = %v, want it to wrap %v", err, errExample)
	}
	if !strings.HasPrefix(err.Error(), judgeErrPrefix) {
		t.Errorf("Closed error = %q, want prefix %q", err, judgeErrPrefix)
	}
}

func TestInboxClosureJudgeRejectsBadVerdict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reply string
	}{
		{"not json", `not json`},
		{"empty reply", ``},
		{"null reply", `null`},
		{"missing closed", `{"why":"no verdict"}`},
		{"closed not boolean", `{"closed":"yes","why":"string verdict"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{reply: json.RawMessage(tc.reply)}
			_, err := New(caller, nil).Closed(t.Context(), exampleBaseURL, nil, exampleRecent)
			if err == nil {
				t.Fatal("Closed: want error, got nil")
			}
			if !strings.HasPrefix(err.Error(), judgeErrPrefix) {
				t.Errorf("Closed error = %q, want prefix %q", err, judgeErrPrefix)
			}
		})
	}
}
