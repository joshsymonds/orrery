// Package guard refuses a task commit that weakens the tests the epic's
// requirements and other tasks rely on.
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

// Refusal is one rule a commit broke: the file, the test (empty when the rule is
// about the file), and the rule.
type Refusal struct {
	File, Test, Rule string
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

// Judge returns the refusals the changes break under the set, in change order, or nil
// when nothing is refused.
func Judge(set Set, changes []Change) []Refusal {
	judged := judgment{
		guarded:  members(set.Guarded),
		captures: members(set.Captures),
		own:      members(set.Own),
		owned:    members(set.Owned),
	}
	refusals := make([]Refusal, 0, len(changes))
	for _, change := range changes {
		refusals = append(refusals, judged.change(change)...)
	}
	if len(refusals) == 0 {
		return nil
	}
	return refusals
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

// change returns the refusals one changed path breaks.
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
		return []Refusal{{File: path, Rule: ruleDeleted}}
	}
	parse := parserFor(path)
	if parse == nil {
		return noRules(change, ownedOwn)
	}
	baseTests, err := readTests(parse, change.AtBase, change.Base)
	if err != nil {
		return []Refusal{{File: path, Rule: ruleUnreadable + err.Error()}}
	}
	headTests, err := readTests(parse, change.AtHead, change.Head)
	if err != nil {
		return []Refusal{{File: path, Rule: ruleUnreadable + err.Error()}}
	}
	var refusals []Refusal
	if change.AtBase && !ownedOwn {
		refusals = append(refusals, removedAndLowered(path, baseTests, headTests)...)
	}
	if change.AtBase || j.own[path] {
		refusals = append(refusals, skipsAdded(path, baseTests, headTests)...)
	}
	return refusals
}

// noRules returns the refusal of a changed file no rules read. Only a file present at the base that the task does not
// own is refused: creating a test is never refused, and an owned file's skip rule has nothing to read.
func noRules(change Change, ownedOwn bool) []Refusal {
	if !change.AtBase || ownedOwn {
		return nil
	}
	return []Refusal{{File: change.Path, Rule: ruleNoRules}}
}

// removedAndLowered returns the refusals for tests the commit removed or whose
// assertions it lowered.
func removedAndLowered(path string, baseTests, headTests []Test) []Refusal {
	baseNames, baseCounts := tally(baseTests)
	_, headCounts := tally(headTests)
	var refusals []Refusal
	for _, name := range baseNames {
		head, present := headCounts[name]
		base := baseCounts[name]
		if !present {
			refusals = append(refusals, Refusal{File: path, Test: name, Rule: ruleRemoved})
		} else if head.Asserts < base.Asserts {
			refusals = append(refusals, Refusal{File: path, Test: name,
				Rule: fmt.Sprintf("assertions lowered from %d to %d", base.Asserts, head.Asserts)})
		}
	}
	return refusals
}

// skipsAdded returns the refusals for tests whose skips the commit raised.
func skipsAdded(path string, baseTests, headTests []Test) []Refusal {
	headNames, headCounts := tally(headTests)
	_, baseCounts := tally(baseTests)
	var refusals []Refusal
	for _, name := range headNames {
		if headCounts[name].Skips > baseCounts[name].Skips {
			refusals = append(refusals, Refusal{File: path, Test: name, Rule: ruleSkip})
		}
	}
	return refusals
}

// parserFor returns the reader of a path's test declarations, or nil when no rules
// read its language.
func parserFor(path string) func(src []byte) ([]Test, error) {
	if strings.HasSuffix(path, "_test.go") {
		return goTests
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
