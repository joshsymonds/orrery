package interpret

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIntegratorRoleTakesOriginsSideOfAMeasuredFile asserts that the role text has a conflicted file that records a
// measurement the Done gates or captures check taken whole from origin, never measured again in the turn, with a kept
// that says the gates measure the merged tree against origin's newer file. It names no path of any one repository.
func TestIntegratorRoleTakesOriginsSideOfAMeasuredFile(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"a baseline, a recorded measurement or a snapshot of measured values, such as a test CPU budget",
		"whose content the epic's Done gates or captures measure again or check",
		"Take origin's whole version of such a file, with took origin",
		"even when the repository has a command that measures it again",
		"do not measure it again and do not edit it by hand",
		"Its kept says that the Done gates measure the merged tree against origin's newer file, so a real " +
			"regression still fails the gate.",
		"A gate that fails against a measured file you took from origin is never answered by measuring the file again",
	} {
		if !strings.Contains(integratorRole, want) {
			t.Errorf("integrator role lacks %q", want)
		}
	}
	for _, path := range []string{"testdata/", "test-budget.json", "remote-test-budget-update"} {
		if strings.Contains(integratorRole, path) {
			t.Errorf("integrator role names the repository path %q", path)
		}
	}
}

// TestIntegratorRoleRegeneratesAFileGeneratedFromItsSources asserts that a file the repository generates from its
// sources is still made again with the repository's generator, and that a snapshot is no longer among them.
func TestIntegratorRoleRegeneratesAFileGeneratedFromItsSources(t *testing.T) {
	t.Parallel()
	want := "A file the repository generates from its sources, such as code generated from a schema or a lock file, is " +
		"never resolved by hand: resolve its sources, then make it again with the repository's own generator, and its " +
		"took is regenerated."
	if !strings.Contains(integratorRole, want) {
		t.Errorf("integrator role lacks %q", want)
	}
	if strings.Contains(integratorRole, "a lock file or a snapshot is never resolved by hand") {
		t.Error("integrator role still has a snapshot made again by its generator")
	}
}

// TestIntegratorRoleStartsNoCommandThatOutlivesItsTurn asserts that the role text forbids a background job or a log
// watcher, and sends a command longer than the shell allows to the Done gates.
func TestIntegratorRoleStartsNoCommandThatOutlivesItsTurn(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"Start no command that outlives your turn",
		"no background job and no watcher of a log, such as `tail -f` or a monitor",
		"the turn ends in one structured return",
		"A command longer than the shell allows belongs to the Done gates, which orreryd runs on your commit after " +
			"you return done.",
	} {
		if !strings.Contains(integratorRole, want) {
			t.Errorf("integrator role lacks %q", want)
		}
	}
}

// TestIntegratorRoleNamesEveryTookTheSchemaAccepts asserts that the role text names the values of took that
// IntegratorSchema accepts, in the schema's order.
func TestIntegratorRoleNamesEveryTookTheSchemaAccepts(t *testing.T) {
	t.Parallel()
	var schema struct {
		Properties struct {
			Resolutions struct {
				Items struct {
					Properties struct {
						Took struct {
							Enum []string `json:"enum"`
						} `json:"took"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"resolutions"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(IntegratorSchema), &schema); err != nil {
		t.Fatalf("decode IntegratorSchema: %v", err)
	}
	took := schema.Properties.Resolutions.Items.Properties.Took.Enum
	if len(took) == 0 {
		t.Fatal("IntegratorSchema has no took values")
	}
	want := "took, which is " + strings.Join(took[:len(took)-1], ", ") + " or " + took[len(took)-1]
	if !strings.Contains(integratorRole, want) {
		t.Errorf("integrator role lacks %q, the took values IntegratorSchema accepts", want)
	}
}
