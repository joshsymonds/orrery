package reviewers

import (
	"slices"
	"strings"
	"testing"
)

func TestStepSendBackFloorRole(t *testing.T) {
	t.Parallel()
	t.Run("the floor role says the number of commits is no defect", func(t *testing.T) {
		t.Parallel()
		rs := mergedDefaults(t)
		i := slices.IndexFunc(rs, func(r Reviewer) bool { return r.Name == "floor" })
		if i < 0 {
			t.Fatalf("no floor reviewer in %v", names(rs))
		}
		if !strings.Contains(rs[i].Prompt, "The number of commits a task makes is no defect.") {
			t.Errorf("floor role text does not say the number of commits a task makes is no defect")
		}
	})
}

// TestDeclaredGateChangesAreAskedForInTheScopeRoles: the floor and conformance roles, which judge work nobody asked
// for, read a declared gate-needed change as asked for when the gate needs it and it is no larger than that need.
func TestDeclaredGateChangesAreAskedForInTheScopeRoles(t *testing.T) {
	t.Parallel()
	roles := []struct{ name, prompt, want string }{
		{name: "floor", prompt: floorRole, want: "A change the prompt lists as a gate-needed change the implementer " +
			"declared is asked for when the task's gate or evidence could not pass without it and it is no larger " +
			"than that need"},
		{name: "conformance", prompt: conformanceRole, want: "A change the prompt lists as a gate-needed change a " +
			"task declared is asked for when that task's gate or evidence could not pass without it and it is no " +
			"larger than that need"},
	}
	for _, role := range roles {
		if !strings.Contains(role.prompt, role.want) {
			t.Errorf("%s role text does not say %q", role.name, role.want)
		}
	}
}
