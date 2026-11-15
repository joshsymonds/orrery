package build

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// verifyStepID and verifyAgent are the id and agent the verifier steps in these tests are started under.
const (
	verifyStepID = "verify-id"
	verifyAgent  = "claude-verify"
)

// verifierEpic is an epic with three Requirements, the second of which the verified task does not cover.
func verifierEpic() store.EpicRow {
	return store.EpicRow{
		ID:    "repo/epic-x",
		Title: "Build the task",
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "one"},
			{ID: "R2", Text: "two"},
			{ID: "R3", Text: "three"},
		},
	}
}

// verifierTask is task T4 with Requirements R3 and R1 in that order.
func verifierTask() store.EpicTask {
	return store.EpicTask{
		Seq:          4,
		Title:        "Render brief",
		Deliverable:  "brief.go",
		Requirements: []string{"R3", "R1"},
	}
}

// verifierFinding is finding F7 of a review, with every field set to its own value.
func verifierFinding() store.TaskFinding {
	return store.TaskFinding{
		ID:       "F7",
		Claim:    "the brief drops a Requirement",
		Citation: "R3",
		Location: "internal/build/brief.go:42",
		Evidence: "requirementLines skips R3",
		VerifyBy: "go test ./internal/build -run Brief",
	}
}

func TestVerifierSchemaParsesWithStatusResultsAndThreeResults(t *testing.T) {
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
	if err := json.Unmarshal([]byte(VerifierSchema), &schema); err != nil {
		t.Fatalf("VerifierSchema does not parse: %v", err)
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
	want := []string{"confirmed", "dropped", "undetermined"}
	if got := schema.Properties.Results.Items.Properties.Result.Enum; !slices.Equal(got, want) {
		t.Errorf("result enum = %q, want %q", got, want)
	}
}

func TestVerifierPromptCarriesRoleTaskRequirementsRangeAndFinding(t *testing.T) {
	t.Parallel()
	got := VerifierPrompt(verifierEpic(), verifierTask(), "base111", "rev222", verifierFinding())
	want := strings.Join([]string{
		VerifierRole,
		"Task T4 of epic repo/epic-x (Build the task): Render brief",
		"Deliverable: brief.go",
		"Requirements:",
		"- R3: three",
		"- R1: one",
		"The change is base111..rev222; this worktree is checked out at rev222.",
		"Finding F7: the brief drops a Requirement",
		"Citation: R3",
		"Location: internal/build/brief.go:42",
		"Evidence: requirementLines skips R3",
		"Verify by: go test ./internal/build -run Brief",
		"Return exactly one result, for F7.",
	}, "\n")
	if got != want {
		t.Fatalf("VerifierPrompt\n got:\n%s\nwant:\n%s", got, want)
	}
}

// checkVerifierStartFields fails t for each field of start that a verifier step carries at either stage: the id and
// agent it was started under, read-only at revision in repository, the verifier schema, its return check and the done
// value.
func checkVerifierStartFields(t *testing.T, start enginehost.StepStart, repository, revision string) {
	t.Helper()
	if start.ID != verifyStepID {
		t.Errorf("ID = %q, want %q", start.ID, verifyStepID)
	}
	if start.Agent != verifyAgent {
		t.Errorf("Agent = %q, want %q", start.Agent, verifyAgent)
	}
	if !start.ReadOnly {
		t.Error("ReadOnly = false, want a read-only step")
	}
	if start.Workspace != repository || start.Revision != revision {
		t.Errorf("Workspace, Revision = %s, %s, want %s, %s", start.Workspace, start.Revision, repository, revision)
	}
	if start.Schema != VerifierSchema {
		t.Error("Schema is not VerifierSchema")
	}
	if start.DoneValue != "done" {
		t.Errorf("DoneValue = %q, want done", start.DoneValue)
	}
	if start.ReturnCheck == nil {
		t.Fatal("ReturnCheck = nil, want VerifierCheck")
	}
	if got := start.ReturnCheck(json.RawMessage(`{"status":"done","results":[]}`)); len(got) == 0 {
		t.Error("ReturnCheck on no results = none, want a failure line")
	}
}

func TestVerifierStartTaskSubjectCarriesTheRunnersStart(t *testing.T) {
	t.Parallel()
	epic := verifierEpic()
	epic.Repository = "/srv/orrery"
	task := verifierTask()
	start := VerifierStart(
		Subject{Epic: epic, Task: &task, Base: "base111", Revision: "rev222"},
		verifierFinding(), verifyStepID, verifyAgent,
	)
	checkVerifierStartFields(t, start, "/srv/orrery", "rev222")
	if start.Epic != "repo/epic-x" || start.Task != "T4" {
		t.Errorf("Epic, Task = %s, %s, want repo/epic-x, T4", start.Epic, start.Task)
	}
	if want := VerifierPrompt(epic, task, "base111", "rev222", verifierFinding()); start.Prompt != want {
		t.Errorf("Prompt\n got:\n%s\nwant the task-stage prompt:\n%s", start.Prompt, want)
	}
}

func TestVerifierStartEpicSubjectNamesEpicAndEveryRequirement(t *testing.T) {
	t.Parallel()
	epic := verifierEpic()
	epic.Repository = "/srv/orrery"
	start := VerifierStart(
		Subject{Epic: epic, Base: "base111", Revision: "rev222"},
		verifierFinding(), verifyStepID, verifyAgent,
	)
	checkVerifierStartFields(t, start, "/srv/orrery", "rev222")
	if start.Epic != "repo/epic-x" || start.Task != "" {
		t.Errorf("Epic, Task = %s, %q, want repo/epic-x and no task label", start.Epic, start.Task)
	}
	want := strings.Join([]string{
		VerifierRole,
		"Epic repo/epic-x (Build the task)",
		"Requirements:",
		"- R1: one",
		"- R2: two",
		"- R3: three",
		"The change is base111..rev222; this worktree is checked out at rev222.",
		"Finding F7: the brief drops a Requirement",
		"Citation: R3",
		"Location: internal/build/brief.go:42",
		"Evidence: requirementLines skips R3",
		"Verify by: go test ./internal/build -run Brief",
		"Return exactly one result, for F7.",
	}, "\n")
	if start.Prompt != want {
		t.Fatalf("Prompt\n got:\n%s\nwant:\n%s", start.Prompt, want)
	}
}

func TestVerifierCheckAcceptsExactlyOneResult(t *testing.T) {
	t.Parallel()
	value := json.RawMessage(`{"status":"done","results":[{"id":"F7","evidence":"brief.go:42","result":"confirmed"}]}`)
	if got := VerifierCheck(value); got != nil {
		t.Fatalf("VerifierCheck(one result) = %q, want nil", got)
	}
}

func TestVerifierCheckRefusesAnyCountOtherThanOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"no results key", `{"status":"done"}`, "results: want exactly one result, got 0"},
		{"empty results", `{"status":"done","results":[]}`, "results: want exactly one result, got 0"},
		{
			"two results",
			`{"status":"done","results":[{"id":"F7","evidence":"a","result":"dropped"},` +
				`{"id":"F8","evidence":"b","result":"dropped"}]}`,
			"results: want exactly one result, got 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := VerifierCheck(json.RawMessage(tt.value))
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("VerifierCheck = %q, want [%q]", got, tt.want)
			}
		})
	}
}

func TestVerifierCheckNamesAValueThatDoesNotParse(t *testing.T) {
	t.Parallel()
	got := VerifierCheck(json.RawMessage(`{"status":"done","results":[`))
	if len(got) != 1 || !strings.Contains(got[0], "unexpected end of JSON input") {
		t.Fatalf("VerifierCheck(unparsable) = %q, want one line naming the parse error", got)
	}
}
