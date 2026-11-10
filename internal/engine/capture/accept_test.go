package capture

import (
	"reflect"
	"strings"
	"testing"
)

// acceptStream is a clean stream in declared shapes: an init, an assistant turn, a permission request
// with its success response, and two successful results.
const acceptStream = `{"type":"system","subtype":"init"}
{"type":"assistant"}
{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool"}}
{"type":"control_response","response":{"subtype":"success","request_id":"req_1"}}
{"type":"result","subtype":"success"}
{"type":"result","subtype":"success"}
`

func acceptCleanExpect() []Expectation {
	return []Expectation{
		{Type: "system", Subtype: "init"},
		{Type: "assistant"},
		{Type: "control_request", Subtype: "can_use_tool"},
		{Type: "control_response", Subtype: "success"},
		{Type: "result", Count: 2},
	}
}

func TestCheckCleanStreamReportsNothing(t *testing.T) {
	t.Parallel()
	report, err := Check([]byte(acceptStream), acceptCleanExpect())
	if err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(report, Report{}) {
		t.Errorf("Check() = %+v, want empty Report", report)
	}
}

func TestCheckReportsMissingAndUnderCount(t *testing.T) {
	t.Parallel()
	stdout := `{"type":"assistant"}
{"type":"result","subtype":"success"}
{"type":"control_request","request_id":"req_1","request":{"subtype":"elicitation"}}
`
	expect := []Expectation{
		{Type: "assistant"},
		{Type: "result", Subtype: "success", Count: 2},
		{Type: "system", Subtype: "init"},
		{Type: "control_request", Subtype: "can_use_tool"},
	}
	report, err := Check([]byte(stdout), expect)
	if err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}
	want := []Expectation{
		{Type: "result", Subtype: "success", Count: 2},
		{Type: "system", Subtype: "init"},
		{Type: "control_request", Subtype: "can_use_tool"},
	}
	if !reflect.DeepEqual(report.Missing, want) {
		t.Errorf("Check() Missing = %+v, want %+v", report.Missing, want)
	}
}

func TestCheckRejectsLineThatIsNotAnEnvelope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stdout string
		line   string
	}{
		{name: "not JSON", stdout: "{\"type\":\"assistant\"}\nnot json\n", line: "line 2:"},
		{name: "JSON array", stdout: "[]\n", line: "line 1:"},
		{name: "missing type", stdout: `{"subtype":"init"}` + "\n", line: "line 1:"},
		{name: "empty type", stdout: `{"type":""}` + "\n", line: "line 1:"},
		{name: "non-string type", stdout: `{"type":5}` + "\n", line: "line 1:"},
		{
			name:   "blank line before the end",
			stdout: "{\"type\":\"assistant\"}\n\n{\"type\":\"assistant\"}\n",
			line:   "line 2:",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Check([]byte(test.stdout), nil)
			if err == nil {
				t.Fatalf("Check() error = nil, want an error naming %q", test.line)
			}
			if !strings.Contains(err.Error(), test.line) {
				t.Errorf("Check() error = %v, want it to name %q", err, test.line)
			}
		})
	}
}

func TestCheckIgnoresOnlyATrailingEmptyLine(t *testing.T) {
	t.Parallel()
	for _, stdout := range []string{"{\"type\":\"assistant\"}", "{\"type\":\"assistant\"}\n"} {
		report, err := Check([]byte(stdout), []Expectation{{Type: "assistant"}})
		if err != nil {
			t.Fatalf("Check(%q) error = %v, want nil", stdout, err)
		}
		if len(report.Missing) != 0 {
			t.Errorf("Check(%q) Missing = %+v, want none", stdout, report.Missing)
		}
	}
}

func TestCheckListsUnknownTypesOnceSorted(t *testing.T) {
	t.Parallel()
	stdout := `{"type":"not_declared_type"}
{"type":"not_declared_type"}
{"type":"system","subtype":"not_declared_subtype"}
{"type":"result","subtype":"not_declared_result"}
{"type":"control_request","request_id":"req_1","request":{"subtype":"not_declared_control"}}
{"type":"control_request","request_id":"req_2","request":{"subtype":"can_use_tool"}}
{"type":"control_response","response":{"subtype":"error","request_id":"req_2"}}
{"type":"assistant"}
`
	report, err := Check([]byte(stdout), nil)
	if err != nil {
		t.Fatalf("Check() error = %v, want nil: unknown types never fail a check", err)
	}
	want := []string{
		"control_request/not_declared_control",
		"control_response/error",
		"not_declared_type",
		"result/not_declared_result",
		"system/not_declared_subtype",
	}
	if !reflect.DeepEqual(report.Unknown, want) {
		t.Errorf("Check() Unknown = %q, want %q", report.Unknown, want)
	}
}

func TestCheckContainsMatchesRawLineText(t *testing.T) {
	t.Parallel()
	const guard = "GUARD REFUSED"
	userGuard := `{"type":"user","message":{"role":"user","content":"` + guard + ` rule 7"}}` + "\n"
	userFatal := `{"type":"user","message":{"role":"user","content":"fatal: not a git repository"}}` + "\n"
	assistantGuard := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + guard + `"}]}}` + "\n"
	tests := []struct {
		name    string
		stdout  string
		expect  []Expectation
		missing []Expectation
	}{
		{
			name:   "user line with the wanted text satisfies",
			stdout: userGuard,
			expect: []Expectation{{Type: "user", Contains: guard}},
		},
		{
			name:    "user line with other error text is missing",
			stdout:  userFatal,
			expect:  []Expectation{{Type: "user", Contains: guard}},
			missing: []Expectation{{Type: "user", Contains: guard}},
		},
		{
			name:    "wanted text on another type does not count",
			stdout:  assistantGuard,
			expect:  []Expectation{{Type: "user", Contains: guard}},
			missing: []Expectation{{Type: "user", Contains: guard}},
		},
		{
			name:    "count applies to contains matches",
			stdout:  userGuard + userFatal,
			expect:  []Expectation{{Type: "user", Contains: guard, Count: 2}},
			missing: []Expectation{{Type: "user", Contains: guard, Count: 2}},
		},
		{
			name:   "count met by contains matches",
			stdout: userGuard + userGuard,
			expect: []Expectation{{Type: "user", Contains: guard, Count: 2}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			report, err := Check([]byte(test.stdout), test.expect)
			if err != nil {
				t.Fatalf("Check() error = %v, want nil", err)
			}
			if !reflect.DeepEqual(report.Missing, test.missing) {
				t.Errorf("Check() Missing = %+v, want %+v", report.Missing, test.missing)
			}
		})
	}
}

func TestSessionIDReturnsFirstInitLine(t *testing.T) {
	t.Parallel()
	stdout := `{"type":"system","subtype":"init","session_id":"sess-first"}
{"type":"assistant","session_id":"sess-assistant"}
{"type":"system","subtype":"init","session_id":"sess-second"}
{"type":"result","subtype":"success","session_id":"sess-result"}
`
	id, err := SessionID([]byte(stdout))
	if err != nil {
		t.Fatalf("SessionID() error = %v, want nil", err)
	}
	if id != "sess-first" {
		t.Errorf("SessionID() = %q, want %q", id, "sess-first")
	}
}

func TestSessionIDErrorsWithoutInitLine(t *testing.T) {
	t.Parallel()
	stdout := `{"type":"assistant","session_id":"sess-assistant"}
{"type":"result","subtype":"success","session_id":"sess-result"}
`
	if _, err := SessionID([]byte(stdout)); err == nil {
		t.Fatal("SessionID() error = nil, want an error for a stream with no system init line")
	}
}

func TestSessionIDErrorsWhenInitHasNoSessionID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stdout string
	}{
		{name: "absent", stdout: `{"type":"system","subtype":"init"}` + "\n"},
		{name: "empty", stdout: `{"type":"system","subtype":"init","session_id":""}` + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			id, err := SessionID([]byte(test.stdout))
			if err == nil {
				t.Fatalf("SessionID() = %q, want an error", id)
			}
			if !strings.Contains(err.Error(), "no session_id") {
				t.Errorf("SessionID() error = %v, want it to say the init line has no session_id", err)
			}
		})
	}
}

func TestSessionIDErrorsNamingNonJSONLine(t *testing.T) {
	t.Parallel()
	stdout := "{\"type\":\"assistant\"}\nnot json\n{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"sess-first\"}\n"
	_, err := SessionID([]byte(stdout))
	if err == nil {
		t.Fatal("SessionID() error = nil, want an error for a line that is not JSON")
	}
	if !strings.Contains(err.Error(), "line 2:") {
		t.Errorf("SessionID() error = %v, want it to name %q", err, "line 2:")
	}
}
