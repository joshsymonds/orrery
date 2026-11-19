package guard

import (
	"slices"
	"strings"
	"testing"
)

const (
	testA1      = `func TestA(t *testing.T) { t.Fatal("one") }`
	testA2      = `func TestA(t *testing.T) { t.Fatal("one"); t.Error("two") }`
	testASkip   = `func TestA(t *testing.T) { t.Fatal("one"); t.Skip("slow") }`
	testB1      = `func TestB(t *testing.T) { t.Fatal("one") }`
	testCSkip   = `func TestC(t *testing.T) { t.Skipf("x") }`
	helperFunc  = `func helper(t *testing.T) { t.Fatal("x") }`
	methodFunc  = `func (s suite) TestX(t *testing.T) { t.Fatal("x") }`
	benchFunc   = `func BenchmarkX(b *testing.B) { b.Fatal("x") }`
	fuzzFunc    = `func FuzzY(f *testing.F) { f.Fail() }`
	exampleFunc = `func ExampleZ() {}`
	unreadRule  = "guarded file changed, and its rules cannot read it: "
	noRulesRule = "guarded file changed, and no rules read its language"
)

// goFile is a Go test file declaring the given functions.
func goFile(funcs ...string) string {
	return "package x\n\nimport \"testing\"\n\n" + strings.Join(funcs, "\n") + "\n"
}

// mod is a path present at the base and at the commit.
func mod(path, base, head string) Change {
	return Change{Path: path, Base: []byte(base), AtBase: true, Head: []byte(head), AtHead: true}
}

// del is a path present at the base and absent at the commit.
func del(path, base string) Change {
	return Change{Path: path, Base: []byte(base), AtBase: true}
}

// add is a path absent at the base and present at the commit.
func add(path, head string) Change {
	return Change{Path: path, Head: []byte(head), AtHead: true}
}

func TestJudge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		set     Set
		changes []Change
		want    []Refusal
	}{
		{
			name:    "capture file changed",
			set:     Set{Captures: []string{"scripts/gen.sh"}},
			changes: []Change{mod("scripts/gen.sh", "old", "new")},
			want:    []Refusal{{File: "scripts/gen.sh", Rule: "capture file changed"}},
		},
		{
			name:    "owned capture file lifted",
			set:     Set{Captures: []string{"scripts/gen.sh"}, Owned: []string{"scripts/gen.sh"}},
			changes: []Change{mod("scripts/gen.sh", "old", "new")},
		},
		{
			name:    "guarded test file deleted",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{del("a_test.go", goFile(testA1))},
			want:    []Refusal{{File: "a_test.go", Rule: "guarded file deleted", Fact: true}},
		},
		{
			name:    "guarded file with no rules changed",
			set:     Set{Guarded: []string{"lib/notes.txt"}},
			changes: []Change{mod("lib/notes.txt", "a", "b")},
			want:    []Refusal{{File: "lib/notes.txt", Rule: noRulesRule, Fact: true}},
		},
		{
			name:    "new guarded file with no rules",
			set:     Set{Guarded: []string{"lib/notes.txt"}},
			changes: []Change{add("lib/notes.txt", "a")},
		},
		{
			name:    "unjudged file ignored",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("b_test.go", goFile(testA2), goFile(testA1))},
		},
		{
			name:    "test removed",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1, testB1), goFile(testA1))},
			want:    []Refusal{{File: "a_test.go", Test: "TestB", Rule: "test present at the base removed", Fact: true}},
		},
		{
			name:    "assertions lowered",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA2), goFile(testA1))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 2 to 1", Fact: true}},
		},
		{
			name:    "assertions raised",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1), goFile(testA2))},
		},
		{
			name:    "new test with assertions",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1), goFile(testA1, testB1))},
		},
		{
			name:    "skip added to existing test",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1), goFile(testASkip))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "skip added", Fact: true}},
		},
		{
			name:    "skip already present kept",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testASkip), goFile(testASkip, testB1))},
		},
		{
			name:    "skip added to new test in existing file",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1), goFile(testA1, testCSkip))},
			want:    []Refusal{{File: "a_test.go", Test: "TestC", Rule: "skip added", Fact: true}},
		},
		{
			name:    "repeated names sum their assertions",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1, testA1), goFile(testA1))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 2 to 1", Fact: true}},
		},
		{
			name:    "repeated names kept",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1, testA1), goFile(testA1, testA1))},
		},
		{
			name:    "helpers and methods are not tests",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(helperFunc, methodFunc, testA1), goFile(testA1))},
		},
		{
			name:    "benchmark fuzz and example are tests",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(benchFunc, fuzzFunc, exampleFunc), goFile())},
			want: []Refusal{
				{File: "a_test.go", Test: "BenchmarkX", Rule: "test present at the base removed", Fact: true},
				{File: "a_test.go", Test: "FuzzY", Rule: "test present at the base removed", Fact: true},
				{File: "a_test.go", Test: "ExampleZ", Rule: "test present at the base removed", Fact: true},
			},
		},
		{
			name: "closure assertions counted",
			set:  Set{Guarded: []string{"a_test.go"}},
			changes: []Change{
				mod(
					"a_test.go",
					goFile(`func TestA(t *testing.T) { func() { t.Fatal("x") }() }`),
					goFile(`func TestA(t *testing.T) { }`),
				),
			},
			want: []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 1 to 0", Fact: true}},
		},
		{
			name: "every assertion form counted",
			set:  Set{Guarded: []string{"a_test.go"}},
			changes: []Change{
				mod(
					"a_test.go",
					goFile(
						`func TestA(t *testing.T) { t.Error("a"); t.Errorf("b"); t.Fail(); t.FailNow(); t.Fatal("c"); t.Fatalf("d") }`,
					),
					goFile(
						`func TestA(t *testing.T) { t.Error("a"); t.Errorf("b"); t.Fail(); t.FailNow(); t.Fatal("c") }`,
					),
				),
			},
			want: []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 6 to 5", Fact: true}},
		},
		{
			name: "skip form Skip counted",
			set:  Set{Guarded: []string{"a_test.go"}},
			changes: []Change{
				mod("a_test.go", goFile(testB1), goFile(`func TestB(t *testing.T) { t.Fatal("one"); t.Skip() }`)),
			},
			want: []Refusal{{File: "a_test.go", Test: "TestB", Rule: "skip added", Fact: true}},
		},
		{
			name: "skip form SkipNow counted",
			set:  Set{Guarded: []string{"a_test.go"}},
			changes: []Change{
				mod("a_test.go", goFile(testB1), goFile(`func TestB(t *testing.T) { t.Fatal("one"); t.SkipNow() }`)),
			},
			want: []Refusal{{File: "a_test.go", Test: "TestB", Rule: "skip added", Fact: true}},
		},
		{
			name: "skip form Skipf counted",
			set:  Set{Guarded: []string{"a_test.go"}},
			changes: []Change{
				mod("a_test.go", goFile(testB1), goFile(`func TestB(t *testing.T) { t.Fatal("one"); t.Skipf("x") }`)),
			},
			want: []Refusal{{File: "a_test.go", Test: "TestB", Rule: "skip added", Fact: true}},
		},
		{
			name:    "owned test file lifts removal",
			set:     Set{Guarded: []string{"a_test.go"}, Owned: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1, testB1), goFile(testA1))},
		},
		{
			name:    "owned test file lifts lowered assertions",
			set:     Set{Guarded: []string{"a_test.go"}, Owned: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA2), goFile(testA1))},
		},
		{
			name:    "owned test file lifts deletion",
			set:     Set{Guarded: []string{"a_test.go"}, Owned: []string{"a_test.go"}},
			changes: []Change{del("a_test.go", goFile(testA1))},
		},
		{
			name:    "owned test file lifts added skip",
			set:     Set{Guarded: []string{"a_test.go"}, Owned: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1), goFile(testASkip))},
		},
		{
			name:    "owned test file lifts unreadable",
			set:     Set{Guarded: []string{"a_test.go"}, Owned: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1), "package x\nfunc {")},
		},
		{
			name:    "owned file with no rules lifted",
			set:     Set{Guarded: []string{"notes.txt"}, Owned: []string{"notes.txt"}},
			changes: []Change{mod("notes.txt", "a", "b")},
		},
		{
			name:    "own and owned test file lifts deletion",
			set:     Set{Guarded: []string{"a_test.go"}, Own: []string{"a_test.go"}, Owned: []string{"a_test.go"}},
			changes: []Change{del("a_test.go", goFile(testA1))},
		},
		{
			name:    "own test file's added skip is a fact",
			set:     Set{Guarded: []string{"a_test.go"}, Own: []string{"a_test.go"}, Owned: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1), goFile(testASkip))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "skip added", Fact: true}},
		},
		{
			name:    "own test file judged by skip rule only",
			set:     Set{Guarded: []string{"a_test.go"}, Own: []string{"a_test.go"}, Owned: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA2), goFile(testA1))},
		},
		{
			name:    "own test file not owned keeps every rule",
			set:     Set{Guarded: []string{"a_test.go"}, Own: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA2), goFile(testA1))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 2 to 1", Fact: true}},
		},
		{
			name:    "new own test file with skip is a fact",
			set:     Set{Guarded: []string{"a_test.go"}, Own: []string{"a_test.go"}},
			changes: []Change{add("a_test.go", goFile(testASkip))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "skip added", Fact: true}},
		},
		{
			name:    "new own test file without skip kept",
			set:     Set{Guarded: []string{"a_test.go"}, Own: []string{"a_test.go"}},
			changes: []Change{add("a_test.go", goFile(testA1))},
		},
		{
			name:    "new guarded test file with skip not own kept",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{add("a_test.go", goFile(testASkip))},
		},
		{
			name:    "owned own file with no rules kept",
			set:     Set{Guarded: []string{"notes.txt"}, Own: []string{"notes.txt"}, Owned: []string{"notes.txt"}},
			changes: []Change{mod("notes.txt", "a", "b")},
		},
		{
			name:    "new own file with no rules kept",
			set:     Set{Guarded: []string{"notes.txt"}, Own: []string{"notes.txt"}},
			changes: []Change{add("notes.txt", "a")},
		},
		{
			name:    "own file with no rules present at the base is a fact",
			set:     Set{Guarded: []string{"notes.txt"}, Own: []string{"notes.txt"}},
			changes: []Change{mod("notes.txt", "a", "b")},
			want:    []Refusal{{File: "notes.txt", Rule: noRulesRule, Fact: true}},
		},
		{
			name: "judgments in change order",
			set:  Set{Guarded: []string{"a_test.go", "b_test.go"}},
			changes: []Change{
				mod("b_test.go", goFile(testA2), goFile(testA1)),
				mod("a_test.go", goFile(testA1, testB1), goFile(testA1)),
			},
			want: []Refusal{
				{File: "b_test.go", Test: "TestA", Rule: "assertions lowered from 2 to 1", Fact: true},
				{File: "a_test.go", Test: "TestB", Rule: "test present at the base removed", Fact: true},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Judge(tc.set, tc.changes)
			if !slices.Equal(got, tc.want) {
				t.Errorf("Judge() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestGuardFactsPassTheGuard covers G18: a change that removes a test, lowers its assertions, adds a skip, deletes a
// guarded file or changes a guarded file no rule reads refuses nothing, and each of its judgments is a fact the floor
// reviewer judges, one line each.
func TestGuardFactsPassTheGuard(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		set    Set
		change Change
		want   []string
	}{
		{
			name:   "test removed",
			set:    Set{Guarded: []string{"a_test.go"}},
			change: mod("a_test.go", goFile(testA1, testB1), goFile(testA1)),
			want:   []string{"a_test.go: TestB: test present at the base removed"},
		},
		{
			name:   "assertions lowered",
			set:    Set{Guarded: []string{"a_test.go"}},
			change: mod("a_test.go", goFile(testA2), goFile(testA1)),
			want:   []string{"a_test.go: TestA: assertions lowered from 2 to 1"},
		},
		{
			name:   "skip added",
			set:    Set{Guarded: []string{"a_test.go"}},
			change: mod("a_test.go", goFile(testA1), goFile(testASkip)),
			want:   []string{"a_test.go: TestA: skip added"},
		},
		{
			name:   "guarded file deleted",
			set:    Set{Guarded: []string{"a_test.go"}},
			change: del("a_test.go", goFile(testA1)),
			want:   []string{"a_test.go: guarded file deleted"},
		},
		{
			name:   "guarded file no rule reads",
			set:    Set{Guarded: []string{"notes.txt"}},
			change: mod("notes.txt", "a", "b"),
			want:   []string{"notes.txt: " + noRulesRule},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			judgments := Judge(tc.set, []Change{tc.change})
			if refusals := Refusals(judgments); refusals != nil {
				t.Errorf("Refusals() = %v, want none", refusals)
			}
			if got := FactLines(judgments); !slices.Equal(got, tc.want) {
				t.Errorf("FactLines() = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("test unreadable", func(t *testing.T) {
		t.Parallel()
		judgments := Judge(Set{Guarded: []string{"a_test.go"}}, []Change{mod("a_test.go", goFile(testA1), "package x\nfunc {")})
		facts := FactLines(judgments)
		if refusals := Refusals(judgments); refusals != nil || len(facts) != 1 ||
			!strings.HasPrefix(facts[0], "a_test.go: "+unreadRule) {
			t.Errorf("Refusals() = %v, FactLines() = %v, want no refusal and one unreadable fact naming a_test.go",
				refusals, facts)
		}
	})
}

// TestGuardFactsRefuseOnlyCaptureAndDOM covers G18: in one change, a changed capture file and an added DOM-node assert
// still refuse, and a lowered assertion is a fact that the refusals leave out.
func TestGuardFactsRefuseOnlyCaptureAndDOM(t *testing.T) {
	t.Parallel()
	set := Set{Guarded: []string{"a_test.go"}, Captures: []string{"scripts/gen.sh"}}
	judgments := Judge(set, []Change{
		mod("scripts/gen.sh", "old", "new"),
		mod("a_test.go", goFile(testA2), goFile(testA1)),
		mod(domTestPath, domBase, withLine(`assert.equal(document.activeElement, box);`)),
	})
	wantRefusals := []Refusal{
		{File: "scripts/gen.sh", Rule: "capture file changed"},
		domRefusal("assert.equal", 3),
	}
	if got := Refusals(judgments); !slices.Equal(got, wantRefusals) {
		t.Errorf("Refusals() = %v, want %v", got, wantRefusals)
	}
	wantFacts := []string{"a_test.go: TestA: assertions lowered from 2 to 1"}
	if got := FactLines(judgments); !slices.Equal(got, wantFacts) {
		t.Errorf("FactLines() = %v, want %v", got, wantFacts)
	}
}

func TestJudgeUnreadable(t *testing.T) {
	t.Parallel()
	set := Set{Guarded: []string{"a_test.go"}}
	const broken = "package x\n\nfunc ("
	for _, tc := range []struct {
		name   string
		change Change
	}{
		{name: "head does not parse", change: mod("a_test.go", goFile(testA1), broken)},
		{name: "base does not parse", change: mod("a_test.go", broken, goFile(testA1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Judge(set, []Change{tc.change})
			if len(got) != 1 || got[0].File != "a_test.go" || got[0].Test != "" || !got[0].Fact ||
				!strings.HasPrefix(got[0].Rule, unreadRule) {
				t.Errorf("Judge() = %v, want one unreadable fact naming a_test.go", got)
			}
		})
	}
	t.Run("new file that does not parse is not judged", func(t *testing.T) {
		t.Parallel()
		if got := Judge(set, []Change{add("a_test.go", broken)}); got != nil {
			t.Errorf("Judge() = %v, want nil", got)
		}
	})
}

func TestRefusalString(t *testing.T) {
	t.Parallel()
	if got, want := (Refusal{File: "a_test.go", Test: "TestA", Rule: "skip added"}).String(), "a_test.go: TestA: skip added"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := (Refusal{File: "lib.txt", Rule: "guarded file deleted"}).String(), "lib.txt: guarded file deleted"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
