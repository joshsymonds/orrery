package guard

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// domTestPath is the node test file the DOM assert tests change.
const domTestPath = "web/src/a.test.tsx"

// domBase is a node test file with no DOM assert, which the DOM assert tests add lines to.
const domBase = `test("a", () => {
  assert.ok(1);
});
`

// withLine returns domBase with line added to its test, as line 3.
func withLine(line string) string {
	return strings.Replace(domBase, "  assert.ok(1);\n", "  assert.ok(1);\n  "+line+"\n", 1)
}

// domRefusal returns the refusal of the DOM assert call, as assert.<name>, at line of domTestPath.
func domRefusal(call string, line int) Refusal {
	return Refusal{File: domTestPath, Rule: fmt.Sprintf(ruleDOMAssert, call, line)}
}

func TestJudgeRefusesAnAddedDOMAssert(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		call string
	}{
		{"querySelector first, null second", `assert.equal(container.querySelector(".x"), null);`, "assert.equal"},
		{"activeElement first", `assert.strictEqual(document.activeElement, box);`, "assert.strictEqual"},
		{"closest", `assert.notEqual(el.closest("li"), other, "moved");`, "assert.notEqual"},
		{"second argument", `assert.deepEqual(null, root?.querySelector("a"));`, "assert.deepEqual"},
		{"optional chain", `assert.notStrictEqual(lineOf(c, "p")?.querySelector("b"), null);`, "assert.notStrictEqual"},
		{"non-null assertion", `assert.deepStrictEqual(c.querySelector("a")!, x);`, "assert.deepStrictEqual"},
		{"cast", `assert.equal(c.querySelector("a") as HTMLElement, x);`, "assert.equal"},
		{"template selector", "assert.equal(c.querySelector(`[data-key=\"${key}\"]`), null);", "assert.equal"},
		{"split over lines", "assert.equal(\n    c.querySelector(\".words\"),\n    null,\n  );", "assert.equal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Judge(Set{}, []Change{mod(domTestPath, domBase, withLine(tc.line))})
			want := []Refusal{domRefusal(tc.call, 3)}
			if !slices.Equal(got, want) {
				t.Errorf("Judge() = %v, want %v", got, want)
			}
		})
	}
}

func TestJudgeLeavesAssertsThatCompareNoDOMNode(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		`assert.equal(container.querySelector(".x") === null, true);`,
		`assert.equal(document.activeElement === box, false);`,
		`assert.equal(container.querySelector(".x")?.textContent, "a");`,
		`assert.equal(el.closest("li")?.className, "row");`,
		`assert.equal(text, "a", container.querySelector(".x"));`,
		`assert.ok(container.querySelector(".x"));`,
		`assert.match(document.activeElement.id, /x/);`,
		`assertNone(container.querySelector(".x"));`,
		`assertSame(document.activeElement, box);`,
		`const text = "assert.equal(document.activeElement, box)";`,
		`// assert.equal(container.querySelector(".x"), null);`,
		`assert.equal(querySelector(".x"), null);`,
		`other.assert.equal(document.activeElement, box);`,
	} {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			if got := Judge(Set{}, []Change{mod(domTestPath, domBase, withLine(line))}); got != nil {
				t.Errorf("Judge() = %v, want no refusal", got)
			}
		})
	}
}

func TestJudgeRefusesOnlyTheDOMAssertsAChangeAdds(t *testing.T) {
	t.Parallel()
	const held = `assert.equal(container.querySelector(".x"), null);`
	base := withLine(held)
	tests := []struct {
		name string
		head string
		want []Refusal
	}{
		{
			name: "a held call with other lines changed",
			head: strings.Replace(base, "assert.ok(1);", "assert.ok(2);", 1),
		},
		{
			name: "a held call moved",
			head: domBase + "test(\"b\", () => {\n  " + held + "\n});\n",
		},
		{
			name: "a second copy of a held call",
			head: strings.Replace(base, held, held+"\n  "+held, 1),
			want: []Refusal{domRefusal("assert.equal", 4)},
		},
		{
			name: "a held call whose message changed",
			head: strings.Replace(base, `null);`, `null, "gone");`, 1),
			want: []Refusal{domRefusal("assert.equal", 3)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Judge(Set{}, []Change{mod(domTestPath, base, tc.head)})
			if !slices.Equal(got, tc.want) {
				t.Errorf("Judge() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestJudgeRefusesDOMAssertsWhateverSetTheFileIsIn(t *testing.T) {
	t.Parallel()
	head := withLine(`assert.equal(document.activeElement, box);`)
	want := []Refusal{domRefusal("assert.equal", 3)}
	for name, set := range map[string]Set{
		"in no set":                {},
		"guarded":                  {Guarded: []string{domTestPath}},
		"owned and not the item's": {Owned: []string{domTestPath}},
		"the task's own":           {Own: []string{domTestPath}, Owned: []string{domTestPath}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := Judge(set, []Change{mod(domTestPath, domBase, head)}); !slices.Equal(got, want) {
				t.Errorf("Judge() = %v, want %v", got, want)
			}
		})
	}
	t.Run("a new file", func(t *testing.T) {
		t.Parallel()
		if got := Judge(Set{}, []Change{add(domTestPath, head)}); !slices.Equal(got, want) {
			t.Errorf("Judge() = %v, want %v", got, want)
		}
	})
}

func TestJudgeReadsNoDOMAssertsOutsideNodeTests(t *testing.T) {
	t.Parallel()
	line := `assert.equal(document.activeElement, box);`
	for _, change := range []Change{
		mod("web/src/a.ts", domBase, withLine(line)),
		del(domTestPath, withLine(line)),
		mod(domTestPath, "test(\"a\", () => {", withLine(line)),
	} {
		if got := Judge(Set{}, []Change{change}); got != nil {
			t.Errorf("Judge(%s) = %v, want no refusal", change.Path, got)
		}
	}
}

func TestNodeTestsCountTheDOMAssertHelpers(t *testing.T) {
	t.Parallel()
	src := `test("a", () => {
  assertNone(container.querySelector(".x"));
  assertSame(document.activeElement, box, "focus");
  helpers.assertNone(x);
});
function assertNoneLocal() {}
`
	got, err := nodeTests([]byte(src))
	if err != nil {
		t.Fatalf("nodeTests() error = %v", err)
	}
	if want := []Test{{Name: "a", Asserts: 2}}; !slices.Equal(got, want) {
		t.Errorf("nodeTests() = %v, want %v", got, want)
	}
}

func TestCheckReadsEveryChangedNodeTestFile(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	base := commitFiles(t, repo, map[string]string{domTestPath: domBase})
	head := commitFiles(t, repo, map[string]string{domTestPath: withLine(`assert.equal(document.activeElement, box);`)})
	got, err := Check(t.Context(), repo, base, head, Spec{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if want := []Refusal{domRefusal("assert.equal", 3)}; !slices.Equal(got, want) {
		t.Errorf("refusals = %v, want %v", got, want)
	}
}
