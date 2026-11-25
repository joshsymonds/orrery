// Package testfacts reads, from the output of a test command, which tests ran and how each ended: go test's -v and -json
// output, node --test's spec reporter as web-check prints it, Gradle's test console lines and JUnit XML reports. It is a
// reading of the output for a judge to weigh, never a verdict: a format it does not know reads as nothing.
package testfacts

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The formats a reading recognizes, as Facts names them.
const (
	FormatGo     = "go test"
	FormatGoJSON = "go test -json"
	FormatNode   = "node --test"
	FormatGradle = "Gradle"
	FormatJUnit  = "JUnit XML"
)

// The results a test is read with.
const (
	resultPassed  = "passed"
	resultFailed  = "failed"
	resultSkipped = "skipped"
)

// The results a go package line is read with: ok with tests, failed, ok with no tests to run, and no test files.
const (
	packageOK        = "ok"
	packageFailed    = "FAIL"
	packageNoTests   = "no tests to run"
	packageNoTestDir = "no test files"
)

// maxNames is the most names a line of Lines lists before it counts the rest.
const maxNames = 40

// Facts is what a reading found. Formats are the formats it recognized, in the order first seen. Tests are the tests it
// saw, in the order first seen, each with its result, which is empty for a test that started and has no result. Packages
// are the go packages a package line names, each with its last result. Summaries are the summary lines the runners
// printed, such as node's counts and Gradle's build result or its finding no test, in order. NoTests are the lines in which a runner said it ran
// no test, such as go's "no tests to run" or Gradle's "No tests found".
type Facts struct {
	Formats   []string
	Tests     []Test
	Packages  []Package
	Summaries []string
	NoTests   []string
}

// Test is one test a reading saw, by the name its runner printed, and its result: passed, failed, skipped, or empty when
// it started and the output shows no result for it.
type Test struct {
	Name   string
	Result string
}

// Package is one go package a package line names and the result that line gives.
type Package struct {
	Name   string
	Result string
}

// The lines a reading matches. goRun is go's start of a test, goEnd its result, goPackage a package's ok or FAIL line,
// goNoFiles a package with no test files, nodeEnd a node --test result, nodeSummary one of its counts, gradleEnd a Gradle
// test result, gradleSummary its count of tests, and junitCase a JUnit testcase element.
var (
	goRun         = regexp.MustCompile(`^=== RUN\s+(\S+)`)
	goEnd         = regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): (\S+)`)
	goPackage     = regexp.MustCompile(`^(ok|FAIL)\s+(\S+)(?:\s+(.*))?$`)
	goNoFiles     = regexp.MustCompile(`^\?\s+(\S+)\s+\[no test files\]`)
	nodeEnd       = regexp.MustCompile(`^\s*(✔|✖|﹣) (.+?)(?: \([0-9.]+m?s\))?( # (?:SKIP|TODO).*)?$`)
	nodeSummary   = regexp.MustCompile(`^ℹ (tests|suites|pass|fail|cancelled|skipped|todo) (\d+)$`)
	gradleEnd     = regexp.MustCompile(`^\s*(\S.*? > .+) (PASSED|FAILED|SKIPPED)$`)
	gradleSummary = regexp.MustCompile(`^\d+ tests? completed(?:, \d+ failed)?(?:, \d+ skipped)?$`)
	junitCase     = regexp.MustCompile(`(?s)<testcase\b([^>]*?)(?:/>|>(.*?)</testcase>)`)
	junitAttr     = regexp.MustCompile(`\b(name|classname)="([^"]*)"`)
)

// The parts a match of goPackage and of nodeEnd has: the whole line and its three groups, and of nodeSummary: the line
// and its two groups.
const (
	threeGroupParts = 4
	twoGroupParts   = 3
)

// Read reads the facts of logs, each the text of one log or kept file, in order. A JUnit testcase element is read
// wherever it appears, so a report printed to a log reads as one kept as a file does.
func Read(logs ...string) Facts {
	reader := newReader()
	for _, text := range logs {
		reader.readJUnit(text)
		for line := range strings.SplitSeq(text, "\n") {
			reader.readLine(strings.TrimRight(line, "\r"))
		}
	}
	return reader.facts
}

// reader is a reading under way: the facts so far, and the index of each test and package in them by name.
type reader struct {
	facts    Facts
	tests    map[string]int
	packages map[string]int
}

// newReader returns a reading with nothing read.
func newReader() *reader {
	return &reader{tests: map[string]int{}, packages: map[string]int{}}
}

// format records that format was recognized.
func (r *reader) format(format string) {
	if !slices.Contains(r.facts.Formats, format) {
		r.facts.Formats = append(r.facts.Formats, format)
	}
}

// test records that the test name was seen with result, which is empty for a start. A result replaces none, and failed
// replaces any other, so a test that failed once reads as failed.
func (r *reader) test(name, result string) {
	i, seen := r.tests[name]
	if !seen {
		r.tests[name] = len(r.facts.Tests)
		r.facts.Tests = append(r.facts.Tests, Test{Name: name, Result: result})
		return
	}
	current := r.facts.Tests[i].Result
	if result != "" && (current == "" || result == resultFailed) {
		r.facts.Tests[i].Result = result
	}
}

// pkg records the result of the go package name. A later line replaces an earlier one, as a package run again ends
// where its last run ended.
func (r *reader) pkg(name, result string) {
	if i, seen := r.packages[name]; seen {
		r.facts.Packages[i].Result = result
		return
	}
	r.packages[name] = len(r.facts.Packages)
	r.facts.Packages = append(r.facts.Packages, Package{Name: name, Result: result})
}

// readLine reads one line of a log. A go test -json event is read as the output it carries.
func (r *reader) readLine(line string) {
	if strings.HasPrefix(line, "{") && strings.Contains(line, `"Action"`) {
		r.readJSON(line)
		return
	}
	if !r.readGo(line) && !r.readNode(line) {
		r.readGradle(line)
	}
}

// readJSON reads a go test -json event: the output an output event carries is read as a line of go test's text.
func (r *reader) readJSON(line string) {
	var event struct {
		Action string `json:"action"`
		Output string `json:"output"`
	}
	if json.Unmarshal([]byte(line), &event) != nil {
		return
	}
	r.format(FormatGoJSON)
	if event.Action == "output" {
		r.readGo(strings.TrimRight(event.Output, "\r\n"))
	}
}

// readGo reads a line of go test's text, and reports whether it was one.
func (r *reader) readGo(line string) bool {
	if m := goRun.FindStringSubmatch(line); m != nil {
		r.format(FormatGo)
		r.test(m[1], "")
		return true
	}
	if m := goEnd.FindStringSubmatch(line); m != nil {
		r.format(FormatGo)
		r.test(m[2], map[string]string{"PASS": resultPassed, "FAIL": resultFailed, "SKIP": resultSkipped}[m[1]])
		return true
	}
	if m := goNoFiles.FindStringSubmatch(line); m != nil {
		r.format(FormatGo)
		r.pkg(m[1], packageNoTestDir)
		return true
	}
	if strings.HasPrefix(line, "testing: warning: no tests to run") {
		r.format(FormatGo)
		return true
	}
	match := goPackage.FindStringSubmatch(line)
	if len(match) < threeGroupParts || (!strings.Contains(match[2], "/") && !strings.Contains(match[2], ".")) {
		return false
	}
	r.format(FormatGo)
	switch {
	case match[1] == "FAIL":
		r.pkg(match[2], packageFailed)
	case strings.Contains(match[3], "[no tests to run]"):
		r.pkg(match[2], packageNoTests)
		r.facts.NoTests = append(r.facts.NoTests, line)
	default:
		r.pkg(match[2], packageOK)
	}
	return true
}

// readNode reads a line of node --test's spec reporter, and reports whether it was one.
func (r *reader) readNode(line string) bool {
	if m := nodeSummary.FindStringSubmatch(line); m != nil {
		r.format(FormatNode)
		r.facts.Summaries = append(r.facts.Summaries, line)
		if m[1] == "tests" && m[2] == "0" {
			r.facts.NoTests = append(r.facts.NoTests, line)
		}
		return true
	}
	match := nodeEnd.FindStringSubmatch(line)
	if len(match) < threeGroupParts {
		return false
	}
	if strings.HasSuffix(match[2], "failing tests:") {
		return true
	}
	r.format(FormatNode)
	result := map[string]string{"✔": resultPassed, "✖": resultFailed, "﹣": resultSkipped}[match[1]]
	if match[3] != "" {
		result = resultSkipped
	}
	r.test(match[2], result)
	return true
}

// readGradle reads a Gradle test console line, and reports whether it was one.
func (r *reader) readGradle(line string) bool {
	if m := gradleEnd.FindStringSubmatch(line); m != nil {
		r.format(FormatGradle)
		r.test(m[1], map[string]string{"PASSED": resultPassed, "FAILED": resultFailed, "SKIPPED": resultSkipped}[m[2]])
		return true
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case gradleSummary.MatchString(trimmed), strings.HasPrefix(trimmed, "BUILD SUCCESSFUL"),
		strings.HasPrefix(trimmed, "BUILD FAILED"):
		r.format(FormatGradle)
		r.facts.Summaries = append(r.facts.Summaries, trimmed)
		return true
	case strings.HasPrefix(trimmed, "No tests found for given includes"):
		r.format(FormatGradle)
		r.facts.Summaries = append(r.facts.Summaries, trimmed)
		r.facts.NoTests = append(r.facts.NoTests, trimmed)
		return true
	}
	return false
}

// readJUnit reads each JUnit testcase element in text. A case named by its classname and name, joined by " > " as
// Gradle prints it, fails when it holds a failure or an error, is skipped when it holds a skipped, and passes otherwise.
func (r *reader) readJUnit(text string) {
	for _, testcase := range junitCase.FindAllStringSubmatch(text, -1) {
		attrs := map[string]string{}
		for _, attr := range junitAttr.FindAllStringSubmatch(testcase[1], -1) {
			attrs[attr[1]] = html.UnescapeString(attr[2])
		}
		if attrs["name"] == "" {
			continue
		}
		r.format(FormatJUnit)
		name := attrs["name"]
		if attrs["classname"] != "" {
			name = attrs["classname"] + " > " + name
		}
		result := resultPassed
		switch {
		case strings.Contains(testcase[2], "<failure"), strings.Contains(testcase[2], "<error"):
			result = resultFailed
		case strings.Contains(testcase[2], "<skipped"):
			result = resultSkipped
		}
		r.test(name, result)
	}
}

// NothingRan reports whether the output shows that no test ran: a runner said so, and no test, no go package that ran
// tests and no count above zero shows one did.
func (f Facts) NothingRan() bool {
	if len(f.NoTests) == 0 || len(f.Tests) > 0 {
		return false
	}
	for _, p := range f.Packages {
		if p.Result == packageOK || p.Result == packageFailed {
			return false
		}
	}
	for _, line := range f.Summaries {
		if m := nodeSummary.FindStringSubmatch(line); len(m) == twoGroupParts && m[1] == "tests" && m[2] != "0" {
			return false
		}
		if gradleSummary.MatchString(line) {
			return false
		}
	}
	return true
}

// names returns the names of the tests of f whose result is result, in the order first seen.
func (f Facts) names(result string) []string {
	var names []string
	for _, test := range f.Tests {
		if test.Result == result {
			names = append(names, test.Name)
		}
	}
	return names
}

// Lines returns the facts as lines for a prompt: the formats recognized, whether no test ran, the count of tests seen,
// then the tests that failed, were skipped, started with no result and passed, each list cut at maxNames names with the
// rest counted, then the go packages counted by result with the failed ones named, and the runners' summary lines. A
// reading that recognized nothing gives one line saying so.
func (f Facts) Lines() []string {
	if len(f.Formats) == 0 {
		return []string{"No test runner's output was recognized."}
	}
	lines := []string{"Formats recognized: " + strings.Join(f.Formats, ", ")}
	if f.NothingRan() {
		lines = append(lines, "No test ran: the output says no test ran, and shows none running.")
	}
	lines = append(lines, fmt.Sprintf("Tests seen: %d", len(f.Tests)))
	for _, list := range []struct{ label, result string }{
		{"Failed", resultFailed}, {"Skipped", resultSkipped}, {"Started with no result", ""}, {"Passed", resultPassed},
	} {
		if names := f.names(list.result); len(names) > 0 {
			lines = append(lines, fmt.Sprintf("%s (%d): %s", list.label, len(names), nameList(names)))
		}
	}
	if len(f.Packages) > 0 {
		lines = append(lines, "Go packages: "+packageCounts(f.Packages))
	}
	if len(f.Summaries) > 0 {
		lines = append(lines, "Summary lines: "+strings.Join(f.Summaries, " | "))
	}
	return lines
}

// nameList returns names joined by ", ", cut at maxNames with the rest counted.
func nameList(names []string) string {
	if len(names) <= maxNames {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:maxNames], ", ") + ", and " + strconv.Itoa(len(names)-maxNames) + " more"
}

// packageCounts returns the go packages counted by result, in the order ok, failed, no tests to run and no test files,
// with each failed package named.
func packageCounts(packages []Package) string {
	counts := map[string]int{}
	var failed []string
	for _, p := range packages {
		counts[p.Result]++
		if p.Result == packageFailed {
			failed = append(failed, p.Name)
		}
	}
	var parts []string
	for _, entry := range []struct{ result, label string }{
		{packageOK, "ok with tests run"}, {packageFailed, "failed"}, {packageNoTests, "with no tests to run"},
		{packageNoTestDir, "with no test files"},
	} {
		if counts[entry.result] == 0 {
			continue
		}
		part := fmt.Sprintf("%d %s", counts[entry.result], entry.label)
		if entry.result == packageFailed {
			part += " (" + nameList(failed) + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}
