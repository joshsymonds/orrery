package protocol

import (
	"crypto/sha256"
	"encoding/hex"
)

// QuestionHash returns the lowercase hex SHA-256 of question's raw bytes, exactly as stored.
// An unset question is the empty string, which hashes to the SHA-256 of no bytes.
func QuestionHash(question string) string {
	sum := sha256.Sum256([]byte(question))
	return hex.EncodeToString(sum[:])
}
