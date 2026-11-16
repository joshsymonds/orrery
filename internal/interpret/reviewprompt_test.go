package interpret

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/store"
)

// rpEpic is an epic with two Requirements, in the repository /repo/epic-x.
func rpEpic() store.EpicRow {
	return store.EpicRow{
		ID:         "repo/epic-x",
		Title:      "Review the epic",
		Repository: "/repo/epic-x",
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "one"},
			{ID: "R2", Text: "two"},
		},
	}
}

// rpFinding is ledger finding correctness-2, confirmed by a verifier, with every field set to its own value.
func rpFinding() store.LedgerFinding {
	return store.LedgerFinding{
		ID:       "correctness-2",
		Position: 2,
		Reviewer: "correctness",
		Finding:  "F7",
		Claim:    "the brief drops a Requirement",
		Citation: "R2",
		Location: "internal/build/brief.go:42",
		Evidence: "requirementLines skips R2",
		VerifyBy: "go test ./internal/build -run Brief",
		Step:     "verify-step",
		Result:   "confirmed",
		Reason:   "brief.go:42 drops R2",
	}
}

// rpOneResult is a closure or verifier return that holds exactly one result.
const rpOneResult = `{"status":"done","results":[{"id":"correctness-2","evidence":"brief.go:42","result":"open"}]}`

// rpTwoResults is a return that holds two results, which both return checks refuse.
const rpTwoResults = `{"status":"done","results":[` +
	`{"id":"correctness-2","evidence":"a","result":"open"},` +
	`{"id":"correctness-3","evidence":"b","result":"closed"}]}`

func TestEpicVerifierStartIsBuildsVerifierStartOfTheEpicAndTheConvertedFinding(t *testing.T) {
	t.Parallel()
	noRequirements := rpEpic()
	noRequirements.Requirements = nil
	tests := []struct {
		name string
		epic store.EpicRow
	}{
		{"with Requirements", rpEpic()},
		{"with no Requirements", noRequirements},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := epicVerifierStart(tt.epic, "base111", "frozen222", rpFinding(), "step-7", "verifier")
			finding := store.TaskFinding{
				Position: 2, ID: "correctness-2", Claim: "the brief drops a Requirement", Citation: "R2",
				Location: "internal/build/brief.go:42", Evidence: "requirementLines skips R2",
				VerifyBy: "go test ./internal/build -run Brief", Step: "verify-step", Result: "confirmed",
				Reason: "brief.go:42 drops R2",
			}
			want := build.VerifierStart(
				build.Subject{Epic: tt.epic, Base: "base111", Revision: "frozen222"}, finding, "step-7", "verifier",
			)
			if got.ReturnCheck == nil {
				t.Fatal("ReturnCheck is nil, want the one-result check")
			}
			if lines := got.ReturnCheck(json.RawMessage(rpOneResult)); lines != nil {
				t.Errorf("ReturnCheck(one result) = %q, want nil", lines)
			}
			if lines := got.ReturnCheck(json.RawMessage(rpTwoResults)); len(lines) != 1 {
				t.Errorf("ReturnCheck(two results) = %q, want one line refusing the count", lines)
			}
			if got.Prompt != want.Prompt {
				t.Fatalf("Prompt\n got:\n%s\nwant:\n%s", got.Prompt, want.Prompt)
			}
			got.ReturnCheck, want.ReturnCheck = nil, nil
			if !reflect.DeepEqual(got, want) {
				t.Errorf("start\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

func TestEpicVerifierPromptNamesTheEpicRangeAndEveryFindingField(t *testing.T) {
	t.Parallel()
	got := epicVerifierStart(rpEpic(), "base111", "frozen222", rpFinding(), "step-7", "verifier").Prompt
	want := strings.Join([]string{
		build.VerifierRole,
		"Epic repo/epic-x (Review the epic)",
		"Requirements:",
		"- R1: one",
		"- R2: two",
		"The change is base111..frozen222; this worktree is checked out at frozen222.",
		"Finding correctness-2: the brief drops a Requirement",
		"Citation: R2",
		"Location: internal/build/brief.go:42",
		"Evidence: requirementLines skips R2",
		"Verify by: go test ./internal/build -run Brief",
		"Return exactly one result, for correctness-2.",
	}, "\n")
	if got != want {
		t.Fatalf("epic verifier prompt\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestClosureStartSetsAReadOnlyStepAtTheCorrectedRevision(t *testing.T) {
	t.Parallel()
	epic := rpEpic()
	got := closureStart(epic, "frozen111", "rev222", rpFinding(), "step-9", "verifier")
	if got.ID != "step-9" || got.Agent != "verifier" {
		t.Errorf("ID, Agent = %q, %q; want step-9, verifier", got.ID, got.Agent)
	}
	if !got.ReadOnly || got.Workspace != epic.Repository || got.Revision != "rev222" {
		t.Errorf("ReadOnly, Workspace, Revision = %t, %q, %q; want true, %q, rev222",
			got.ReadOnly, got.Workspace, got.Revision, epic.Repository)
	}
	if got.Schema != ClosureSchema || got.DoneValue != "done" || got.Epic != epic.ID {
		t.Errorf("Schema, DoneValue, Epic = %t, %q, %q; want the closure schema, done, %q",
			got.Schema == ClosureSchema, got.DoneValue, got.Epic, epic.ID)
	}
	if got.ReturnCheck == nil {
		t.Fatal("ReturnCheck is nil, want the one-result check")
	}
	if lines := got.ReturnCheck(json.RawMessage(rpOneResult)); lines != nil {
		t.Errorf("ReturnCheck(one result) = %q, want nil", lines)
	}
	if lines := got.ReturnCheck(json.RawMessage(rpTwoResults)); len(lines) != 1 {
		t.Errorf("ReturnCheck(two results) = %q, want one line refusing the count", lines)
	}
}

func TestClosurePromptNamesTheFrozenRangeTheFindingAndItsVerification(t *testing.T) {
	t.Parallel()
	got := closureStart(rpEpic(), "frozen111", "rev222", rpFinding(), "step-9", "verifier").Prompt
	want := strings.Join([]string{
		closureRole,
		"The change is frozen111..rev222; this worktree is checked out at rev222.",
		"Finding correctness-2: the brief drops a Requirement",
		"Citation: R2",
		"Location: internal/build/brief.go:42",
		"Evidence: requirementLines skips R2",
		"Verify by: go test ./internal/build -run Brief",
		"Confirmation: brief.go:42 drops R2",
		"Return exactly one result, for correctness-2.",
	}, "\n")
	if got != want {
		t.Fatalf("closure prompt\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestClosureRoleNamesEveryResultTheClosureSchemaAccepts(t *testing.T) {
	t.Parallel()
	for _, result := range []string{"closed", "open", "undetermined"} {
		if !strings.Contains(closureRole, "`"+result+"`") {
			t.Errorf("closure role does not name the result `%s`", result)
		}
	}
}

func TestClosureSchemaParsesAndAcceptsEveryClosureResult(t *testing.T) {
	t.Parallel()
	var schema struct {
		Required   []string `json:"required"`
		Properties struct {
			Status struct {
				Enum []string `json:"enum"`
			} `json:"status"`
			Results struct {
				Items struct {
					Required   []string `json:"required"`
					Properties struct {
						Result struct {
							Enum []string `json:"enum"`
						} `json:"result"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"results"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(ClosureSchema), &schema); err != nil {
		t.Fatalf("ClosureSchema does not parse: %v", err)
	}
	if want := []string{"status", "results"}; !slices.Equal(schema.Required, want) {
		t.Errorf("required = %q, want %q", schema.Required, want)
	}
	if want := []string{"done", "blocked"}; !slices.Equal(schema.Properties.Status.Enum, want) {
		t.Errorf("status enum = %q, want %q", schema.Properties.Status.Enum, want)
	}
	if want := []string{"id", "evidence", "result"}; !slices.Equal(schema.Properties.Results.Items.Required, want) {
		t.Errorf("result required = %q, want %q", schema.Properties.Results.Items.Required, want)
	}
	want := []string{"closed", "open", "undetermined"}
	if got := schema.Properties.Results.Items.Properties.Result.Enum; !slices.Equal(got, want) {
		t.Errorf("result enum = %q, want %q", got, want)
	}
}

func TestClosureCheckAcceptsExactlyOneResult(t *testing.T) {
	t.Parallel()
	if got := closureCheck(json.RawMessage(rpOneResult)); got != nil {
		t.Fatalf("closureCheck(one result) = %q, want nil", got)
	}
}

func TestClosureCheckRefusesAnyCountOtherThanOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"no results key", `{"status":"done"}`, "results: want exactly one result, got 0"},
		{"empty results", `{"status":"done","results":[]}`, "results: want exactly one result, got 0"},
		{"two results", rpTwoResults, "results: want exactly one result, got 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := closureCheck(json.RawMessage(tt.value))
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("closureCheck = %q, want [%q]", got, tt.want)
			}
		})
	}
}

func TestClosureCheckNamesAValueThatDoesNotParse(t *testing.T) {
	t.Parallel()
	got := closureCheck(json.RawMessage(`{"status":"done","results":[`))
	if len(got) != 1 || !strings.Contains(got[0], "unexpected end of JSON input") {
		t.Fatalf("closureCheck(unparsable) = %q, want one line naming the parse error", got)
	}
}
