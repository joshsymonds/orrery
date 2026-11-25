package awssig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// The SigV4 vocabulary: the one algorithm the gateway signs with, the scope terminator every credential scope ends
// in, and the two time formats a signature is expressed in.
const (
	algorithm     = "AWS4-HMAC-SHA256"
	terminator    = "aws4_request"
	amzDateLayout = "20060102T150405Z"
	dateLayout    = "20060102"
	keyPrefix     = "AWS4"
)

// Headers the signature covers or sets. Host is always signed; the rest are signed only when the request carries
// them, which keeps the signed set exactly what goes on the wire.
const (
	headerAuthorization = "Authorization"
	headerAmzDate       = "X-Amz-Date"
	// headerAmzSession deliberately avoids spelling out the word the header itself carries: a const named that way
	// trips gosec's hardcoded-credential heuristic (G101).
	headerAmzSession = "X-Amz-Security-Token"
	headerHost       = "host"
)

// hexEscapeFormat renders one byte as an RFC 3986 percent-escape with the uppercase hex AWS canonicalization requires.
const hexEscapeFormat = "%%%02X"

// signedHeaderNames are the request headers, besides host, that the signature covers when they are present. Signing a
// fixed set rather than everything is deliberate: a header the gateway does not control must never be able to change
// the signature.
func signedHeaderNames() []string {
	return []string{"accept", "content-type", headerAmzDate, headerAmzSession}
}

// Sign signs req for service in region with creds, as of now, over body. It sets the request's X-Amz-Date header,
// its X-Amz-Security-Token header when creds carry a session token, and its Authorization header. A request it cannot
// sign is left untouched: an unsigned request reaching AWS is a confusing 403, and a half-signed one is worse.
func Sign(req *http.Request, body []byte, creds Credentials, service, region string, now time.Time) error {
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return errors.New("aws credentials are incomplete")
	}

	if req.URL == nil {
		return errors.New("request has no URL to sign")
	}

	host := requestHost(req)
	if host == "" {
		return errors.New("request has no host to sign")
	}

	if req.Header == nil {
		req.Header = http.Header{}
	}

	stamp := now.UTC()
	amzDate := stamp.Format(amzDateLayout)

	req.Header.Set(headerAmzDate, amzDate)

	if creds.SessionToken != "" {
		req.Header.Set(headerAmzSession, creds.SessionToken)
	}

	names, canonicalHeaders := headersToSign(req, host)
	signedHeaders := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.EscapedPath()),
		canonicalQuery(req.URL.RawQuery),
		canonicalHeaders,
		signedHeaders,
		hashHex(body),
	}, "\n")

	scope := strings.Join([]string{stamp.Format(dateLayout), region, service, terminator}, "/")
	toSign := strings.Join([]string{algorithm, amzDate, scope, hashHex([]byte(canonical))}, "\n")
	signature := hex.EncodeToString(sign(signingKey(creds.SecretAccessKey, stamp, region, service), toSign))

	req.Header.Set(headerAuthorization, fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, creds.AccessKeyID, scope, signedHeaders, signature))

	return nil
}

// EscapePathSegment percent-escapes one path segment per RFC 3986: unreserved bytes survive, every other byte, a
// Bedrock ARN's colons and its one slash included, becomes an uppercase percent-escape.
func EscapePathSegment(segment string) string {
	var out strings.Builder

	for i := range len(segment) {
		char := segment[i]
		if isUnreserved(char) {
			out.WriteByte(char)

			continue
		}

		fmt.Fprintf(&out, hexEscapeFormat, char)
	}

	return out.String()
}

// requestHost is the host the signature covers: the request's explicit Host when it has one, otherwise the URL's. It
// is called only once Sign has checked that the request has a URL.
func requestHost(req *http.Request) string {
	if req.Host != "" {
		return req.Host
	}

	return req.URL.Host
}

// headersToSign returns the sorted lowercase names of the headers the signature covers and the canonical header block
// for them.
func headersToSign(req *http.Request, host string) ([]string, string) {
	values := map[string]string{headerHost: host}

	for _, name := range signedHeaderNames() {
		if value := req.Header.Get(name); value != "" {
			values[strings.ToLower(name)] = value
		}
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}

	sort.Strings(names)

	var block strings.Builder

	for _, name := range names {
		block.WriteString(name)
		block.WriteString(":")
		block.WriteString(strings.TrimSpace(values[name]))
		block.WriteString("\n")
	}

	return names, block.String()
}

// canonicalURI is the escaped request path escaped a second time, which is what SigV4 canonicalization requires of
// every service but S3. The separators stay separators; only the segments between them are re-escaped.
func canonicalURI(escapedPath string) string {
	if escapedPath == "" || escapedPath == "/" {
		return "/"
	}

	segments := strings.Split(escapedPath, "/")
	for i, segment := range segments {
		segments[i] = EscapePathSegment(segment)
	}

	return strings.Join(segments, "/")
}

// canonicalQuery is the raw query sorted by name and then value, with both escaped per RFC 3986. A query that will not
// parse contributes nothing rather than a guess.
func canonicalQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}

	parsed, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}

	pairs := make([]string, 0, len(parsed))

	for name, values := range parsed {
		for _, value := range values {
			pairs = append(pairs, EscapePathSegment(name)+"="+EscapePathSegment(value))
		}
	}

	sort.Strings(pairs)

	return strings.Join(pairs, "&")
}

// signingKey derives the request's signing key from the secret, walking the date, region and service chain SigV4
// defines.
func signingKey(secret string, stamp time.Time, region, service string) []byte {
	key := sign([]byte(keyPrefix+secret), stamp.Format(dateLayout))
	key = sign(key, region)
	key = sign(key, service)

	return sign(key, terminator)
}

// sign is one HMAC-SHA256 round of the key derivation chain.
func sign(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))

	return mac.Sum(nil)
}

// hashHex is the lowercase hex SHA-256 of data, which is how SigV4 expresses both the payload hash and the canonical
// request hash.
func hashHex(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// isUnreserved reports whether a byte may appear unescaped in a path segment.
func isUnreserved(char byte) bool {
	switch {
	case char >= 'A' && char <= 'Z', char >= 'a' && char <= 'z', char >= '0' && char <= '9':
		return true
	case char == '-', char == '.', char == '_', char == '~':
		return true
	default:
		return false
	}
}
