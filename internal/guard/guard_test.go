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
			want:    []Refusal{{File: "a_test.go", Rule: "guarded file deleted"}},
		},
		{
			name:    "guarded file with no rules changed",
			set:     Set{Guarded: []string{"lib/notes.txt"}},
			changes: []Change{mod("lib/notes.txt", "a", "b")},
			want:    []Refusal{{File: "lib/notes.txt", Rule: noRulesRule}},
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
			want:    []Refusal{{File: "a_test.go", Test: "TestB", Rule: "test present at the base removed"}},
		},
		{
			name:    "assertions lowered",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA2), goFile(testA1))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 2 to 1"}},
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
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "skip added"}},
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
			want:    []Refusal{{File: "a_test.go", Test: "TestC", Rule: "skip added"}},
		},
		{
			name:    "repeated names sum their assertions",
			set:     Set{Guarded: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1, testA1), goFile(testA1))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 2 to 1"}},
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
				{File: "a_test.go", Test: "BenchmarkX", Rule: "test present at the base removed"},
				{File: "a_test.go", Test: "FuzzY", Rule: "test present at the base removed"},
				{File: "a_test.go", Test: "ExampleZ", Rule: "test present at the base removed"},
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
			want: []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 1 to 0"}},
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
			want: []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 6 to 5"}},
		},
		{
			name: "skip form Skip counted",
			set:  Set{Guarded: []string{"a_test.go"}},
			changes: []Change{
				mod("a_test.go", goFile(testB1), goFile(`func TestB(t *testing.T) { t.Fatal("one"); t.Skip() }`)),
			},
			want: []Refusal{{File: "a_test.go", Test: "TestB", Rule: "skip added"}},
		},
		{
			name: "skip form SkipNow counted",
			set:  Set{Guarded: []string{"a_test.go"}},
			changes: []Change{
				mod("a_test.go", goFile(testB1), goFile(`func TestB(t *testing.T) { t.Fatal("one"); t.SkipNow() }`)),
			},
			want: []Refusal{{File: "a_test.go", Test: "TestB", Rule: "skip added"}},
		},
		{
			name: "skip form Skipf counted",
			set:  Set{Guarded: []string{"a_test.go"}},
			changes: []Change{
				mod("a_test.go", goFile(testB1), goFile(`func TestB(t *testing.T) { t.Fatal("one"); t.Skipf("x") }`)),
			},
			want: []Refusal{{File: "a_test.go", Test: "TestB", Rule: "skip added"}},
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
			name:    "own test file refuses added skip",
			set:     Set{Guarded: []string{"a_test.go"}, Own: []string{"a_test.go"}, Owned: []string{"a_test.go"}},
			changes: []Change{mod("a_test.go", goFile(testA1), goFile(testASkip))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "skip added"}},
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
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "assertions lowered from 2 to 1"}},
		},
		{
			name:    "new own test file with skip refused",
			set:     Set{Guarded: []string{"a_test.go"}, Own: []string{"a_test.go"}},
			changes: []Change{add("a_test.go", goFile(testASkip))},
			want:    []Refusal{{File: "a_test.go", Test: "TestA", Rule: "skip added"}},
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
			name:    "own file with no rules present at the base refused",
			set:     Set{Guarded: []string{"notes.txt"}, Own: []string{"notes.txt"}},
			changes: []Change{mod("notes.txt", "a", "b")},
			want:    []Refusal{{File: "notes.txt", Rule: noRulesRule}},
		},
		{
			name: "refusals in change order",
			set:  Set{Guarded: []string{"a_test.go", "b_test.go"}},
			changes: []Change{
				mod("b_test.go", goFile(testA2), goFile(testA1)),
				mod("a_test.go", goFile(testA1, testB1), goFile(testA1)),
			},
			want: []Refusal{
				{File: "b_test.go", Test: "TestA", Rule: "assertions lowered from 2 to 1"},
				{File: "a_test.go", Test: "TestB", Rule: "test present at the base removed"},
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
			if len(got) != 1 || got[0].File != "a_test.go" || got[0].Test != "" ||
				!strings.HasPrefix(got[0].Rule, unreadRule) {
				t.Errorf("Judge() = %v, want one unreadable refusal naming a_test.go", got)
			}
		})
	}
	t.Run("new file that does not parse is not refused", func(t *testing.T) {
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
