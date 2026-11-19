package reviewers

import (
	_ "embed"
	"slices"
)

// The role text of each default reviewer, embedded from its file in roles.
//
//go:embed roles/floor.md
var floorRole string

//go:embed roles/conformance.md
var conformanceRole string

//go:embed roles/integration.md
var integrationRole string

//go:embed roles/security.md
var securityRole string

//go:embed roles/test_parsimony.md
var testParsimonyRole string

// defaultsSource names the first layer in the errors a default reviewer's entry would raise.
const defaultsSource = "orrery defaults"

// ruledOut opens the must_not that Generate adds to conformance for each Must Not Ship entry.
const ruledOut = "The change does what this entry rules out: "

// Defaults returns orrery's default reviewers, the first layer Merge applies: floor, conformance, integration, security
// and test_parsimony. Each is read-only, verified, and runs on the model its entry names. Every call returns fresh
// entries, so a caller may change them.
func Defaults() []Entry {
	entries := []Entry{
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
			Prompt: new(floorRole),
			Model:  new("opus"),
			Tools:  readOnlyTools(),
			Verify: new(true),
		},
		{
			Name:    "conformance",
			Stage:   []string{StageEpic},
			MustNot: []string{"The change does something the contract did not ask for."},
			Prompt:  new(conformanceRole), Model: new("sonnet"), Tools: readOnlyTools(), Verify: new(true),
		},
		{
			Name:  "integration",
			Stage: []string{StageEpic},
			MustNot: []string{
				"A task's change breaks an interface, ordering, lifecycle, configuration or data format " +
					"that another task's change relies on.",
			},
			Prompt: new(integrationRole), Model: new("sonnet"), Tools: readOnlyTools(), Verify: new(true),
		},
		{
			Name:  "security",
			Stage: []string{StageEpic},
			MustNot: []string{
				"The change introduces a security, data-loss or resource failure with a reachable precondition.",
			},
			Prompt: new(securityRole), Model: new("sonnet"), Tools: readOnlyTools(), Verify: new(true),
		},
		{
			Name:  TestParsimony,
			Stage: []string{StageTask, StageEpic},
			MustNot: []string{
				"The change adds a test that uses a real wait or a real clock.",
				"The change adds a test that starts its own real resource where a shared one exists.",
				"The change adds a test in a package that unrelated changes will select.",
				"The change adds a redundant or tautological test.",
				"The change adds an unjustified golden suite.",
				"The change adds a test that asserts wall-clock timing.",
				"The change adds a test that checks model output deterministically.",
				"The change adds or changes tests that push a package over its test CPU budget (a test-budget line " +
					"in Test cost), or a test whose own run is slow (seconds, not milliseconds), without a stated " +
					"reason in the task.",
			},
			Prompt: new(testParsimonyRole), Model: new("sonnet"), Tools: readOnlyTools(), Verify: new(true),
		},
	}
	for i := range entries {
		entries[i].Source = defaultsSource
		entries[i].Index = i
	}
	return entries
}

// readOnlyTools returns the tools every default reviewer runs under: Read to read files and Bash to run commands. Its
// role text says it never edits. The list names only tools Claude Code 2.1.321 has.
func readOnlyTools() []string {
	return []string{"Read", "Bash"}
}

// Generate returns rs with the enabled conformance reviewer's must_nots extended, after its own, by one entry per
// exclusion: the contract's Must Not Ship entries, in order. Every other reviewer is returned unchanged. rs is not
// modified.
func Generate(rs []Reviewer, exclusions []string) []Reviewer {
	out := slices.Clone(rs)
	for i := range out {
		if out[i].Name != "conformance" || !out[i].Enabled {
			continue
		}
		added := make([]string, 0, len(exclusions))
		for _, exclusion := range exclusions {
			added = append(added, ruledOut+exclusion)
		}
		out[i].MustNot = slices.Concat(out[i].MustNot, added)
	}
	return out
}
