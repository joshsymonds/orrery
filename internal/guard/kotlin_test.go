package guard

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestKotlinTests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []Test
	}{
		{
			name: "plain and backtick names come from their identifiers",
			src: `@Test fun plain() { assertTrue(true) }
@Test fun ` + backtick + `backtick name` + backtick + `() { assertTrue(true) }`,
			want: []Test{{Name: "plain", Asserts: 1}, {Name: "backtick name", Asserts: 1}},
		},
		{
			name: "a backtick name with a template keeps its source text",
			src:  `@Test fun ` + backtick + `case ${n}` + backtick + `() { assertTrue(true) }`,
			want: []Test{{Name: "case ${n}", Asserts: 1}},
		},
		{
			name: "functions without a test annotation declare nothing and their assertions count nowhere",
			src: `fun check(value: Boolean) { assertTrue(value) }
@Ignore fun helper() { assertTrue(true) }
@Test fun a() { check(true) }`,
			want: []Test{{Name: "a"}},
		},
		{
			name: "annotations directly before the fun make it a test",
			src: `@Test internal fun a() { assertTrue(true) }
@org.junit.Test
override fun b() { assertTrue(true) }
@Test @Ignore
fun c() { assertTrue(true) }
@Test
val d = 1
fun e() { assertTrue(true) }
@get:Rule val rule = x
@Test(expected = IllegalStateException::class) fun f() { assertTrue(true) }`,
			want: []Test{
				{Name: "a", Asserts: 1},
				{Name: "b", Asserts: 1},
				{Name: "c", Asserts: 1, Skips: 1},
				{Name: "f", Asserts: 1},
			},
		},
		{
			name: "assertion calls are identifiers starting with assert and followed by a paren",
			src: `@Test fun a() {
  assertEquals(1, 1)
  assertNotNull(x)
  assertThat(x).isEqualTo(1)
  assert(true)
  onNode(x).assertIsDisplayed()
  myassertTrue(1)
  val asserted = 1
}`,
			want: []Test{{Name: "a", Asserts: 5}},
		},
		{
			name: "comments, strings, characters and raw strings are not code",
			src: `@Test fun a() {
  // assertTrue(false)
  /* assertTrue(false) /* nested } */ assertTrue(false) */
  val text = "assertTrue(false) }"
  val raw = """assertTrue(false) } " quote"""
  val brace = '{'
  val quote = '"'
  assertTrue(true)
}`,
			want: []Test{{Name: "a", Asserts: 1}},
		},
		{
			name: "a substitution holds code whose braces and quotes do not end the string",
			src: `@Test fun a() {
  val text = "x ${ listOf("}").map { it + "{" }.joinToString() } y"
  assertEquals(text, text)
}`,
			want: []Test{{Name: "a", Asserts: 1}},
		},
		{
			name: "a raw string substitution is code and a run of quotes closes with its last three",
			src: `@Test fun a() {
  val raw = """${ "}" } a""""
  assertTrue(true)
}`,
			want: []Test{{Name: "a", Asserts: 1}},
		},
		{
			name: "an expression-bodied test takes no brace of a later property",
			src: `@Test fun a() = check()
val b = listOf(1).map { assertTrue(true) }
@Test fun c() { assertTrue(true) }`,
			want: []Test{{Name: "a"}, {Name: "c", Asserts: 1}},
		},
		{
			name: "an expression-bodied test counts the assertions of its expression",
			src: `@Test fun a() = assertHint(state = "done", expected = HINT)
@Test fun b(): Unit = runTest { assertEquals(1, 1) }
private fun assertHint(state: String, expected: String) { assertEquals(state, expected) }`,
			want: []Test{{Name: "a", Asserts: 1}, {Name: "b", Asserts: 1}},
		},
		{
			name: "method and class ignores count as skips",
			src: `@Test @Ignore fun a() { assertTrue(true) }
@Disabled @Test fun b() { assertTrue(true) }
@org.junit.Ignore
class C {
  @Test fun c() { assertTrue(true) }
  @Test @Ignore fun d() { assertTrue(true) }
  @Ignore class Inner {
    @Test fun e() { assertTrue(true) }
  }
}
class F {
  @Test fun g() {}
}
@Ignore class Outer {
  class Plain {
    @Test fun h() { assertTrue(true) }
  }
}`,
			want: []Test{
				{Name: "a", Asserts: 1, Skips: 1},
				{Name: "b", Asserts: 1, Skips: 1},
				{Name: "c", Asserts: 1, Skips: 1},
				{Name: "d", Asserts: 1, Skips: 2},
				{Name: "e", Asserts: 1, Skips: 1},
				{Name: "g"},
				{Name: "h", Asserts: 1},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := kotlinTests([]byte(tc.src))
			if err != nil {
				t.Fatalf("kotlinTests() error = %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("kotlinTests() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestKotlinTestsUnreadable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		src  string
	}{
		{name: "unclosed brace", src: `@Test fun a() { assertTrue(true)`},
		{name: "extra close brace", src: `@Test fun a() { assertTrue(true) } }`},
		{name: "unterminated string", src: "@Test fun a() { val s = \"abc }\n}"},
		{name: "unterminated string after a substitution", src: `@Test fun a() { val s = "${ x }`},
		{name: "unterminated raw string", src: `@Test fun a() { val s = """abc }`},
		{name: "unterminated comment", src: `/* @Test fun a() {} `},
		{name: "unterminated character", src: "@Test fun a() { val c = 'x\n}"},
		{name: "unterminated backtick name", src: "@Test fun `a() {}\n"},
		{name: "unclosed parameter list", src: `@Test fun a(x: Int { assertTrue(true) }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, err := kotlinTests([]byte(tc.src)); err == nil {
				t.Errorf("kotlinTests() = %v, nil error; want an error", got)
			}
		})
	}
}

func TestKotlinTestsReadAndroidSuite(t *testing.T) {
	t.Parallel()
	const root = "../../android/app/src/test"
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".kt") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tests, err := kotlinTests(src)
		if err != nil {
			t.Errorf("%s: kotlinTests() error = %v", path, err)
		}
		if strings.HasSuffix(path, "Test.kt") && len(tests) == 0 {
			t.Errorf("%s: kotlinTests() read no tests", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func TestJudgeKotlinTests(t *testing.T) {
	t.Parallel()
	const path = "android/app/src/test/kotlin/io/github/joshsymonds/orrery/FooTest.kt"
	const pair = `@Test fun a() {
  assertEquals(1, 1)
  assertEquals(2, 2)
}
@Test fun b() {
  assertTrue(true)
}
`
	const onlyA = `@Test fun a() {
  assertEquals(1, 1)
  assertEquals(2, 2)
}
`
	const lowered = `@Test fun a() {
  assertEquals(1, 1)
}
@Test fun b() {
  assertTrue(true)
}
`
	const ignored = `@Test fun a() {
  assertEquals(1, 1)
  assertEquals(2, 2)
}
@Ignore @Test fun b() {
  assertTrue(true)
}
`
	tests := []struct {
		name string
		base string
		head string
		want []Refusal
	}{
		{
			name: "removed test in a guarded kotlin test file",
			base: pair,
			head: onlyA,
			want: []Refusal{{File: path, Test: "b", Rule: "test present at the base removed"}},
		},
		{
			name: "lowered assertions in a guarded kotlin test file",
			base: pair,
			head: lowered,
			want: []Refusal{{File: path, Test: "a", Rule: "assertions lowered from 2 to 1"}},
		},
		{
			name: "added ignore in a guarded kotlin test file",
			base: pair,
			head: ignored,
			want: []Refusal{{File: path, Test: "b", Rule: "skip added"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			set := Set{Guarded: []string{path}}
			got := Judge(set, []Change{mod(path, tc.base, tc.head)})
			if !slices.Equal(got, tc.want) {
				t.Errorf("Judge() = %v, want %v", got, tc.want)
			}
		})
	}
}
