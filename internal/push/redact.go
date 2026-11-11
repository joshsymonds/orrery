package push

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// RedactEndpoints returns the message of err with the endpoint URL of each failed push request replaced. The
// request errors quote the URL they posted to.
func RedactEndpoints(err error) string {
	msg := err.Error()
	for _, urlErr := range requestErrors(err) {
		msg = strings.ReplaceAll(msg, strconv.Quote(urlErr.URL), `"<endpoint>"`)
	}
	return msg
}

// requestErrors returns the *url.Error values in the tree of err. A URL error under several wrapping errors is
// found once per wrapper; redaction treats the repeats as no-ops.
func requestErrors(err error) []*url.Error {
	var found []*url.Error
	var walk func(node error)
	walk = func(node error) {
		var urlErr *url.Error
		if errors.As(node, &urlErr) {
			found = append(found, urlErr)
		}
		if multi, ok := node.(interface{ Unwrap() []error }); ok {
			for _, child := range multi.Unwrap() {
				walk(child)
			}
			return
		}
		if child := errors.Unwrap(node); child != nil {
			walk(child)
		}
	}
	walk(err)
	return found
}
