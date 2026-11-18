package evidence

import (
	"slices"
	"testing"
)

// closureLine is the refusal Check gives a closure item, which only a task's evidence may be.
const closureLine = "kind: closure is for a task in a correction round only; " +
	"a Requirement's or an amendment's item is test, capture or supplied"

// closureItem is a well-formed item of kind closure: a correction-round task's evidence with a conclusion and nothing
// else set.
func closureItem() Item {
	return Item{Kind: KindClosure, Conclusion: "the round's closure check holds"}
}

func TestClosureEvidenceKind(t *testing.T) {
	t.Parallel()
	t.Run("an evidence item of kind closure with no capture is valid for a task", func(t *testing.T) {
		t.Parallel()
		if got := CheckTask(closureItem()); got != nil {
			t.Errorf("CheckTask(closure item) = %q, want nil", got)
		}
	})
	t.Run("a Requirement's item of kind closure is refused", func(t *testing.T) {
		t.Parallel()
		item := edit(captureItem(), func(i *Item) { i.Kind = KindClosure })
		if got, want := Check(item), []string{closureLine}; !slices.Equal(got, want) {
			t.Errorf("Check(closure item) = %q, want %q", got, want)
		}
	})
	t.Run("a closure item that sets a capture, limit, artifacts, supplied files, tests or a judge "+
		"is refused for a task", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name string
			item Item
			want string
		}{
			{
				name: "capture",
				item: edit(closureItem(), func(i *Item) { i.Capture = []string{"scripts/gate"} }),
				want: "capture: set on a closure item",
			},
			{
				name: "limit",
				item: edit(closureItem(), func(i *Item) { i.Limit = 60 }),
				want: "limit: set on a closure item",
			},
			{
				name: "artifacts",
				item: edit(closureItem(), func(i *Item) { i.Artifacts = []string{"out/report.json"} }),
				want: "artifacts: set on a closure item",
			},
			{
				name: "supplied files",
				item: edit(closureItem(), func(i *Item) { i.Supplied = []string{"docs/evidence.md"} }),
				want: "supplied: set on a closure item",
			},
			{
				name: "tests",
				item: edit(closureItem(), func(i *Item) { i.Tests = []string{"internal/evidence/item_test.go"} }),
				want: "tests: set on a closure item",
			},
			{
				name: "judge",
				item: edit(closureItem(), func(i *Item) { i.Judge = "a reviewer reads the round" }),
				want: "judge: set on a closure item; the round's closure check settles it",
			},
		}
		for _, c := range cases {
			if got, want := CheckTask(c.item), []string{c.want}; !slices.Equal(got, want) {
				t.Errorf("%s: CheckTask(closure item) = %q, want %q", c.name, got, want)
			}
		}
	})
	t.Run("a closure item without a conclusion is refused for a task", func(t *testing.T) {
		t.Parallel()
		item := edit(closureItem(), func(i *Item) { i.Conclusion = "  " })
		if got, want := CheckTask(item), []string{"conclusion: empty or blank"}; !slices.Equal(got, want) {
			t.Errorf("CheckTask(closure item without conclusion) = %q, want %q", got, want)
		}
	})
	t.Run("CheckTask matches Check for the other kinds", func(t *testing.T) {
		t.Parallel()
		items := []Item{
			testItem(),
			captureItem(),
			suppliedItem(),
			edit(captureItem(), func(i *Item) { i.Kind = "" }),
			edit(captureItem(), func(i *Item) { i.Kind = Kind("bogus") }),
			edit(captureItem(), func(i *Item) { i.Judge = "" }),
		}
		for _, item := range items {
			if got, want := CheckTask(item), Check(item); !slices.Equal(got, want) {
				t.Errorf("CheckTask(%+v) = %q, want Check's %q", item, got, want)
			}
		}
	})
	t.Run("test files are Go, web and android/app/src/test Kotlin tests", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			file string
			want bool
		}{
			{"internal/x/x_test.go", true},
			{"web/src/x.test.ts", true},
			{"web/src/x.test.tsx", true},
			{"android/app/src/test/x/A.kt", true},
			{"android/app/src/main/x/A.kt", false},
			{"internal/x/x.go", false},
			{"web/src/x.ts", false},
		}
		for _, c := range cases {
			if got := TestFile(c.file); got != c.want {
				t.Errorf("TestFile(%q) = %t, want %t", c.file, got, c.want)
			}
		}
	})
}
