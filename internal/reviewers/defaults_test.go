package reviewers

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/gates"
)

// mergedDefaults returns the reviewers orrery's defaults merge to, freshly built for each caller.
func mergedDefaults(t *testing.T) []Reviewer {
	t.Helper()
	rs, err := Merge(Defaults())
	if err != nil {
		t.Fatalf("Merge(Defaults()): %v", err)
	}
	return rs
}

// conformanceIndex returns the position of the conformance reviewer in rs, failing the test when rs holds none.
func conformanceIndex(t *testing.T, rs []Reviewer) int {
	t.Helper()
	i := slices.Index(names(rs), "conformance")
	if i < 0 {
		t.Fatalf("no conformance reviewer in %v", names(rs))
	}
	return i
}

// checkRoleText asserts a default's role text says it never edits and reports in the gate's verdict schema.
func checkRoleText(t *testing.T, name, prompt string) {
	t.Helper()
	for _, phrase := range []string{"never edits", "verdict schema"} {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("%s: role text does not say %q", name, phrase)
		}
	}
}

func TestDefaultsMerge(t *testing.T) {
	t.Parallel()
	readOnly := []string{"Read", "Bash"}
	got := mergedDefaults(t)
	want := []Reviewer{
		{
			Name:  "floor",
			Stage: []string{StageTask},
			Must: []string{
				"The task's own tests would fail if the behavior they cover were removed.",
			},
			MustNot: []string{
				"The change suppresses, skips or disables a check.",
				"The change weakens a test or makes one tautological.",
				"The change adds dead code.",
				"The change leaves an error unhandled.",
				"The change does something the task did not ask for.",
				"The change introduces a security or data-loss failure with a reachable precondition.",
			},
			Model:   "opus",
			Tools:   readOnly,
			Limit:   1800,
			Verify:  true,
			Enabled: true,
		},
		{
			Name:    "conformance",
			Stage:   []string{StageEpic},
			MustNot: []string{"The change does something the contract did not ask for."},
			Model:   "sonnet", Tools: readOnly, Limit: 1800, Verify: true, Enabled: true,
		},
		{
			Name:  "integration",
			Stage: []string{StageEpic},
			MustNot: []string{
				"A task's change breaks an interface, ordering, lifecycle, configuration or data format " +
					"that another task's change relies on.",
			},
			Model: "sonnet", Tools: readOnly, Limit: 1800, Verify: true, Enabled: true,
		},
		{
			Name:  "security",
			Stage: []string{StageEpic},
			MustNot: []string{
				"The change introduces a security, data-loss or resource failure with a reachable precondition.",
			},
			Model:   "sonnet",
			Tools:   readOnly,
			Limit:   1800,
			Verify:  true,
			Enabled: true,
		},
		{
			Name:  "test_parsimony",
			Stage: []string{StageTask, StageEpic},
			MustNot: []string{
				"The change adds a test that uses a real wait or a real clock.",
				"The change adds a test that starts its own real resource where a shared one exists.",
				"The change adds a test in a package that unrelated changes will select.",
				"The change adds a redundant or tautological test.",
				"The change adds an unjustified golden suite.",
				"The change adds a test that asserts wall-clock timing.",
				"The change adds a test that checks model output deterministically.",
				testCostMustNot,
			},
			Model: "sonnet", Tools: readOnly, Limit: 1800, Verify: true, Enabled: true,
		},
		{
			Name:    "release",
			Stage:   []string{StageRelease},
			MustNot: []string{releaseMustNot},
			Model:   "opus",
			Tools:   readOnly,
			Limit:   1800,
			Enabled: true,
		},
	}
	for i := range got {
		checkRoleText(t, got[i].Name, got[i].Prompt)
		if got[i].Prompt == "" {
			t.Errorf("%s: prompt is empty", got[i].Name)
		}
		got[i].Prompt = ""
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Merge(Defaults()) =\n%+v\nwant\n%+v", got, want)
	}
}

// releaseMustNot is the release reviewer's must_not: a refusal is the assertion holding, so the reviewer refuses only for
// a danger the contract did not anticipate.
const releaseMustNot = "The candidate carries a clear danger to the release's destination that the contract did not anticipate."

// testCostMustNot is the test_parsimony must_not that judges what the tests a change adds cost.
const testCostMustNot = "The change adds or changes tests that push a package over its test CPU budget (a test-budget " +
	"line in Test cost), or a test whose own run is slow (seconds, not milliseconds), without a stated reason in the task."

// TestTestParsimonyGateAssertsTestCost: the test_parsimony reviewer's compiled gate carries the test cost must_not, so
// a review can fail a change on what its tests cost.
func TestTestParsimonyGateAssertsTestCost(t *testing.T) {
	t.Parallel()
	rs := mergedDefaults(t)
	i := slices.Index(names(rs), TestParsimony)
	if i < 0 {
		t.Fatalf("no %s reviewer in %v", TestParsimony, names(rs))
	}
	want := gates.Assertion{Text: testCostMustNot, Kind: gates.KindMustNot}
	if got := rs[i].Gate().Assertions; !slices.Contains(got, want) {
		t.Errorf("%s gate assertions = %+v, want one to be %+v", TestParsimony, got, want)
	}
}

// TestTestParsimonyRoleAdmitsTestCost: the test_parsimony role text admits a test cost finding, reads it from the Test
// cost section, and lets a migration test migrate only what it tests.
func TestTestParsimonyRoleAdmitsTestCost(t *testing.T) {
	t.Parallel()
	var admissible string
	for line := range strings.Lines(testParsimonyRole) {
		if strings.HasPrefix(line, "- **Admissible as a Quality Bar defect:**") {
			admissible = line
		}
	}
	for _, phrase := range []string{
		"push a package over its test CPU budget (a test-budget line in Test cost)",
		"a test whose own run is slow (seconds, not milliseconds), without a stated reason in the task (rule 5)",
	} {
		if !strings.Contains(admissible, phrase) {
			t.Errorf("admissible list %q does not name %q", admissible, phrase)
		}
	}
	for _, phrase := range []string{
		"Read the prompt's Test cost section",
		"A migration test may migrate what it tests; it may not replay the whole migration chain per test.",
	} {
		if !strings.Contains(testParsimonyRole, phrase) {
			t.Errorf("test_parsimony role text does not say %q", phrase)
		}
	}
	if strings.Contains(testParsimonyRole, "unless the test is about migrations") {
		t.Error("test_parsimony role text still exempts migration tests from the shared fixture")
	}
}

func TestDefaultsSource(t *testing.T) {
	t.Parallel()
	for i, entry := range Defaults() {
		if entry.Source != "orrery defaults" {
			t.Errorf("entry %d source = %q, want %q", i, entry.Source, "orrery defaults")
		}
		if entry.Index != i {
			t.Errorf("entry %d index = %d", i, entry.Index)
		}
	}
}

func TestDefaultsForStage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		stage string
		want  []string
	}{
		{stage: StageTask, want: []string{"floor", "test_parsimony"}},
		{stage: StageEpic, want: []string{"conformance", "integration", "security", "test_parsimony"}},
		{stage: StageRelease, want: []string{"release"}},
	}
	for _, tc := range tests {
		t.Run(tc.stage, func(t *testing.T) {
			t.Parallel()
			if got := names(ForStage(mergedDefaults(t), tc.stage)); !slices.Equal(got, tc.want) {
				t.Errorf("ForStage(%s) = %v, want %v", tc.stage, got, tc.want)
			}
		})
	}
}

func TestGenerate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		exclusions []string
		added      []string
	}{
		{name: "no exclusions add nothing"},
		{
			name:       "a No entry appends its assertion",
			exclusions: []string{"No money moves without a ledger row."},
			added:      []string{"The change does what this entry rules out: No money moves without a ledger row."},
		},
		{
			name:       "entries append in order after the conformance's own must_nots",
			exclusions: []string{"No retries past the budget.", "No writes outside the epic's directory."},
			added: []string{
				"The change does what this entry rules out: No retries past the budget.",
				"The change does what this entry rules out: No writes outside the epic's directory.",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Generate(mergedDefaults(t), tc.exclusions)
			want := mergedDefaults(t)
			conformance := conformanceIndex(t, want)
			want[conformance].MustNot = append(want[conformance].MustNot, tc.added...)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Generate =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestGenerateDisabledConformance(t *testing.T) {
	t.Parallel()
	rs := mergedDefaults(t)
	conformance := conformanceIndex(t, rs)
	rs[conformance].Enabled = false
	want := mergedDefaults(t)
	want[conformance].Enabled = false
	if got := Generate(rs, []string{"No money moves without a ledger row."}); !reflect.DeepEqual(got, want) {
		t.Errorf("Generate on a disabled conformance =\n%+v\nwant\n%+v", got, want)
	}
}

func TestGenerateLeavesInputAlone(t *testing.T) {
	t.Parallel()
	rs := mergedDefaults(t)
	conformance := conformanceIndex(t, rs)
	n := len(rs[conformance].MustNot)
	// A must_not slice with spare capacity: an append in place would write past its length.
	backing := make([]string, n, n+2)
	copy(backing, rs[conformance].MustNot)
	rs[conformance].MustNot = backing
	want := mergedDefaults(t)
	want[conformance].MustNot = slices.Clone(backing)
	Generate(rs, []string{"No money moves without a ledger row.", "No retries past the budget."})
	if !reflect.DeepEqual(rs, want) {
		t.Errorf("Generate changed its input =\n%+v\nwant\n%+v", rs, want)
	}
	if spare := backing[:cap(backing)]; spare[n] != "" {
		t.Errorf("Generate wrote %q past the input's must_not", spare[n])
	}
}

// TestReviewerRolesPromptsTightened: the default reviewers' role texts leave the verdict instruction's report paragraph
// to the shared verdict schema, keep the report sentence each role adds, and quote no owner in test_parsimony.
func TestReviewerRolesPromptsTightened(t *testing.T) {
	t.Parallel()
	const reportSentence = "Report your findings in the gate's verdict schema."
	roles := []struct {
		name      string
		prompt    string
		sentences []string
	}{
		{name: "floor", prompt: floorRole, sentences: []string{reportSentence}},
		{name: "conformance", prompt: conformanceRole, sentences: []string{reportSentence}},
		{name: "integration", prompt: integrationRole, sentences: []string{reportSentence}},
		{name: "security", prompt: securityRole, sentences: []string{
			reportSentence,
			"For each security, data-loss or resource claim, fill precondition and consequence.",
		}},
		{name: "test_parsimony", prompt: testParsimonyRole, sentences: []string{
			reportSentence,
			"Each fails result adds one finding that cites the assertion, with the test, the rule it breaks, " +
				"its file and symbol, and the concrete fix.",
		}},
	}
	t.Run("no reviewer role file repeats the verdict instruction's report paragraph", func(t *testing.T) {
		t.Parallel()
		repeated := []string{
			"Give each declared assertion one result",
			"Put anything else you notice in observations",
			"Put the observations in observations",
			"For a security or data-loss claim, fill precondition and consequence",
		}
		for _, role := range roles {
			for _, phrase := range repeated {
				if strings.Contains(role.prompt, phrase) {
					t.Errorf("%s: role text repeats the verdict instruction: contains %q", role.name, phrase)
				}
			}
		}
	})
	t.Run("each reviewer role file keeps its role-specific report sentence", func(t *testing.T) {
		t.Parallel()
		for _, role := range roles {
			for _, sentence := range role.sentences {
				if !strings.Contains(role.prompt, sentence) {
					t.Errorf("%s: role text does not say %q", role.name, sentence)
				}
			}
		}
	})
	t.Run("test_parsimony carries no owner quotation", func(t *testing.T) {
		t.Parallel()
		for _, phrase := range []string{"**Source:**", "the owner ("} {
			if strings.Contains(testParsimonyRole, phrase) {
				t.Errorf("test_parsimony role text quotes the owner: contains %q", phrase)
			}
		}
	})
}
