package store

import (
	"fmt"
	"strings"
)

// EventAttr is one attribute of a caller event's opening tag. Its name is written as given, since the daemon gives the
// names; its value is escaped (see CallerEventBlock).
type EventAttr struct {
	Name, Value string
}

// CallerEventBlock returns the block that carries an event of kind, with id and attrs, to its caller: an
// <orrery-event> opening tag whose attributes are kind, id, then each of attrs in the order given, the body on a line
// of its own, and the closing tag. The values of kind, id and attrs, and the whole body, are escaped, so the closing
// tag is the block's only one.
func CallerEventBlock(kind, id string, attrs []EventAttr, body string) string {
	return eventBlockTag(kind, id, attrs) + "\n" + escapeEvent(body) + "\n</orrery-event>"
}

// eventBlockTag returns the opening tag of a caller event block of kind and id with attrs, each value escaped.
func eventBlockTag(kind, id string, attrs []EventAttr) string {
	var tag strings.Builder
	fmt.Fprintf(&tag, `<orrery-event kind="%s" id="%s"`, escapeEvent(kind), escapeEvent(id))
	for _, attr := range attrs {
		fmt.Fprintf(&tag, ` %s="%s"`, attr.Name, escapeEvent(attr.Value))
	}
	tag.WriteString(">")
	return tag.String()
}

// escapeEvent returns s with each character that a caller event block may not carry raw escaped: & < > and ". The
// replacer is built here, not kept in a package variable.
func escapeEvent(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
