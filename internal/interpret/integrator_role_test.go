package interpret

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIntegratorRoleTakesOriginsSideOfAMeasuredFile asserts that the role text has a conflicted file that records a
// measurement the Done gates or evidence commands check taken whole from origin, never measured again in the turn, with
// a kept that says the check that measures the merged tree reads origin's newer file. It names no path of any one
// repository.
func TestIntegratorRoleTakesOriginsSideOfAMeasuredFile(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"a baseline, a recorded measurement or a snapshot of measured values, such as a test CPU budget",
		"whose content the epic's Done gates or evidence commands measure again or check",
		"Take origin's whole version of such a file, with took origin",
		"even when the repository has a command that measures it again",
		"do not measure it again and do not edit it by hand",
		"Its kept says that the check that measures the merged tree reads origin's newer file, so a real " +
			"regression still fails that check.",
		"A check that fails against a measured file you took from origin is never answered by measuring the file " +
			"again",
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

// TestIntegratorRoleRunsEveryCommandToItsEnd asserts that the role text has every command the integrator starts run to
// its end within its turn, a long one in the background that orreryd waits for, and forbids a log watcher.
func TestIntegratorRoleRunsEveryCommandToItsEnd(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"Run every command to its end and read its result before you return.",
		"A command that runs longer than one shell call allows runs in the background, with the shell's own option " +
			"for that, and orreryd waits for it; return only once it has ended and you have read its result.",
		"Start no watcher of a log, such as `tail -f` or a monitor",
		"the turn ends in one structured return",
	} {
		if !strings.Contains(integratorRole, want) {
			t.Errorf("integrator role lacks %q", want)
		}
	}
}

// TestIntegratorRoleMergesCleanOrConflicting asserts that the role text covers a merge git made cleanly as well as one
// that conflicts, and says that orreryd runs no Done gate or capture on the merge.
func TestIntegratorRoleMergesCleanOrConflicting(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"git may have merged the two cleanly, or the merge may conflict",
		"It runs no Done gate and no evidence capture on the merge, so the checks you run are the only ones the " +
			"merged tree gets before it ships.",
		"When the merge conflicts, read every conflict before you edit any.",
		"Look also at what git merged without conflict, which is the whole merge when it is clean.",
		"A clean merge has none.",
	} {
		if !strings.Contains(integratorRole, want) {
			t.Errorf("integrator role lacks %q", want)
		}
	}
	for _, gone := range []string{"runs the epic's Done gates and evidence captures on it", "Previous attempt"} {
		if strings.Contains(integratorRole, gone) {
			t.Errorf("integrator role still says %q", gone)
		}
	}
}

// TestIntegratorRoleDecidesAndRunsTheVerificationsTheMergeNeeds asserts that the role text has the integrator read both
// sides' changes, choose the checks where the two sides meet from the narrowest command up to the epic's full Done
// gates, read the Done gates and evidence commands as references, run heavy commands where the repository's own
// instructions say they run without naming any one project's command, list every command it ran, return done only when
// all of them passed or failed for a cause origin's side alone holds, shown by the same command failing on origin's
// commit, which stops no merge.
func TestIntegratorRoleDecidesAndRunsTheVerificationsTheMergeNeeds(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"Read what the candidate changed since the merge base and what origin changed since it",
		"`git diff <merge base> HEAD` and `git diff <merge base> MERGE_HEAD` show each side's change",
		"The merge needs checking where the two sides meet",
		"a migration or schema and the code that reads it, generated code and the sources it is made from, the web " +
			"client, the Android app, and the documents that cite code either side moved",
		"choose the narrowest command that shows the merge holds there",
		"up to the epic's full Done gates when the two sides overlap broadly or you cannot tell how far a change reaches",
		"Verification references lists the epic's Done gates and each Requirement's evidence command.",
		"you read them as references for what the repository checks and how, not as a list to run",
		"A clean merge of areas that do not touch each other may need no more than a build, and then the evidence " +
			"says why nothing more is needed.",
		"Run heavy commands, such as builds and test suites, where the repository's own instructions say they run: " +
			"its CLAUDE.md, AGENTS.md or CONTRIBUTING.md names the wrapper, the remote machine or the recipe for them.",
		"return done only when every command you ran passed on the merged tree, or failed for a cause origin's side " +
			"alone holds, shown as above",
		"A check that fails for a cause origin's side alone holds does not stop the merge",
		"Show that it is origin's by running the same command on origin's commit, where it fails the same way, in " +
			"code the candidate does not change",
		"checks: each command you ran on the merged tree, with command and result; a done return lists at least one.",
		"The side is merge when the decision is how to resolve the merge itself, such as which side's behavior " +
			"holds. Either way",
		"A check fails for a cause the candidate's side alone holds.",
	} {
		if !strings.Contains(integratorRole, want) {
			t.Errorf("integrator role lacks %q", want)
		}
	}
	for _, command := range []string{"just remote", "just check", "go test", "golangci-lint"} {
		if strings.Contains(integratorRole, command) {
			t.Errorf("integrator role names the project command %q", command)
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
