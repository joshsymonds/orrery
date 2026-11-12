package donejudge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/route"
)

// Synthetic values only; no test reaches the real claude binary.
const (
	exampleContext = "example-context"
	exampleBaseURL = "https://patchbay.example.com/ctx/example-context"
	exampleModel   = "example-model"
	exampleMessage = "example final message"
	judgeErrPrefix = "done judge: "
	judgeSchema    = `{"type":"object","additionalProperties":false,"required":["needed","why"],"properties":{"needed":{"type":"boolean"},"why":{"type":"string"}}}`
)

// errExample is the failure a fake caller returns.
var errExample = errors.New("example caller failure")

// call is what one fake CallVia call recorded.
type call struct {
	baseURL, model, schema, input string
}

// fakeCaller is a Caller that records its calls and returns a canned reply and error.
type fakeCaller struct {
	reply json.RawMessage
	err   error
	calls []call
}

func (f *fakeCaller) CallVia(_ context.Context, baseURL, model, schema, input string) (json.RawMessage, error) {
	f.calls = append(f.calls, call{baseURL: baseURL, model: model, schema: schema, input: input})
	return f.reply, f.err
}

func TestDonePassesPaneBaseURLAndModel(t *testing.T) {
	t.Parallel()
	models := map[string]string{exampleContext: exampleModel}
	tests := []struct {
		name      string
		baseURL   string
		wantModel string
	}{
		{"mapped context", exampleBaseURL, exampleModel},
		{"unmapped context", "https://patchbay.example.com/ctx/other-context", protocol.DefaultSummaryModel},
		{"no context", "https://api.example.com", protocol.DefaultSummaryModel},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{reply: json.RawMessage(`{"needed":false,"why":"finished"}`)}
			if _, err := New(caller, models).Done(t.Context(), tc.baseURL, exampleMessage); err != nil {
				t.Fatalf("Done: %v", err)
			}
			if len(caller.calls) != 1 {
				t.Fatalf("CallVia calls = %d, want 1", len(caller.calls))
			}
			got := caller.calls[0]
			if got.baseURL != tc.baseURL {
				t.Errorf("baseURL = %q, want %q", got.baseURL, tc.baseURL)
			}
			if got.model != tc.wantModel {
				t.Errorf("model = %q, want %q", got.model, tc.wantModel)
			}
			if got.schema != judgeSchema {
				t.Errorf("schema = %q, want %q", got.schema, judgeSchema)
			}
			if want := instruction + "\n\n" + exampleMessage; got.input != want {
				t.Errorf("input = %q, want %q", got.input, want)
			}
		})
	}
}

func TestDoneReturnsVerdict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reply string
		want  route.Verdict
	}{
		{
			name:  "finished work needs nothing",
			reply: `{"needed":false,"why":"finished work that needs nothing"}`,
			want:  route.Verdict{Needed: false, Why: "finished work that needs nothing"},
		},
		{
			name:  "asks the user",
			reply: `{"needed":true,"why":"asks which branch to merge"}`,
			want:  route.Verdict{Needed: true, Why: "asks which branch to merge"},
		},
		{
			name:  "why is trimmed with newlines turned into spaces",
			reply: `{"needed":true,"why":"  asks which branch\nto merge \n"}`,
			want:  route.Verdict{Needed: true, Why: "asks which branch to merge"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{reply: json.RawMessage(tc.reply)}
			got, err := New(caller, nil).Done(t.Context(), exampleBaseURL, exampleMessage)
			if err != nil {
				t.Fatalf("Done: %v", err)
			}
			if got != tc.want {
				t.Errorf("Done = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDoneEmptyMessageErrorsWithoutCall(t *testing.T) {
	t.Parallel()
	for _, message := range []string{"", " \n\t "} {
		caller := &fakeCaller{reply: json.RawMessage(`{"needed":false,"why":"unused"}`)}
		_, err := New(caller, nil).Done(t.Context(), exampleBaseURL, message)
		if err == nil {
			t.Fatalf("Done(%q): want error, got nil", message)
		}
		if !strings.HasPrefix(err.Error(), judgeErrPrefix) {
			t.Errorf("Done(%q) error = %q, want prefix %q", message, err, judgeErrPrefix)
		}
		if len(caller.calls) != 0 {
			t.Errorf("Done(%q) made %d calls, want none", message, len(caller.calls))
		}
	}
}

func TestDoneCallerErrorIsWrapped(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{err: errExample}
	_, err := New(caller, nil).Done(t.Context(), exampleBaseURL, exampleMessage)
	if !errors.Is(err, errExample) {
		t.Fatalf("Done error = %v, want it to wrap %v", err, errExample)
	}
	if !strings.HasPrefix(err.Error(), judgeErrPrefix) {
		t.Errorf("Done error = %q, want prefix %q", err, judgeErrPrefix)
	}
}

func TestDoneRejectsBadVerdict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reply string
	}{
		{"not json", `not json`},
		{"empty reply", ``},
		{"null reply", `null`},
		{"missing needed", `{"why":"no verdict"}`},
		{"needed not boolean", `{"needed":"yes","why":"string verdict"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{reply: json.RawMessage(tc.reply)}
			_, err := New(caller, nil).Done(t.Context(), exampleBaseURL, exampleMessage)
			if err == nil {
				t.Fatal("Done: want error, got nil")
			}
			if !strings.HasPrefix(err.Error(), judgeErrPrefix) {
				t.Errorf("Done error = %q, want prefix %q", err, judgeErrPrefix)
			}
		})
	}
}
