package evidence

import (
	"encoding/json"
	"slices"
	"testing"
)

// maxLimit is the largest limit in seconds that Check accepts: math.MaxInt64 divided by one second in nanoseconds.
const maxLimit = 9223372036

// captureItem is a well-formed item of kind capture that runs a capture.
func captureItem() Item {
	return Item{
		Kind:       KindCapture,
		Capture:    []string{"scripts/gate", "--quick"},
		Limit:      60,
		Artifacts:  []string{"out/report.json"},
		Conclusion: "the gate holds",
		Judge:      "a reviewer reads the report",
	}
}

// suppliedItem is a well-formed item of kind supplied whose files a person supplies in place of a capture.
func suppliedItem() Item {
	return Item{
		Kind:       KindSupplied,
		Supplied:   []string{"docs/evidence.md"},
		Conclusion: "the document holds",
		Judge:      "a reviewer reads the document",
	}
}

// testItem is a well-formed item of kind test that runs a capture beside the test file it lists.
func testItem() Item {
	return edit(captureItem(), func(i *Item) {
		i.Kind = KindTest
		i.Tests = []string{"internal/evidence/item_test.go"}
	})
}

// edit returns a copy of item changed by change.
func edit(item Item, change func(*Item)) Item {
	change(&item)
	return item
}

func TestCheckRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		item Item
		want []string
	}{
		{
			name: "neither capture nor supplied",
			item: edit(captureItem(), func(i *Item) {
				i.Capture = nil
				i.Limit = 0
				i.Artifacts = nil
			}),
			want: []string{"capture: none, name a capture or supplied files", "kind: capture without a capture"},
		},
		{
			name: "kind none",
			item: edit(captureItem(), func(i *Item) { i.Kind = "" }),
			want: []string{"kind: none, name test, capture or supplied"},
		},
		{
			name: "kind not test, capture or supplied",
			item: edit(captureItem(), func(i *Item) { i.Kind = "check" }),
			want: []string{`kind: "check" is not test, capture or supplied`},
		},
		{
			name: "test kind without a capture",
			item: edit(suppliedItem(), func(i *Item) {
				i.Kind = KindTest
				i.Tests = []string{"internal/evidence/item_test.go"}
			}),
			want: []string{"kind: test without a capture"},
		},
		{
			name: "capture kind without a capture",
			item: edit(suppliedItem(), func(i *Item) { i.Kind = KindCapture }),
			want: []string{"kind: capture without a capture"},
		},
		{
			name: "supplied kind without supplied files",
			item: edit(captureItem(), func(i *Item) { i.Kind = KindSupplied }),
			want: []string{"kind: supplied without supplied files"},
		},
		{
			name: "test kind without test files",
			item: edit(captureItem(), func(i *Item) { i.Kind = KindTest }),
			want: []string{"tests: none, a test item lists its test files"},
		},
		{
			name: "tests set on a capture item",
			item: edit(captureItem(), func(i *Item) { i.Tests = []string{"internal/evidence/item_test.go"} }),
			want: []string{"tests: set on a capture item"},
		},
		{
			name: "tests set on a supplied item",
			item: edit(suppliedItem(), func(i *Item) { i.Tests = []string{"internal/evidence/item_test.go"} }),
			want: []string{"tests: set on a supplied item"},
		},
		{
			name: "test file not a normalized path",
			item: edit(testItem(), func(i *Item) { i.Tests = []string{"internal/a_test.go", "internal/../b_test.go"} }),
			want: []string{"tests[1]: not a normalized repository-relative file path"},
		},
		{
			name: "test file named twice",
			item: edit(testItem(), func(i *Item) { i.Tests = []string{"internal/a_test.go", "internal/a_test.go"} }),
			want: []string{"tests[1]: named twice within the list"},
		},
		{
			name: "both capture and supplied",
			item: edit(captureItem(), func(i *Item) { i.Supplied = []string{"docs/evidence.md"} }),
			want: []string{"supplied: set beside a capture"},
		},
		{
			name: "empty capture element after the first",
			item: edit(captureItem(), func(i *Item) { i.Capture = []string{"scripts/gate", ""} }),
			want: []string{"capture[1]: empty"},
		},
		{
			name: "empty first capture element",
			item: edit(captureItem(), func(i *Item) { i.Capture = []string{""} }),
			want: []string{"capture[0]: empty"},
		},
		{
			name: "capture program without a slash",
			item: edit(captureItem(), func(i *Item) { i.Capture = []string{"gate", "--quick"} }),
			want: []string{"capture[0]: not a path with a / in it"},
		},
		{
			name: "limit zero with a capture",
			item: edit(captureItem(), func(i *Item) { i.Limit = 0 }),
			want: []string{"limit: 0 is outside 1 to 9223372036 seconds"},
		},
		{
			name: "limit above the duration ceiling",
			item: edit(captureItem(), func(i *Item) { i.Limit = maxLimit + 1 }),
			want: []string{"limit: 9223372037 is outside 1 to 9223372036 seconds"},
		},
		{
			name: "limit set without a capture",
			item: edit(suppliedItem(), func(i *Item) { i.Limit = 5 }),
			want: []string{"limit: set without a capture"},
		},
		{
			name: "artifacts set without a capture",
			item: edit(suppliedItem(), func(i *Item) { i.Artifacts = []string{"out/report.json"} }),
			want: []string{"artifacts: set without a capture"},
		},
		{
			name: "artifact that is not a normalized path",
			item: edit(captureItem(), func(i *Item) { i.Artifacts = []string{"out/report.json", "/abs/report.json"} }),
			want: []string{"artifacts[1]: not a normalized repository-relative file path"},
		},
		{
			name: "artifact inside a .git directory in any case",
			item: edit(captureItem(), func(i *Item) { i.Artifacts = []string{".GIT/hooks/post-commit"} }),
			want: []string{"artifacts[0]: inside a .git directory"},
		},
		{
			name: "artifact named twice",
			item: edit(captureItem(), func(i *Item) { i.Artifacts = []string{"out/a.json", "out/a.json"} }),
			want: []string{"artifacts[1]: named twice within the list"},
		},
		{
			name: "supplied file that is not a normalized path",
			item: edit(suppliedItem(), func(i *Item) { i.Supplied = []string{"docs/../evidence.md"} }),
			want: []string{"supplied[0]: not a normalized repository-relative file path"},
		},
		{
			name: "supplied file inside a .git directory",
			item: edit(suppliedItem(), func(i *Item) { i.Supplied = []string{"docs/a.md", "repo/.git/config"} }),
			want: []string{"supplied[1]: inside a .git directory"},
		},
		{
			name: "supplied file named twice",
			item: edit(suppliedItem(), func(i *Item) { i.Supplied = []string{"docs/a.md", "docs/a.md"} }),
			want: []string{"supplied[1]: named twice within the list"},
		},
		{
			name: "empty conclusion",
			item: edit(captureItem(), func(i *Item) { i.Conclusion = "" }),
			want: []string{"conclusion: empty or blank"},
		},
		{
			name: "blank conclusion",
			item: edit(captureItem(), func(i *Item) { i.Conclusion = " \t " }),
			want: []string{"conclusion: empty or blank"},
		},
		{
			name: "empty judge",
			item: edit(captureItem(), func(i *Item) { i.Judge = "" }),
			want: []string{"judge: empty or blank"},
		},
		{
			name: "blank judge",
			item: edit(captureItem(), func(i *Item) { i.Judge = "\n" }),
			want: []string{"judge: empty or blank"},
		},
		{
			name: "two bad fields in field order",
			item: edit(captureItem(), func(i *Item) {
				i.Limit = 0
				i.Judge = ""
			}),
			want: []string{"limit: 0 is outside 1 to 9223372036 seconds", "judge: empty or blank"},
		},
		{
			name: "smallest accepted limit",
			item: edit(captureItem(), func(i *Item) { i.Limit = 1 }),
			want: nil,
		},
		{
			name: "largest accepted limit",
			item: edit(captureItem(), func(i *Item) { i.Limit = maxLimit }),
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Check(tc.item)
			if !slices.Equal(got, tc.want) {
				t.Errorf("Check() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckAcceptsWellFormedItems(t *testing.T) {
	t.Parallel()
	if got := Check(testItem()); got != nil {
		t.Errorf("Check(test item) = %q, want nil", got)
	}
	if got := Check(captureItem()); got != nil {
		t.Errorf("Check(capture item) = %q, want nil", got)
	}
	if got := Check(suppliedItem()); got != nil {
		t.Errorf("Check(supplied item) = %q, want nil", got)
	}
}

// TestItemStoredBeforeKindsReadsBackUnchanged asserts an item stored before kinds, which names neither capture nor
// supplied files, decodes kindless and encodes back to the same stored form: the kind and tests keys stay out of it.
func TestItemStoredBeforeKindsReadsBackUnchanged(t *testing.T) {
	t.Parallel()
	const stored = `{"capture":[],"limit":0,"artifacts":[],"supplied":[],"conclusion":"c","judge":"j"}`
	var item Item
	if err := json.Unmarshal([]byte(stored), &item); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if item.Kind != "" || item.Tests != nil {
		t.Fatalf("item = %+v, want no kind and no tests", item)
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != stored {
		t.Errorf("Marshal = %s, want the stored form %s", data, stored)
	}
}

func TestItemJSONRoundTrip(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"capture": true, "limit": true, "artifacts": true, "supplied": true, "conclusion": true, "judge": true,
		"kind": true,
	}
	for name, item := range map[string]Item{"capture": captureItem(), "supplied": suppliedItem()} {
		data, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", name, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatalf("%s: Unmarshal fields: %v", name, err)
		}
		if len(fields) != len(want) {
			t.Errorf("%s: encoded keys = %q, want exactly %d keys", name, fields, len(want))
		}
		for key := range want {
			if _, ok := fields[key]; !ok {
				t.Errorf("%s: encoded item has no %q key: %s", name, key, data)
			}
		}
		var back Item
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("%s: Unmarshal: %v", name, err)
		}
		if back.Kind != item.Kind || !slices.Equal(back.Capture, item.Capture) || back.Limit != item.Limit ||
			!slices.Equal(back.Artifacts, item.Artifacts) || !slices.Equal(back.Supplied, item.Supplied) ||
			back.Conclusion != item.Conclusion || back.Judge != item.Judge {
			t.Errorf("%s: round trip = %+v, want %+v", name, back, item)
		}
	}
}
