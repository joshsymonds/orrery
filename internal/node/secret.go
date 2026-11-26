package node

import "fmt"

// ReadSecret returns the fleet secret that the file at path holds, with its surrounding whitespace trimmed. It refuses,
// naming path, a file that is missing or unreadable, a secret that is empty after the trim, and a file whose mode grants
// any permission bit to its group or to other, since such a file would leak the secret to other users.
func ReadSecret(path string) (string, error) {
	return "", fmt.Errorf("node: read secret %s: not implemented", path)
}

// SecretMatches reports whether got is the fleet secret want. It compares the two with crypto/subtle.ConstantTimeCompare,
// so the comparison does not reveal where they differ. It is false when want is empty, so an unset secret admits nothing.
func SecretMatches(want, got string) bool {
	return false
}
