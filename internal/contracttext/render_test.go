package contracttext

import (
	"strings"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// checksWithoutQualityBar is a checks text of two lines, neither of them the Quality Bar line.
const checksWithoutQualityBar = "go test ./...\ngo vet ./..."

// checksWith is checksWithoutQualityBar with line written between its two lines.
func checksWith(line string) string {
	return strings.Replace(checksWithoutQualityBar, "\n", "\n"+line+"\n", 1)
}

// sampleContract is a contract with every section filled, so Render draws each one.
func sampleContract() *v1.EpicContract {
	return &v1.EpicContract{
		Title:    "Any conversation runs the contract stage",
		Approved: "any conversation runs the contract stage through Orrery's own tools. Care: serious. Decisions: none.",
		Intent:   "Run the contract stage in any conversation, through Orrery's own tools.",
		Failures: []*v1.EpicFailure{
			{Id: "F1", Text: "A contract was accepted early", Rating: "serious", Response: "prevent: R5"},
		},
		Care: "serious",
		Additions: []*v1.EpicAddition{
			{Location: "release step 3", What: "two role keys", Why: "D4", Cost: "two lines"},
		},
		Requirements: []*v1.EpicRequirement{{
			Id:   "R1",
			Text: "The roles live in Orrery",
			Item: &v1.EvidenceItem{
				Capture:    []string{"go", "test", "-run", "StageRoles"},
				Conclusion: "Passing tests show each role resolving.",
			},
		}},
		Premises: []*v1.EpicPremise{
			{Id: "P1", Text: "gambit's contract describes the stage", IfWrong: "the rules are rewritten"},
		},
		Exclusions: []string{"No new kind of session"},
		Approach:   "The tools act on the design record.",
		Alternatives: []*v1.EpicAlternative{
			{
				Alternative: "An engine workflow",
				WhyNot:      "the stage is a conversation",
				Reconsider:  "if stages run unattended",
			},
		},
		ReleaseSteps: []*v1.EpicReleaseStep{
			{Step: "Fast-forward main", Target: "origin main", Undo: "push a revert"},
		},
		ReleaseThen: "the rendered roles.toml names scout and steelman.",
		Checks:      checksWith(qualityBarLine),
	}
}

// sampleDecisions is one decision the owner still needs to answer.
func sampleDecisions() []*v1.ContractDecision {
	return []*v1.ContractDecision{
		{
			Id:             "D1",
			Question:       "Which store?",
			Options:        []string{"a", "b"},
			Recommendation: "a",
			Reason:         "fits the record",
		},
	}
}

func TestContractTextRender(t *testing.T) {
	got := Render(sampleContract(), sampleDecisions())
	sections := []string{
		"# Any conversation runs the contract stage\n",
		"\nApproved: ",
		"\n## What you asked for\n",
		"\n## What could go wrong, and how much we care\n",
		"\n## Things you did not ask for\n",
		"\n## What will be true when done\n",
		"\n## What I'm assuming\n",
		"\n## What we won't do\n",
		"\n## How, and why not the other ways\n",
		"\n## What leaves this machine or can't be undone\n",
		"\n## Decisions I need from you\n",
		"\n" + checksHeading + "\n",
	}
	last := -1
	for _, section := range sections {
		i := strings.Index(got, section)
		if i < 0 {
			t.Fatalf("Render has no section %q", section)
		}
		if i <= last {
			t.Errorf("section %q comes before the section above it", section)
		}
		last = i
	}
	if want := checksHeading + "\n" + checksWith(qualityBarLine) + "\n"; !strings.HasSuffix(got, want) {
		t.Errorf("Render does not end with the checks section, %q", want)
	}
}

func TestContractTextLinesAboveChecks(t *testing.T) {
	// The sample contract writes 50 lines before the checks heading: the title and approval, then each section with its
	// table or list and the blank line that ends it.
	const want = 50
	contract, decisions := sampleContract(), sampleDecisions()
	if got := LinesAboveChecks(contract, decisions); got != want {
		t.Errorf("LinesAboveChecks = %d, want %d", got, want)
	}
	rendered := Render(contract, decisions)
	heading := strings.Index(rendered, checksHeading)
	if heading < 0 {
		t.Fatalf("Render has no %q", checksHeading)
	}
	if got := strings.Count(rendered[:heading], "\n"); got != want {
		t.Errorf("Render writes %d lines before %q, want %d", got, checksHeading, want)
	}
}

func TestContractTextQualityBarLine(t *testing.T) {
	verbatim := "Quality Bar: " + QualityBar
	oneChanged := strings.Replace(verbatim, "Failing,", "failing,", 1)
	tests := []struct {
		name   string
		checks string
		want   bool
	}{
		{name: "verbatim line among other lines", checks: checksWith(verbatim), want: true},
		{name: "one character changed", checks: checksWith(oneChanged), want: false},
		{name: "no Quality Bar line", checks: checksWithoutQualityBar, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QualityBarLine(tt.checks); got != tt.want {
				t.Errorf("QualityBarLine(%q) = %v, want %v", tt.checks, got, tt.want)
			}
		})
	}
}
