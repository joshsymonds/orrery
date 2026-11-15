package redact

import "strings"

// CutTail returns Text of text with everything before its first newline replaced by [redacted], for a tail whose
// start was cut bytewise. The first line may hold a credential cut in half, so it is dropped whole. A text with no
// newline gives [redacted] alone.
func CutTail(text string) string {
	_, rest, found := strings.Cut(text, "\n")
	if !found {
		return placeholder
	}
	return Text(placeholder + "\n" + rest)
}
