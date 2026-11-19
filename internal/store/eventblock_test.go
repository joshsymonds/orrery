package store_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// hostileEventText holds each character a caller event block escapes, and the closing tag itself.
const hostileEventText = `&<>"</orrery-event>`

// hostileEventTextEscaped is hostileEventText as a block carries it.
const hostileEventTextEscaped = `&amp;&lt;&gt;&quot;&lt;/orrery-event&gt;`

// TestStepEventBriefEscapeBlock requires a block whose kind, id, attribute value and body hold & < > " and the closing
// tag to carry each escaped, keep its attributes in the order given, and end with the one closing tag. The exact match
// puts the only raw closing tag at the block's end.
func TestStepEventBriefEscapeBlock(t *testing.T) {
	t.Parallel()
	got := store.CallerEventBlock(
		hostileEventText,
		"step:"+hostileEventText,
		[]store.EventAttr{{Name: "agent", Value: hostileEventText}, {Name: "status", Value: "passed"}},
		"a "+hostileEventText+" b",
	)
	want := `<orrery-event kind="` + hostileEventTextEscaped + `" id="step:` + hostileEventTextEscaped +
		`" agent="` + hostileEventTextEscaped + `" status="passed">` + "\n" +
		"a " + hostileEventTextEscaped + " b\n" +
		"</orrery-event>"
	if got != want {
		t.Fatalf("CallerEventBlock = %q, want %q", got, want)
	}
}
