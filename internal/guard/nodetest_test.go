package guard

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// backtick is the character a raw Go string cannot hold, so template literals are built from it.
const backtick = "`"

func TestNodeTests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []Test
	}{
		{
			name: "names come from each quote kind",
			src: `import { test } from "node:test";
test("double", () => {});
test('single', () => {});
test(` + backtick + `back` + backtick + `, () => {});`,
			want: []Test{{Name: "double"}, {Name: "single"}, {Name: "back"}},
		},
		{
			name: "assertions count to the innermost test",
			src: `describe("outer", () => {
  assert.ok(0);
  test("inner", () => {
    assert.equal(1, 1);
    assert.deepEqual([], []);
  });
  assert.ok(1);
});`,
			want: []Test{{Name: "outer", Asserts: 2}, {Name: "inner", Asserts: 2}},
		},
		{
			name: "assertions outside any test count nowhere",
			src: `function check(value) {
  assert.ok(value);
}
test("a", () => {
  check(1);
});`,
			want: []Test{{Name: "a"}},
		},
		{
			name: "comments and string text are not read",
			src: `// test("line", () => { assert.ok(1); });
/* test("block", () => { assert.ok(1); }); */
test("a", () => {
  const text = "assert.ok(1); test('fake', () => {})";
  const more = 'assert(2)';
  assert.ok(text + more);
});`,
			want: []Test{{Name: "a", Asserts: 1}},
		},
		{
			name: "template text and regular expressions are not read",
			src: `test("a", () => {
  const text = ` + backtick + `assert.ok(1) (` + backtick + `;
  assert.match(text, /\(it's\)|[{]/);
});`,
			want: []Test{{Name: "a", Asserts: 1}},
		},
		{
			name: "JSX text is not read",
			src: `test("a", () => {
  assert.match(text, /x/);
  render(<p>don't stop (here</p>);
  assert.ok(text);
});`,
			want: []Test{{Name: "a", Asserts: 2}},
		},
		{
			name: "a template with a substitution names its test by its text",
			src: `describe("d", () => {
  test(` + backtick + `case ${n}` + backtick + `, () => { assert.ok(1); });
});`,
			want: []Test{{Name: "d"}, {Name: "case ${n}", Asserts: 1}},
		},
		{
			name: "a substitution with braces is read as code",
			src: `test("a", () => {
  assert.equal(` + backtick + `${ {k: 1}.k }` + backtick + `, "1");
});`,
			want: []Test{{Name: "a", Asserts: 1}},
		},
		{
			name: "a first argument that is not a literal name declares nothing",
			src: `test(name, () => {});
test("a" + name, () => {});
foo.test("b", () => {});
function it(value) {}`,
		},
		{
			name: "each skip form counts toward its own test",
			src: `test.skip("a", () => { assert.ok(1); });
it.todo("b", () => {});
describe.only("c", () => {
  test("d", () => { t.skip(); t.todo("x"); t.only(); });
});
test("e", { skip: true }, () => {});
test("f", { todo: "later", only: false }, () => {});
test("g", { skipped: true }, () => {});`,
			want: []Test{
				{Name: "a", Asserts: 1, Skips: 1},
				{Name: "b", Skips: 1},
				{Name: "c", Skips: 1},
				{Name: "d", Skips: 3},
				{Name: "e", Skips: 1},
				{Name: "f", Skips: 2},
				{Name: "g"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := nodeTests([]byte(tc.src))
			if err != nil {
				t.Fatalf("nodeTests() error = %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("nodeTests() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNodeTestsUnreadable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		src  string
	}{
		{name: "unclosed call", src: `test("a", () => { assert.ok(1);`},
		{name: "extra close paren", src: `test("a", () => {}));`},
		{name: "unterminated string", src: `test("a, () => {});`},
		{name: "unterminated template", src: `test(` + backtick + `a, () => {});`},
		{name: "unterminated comment", src: `/* test("a", () => {})`},
		{name: "unterminated JSX element", src: `render(<p>text`},
		{name: "unterminated regular expression", src: "const r = /abc\nassert.ok(r);"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, err := nodeTests([]byte(tc.src)); err == nil {
				t.Errorf("nodeTests() = %v, nil error; want an error", got)
			}
		})
	}
}

func TestNodeTestsReadWebSuite(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{"../../web/src/*.test.ts", "../../web/src/*.test.tsx"} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("Glob(%s) error = %v", pattern, err)
		}
		if len(paths) == 0 {
			t.Fatalf("Glob(%s) matched no files", pattern)
		}
		for _, path := range paths {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			tests, err := nodeTests(src)
			if err != nil {
				t.Errorf("%s: nodeTests() error = %v", path, err)
			}
			if len(tests) == 0 {
				t.Errorf("%s: nodeTests() read no tests", path)
			}
		}
	}
}

func TestJudgeNodeTests(t *testing.T) {
	t.Parallel()
	const pair = `test("a", () => {
  assert.ok(1);
  assert.equal(2, 2);
});
test("b", () => {
  assert.ok(3);
});
`
	const onlyA = `test("a", () => {
  assert.ok(1);
  assert.equal(2, 2);
});
`
	const lowered = `test("a", () => {
  assert.ok(1);
});
test("b", () => {
  assert.ok(3);
});
`
	const skipped = `test.skip("a", () => {
  assert.ok(1);
  assert.equal(2, 2);
});
test("b", () => {
  assert.ok(3);
});
`
	tests := []struct {
		name string
		path string
		base string
		head string
		want []Refusal
	}{
		{
			name: "removed test in a guarded test file",
			path: "web/src/a.test.ts",
			base: pair,
			head: onlyA,
			want: []Refusal{
				{File: "web/src/a.test.ts", Test: "b", Rule: "test present at the base removed", Fact: true},
			},
		},
		{
			name: "lowered assertions in a guarded test file",
			path: "web/src/a.test.ts",
			base: pair,
			head: lowered,
			want: []Refusal{{File: "web/src/a.test.ts", Test: "a", Rule: "assertions lowered from 2 to 1", Fact: true}},
		},
		{
			name: "added skip in a guarded test file",
			path: "web/src/a.test.ts",
			base: pair,
			head: skipped,
			want: []Refusal{{File: "web/src/a.test.ts", Test: "a", Rule: "skip added", Fact: true}},
		},
		{
			name: "removed test in a guarded tsx test file",
			path: "web/src/a.test.tsx",
			base: pair,
			head: onlyA,
			want: []Refusal{
				{File: "web/src/a.test.tsx", Test: "b", Rule: "test present at the base removed", Fact: true},
			},
		},
		{
			name: "removed test in a guarded js test file",
			path: "web/src/a.test.js",
			base: pair,
			head: onlyA,
			want: []Refusal{
				{File: "web/src/a.test.js", Test: "b", Rule: "test present at the base removed", Fact: true},
			},
		},
		{
			name: "guarded source that is not a test file has no rules",
			path: "web/src/rows.ts",
			base: "export const a = 1;\n",
			head: "export const a = 2;\n",
			want: []Refusal{{File: "web/src/rows.ts", Rule: noRulesRule, Fact: true}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			set := Set{Guarded: []string{tc.path}}
			got := Judge(set, []Change{mod(tc.path, tc.base, tc.head)})
			if !slices.Equal(got, tc.want) {
				t.Errorf("Judge() = %v, want %v", got, tc.want)
			}
		})
	}
}
