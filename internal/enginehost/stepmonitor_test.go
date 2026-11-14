package enginehost

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStepMonitorSchemaIsFourVerdictObject(t *testing.T) {
	t.Parallel()
	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string   `json:"type"`
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties *bool    `json:"additionalProperties"`
	}
	if err := json.Unmarshal([]byte(stepMonitorSchema), &schema); err != nil {
		t.Fatalf("stepMonitorSchema is not JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("root type = %q, want object", schema.Type)
	}
	if len(schema.Properties) != 2 {
		t.Fatalf("properties = %v, want exactly verdict and doing", schema.Properties)
	}
	verdict, ok := schema.Properties["verdict"]
	if !ok {
		t.Fatal("schema has no verdict property")
	}
	wantEnum := []string{watchVerdictProgressing, watchVerdictLongCommand, watchVerdictLooping, watchVerdictWedged}
	if verdict.Type != "string" || !slices.Equal(verdict.Enum, wantEnum) {
		t.Fatalf("verdict = %+v, want string enum %v", verdict, wantEnum)
	}
	doing, ok := schema.Properties["doing"]
	if !ok || doing.Type != "string" {
		t.Fatalf("doing = %+v, want a string property", doing)
	}
	wantRequired := []string{"verdict", "doing"}
	if !slices.Equal(schema.Required, wantRequired) {
		t.Fatalf("required = %v, want verdict and doing", schema.Required)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatal("schema must set additionalProperties to false")
	}
}

func TestStepMonitorInputDefinesVerdictsAndCarriesWhyAndRecent(t *testing.T) {
	t.Parallel()
	why := "The step has produced no event for ten minutes of model time."
	got := stepMonitorInput(why, "10:02 bash: go test ./...")
	for _, want := range []string{
		watchVerdictProgressing + ": real progress",
		watchVerdictLongCommand + ": waiting on one long command still plausibly working",
		watchVerdictLooping + ": repeating actions without progress",
		watchVerdictWedged + ": stuck, no sign of recovery",
		"one line under 100 characters",
		"doing",
		"The step has produced no event for ten minutes of model time.",
		"## Recent events\n10:02 bash: go test ./...",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("input is missing %q:\n%s", want, got)
		}
	}
	whyAt := strings.Index(got, "The step has produced")
	recentAt := strings.Index(got, "## Recent events")
	if whyAt < 0 || recentAt < 0 || whyAt > recentAt {
		t.Fatalf("why must come before the recent events:\n%s", got)
	}
}

func TestParseStepMonitorReturnsEachVerdict(t *testing.T) {
	t.Parallel()
	for _, verdict := range []string{watchVerdictProgressing, watchVerdictLongCommand, watchVerdictLooping, watchVerdictWedged} {
		raw := json.RawMessage(`{"verdict":"` + verdict + `","doing":"running the suite"}`)
		gotVerdict, gotDoing, err := parseStepMonitor(raw)
		if err != nil {
			t.Fatalf("verdict %q: unexpected error %v", verdict, err)
		}
		if gotVerdict != verdict || gotDoing != "running the suite" {
			t.Fatalf("verdict %q: got (%q, %q)", verdict, gotVerdict, gotDoing)
		}
	}
}

func TestParseStepMonitorTrimsDoing(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"verdict":"progressing","doing":"   editing the parser  \t"}`)
	_, doing, err := parseStepMonitor(raw)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if doing != "editing the parser" {
		t.Fatalf("doing = %q, want trimmed text", doing)
	}
}

func TestParseStepMonitorKeepsOnlyFirstLine(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"verdict":"looping","doing":"  rerunning the build\nthen a second line\n"}`)
	_, doing, err := parseStepMonitor(raw)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if doing != "rerunning the build" {
		t.Fatalf("doing = %q, want the first line only", doing)
	}
}

func TestParseStepMonitorCutsLongDoingAtTwoHundredBytes(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 300)
	raw, err := json.Marshal(map[string]string{"verdict": "progressing", "doing": long})
	if err != nil {
		t.Fatal(err)
	}
	_, doing, err := parseStepMonitor(raw)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if len(doing) != 200 {
		t.Fatalf("doing is %d bytes, want 200", len(doing))
	}
}

func TestParseStepMonitorCutsOnRuneBoundary(t *testing.T) {
	t.Parallel()
	// A two-byte rune straddling byte 200 must not be split: the cut backs off to 199 bytes.
	text := strings.Repeat("a", 199) + "\u00e9"
	raw, err := json.Marshal(map[string]string{"verdict": "wedged", "doing": text})
	if err != nil {
		t.Fatal(err)
	}
	_, doing, err := parseStepMonitor(raw)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !utf8.ValidString(doing) {
		t.Fatal("cut produced invalid UTF-8")
	}
	if doing != strings.Repeat("a", 199) {
		t.Fatalf("doing kept %d bytes, want the 199 whole bytes before the split rune", len(doing))
	}
}

func TestParseStepMonitorFailsBadReplies(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"not JSON":           `{"verdict":`,
		"verdict outside":    `{"verdict":"done","doing":"writing docs"}`,
		"verdict empty":      `{"doing":"writing docs"}`,
		"doing empty":        `{"verdict":"progressing","doing":""}`,
		"doing blank":        `{"verdict":"progressing","doing":"   \n  "}`,
		"doing missing":      `{"verdict":"progressing"}`,
		"verdict wrong case": `{"verdict":"Progressing","doing":"writing docs"}`,
	} {
		if _, _, err := parseStepMonitor(json.RawMessage(raw)); err == nil {
			t.Fatalf("%s: parseStepMonitor(%s) returned no error", name, raw)
		}
	}
}
