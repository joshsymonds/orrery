package enginehost

import (
	"regexp"
	"testing"
)

// uuidV4Pattern matches a version-4 UUID in lowercase 8-4-4-4-12 form.
var uuidV4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewWriteIDReturnsDistinctV4UUIDs(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for range 3 {
		id, err := newWriteID()
		if err != nil {
			t.Fatalf("newWriteID: %v", err)
		}
		if !uuidV4Pattern.MatchString(id) {
			t.Fatalf("newWriteID() = %q, want a lowercase version-4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("newWriteID() = %q again, want a distinct id per call", id)
		}
		seen[id] = true
	}
}
