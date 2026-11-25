package enginehost

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestEpicGateCarriesAlwaysAndNote: a contract gate's always and note reach the store form, the note trimmed, and come
// back in the message the stored gate is served as.
func TestEpicGateCarriesAlwaysAndNote(t *testing.T) {
	t.Parallel()
	sent := &v1.EpicGate{
		Name: "check", Argv: []string{"scripts/check"}, Limit: 60, Always: true, Note: "  the full suite  ",
	}
	stored := epicGateOf(sent)
	if want := (store.EpicGate{
		Name: "check", Argv: []string{"scripts/check"}, Limit: 60, Needs: []store.EpicGateNeed{}, Always: true,
		Note: "the full suite",
	}); !reflect.DeepEqual(stored, want) {
		t.Errorf("epicGateOf = %+v, want %+v", stored, want)
	}
	served := epicGateMessage(stored)
	if !served.GetAlways() || served.GetNote() != "the full suite" {
		t.Errorf("epicGateMessage = %+v, want always with the note", served)
	}
}

// TestCheckEpicGatesReadsAlwaysAndNote: a contract gate declared always with a note defines as such, and an agent gate
// declared always is refused at its index, since it is never a Done gate.
func TestCheckEpicGatesReadsAlwaysAndNote(t *testing.T) {
	t.Parallel()
	defined, err := checkEpicGates([]*v1.EpicGate{
		{Name: "check", Argv: []string{"scripts/check"}, Limit: 60, Always: true, Note: "the full suite"},
	})
	if err != nil {
		t.Fatalf("checkEpicGates: %v", err)
	}
	if !defined[0].Always || defined[0].Note != "the full suite" {
		t.Errorf("defined = %+v, want always with the note", defined[0])
	}
	_, err = checkEpicGates([]*v1.EpicGate{
		{Name: "check", Argv: []string{"scripts/check"}, Limit: 60},
		{Name: "review", Agent: "reviewer", Limit: 300, Must: []string{"it holds"}, Always: true},
	})
	var field *epicFieldError
	if !errors.As(err, &field) || field.field != "gates[1]" ||
		!strings.Contains(field.reason, "always cannot sit beside agent") {
		t.Errorf("checkEpicGates = %v, want gates[1] refused for always beside agent", err)
	}
}
