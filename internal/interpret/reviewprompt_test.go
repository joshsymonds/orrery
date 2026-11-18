package interpret

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/planner"
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
		build.VerifierRole(),
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
		closureRole(),
		"The change is frozen111..rev222; this worktree is checked out at rev222.",
		"Owner decisions:",
		"- none",
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
		if !strings.Contains(closureRole(), "`"+result+"`") {
			t.Errorf("closure role does not name the result `%s`", result)
		}
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

// TestVerifierPromptsTightened asserts that the closure prompt carries the epic's owner decisions, and that the finding
// and closure verifier prompts share one source for their common text.
func TestVerifierPromptsTightened(t *testing.T) {
	t.Parallel()
	t.Run("the closure prompt carries owner decisions", func(t *testing.T) {
		t.Parallel()
		epic := rpEpic()
		epic.Questions = []store.EpicQuestion{{Seq: 1, Text: "Keep the flag?", Choice: "keep"}}
		epic.Decisions = []store.EpicDecision{
			{ID: "D1", Seq: 1, Question: 1, Text: "the flag stays"},
			{ID: "D2", Seq: 2, Text: "one release"},
		}
		block := "Owner decisions:\n" + strings.Join(planner.DecisionLines(epic, 0), "\n") + "\nFinding correctness-2: "
		got := closureStart(epic, "frozen111", "rev222", rpFinding(), "step-9", "verifier").Prompt
		if !strings.Contains(got, block) {
			t.Fatalf("closure prompt lacks the owner decisions\n got:\n%s\nwant block:\n%s", got, block)
		}
	})
	t.Run("the finding and closure verifier prompts share one source for their common text", func(t *testing.T) {
		t.Parallel()
		common := build.VerifierCommon
		if !strings.Contains(common, "## Freeze") || !strings.Contains(common, "## Return") {
			t.Fatal("build.VerifierCommon lacks the Freeze or the Return section")
		}
		task := store.EpicTask{Seq: 4, Title: "Render brief", Deliverable: "brief.go", Requirements: []string{"R1"}}
		finding := store.TaskFinding{
			ID: "F7", Claim: "c", Citation: "R1", Location: "brief.go:42", Evidence: "e", VerifyBy: "v",
		}
		verifierPrompt := build.VerifierPrompt(rpEpic(), task, "base111", "rev222", finding)
		if !strings.Contains(verifierPrompt, build.VerifierCommon) {
			t.Error("the finding verifier prompt lacks build.VerifierCommon")
		}
		prompt := closureStart(rpEpic(), "frozen111", "rev222", rpFinding(), "step-9", "verifier").Prompt
		if !strings.Contains(prompt, build.VerifierCommon) {
			t.Error("the closure verifier prompt lacks build.VerifierCommon")
		}
		verifierMD, err := os.ReadFile("../build/verifier.md")
		if err != nil {
			t.Fatalf("read verifier.md: %v", err)
		}
		closureMD, err := os.ReadFile("closure.md")
		if err != nil {
			t.Fatalf("read closure.md: %v", err)
		}
		verifierSentences := sentencesOf(string(verifierMD))
		for sentence := range sentencesOf(string(closureMD)) {
			if len(sentence) > 40 && verifierSentences[sentence] {
				t.Errorf("sentence longer than 40 characters is in both verifier.md and closure.md: %q", sentence)
			}
		}
	})
}

// sentencesOf returns the sentences of the markdown text as a set: each line without its bullet or heading marker, cut
// after every full stop that ends a sentence, with the full stop removed.
func sentencesOf(text string) map[string]bool {
	sentences := make(map[string]bool)
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimLeft(strings.TrimPrefix(strings.TrimSpace(line), "- "), "# ")
		for s := range strings.SplitSeq(line, ". ") {
			if s = strings.TrimSuffix(strings.TrimSpace(s), "."); s != "" {
				sentences[s] = true
			}
		}
	}
	return sentences
}
