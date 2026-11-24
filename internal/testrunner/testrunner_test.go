package testrunner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/enginehost"
)

// fakeEnv returns a getenv that reads vars and reports every other name as unset.
func fakeEnv(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// roleEnv returns a getenv whose XDG_CONFIG_HOME holds a role file with text as its contents.
func roleEnv(t *testing.T, text string) func(string) string {
	t.Helper()
	xdg := t.TempDir()
	path := filepath.Join(xdg, "orrery", "roles.toml")
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return fakeEnv(map[string]string{"XDG_CONFIG_HOME": xdg})
}

func TestTestRunnerRequestsRoleFromRolesFile(t *testing.T) {
	t.Parallel()
	getenv := roleEnv(t, `test_runner = "runner-agent"`+"\n"+`implementer = "impl-agent"`+"\n")
	got, err := Agent(getenv)
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	if got != "runner-agent" {
		t.Fatalf("Agent = %q, want the test_runner key's %q", got, "runner-agent")
	}
}

func TestTestRunnerRequestsRoleFallsBackToImplementer(t *testing.T) {
	t.Parallel()
	got, err := Agent(roleEnv(t, `implementer = "impl-agent"`+"\n"))
	if err != nil {
		t.Fatalf("Agent without a test_runner key: %v", err)
	}
	if got != "impl-agent" {
		t.Fatalf("Agent without a test_runner key = %q, want the implementer's %q", got, "impl-agent")
	}
	if _, err = Agent(roleEnv(t, `planner = "planner-agent"`+"\n")); err == nil {
		t.Fatal("Agent with neither a test_runner nor an implementer key: nil error, want an error")
	}
}

func TestTestRunnerRequestsStartIsScratchAtRevision(t *testing.T) {
	t.Parallel()
	getenv := roleEnv(t, `test_runner = "runner-agent"`+"\n")
	req := Request{Command: "go test ./internal/x", Revision: "abc1234"}
	start, err := Start(getenv, "/repo", "step-id", req, "epic-1", "T7")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := enginehost.StepStart{
		ID:        "step-id",
		Agent:     "runner-agent",
		Prompt:    Prompt(req),
		Schema:    Schema,
		Workspace: "/repo",
		Revision:  "abc1234",
		ReadOnly:  true,
		Scratch:   true,
		DoneValue: "done",
		Epic:      "epic-1",
		Task:      "T7",
	}
	if !reflect.DeepEqual(start, want) {
		t.Fatalf("Start = %+v, want %+v", start, want)
	}
	_, err = Start(fakeEnv(map[string]string{}), "/repo", "step-id", req, "epic-1", "T7")
	if err == nil || errors.Unwrap(err) == nil {
		t.Fatalf("Start without a role file: error %v, want a wrapped role error", err)
	}
}

func TestTestRunnerRequestsEmbeddedContractAndSchema(t *testing.T) {
	t.Parallel()
	var schema struct {
		Type                 string `json:"type"`
		AdditionalProperties *bool  `json:"additionalProperties"`
		Required             []string
		Properties           map[string]struct {
			Type string   `json:"type"`
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(Schema), &schema); err != nil {
		t.Fatalf("Schema does not parse as JSON: %v", err)
	}
	if schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatal("Schema must be an object with additionalProperties false")
	}
	required := slices.Clone(schema.Required)
	slices.Sort(required)
	if want := []string{"exit_code", "output", "status", "summary"}; !slices.Equal(required, want) {
		t.Fatalf("Schema requires %v, want %v", required, want)
	}
	if got := schema.Properties["status"].Enum; !slices.Equal(got, []string{"done", "blocked"}) {
		t.Fatalf("Schema status enum = %v, want [done blocked]", got)
	}
	if got := schema.Properties["exit_code"].Type; got != "integer" {
		t.Fatalf("Schema exit_code type = %q, want integer", got)
	}
	if strings.TrimSpace(Contract) == "" {
		t.Fatal("Contract is empty")
	}
	req := Request{Command: "go test ./internal/x", Revision: "deadbeef"}
	prompt := Prompt(req)
	if !strings.HasPrefix(prompt, Contract) {
		t.Fatal("Prompt does not open with Contract")
	}
	for _, part := range []string{"Command: " + req.Command, "Revision: " + req.Revision} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("Prompt does not carry %q", part)
		}
	}
}

// expectOutput fails t unless Output(result) is want.
func expectOutput(t *testing.T, result, want string) {
	t.Helper()
	if got := Output(result); got != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
}

func TestTestRunnerRequestsOutputOfReturn(t *testing.T) {
	t.Parallel()
	expectOutput(t,
		`{"status":"done","exit_code":3,"output":"line one\nline two","summary":"tests failed"}`,
		"exit 3: tests failed\nline one\nline two")
	expectOutput(t, "not a return", "unreadable test runner return: not a return")

	// A rendered return over 64 KiB keeps its tail and drops its head.
	long := "head-marker" + strings.Repeat("x", 70<<10) + "tail-marker"
	got := Output(`{"status":"done","exit_code":0,"output":"` + long + `","summary":"s"}`)
	if len(got) != 64<<10 || !strings.HasSuffix(got, "tail-marker") || strings.Contains(got, "head-marker") {
		t.Fatalf("long Output: len %d, want %d keeping the tail", len(got), 64<<10)
	}
	got = Output(long)
	if len(got) != 64<<10 || !strings.HasSuffix(got, "tail-marker") || strings.Contains(got, "head-marker") {
		t.Fatalf("long unreadable Output: len %d, want %d keeping the tail", len(got), 64<<10)
	}

	// A cut that lands inside a two-byte character moves forward, so the kept text stays valid UTF-8.
	multi := strings.Repeat("é", 40000) + "x"
	got = Output(`{"status":"done","exit_code":0,"output":"` + multi + `","summary":"s"}`)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "x") || len(got) > 64<<10 {
		t.Fatalf("multi-byte Output: valid UTF-8 %t, len %d, want valid UTF-8 ending in x within 64 KiB",
			utf8.ValidString(got), len(got))
	}
}
