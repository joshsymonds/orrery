package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

// storedForm is an item's stored JSON: Unmarshal then Marshal of text, which must be byte-identical to text.
func storedForm(t *testing.T, text string) (Item, string) {
	t.Helper()
	var item Item
	if err := json.Unmarshal([]byte(text), &item); err != nil {
		t.Fatalf("Unmarshal %s: %v", text, err)
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return item, string(data)
}

func TestItemStoredWithoutKindRoundTrips(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"capture": `{"capture":["scripts/gate","--quick"],"limit":60,"artifacts":["out/report.json"],` +
			`"supplied":null,"conclusion":"the gate holds","judge":"a reviewer reads the report"}`,
		"supplied": `{"capture":null,"limit":0,"artifacts":null,"supplied":["docs/evidence.md"],` +
			`"conclusion":"the document holds","judge":"a reviewer reads the document"}`,
	} {
		item, back := storedForm(t, text)
		if back != text {
			t.Errorf("%s: written back as %s, want %s", name, back, text)
		}
		if item.Kind != "" || item.Tests != nil {
			t.Errorf("%s: read kind %q and tests %q, want kindless and no tests", name, item.Kind, item.Tests)
		}
	}
}

func TestItemKindAndTestsRoundTrip(t *testing.T) {
	t.Parallel()
	for kind, want := range map[Kind]string{
		KindTest:     "test",
		KindCapture:  "capture",
		KindSupplied: "supplied",
		KindClosure:  "closure",
	} {
		text := `{"capture":["scripts/gate"],"limit":60,"artifacts":null,"supplied":null,` +
			`"conclusion":"the gate holds","judge":"a reviewer","kind":"` + want + `","tests":["internal/a/a_test.go"]}`
		item, back := storedForm(t, text)
		if back != text {
			t.Errorf("%s: written back as %s, want %s", want, back, text)
		}
		if item.Kind != kind || strings.Join(item.Tests, ",") != "internal/a/a_test.go" {
			t.Errorf("%s: read kind %q and tests %q", want, item.Kind, item.Tests)
		}
	}
}

func TestCurrent(t *testing.T) {
	t.Parallel()
	testItem := edit(captureItem(), func(i *Item) {
		i.Kind = KindTest
		i.Tests = []string{"internal/a/a_test.go", "internal/b/b_test.go"}
	})
	cases := []struct {
		name string
		item Item
		want bool
	}{
		{name: "the same item", item: testItem, want: true},
		{name: "a copy with its own slices", item: edit(testItem, func(i *Item) {
			i.Capture = append([]string(nil), testItem.Capture...)
			i.Tests = append([]string(nil), testItem.Tests...)
		}), want: true},
		{name: "another conclusion", item: edit(testItem, func(i *Item) {
			i.Conclusion = "the gate fails"
		}), want: false},
		{name: "another limit", item: edit(testItem, func(i *Item) { i.Limit = 61 }), want: false},
		{name: "another capture argument", item: edit(testItem, func(i *Item) {
			i.Capture = []string{"scripts/gate", "--full"}
		}), want: false},
		{name: "kindless", item: edit(testItem, func(i *Item) { i.Kind = "" }), want: false},
		{name: "another kind", item: edit(testItem, func(i *Item) { i.Kind = KindCapture }), want: false},
		{name: "a test file fewer", item: edit(testItem, func(i *Item) { i.Tests = i.Tests[:1] }), want: false},
		{name: "test files reordered", item: edit(testItem, func(i *Item) {
			i.Tests = []string{"internal/b/b_test.go", "internal/a/a_test.go"}
		}), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Current(testItem, tc.item); got != tc.want {
				t.Errorf("Current = %t, want %t", got, tc.want)
			}
		})
	}
	noTests := edit(captureItem(), func(i *Item) { i.Tests = nil })
	if !Current(noTests, edit(noTests, func(i *Item) { i.Tests = []string{} })) {
		t.Error("Current(no tests, empty tests) = false, want true: both store without a tests key")
	}
	if Current(noTests, edit(noTests, func(i *Item) { i.Supplied = []string{} })) {
		t.Error("Current(null supplied, empty supplied) = true, want false: they store as null and []")
	}
}
