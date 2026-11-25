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

// TestIntegratorRoleRunsTheDoneGatesUnlessASkipIsShown asserts that the role text has the integrator run every Done gate
// on the merged tree by default, skip one only with a reason grounded in what the two sides changed since the merge
// base, never skip a gate declared always, stop on a gate that does not resolve, and run narrower checks of its own only
// beside the gates; read the evidence commands as references; run heavy commands where the repository's own
// instructions say they run without naming any one project's command; list every command it ran and its choice of each
// Done gate; and return done only when all of them passed or failed for a cause origin's side alone holds, shown by the
// same command failing on origin's commit, which stops no merge. The narrow-checks-as-a-default text it replaces is gone.
func TestIntegratorRoleRunsTheDoneGatesUnlessASkipIsShown(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"by default you run every one of them on the merged tree",
		"You may skip a Done gate only when you can show from what the two sides changed that nothing it checks " +
			"could have changed in the merge.",
		"Read the epic's whole change since the merge base and origin's change since it",
		"`git diff <merge base> HEAD` and `git diff <merge base> MERGE_HEAD` show each side's change",
		"The merge needs checking where the two sides meet",
		"a migration or schema and the code that reads it, generated code and the sources it is made from, the web " +
			"client, the Android app, and the documents that cite code either side moved. A gate that checks any such " +
			"place runs.",
		"A skip needs a reason grounded in what the two sides changed",
		"When you cannot show that a skip is safe, run the gate",
		"A gate declared always is never skipped.",
		"A gate that does not resolve has no command to run and cannot be skipped, so the merged tree cannot pass it: " +
			"stop and return blocked, with evidence that names it.",
		"You may also run narrower checks of your own where the sides meet",
		"a narrower check never stands in for a gate you skip without a reason of its own",
		"Evidence commands lists each Requirement's evidence command, which you read as a reference for what the " +
			"repository checks and how.",
		"Run heavy commands, such as builds and test suites, where the repository's own instructions say they run: " +
			"its CLAUDE.md, AGENTS.md or CONTRIBUTING.md names the wrapper, the remote machine or the recipe for them.",
		"return done only when every command you ran passed on the merged tree, or failed for a cause origin's side " +
			"alone holds, shown as above",
		"A check that fails for a cause origin's side alone holds does not stop the merge",
		"Show that it is origin's by running the same command on origin's commit, where it fails the same way, in " +
			"code the candidate does not change",
		"checks: each command you ran on the merged tree, with command and result; a done return lists at least one. " +
			"The Done gates you ran are among them.",
		"gates: one entry for each Done gate, with gate, its name; run, true when you ran it on the merged tree and " +
			"false when you skipped it; and reason",
		"orreryd refuses a done return that leaves out a Done gate or a reviewer Earlier reviews lists, names one " +
			"twice or names one that is not listed, gives an entry no reason, or skips a gate declared always.",
		"The side is merge when the decision is how to resolve the merge itself, such as which side's behavior " +
			"holds. Either way",
		"A check fails for a cause the candidate's side alone holds.",
	} {
		if !strings.Contains(integratorRole, want) {
			t.Errorf("integrator role lacks %q", want)
		}
	}
	for _, gone := range []string{
		"Verification references",
		"not as a list to run",
		"choose the narrowest command that shows the merge holds there",
		"may need no more than a build",
	} {
		if strings.Contains(integratorRole, gone) {
			t.Errorf("integrator role still says %q", gone)
		}
	}
	for _, command := range []string{"just remote", "just check", "go test", "golangci-lint"} {
		if strings.Contains(integratorRole, command) {
			t.Errorf("integrator role names the project command %q", command)
		}
	}
}

// TestIntegratorRoleDecidesWhetherEachEarlierVerdictStands asserts that the role text has the integrator decide, for
// each release reviewer Earlier reviews lists, whether its verdict on the candidate it last read stands for the merge,
// from origin's change since that candidate and its own changes outside git's merge, with doubt counting against it,
// and give one reviews entry for each listed reviewer.
func TestIntegratorRoleDecidesWhetherEachEarlierVerdictStands(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"Earlier reviews lists each release reviewer's verdict on the candidate it last read, with what its review " +
			"returned, when the release made a candidate from this same release tip before origin's main moved past " +
			"it and git merges origin's commit into the tip without conflict",
		"Origin since the reviewed candidates gives origin's change since each such candidate",
		"For each reviewer Earlier reviews lists, decide whether its verdict still stands for the merge you make.",
		"It stands when nothing origin changed since that candidate, and nothing you changed outside git's merge, " +
			"bears on what that reviewer reads and the verdict it gave, as its return shows.",
		"When you cannot tell, its verdict does not stand.",
		"When Earlier reviews says None., every reviewer reviews the new candidate in full.",
		"reviews: one entry for each release reviewer Earlier reviews lists, with reviewer, its name; stands, true " +
			"when its earlier verdict still stands for the merge you made; and reason",
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
