package reviewers

import (
	"reflect"
	"slices"
	"strings"
	"testing"
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
	readOnly := []string{"Read", "Grep", "Glob", "Bash"}
	got := mergedDefaults(t)
	want := []Reviewer{
		{
			Name:  "floor",
			Stage: []string{StageTask},
			Must: []string{
				"Each capture for the task's Requirements would fail if the behavior it proves were removed.",
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
			},
			Model: "sonnet", Tools: readOnly, Limit: 1800, Verify: true, Enabled: true,
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
