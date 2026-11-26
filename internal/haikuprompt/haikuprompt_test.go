package haikuprompt_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/haikuprompt"
)

// TestInputPutsTheMaterialFirstAndTheInstructionLast lays out two parts and an instruction: each part in its tag, in
// order, with its trailing newlines trimmed, then the instruction, then the closing answer line.
func TestInputPutsTheMaterialFirstAndTheInstructionLast(t *testing.T) {
	t.Parallel()
	got := haikuprompt.Input("Judge it.",
		haikuprompt.Part{Tag: "why", Text: "it tripped\n\n"},
		haikuprompt.Part{Tag: "recent_events", Text: "one\ntwo"},
	)
	want := "<why>\nit tripped\n</why>\n\n<recent_events>\none\ntwo\n</recent_events>\n\nJudge it.\n\n" +
		haikuprompt.Answer
	if got != want {
		t.Errorf("Input = %q, want %q", got, want)
	}
}

// TestInputWithNoPartsIsTheInstructionAndTheAnswer lays out an instruction with no material.
func TestInputWithNoPartsIsTheInstructionAndTheAnswer(t *testing.T) {
	t.Parallel()
	if got, want := haikuprompt.Input("Judge it."), "Judge it.\n\n"+haikuprompt.Answer; got != want {
		t.Errorf("Input = %q, want %q", got, want)
	}
}
