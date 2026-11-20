// Package guard judges a task commit against the tests the epic's requirements and other tasks rely on. It refuses a
// commit that changes a capture file or adds a DOM-node assert, and reports each other weakening of a test as a fact.
package guard

import (
	"fmt"
	"strings"
)

// Test is one test a file declares: its name, and the assertions and skips in its body.
type Test struct {
	Name           string
	Asserts, Skips int
}

// Set names the files a task's commit is judged against.
type Set struct {
	// Guarded are the tests files of the epic's Requirements' items and of every task not failed.
	Guarded []string
	// Captures are the repository files the captures of those items name.
	Captures []string
	// Own are the task's own item's tests files.
	Own []string
	// Owned are the task's test_changes.
	Owned []string
}

// Change is one path the diff from the task base to the commit changed, with its
// content at each side when present there.
type Change struct {
	Path   string
	Base   []byte
	AtBase bool
	Head   []byte
	AtHead bool
}

// Refusal is one rule a commit's changes broke: the file, the test (empty when the rule is about the file), the rule,
// and whether the rule is a fact. A refusal refuses the commit. A fact refuses nothing: the task's floor reviewer judges
// it.
type Refusal struct {
	File, Test, Rule string
	Fact             bool
}

// Refusals returns the rules among judgments that refuse the commit, in order: a changed capture file and an added
// DOM-node assert.
func Refusals(judgments []Refusal) []Refusal {
	var refusals []Refusal
	for _, judgment := range judgments {
		if !judgment.Fact {
			refusals = append(refusals, judgment)
		}
	}
	return refusals
}

// FactLines returns the facts among judgments as lines, in order, each rendered as Refusal.String renders it. The
// task's floor reviewer judges them.
func FactLines(judgments []Refusal) []string {
	var lines []string
	for _, judgment := range judgments {
		if judgment.Fact {
			lines = append(lines, judgment.String())
		}
	}
	return lines
}

// String renders the refusal as `<file>: <test>: <rule>`, or `<file>: <rule>` when
// the refusal names no test.
func (r Refusal) String() string {
	if r.Test == "" {
		return r.File + ": " + r.Rule
	}
	return r.File + ": " + r.Test + ": " + r.Rule
}

const (
	ruleCapture    = "capture file changed"
	ruleDeleted    = "guarded file deleted"
	ruleNoRules    = "guarded file changed, and no rules read its language"
	ruleUnreadable = "guarded file changed, and its rules cannot read it: "
	ruleRemoved    = "test present at the base removed"
	ruleSkip       = "skip added"
)

// judgment holds the membership of each set field for one judging pass.
type judgment struct {
	guarded, captures, own, owned map[string]bool
}

// Judge returns the rules the changes break under the set, in change order, or nil when
// nothing is broken. Only a changed capture file and an added DOM-node assert refuse the commit; every other rule is a
// fact (see Refusals and FactLines). Every node test file the changes hold is also judged by the DOM assert rule,
// whatever set it is in (see domAssertsAdded).
func Judge(set Set, changes []Change) []Refusal {
	judged := judgment{
		guarded:  members(set.Guarded),
		captures: members(set.Captures),
		own:      members(set.Own),
		owned:    members(set.Owned),
	}
	judgments := make([]Refusal, 0, len(changes))
	for _, change := range changes {
		judgments = append(judgments, judged.change(change)...)
		judgments = append(judgments, domAssertsAdded(change)...)
	}
	if len(judgments) == 0 {
		return nil
	}
	return judgments
}

// judged reports whether the rules judge a change: a capture file, or a guarded file
// present at the base or owned by the task's own item. A file the task owns but does
// not own by its item is never judged.
func (j judgment) judged(change Change) bool {
	path := change.Path
	if j.owned[path] && !j.own[path] {
		return false
	}
	if j.captures[path] {
		return true
	}
	return j.guarded[path] && (change.AtBase || j.own[path])
}

// change returns the judgments one changed path breaks.
func (j judgment) change(change Change) []Refusal {
	path := change.Path
	if !j.judged(change) {
		return nil
	}
	if j.captures[path] {
		return []Refusal{{File: path, Rule: ruleCapture}}
	}
	// An owned test file that is also the task's own is judged by the skip rule only.
	ownedOwn := j.owned[path] && j.own[path]
	if change.AtBase && !change.AtHead && !ownedOwn {
		return []Refusal{{File: path, Rule: ruleDeleted, Fact: true}}
	}
	parse := parserFor(path)
	if parse == nil {
		return noRules(change, ownedOwn)
	}
	baseTests, err := readTests(parse, change.AtBase, change.Base)
	if err != nil {
		return []Refusal{{File: path, Rule: ruleUnreadable + err.Error(), Fact: true}}
	}
	headTests, err := readTests(parse, change.AtHead, change.Head)
	if err != nil {
		return []Refusal{{File: path, Rule: ruleUnreadable + err.Error(), Fact: true}}
	}
	var judgments []Refusal
	if change.AtBase && !ownedOwn {
		judgments = append(judgments, removedAndLowered(path, baseTests, headTests)...)
	}
	if change.AtBase || j.own[path] {
		judgments = append(judgments, skipsAdded(path, baseTests, headTests)...)
	}
	return judgments
}

// noRules returns the fact of a changed file no rules read. Only a file present at the base that the task does not
// own is a fact: creating a test is never judged, and an owned file's skip rule has nothing to read.
func noRules(change Change, ownedOwn bool) []Refusal {
	if !change.AtBase || ownedOwn {
		return nil
	}
	return []Refusal{{File: change.Path, Rule: ruleNoRules, Fact: true}}
}

// removedAndLowered returns the facts of the tests the commit removed or whose assertions it lowered.
func removedAndLowered(path string, baseTests, headTests []Test) []Refusal {
	baseNames, baseCounts := tally(baseTests)
	_, headCounts := tally(headTests)
	var facts []Refusal
	for _, name := range baseNames {
		head, present := headCounts[name]
		base := baseCounts[name]
		if !present {
			facts = append(facts, Refusal{File: path, Test: name, Rule: ruleRemoved, Fact: true})
		} else if head.Asserts < base.Asserts {
			facts = append(facts, Refusal{File: path, Test: name, Fact: true,
				Rule: fmt.Sprintf("assertions lowered from %d to %d", base.Asserts, head.Asserts)})
		}
	}
	return facts
}

// skipsAdded returns the facts of the tests whose skips the commit raised.
func skipsAdded(path string, baseTests, headTests []Test) []Refusal {
	headNames, headCounts := tally(headTests)
	_, baseCounts := tally(baseTests)
	var facts []Refusal
	for _, name := range headNames {
		if headCounts[name].Skips > baseCounts[name].Skips {
			facts = append(facts, Refusal{File: path, Test: name, Rule: ruleSkip, Fact: true})
		}
	}
	return facts
}

// parserFor returns the reader of a path's test declarations, or nil when no rules
// read its language.
func parserFor(path string) func(src []byte) ([]Test, error) {
	if strings.HasSuffix(path, "_test.go") {
		return goTests
	}
	if nodeTestPath(path) {
		return nodeTests
	}
	if strings.HasSuffix(path, ".kt") {
		return kotlinTests
	}
	return nil
}

// readTests returns the tests a side declares, or none when the side is absent.
func readTests(parse func(src []byte) ([]Test, error), present bool, src []byte) ([]Test, error) {
	if !present {
		return nil, nil
	}
	return parse(src)
}

// tally sums each name's counts, so a repeated name counts as one test, and returns
// the names in declaration order.
func tally(tests []Test) ([]string, map[string]Test) {
	var names []string
	counts := make(map[string]Test, len(tests))
	for _, test := range tests {
		sum, seen := counts[test.Name]
		if !seen {
			names = append(names, test.Name)
		}
		sum.Name = test.Name
		sum.Asserts += test.Asserts
		sum.Skips += test.Skips
		counts[test.Name] = sum
	}
	return names, counts
}

// members returns the set of paths.
func members(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, path := range paths {
		set[path] = true
	}
	return set
}
