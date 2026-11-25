package testfacts

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// goVerbose is go test -v output as an evidence capture keeps it: packages that match no test, one with no test files,
// and one whose tests ran, one failing.
const goVerbose = `testing: warning: no tests to run
PASS
ok  	github.com/joshsymonds/orrery/internal/agents	1.009s [no tests to run]
?   	github.com/joshsymonds/orrery/internal/exectest	[no test files]
=== RUN   TestNodeReattachSessionOutlivesControllerRestart
=== PAUSE TestNodeReattachSessionOutlivesControllerRestart
=== RUN   TestNodeReattachSessionOutlivesDroppedLink
=== PAUSE TestNodeReattachSessionOutlivesDroppedLink
=== CONT  TestNodeReattachSessionOutlivesControllerRestart
=== CONT  TestNodeReattachSessionOutlivesDroppedLink
    node_reattach_test.go:313: row f553 = {state:working}, want {state:done}
--- PASS: TestNodeReattachSessionOutlivesControllerRestart (30.52s)
--- FAIL: TestNodeReattachSessionOutlivesDroppedLink (33.55s)
FAIL
FAIL	github.com/joshsymonds/orrery/internal/enginehost	82.526s
`

// goNothing is go test -v output in which the -run pattern matched no test in any package.
const goNothing = `testing: warning: no tests to run
PASS
ok  	github.com/joshsymonds/orrery/internal/agents	1.009s [no tests to run]
testing: warning: no tests to run
PASS
ok  	github.com/joshsymonds/orrery/internal/alerts	1.016s [no tests to run]
?   	github.com/joshsymonds/orrery/internal/exectest	[no test files]
`

// nodeSpec is node --test's spec reporter as just web-check prints it.
const nodeSpec = `✔ AccountsWebAdd: Add account in the page head opens the form (61.834827ms)
✖ AccountsWebAdd: Cancel closes the form (24.93674ms)
﹣ AccountsWebAdd: Plan offers Claude Max (0.1ms) # SKIP
✖ failing tests:

✖ AccountsWebAdd: Cancel closes the form (24.93674ms)
ℹ tests 3
ℹ suites 0
ℹ pass 1
ℹ fail 1
ℹ skipped 1
`

// gradleConsole is Gradle's test console output as just android-test prints it.
const gradleConsole = `> Task :app:testDebugUnitTest

AppThreadsTest > appCodeStartsThreadsOnlyThroughBackgroundWork PASSED

MainActivityInsetsTest > keyboardUpFoldsTheFoldedLine FAILED
    androidx.test.espresso.AppNotIdleException at MainActivityInsetsTest.kt:217

UsageSheetTest > sheetOpens SKIPPED
BUILD SUCCESSFUL in 9m 22s
`

// junitReport is a JUnit XML report, as a kept artifact or a log that prints one holds it.
const junitReport = `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="io.orrery.UsageSheetTest" tests="3">
  <testcase name="opens &amp; closes" classname="io.orrery.UsageSheetTest" time="0.1"/>
  <testcase name="draws" classname="io.orrery.UsageSheetTest" time="0.2">
    <failure message="boom">trace</failure>
  </testcase>
  <testcase name="waits" classname="io.orrery.UsageSheetTest"><skipped/></testcase>
</testsuite>
`

// goJSON is go test -json output for one passing test.
const goJSON = `{"Action":"start","Package":"example.com/p"}
{"Action":"run","Package":"example.com/p","Test":"TestA"}
{"Action":"output","Package":"example.com/p","Test":"TestA","Output":"=== RUN   TestA\n"}
{"Action":"output","Package":"example.com/p","Test":"TestA","Output":"--- PASS: TestA (0.00s)\n"}
{"Action":"pass","Package":"example.com/p","Test":"TestA","Elapsed":0}
{"Action":"output","Package":"example.com/p","Output":"ok  \texample.com/p\t0.002s\n"}
{"Action":"pass","Package":"example.com/p","Elapsed":0.002}
`

func TestRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		logs     []string
		formats  []string
		tests    []Test
		packages []Package
		nothing  bool
	}{
		{
			name:    "go test -v reads each test by its last result and each package by its line",
			logs:    []string{goVerbose},
			formats: []string{FormatGo},
			tests: []Test{
				{Name: "TestNodeReattachSessionOutlivesControllerRestart", Result: resultPassed},
				{Name: "TestNodeReattachSessionOutlivesDroppedLink", Result: resultFailed},
			},
			packages: []Package{
				{Name: "github.com/joshsymonds/orrery/internal/agents", Result: packageNoTests},
				{Name: "github.com/joshsymonds/orrery/internal/exectest", Result: packageNoTestDir},
				{Name: "github.com/joshsymonds/orrery/internal/enginehost", Result: packageFailed},
			},
		},
		{
			name:    "go test -v whose pattern matched nothing reads as no test ran",
			logs:    []string{goNothing},
			formats: []string{FormatGo},
			packages: []Package{
				{Name: "github.com/joshsymonds/orrery/internal/agents", Result: packageNoTests},
				{Name: "github.com/joshsymonds/orrery/internal/alerts", Result: packageNoTests},
				{Name: "github.com/joshsymonds/orrery/internal/exectest", Result: packageNoTestDir},
			},
			nothing: true,
		},
		{
			name:    "go test -json reads the output its events carry",
			logs:    []string{goJSON},
			formats: []string{FormatGoJSON, FormatGo},
			tests:   []Test{{Name: "TestA", Result: resultPassed}},
			packages: []Package{
				{Name: "example.com/p", Result: packageOK},
			},
		},
		{
			name:    "node --test reads passes, fails and skips, and its failing-tests list adds nothing",
			logs:    []string{nodeSpec},
			formats: []string{FormatNode},
			tests: []Test{
				{Name: "AccountsWebAdd: Add account in the page head opens the form", Result: resultPassed},
				{Name: "AccountsWebAdd: Cancel closes the form", Result: resultFailed},
				{Name: "AccountsWebAdd: Plan offers Claude Max", Result: resultSkipped},
			},
		},
		{
			name:    "node --test that counts no test reads as no test ran",
			logs:    []string{"ℹ tests 0\nℹ pass 0\n"},
			formats: []string{FormatNode},
			nothing: true,
		},
		{
			name:    "Gradle reads each class and method with its result",
			logs:    []string{gradleConsole},
			formats: []string{FormatGradle},
			tests: []Test{
				{Name: "AppThreadsTest > appCodeStartsThreadsOnlyThroughBackgroundWork", Result: resultPassed},
				{Name: "MainActivityInsetsTest > keyboardUpFoldsTheFoldedLine", Result: resultFailed},
				{Name: "UsageSheetTest > sheetOpens", Result: resultSkipped},
			},
		},
		{
			name:    "Gradle that found no test for its filter reads as no test ran",
			logs:    []string{"> Task :app:testDebugUnitTest FAILED\nNo tests found for given includes: [x.YTest]\n"},
			formats: []string{FormatGradle},
			nothing: true,
		},
		{
			name:    "a JUnit report reads each case, a failure failing it and a skipped skipping it",
			logs:    []string{junitReport},
			formats: []string{FormatJUnit},
			tests: []Test{
				{Name: "io.orrery.UsageSheetTest > opens & closes", Result: resultPassed},
				{Name: "io.orrery.UsageSheetTest > draws", Result: resultFailed},
				{Name: "io.orrery.UsageSheetTest > waits", Result: resultSkipped},
			},
		},
		{
			name:    "logs of several runners read together, and one that ran tests is not nothing",
			logs:    []string{goNothing, nodeSpec},
			formats: []string{FormatGo, FormatNode},
			tests: []Test{
				{Name: "AccountsWebAdd: Add account in the page head opens the form", Result: resultPassed},
				{Name: "AccountsWebAdd: Cancel closes the form", Result: resultFailed},
				{Name: "AccountsWebAdd: Plan offers Claude Max", Result: resultSkipped},
			},
			packages: []Package{
				{Name: "github.com/joshsymonds/orrery/internal/agents", Result: packageNoTests},
				{Name: "github.com/joshsymonds/orrery/internal/alerts", Result: packageNoTests},
				{Name: "github.com/joshsymonds/orrery/internal/exectest", Result: packageNoTestDir},
			},
		},
		{
			name: "output of no known runner reads as nothing recognized",
			logs: []string{"• Validating lock\n✓ Validating lock in 1.35ms\nok\nsrc/a.go:3: match\n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Read(tc.logs...)
			if !reflect.DeepEqual(got.Formats, tc.formats) {
				t.Errorf("Formats = %q, want %q", got.Formats, tc.formats)
			}
			if !reflect.DeepEqual(got.Tests, tc.tests) {
				t.Errorf("Tests = %+v, want %+v", got.Tests, tc.tests)
			}
			if !reflect.DeepEqual(got.Packages, tc.packages) {
				t.Errorf("Packages = %+v, want %+v", got.Packages, tc.packages)
			}
			if got.NothingRan() != tc.nothing {
				t.Errorf("NothingRan() = %t, want %t", got.NothingRan(), tc.nothing)
			}
		})
	}
}

func TestLinesNameWhatRanAndCutLongLists(t *testing.T) {
	t.Parallel()
	got := Read(goVerbose).Lines()
	want := []string{
		"Formats recognized: go test",
		"Tests seen: 2",
		"Failed (1): TestNodeReattachSessionOutlivesDroppedLink",
		"Passed (1): TestNodeReattachSessionOutlivesControllerRestart",
		"Go packages: 1 failed (github.com/joshsymonds/orrery/internal/enginehost), 1 with no tests to run, " +
			"1 with no test files",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Lines() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	var many strings.Builder
	for i := range maxNames + 3 {
		many.WriteString("--- PASS: TestN" + strings.Repeat("x", i) + " (0.00s)\n")
	}
	lines := Read(many.String()).Lines()
	passed := lines[slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, "Passed") })]
	if !strings.HasPrefix(passed, "Passed (43): ") || !strings.HasSuffix(passed, ", and 3 more") {
		t.Errorf("Passed line = %q, want 43 counted and the last 3 cut", passed)
	}
}

func TestLinesSayNoTestRanAndNothingRecognized(t *testing.T) {
	t.Parallel()
	const nothing = "No test ran: the output says no test ran, and shows none running."
	if got := Read(goNothing).Lines(); !slices.Contains(got, nothing) {
		t.Errorf("Lines() = %q, want the no-test-ran line", got)
	}
	want := []string{"No test runner's output was recognized."}
	if got := Read("plain output\n").Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}
