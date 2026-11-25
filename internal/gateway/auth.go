package gateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// patchbayKeyHeader is the gateway's own authentication header: the shared secret a caller must present before an
// inject or sigv4 seat will spend the household key. It is the gateway's and never the upstream's, so it is removed
// from every upstream request.
const patchbayKeyHeader = "X-Patchbay-Key"

// errNoCallerKey is what a request to an inject or sigv4 seat is refused with when New loaded no caller key.
var errNoCallerKey = errors.New("gateway: no caller key is loaded, so inject and sigv4 seats are refused")

// errCallerKeyRefused is what a request to an inject or sigv4 seat is refused with when it does not present the
// caller key.
var errCallerKeyRefused = errors.New("gateway: caller key missing or mismatched")

// applyAuth sets the credential a seat pays with on an upstream request's headers. The caller's own Authorization and
// x-api-key are deleted first, so the caller's credential never reaches an upstream beside the seat's.
func applyAuth(header http.Header, bearer string) {
	header.Del(headerAuthorization)
	header.Del(headerClientKey)
	header.Set(headerAuthorization, "Bearer "+bearer)
}

// checkCallerKey refuses a request to an inject or sigv4 seat that does not present the caller key, so a local process
// that lacks it cannot spend the household key the seat injects. It reads the inbound request, before any copy of it is
// made for upstream, and fails closed: with no caller key loaded every such request is refused, so an empty header can
// never match an empty key.
func (g *Gateway) checkCallerKey(r *http.Request) error {
	if g.callerKey == "" {
		return errNoCallerKey
	}
	if !CallerKeyMatches(r.Header.Get(patchbayKeyHeader), g.callerKey) {
		return errCallerKeyRefused
	}

	return nil
}

// CallerKey reads the caller key the file at path holds. An empty path, an unreadable file and an empty file all fail,
// and the gateway then holds no caller key.
func CallerKey(path string) (string, error) {
	if path == "" {
		return "", errors.New("gateway: Config.CallerKeyFile is not set")
	}

	return readKeyFile(path)
}

// CallerKeyMatches reports whether a presented caller key is the expected one.
//
// Both sides are hashed first: SHA-256 gives two equal-length digests, so the constant-time compare never leaks the key
// length through an early return, the way comparing the raw values would.
func CallerKeyMatches(presented, expected string) bool {
	presentedSum := sha256.Sum256([]byte(strings.TrimSpace(presented)))
	expectedSum := sha256.Sum256([]byte(expected))

	return subtle.ConstantTimeCompare(presentedSum[:], expectedSum[:]) == 1
}

// readAPIKey resolves the double indirection an inject seat uses: envName is the name of an environment variable whose
// value is the path of a file holding the key. The file is read per request, so rotating the key on disk takes effect
// at once, and nothing keeps the key in memory afterwards.
func readAPIKey(getenv func(string) string, envName string) (string, error) {
	path := getenv(envName)
	if path == "" {
		return "", fmt.Errorf("environment variable %s is not set", envName)
	}

	key, err := readKeyFile(path)
	if err != nil {
		return "", fmt.Errorf("key named by %s: %w", envName, err)
	}

	return key, nil
}

// readKeyFile returns the key a file holds, without the whitespace around it. An unreadable file and an empty one are
// both errors.
func readKeyFile(path string) (string, error) {
	contents, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("read key file: %w", err)
	}

	key := strings.TrimSpace(string(contents))
	if key == "" {
		return "", errors.New("key file is empty")
	}

	return key, nil
}
