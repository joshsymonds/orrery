package stage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
)

// stageRoleHome returns a getenv whose HOME and XDG_CONFIG_HOME are one temp dir holding only orrery/roles.toml, with
// text as its contents.
func stageRoleHome(t *testing.T, text string) func(string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "orrery", "roles.toml")
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return func(name string) string {
		if name == "HOME" || name == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	}
}

func TestStageRolesScoutStartsUnderItsRoleAgent(t *testing.T) {
	t.Parallel()
	getenv := stageRoleHome(t, `scout = "scout-agent"`+"\n"+`steelman = "steelman-agent"`+"\n")
	const question = "Does Foo call Bar?"
	start, err := ScoutStart(getenv, "/repo", "abc1234", question)
	if err != nil {
		t.Fatalf("ScoutStart: %v", err)
	}
	want := enginehost.StepStart{
		Agent:     "scout-agent",
		Prompt:    scoutContract + question,
		Schema:    scoutSchema,
		Workspace: "/repo",
		Revision:  "abc1234",
		ReadOnly:  true,
		DoneValue: "done",
	}
	if !reflect.DeepEqual(start, want) {
		t.Fatalf("ScoutStart = %+v, want %+v", start, want)
	}
	if !strings.HasPrefix(start.Prompt, scoutContract) || scoutContract == "" {
		t.Fatal("ScoutStart prompt does not open with the embedded scout contract")
	}
}

func TestStageRolesSteelmanStartsUnderItsRoleAgent(t *testing.T) {
	t.Parallel()
	getenv := stageRoleHome(t, `scout = "scout-agent"`+"\n"+`steelman = "steelman-agent"`+"\n")
	const packet = "Draft contract: Intent, R1, P1."
	start, err := SteelmanStart(getenv, "/repo", packet)
	if err != nil {
		t.Fatalf("SteelmanStart: %v", err)
	}
	want := enginehost.StepStart{
		Agent:     "steelman-agent",
		Prompt:    steelmanContract + packet,
		Schema:    steelmanSchema,
		Workspace: "/repo",
		ReadOnly:  true,
		DoneValue: "done",
	}
	if !reflect.DeepEqual(start, want) {
		t.Fatalf("SteelmanStart = %+v, want %+v", start, want)
	}
	if !strings.HasPrefix(start.Prompt, steelmanContract) || steelmanContract == "" {
		t.Fatal("SteelmanStart prompt does not open with the embedded steelman contract")
	}
}

func TestStageRolesRefusesRoleWithoutItsKey(t *testing.T) {
	t.Parallel()
	_, err := ScoutStart(stageRoleHome(t, `steelman = "steelman-agent"`+"\n"), "/repo", "abc1234", "q")
	if err == nil || !strings.Contains(err.Error(), "scout role") {
		t.Fatalf("ScoutStart without a scout key: error %v, want a scout role error", err)
	}
	if errors.Unwrap(err) == nil {
		t.Fatalf("ScoutStart error %v wraps no cause", err)
	}
	_, err = SteelmanStart(stageRoleHome(t, `scout = "scout-agent"`+"\n"), "/repo", "packet")
	if err == nil || !strings.Contains(err.Error(), "steelman role") {
		t.Fatalf("SteelmanStart without a steelman key: error %v, want a steelman role error", err)
	}
	if errors.Unwrap(err) == nil {
		t.Fatalf("SteelmanStart error %v wraps no cause", err)
	}
}

func TestStageRolesDecodeSteelmanReadsFindingsLedgerAndConcerns(t *testing.T) {
	t.Parallel()
	result := `{"status":"done","pass":"closure","verdict":"STILL_OPEN",` +
		`"strongest_case":"the case","strongest_alternative":"the alternative",` +
		`"findings":[{"id":"S1","contract":"R2","impact":"breaks retries","evidence":"x.go:3","change":"add R3"}],` +
		`"ledger":[{"id":"S1","word":"not_satisfied","note":"R2 unchanged"},` +
		`{"id":"S2","word":"satisfied","note":"P1 now states the limit"}],` +
		`"concerns":[{"id":"C1","contract":"P1","text":"the limit now bites the retry path"}],` +
		`"evidence_and_coverage":"read x.go"}`
	got, err := DecodeSteelman(result)
	if err != nil {
		t.Fatalf("DecodeSteelman: %v", err)
	}
	want := SteelmanReturn{
		Status:               "done",
		Pass:                 "closure",
		Verdict:              "STILL_OPEN",
		StrongestCase:        "the case",
		StrongestAlternative: "the alternative",
		Findings: []SteelmanFinding{
			{ID: "S1", Contract: "R2", Impact: "breaks retries", Evidence: "x.go:3", Change: "add R3"},
		},
		Ledger: []SteelmanLedgerItem{
			{ID: "S1", Word: "not_satisfied", Note: "R2 unchanged"},
			{ID: "S2", Word: "satisfied", Note: "P1 now states the limit"},
		},
		Concerns: []SteelmanConcern{
			{ID: "C1", Contract: "P1", Text: "the limit now bites the retry path"},
		},
		EvidenceAndCoverage: "read x.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeSteelman = %+v, want %+v", got, want)
	}

	minimal, err := DecodeSteelman(`{"status":"blocked","pass":"discovery",` +
		`"verdict":"BLOCKED","evidence_and_coverage":"none"}`)
	if err != nil {
		t.Fatalf("DecodeSteelman of a minimal return: %v", err)
	}
	if minimal.Verdict != "BLOCKED" || minimal.Findings != nil || minimal.Ledger != nil || minimal.Concerns != nil {
		t.Fatalf("minimal DecodeSteelman = %+v, want a blocked return with no lists", minimal)
	}

	for _, bad := range []string{"not a return", `{"findings":"none"}`, `{"ledger":[{"id":3}]}`} {
		if _, err := DecodeSteelman(bad); err == nil {
			t.Fatalf("DecodeSteelman(%s): nil error, want an error", bad)
		}
	}
}

func TestStageRolesSchemasRequireTheirFields(t *testing.T) {
	t.Parallel()
	var scout struct {
		Required   []string
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(scoutSchema), &scout); err != nil {
		t.Fatalf("scout schema does not parse as JSON: %v", err)
	}
	required := slices.Clone(scout.Required)
	slices.Sort(required)
	if want := []string{"answer", "evidence", "not_inspected", "status"}; !slices.Equal(required, want) {
		t.Fatalf("scout schema requires %v, want %v", required, want)
	}
	if got := scout.Properties["status"].Enum; !slices.Equal(got, []string{"done", "blocked"}) {
		t.Fatalf("scout schema status enum = %v, want [done blocked]", got)
	}

	var steelman struct {
		Required   []string
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(steelmanSchema), &steelman); err != nil {
		t.Fatalf("steelman schema does not parse as JSON: %v", err)
	}
	required = slices.Clone(steelman.Required)
	slices.Sort(required)
	if want := []string{"evidence_and_coverage", "pass", "status", "verdict"}; !slices.Equal(required, want) {
		t.Fatalf("steelman schema requires %v, want %v", required, want)
	}
	if got := steelman.Properties["pass"].Enum; !slices.Equal(got, []string{"discovery", "closure"}) {
		t.Fatalf("steelman schema pass enum = %v, want [discovery closure]", got)
	}
	if got := steelman.Properties["verdict"].Enum; !slices.Equal(got, []string{
		"READY", "REVISE", "NEEDS_DECISION", "BLOCKED", "STILL_OPEN", "CHANGE_INDUCED_CONCERN",
	}) {
		t.Fatalf("steelman schema verdict enum = %v", got)
	}
}

func TestStageRolesTextsNameNoGambitOrClaudeHome(t *testing.T) {
	t.Parallel()
	texts := map[string]string{
		"scout contract":    scoutContract,
		"scout schema":      scoutSchema,
		"steelman contract": steelmanContract,
		"steelman schema":   steelmanSchema,
	}
	for name, text := range texts {
		lower := strings.ToLower(text)
		for _, banned := range []string{"gambit", "/.claude"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("%s names %q, which points at another install", name, banned)
			}
		}
	}
}
