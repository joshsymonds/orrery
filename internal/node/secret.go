package node

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ReadSecret returns the fleet secret that the file at path holds, with its surrounding whitespace trimmed. It refuses,
// naming path, a file that is missing or unreadable, a secret that is empty after the trim, and a file whose mode grants
// any permission bit to its group or to other, since such a file would leak the secret to other users.
func ReadSecret(path string) (string, error) {
	data, err := readOwnerOnly(path)
	if err != nil {
		return "", fmt.Errorf("node: read secret %s: %w", path, err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("node: read secret %s: the file holds no secret", path)
	}
	return secret, nil
}

// readOwnerOnly returns the contents of the file at path, and refuses a file whose mode grants any permission bit to
// its group or to other. It checks the mode of the file it opens, so the mode it checks is the one it reads.
func readOwnerOnly(path string) (data []byte, err error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf("mode %v grants group or other a permission bit", mode)
	}
	data, err = io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return data, nil
}

// SecretMatches reports whether got is the fleet secret want. It compares the two with crypto/subtle.ConstantTimeCompare,
// so the comparison does not reveal where they differ. It is false when want is empty, so an unset secret admits nothing.
func SecretMatches(want, got string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}
