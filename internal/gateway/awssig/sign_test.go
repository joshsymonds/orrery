package awssig

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The credentials, instant and request of AWS's own published SigV4 example. Reproducing its signature is what proves
// the canonicalization is AWS's and not merely self-consistent. The key pair is AWS's documented example, not a real
// one.
const (
	exampleKeyID  = "AKIDEXAMPLE"
	exampleSecret = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	exampleStamp  = "2015-08-30T12:36:00Z"
)

// signingTime parses a fixed instant, so every signature in this file is reproducible.
func signingTime(t *testing.T) time.Time {
	t.Helper()

	when, err := time.Parse(time.RFC3339, exampleStamp)
	if err != nil {
		t.Fatalf("parse %q: %v", exampleStamp, err)
	}

	return when
}

// signatureOf pulls the Signature= value out of an Authorization header.
func signatureOf(t *testing.T, authorization string) string {
	t.Helper()

	_, sig, found := strings.Cut(authorization, "Signature=")
	if !found {
		t.Fatalf("Authorization %q carries no Signature=", authorization)
	}

	return sig
}

// TestGatewayRoutesSigV4SigningReproducesTheAWSExampleVector pins the canonicalization against the ListUsers example
// AWS publishes: same credentials, same instant, same request, same signature.
func TestGatewayRoutesSigV4SigningReproducesTheAWSExampleVector(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

	creds := Credentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret}

	if signErr := Sign(req, nil, creds, "iam", "us-east-1", signingTime(t)); signErr != nil {
		t.Fatalf("Sign() = %v; want no error", signErr)
	}

	authorization := req.Header.Get("Authorization")

	const wantCredential = "Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request"

	const wantSigned = "SignedHeaders=content-type;host;x-amz-date"

	const wantSignature = "5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"

	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") {
		t.Errorf("Authorization = %q; want the AWS4-HMAC-SHA256 scheme", authorization)
	}

	if !strings.Contains(authorization, wantCredential) {
		t.Errorf("Authorization = %q; want it to carry %q", authorization, wantCredential)
	}

	if !strings.Contains(authorization, wantSigned) {
		t.Errorf("Authorization = %q; want it to carry %q", authorization, wantSigned)
	}

	if got := signatureOf(t, authorization); got != wantSignature {
		t.Errorf("signature = %q; want %q", got, wantSignature)
	}

	if got := req.Header.Get("X-Amz-Date"); got != "20150830T123600Z" {
		t.Errorf("X-Amz-Date = %q; want %q", got, "20150830T123600Z")
	}
}

// TestGatewayRoutesSigV4SigningBedrockARNPathIsStable is the regression pin for the path the gateway actually signs: an
// application-inference-profile ARN, percent-escaped in the request line and escaped a second time in the canonical
// request. The expected signature was computed once from this implementation and is asserted stable; what makes it
// meaningful is that the same implementation reproduces AWS's own published vector above.
func TestGatewayRoutesSigV4SigningBedrockARNPathIsStable(t *testing.T) {
	t.Parallel()

	const arn = "arn:aws:bedrock:us-east-2:123456789012:application-inference-profile/abcdef123456"

	escaped := EscapePathSegment(arn)
	if strings.ContainsAny(escaped, ":/") {
		t.Fatalf("EscapePathSegment(%q) = %q; want every %q and %q percent-escaped", arn, escaped, ":", "/")
	}

	target := "https://bedrock-runtime.us-east-2.amazonaws.com/model/" + escaped + "/invoke"

	body := []byte(`{"anthropic_version":"bedrock-2023-05-31","max_tokens":16}`)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	creds := Credentials{
		AccessKeyID:     exampleKeyID,
		SecretAccessKey: exampleSecret,
		SessionToken:    "session-token-value",
	}

	if signErr := Sign(req, body, creds, "bedrock", "us-east-2", signingTime(t)); signErr != nil {
		t.Fatalf("Sign() = %v; want no error", signErr)
	}

	authorization := req.Header.Get("Authorization")

	const wantSigned = "SignedHeaders=accept;content-type;host;x-amz-date;x-amz-security-token"

	const wantSignature = "b3f0e2944892fd2fae3ad5a8fddd42e0bb7e64b55ed6163674551e3b69903ef9"

	if !strings.Contains(authorization, wantSigned) {
		t.Errorf("Authorization = %q; want it to carry %q", authorization, wantSigned)
	}

	if got := req.URL.EscapedPath(); !strings.Contains(got, "%3A") {
		t.Errorf("request path = %q; want the ARN colons escaped", got)
	}

	if got := signatureOf(t, authorization); got != wantSignature {
		t.Errorf("signature = %q; want the pinned %q", got, wantSignature)
	}
}

// A session token is set as its own header and is part of what the signature covers: the same request signed with and
// without one must differ, or a token could be swapped without invalidating the signature.
func TestGatewayRoutesSigV4SigningSessionTokenIsSetAndSigned(t *testing.T) {
	t.Parallel()

	signWith := func(token string) (*http.Request, string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			"https://bedrock-runtime.us-east-2.amazonaws.com/model/m/invoke", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}

		creds := Credentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret, SessionToken: token}
		if signErr := Sign(req, nil, creds, "bedrock", "us-east-2", signingTime(t)); signErr != nil {
			t.Fatalf("Sign() = %v; want no error", signErr)
		}

		return req, req.Header.Get("Authorization")
	}

	withToken, authWithToken := signWith("session-token-value")
	if got := withToken.Header.Get("X-Amz-Security-Token"); got != "session-token-value" {
		t.Errorf("X-Amz-Security-Token = %q; want the session token", got)
	}

	if !strings.Contains(authWithToken, "SignedHeaders=host;x-amz-date;x-amz-security-token") {
		t.Errorf("Authorization = %q; want the session token header in the signed set", authWithToken)
	}

	_, authWithoutToken := signWith("")
	if signatureOf(t, authWithToken) == signatureOf(t, authWithoutToken) {
		t.Errorf("signature is the same with and without a session token; want the token signed")
	}
}

// Static credentials carry no session token, so the request must not gain a token header nor sign one.
func TestGatewayRoutesSigV4SigningStaticKeysCarryNoSessionToken(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"https://bedrock-runtime.us-east-2.amazonaws.com/model/m/invoke", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	creds := Credentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret}
	if signErr := Sign(req, nil, creds, "bedrock", "us-east-2", signingTime(t)); signErr != nil {
		t.Fatalf("Sign() = %v; want no error", signErr)
	}

	if got, present := req.Header["X-Amz-Security-Token"]; present {
		t.Errorf("X-Amz-Security-Token = %q; want no token header for a static key pair", got)
	}

	if authorization := req.Header.Get("Authorization"); strings.Contains(authorization, "x-amz-security-token") {
		t.Errorf("Authorization = %q; want no session token in the signed set", authorization)
	}
}

// A signature over a different body must differ: the payload hash is part of the canonical request, so a tampered body
// cannot ride a stale signature.
func TestGatewayRoutesSigV4SigningCoversThePayload(t *testing.T) {
	t.Parallel()

	signBody := func(body string) string {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			"https://bedrock-runtime.us-east-2.amazonaws.com/model/m/invoke", strings.NewReader(body))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}

		req.Header.Set("Content-Type", "application/json")

		creds := Credentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret}
		if signErr := Sign(req, []byte(body), creds, "bedrock", "us-east-2", signingTime(t)); signErr != nil {
			t.Fatalf("Sign() = %v; want no error", signErr)
		}

		return signatureOf(t, req.Header.Get("Authorization"))
	}

	if first, second := signBody(`{"a":1}`), signBody(`{"a":2}`); first == second {
		t.Errorf("signatures over different bodies are both %q; want them to differ", first)
	}
}

// Sign refuses credentials it cannot sign with rather than sending an unsigned, or half-signed, request upstream.
func TestGatewayRoutesSigV4SigningRefusesIncompleteCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		creds Credentials
	}{
		{name: "no access key id", creds: Credentials{SecretAccessKey: exampleSecret}},
		{name: "no secret", creds: Credentials{AccessKeyID: exampleKeyID}},
		{name: "nothing at all", creds: Credentials{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				"https://bedrock-runtime.us-east-2.amazonaws.com/model/m/invoke", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}

			if signErr := Sign(req, nil, tc.creds, "bedrock", "us-east-2", signingTime(t)); signErr == nil {
				t.Fatal("Sign() = nil; want an error for incomplete credentials")
			}

			if got := req.Header.Get("Authorization"); got != "" {
				t.Errorf("Authorization = %q; want no header on a refused signature", got)
			}

			if got := req.Header.Get("X-Amz-Date"); got != "" {
				t.Errorf("X-Amz-Date = %q; want no header on a refused signature", got)
			}
		})
	}
}

// Sign refuses a request it has nothing to sign for: one with no URL, and one with no host at all.
func TestGatewayRoutesSigV4SigningRefusesARequestWithNoURLOrHost(t *testing.T) {
	t.Parallel()

	creds := Credentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret}

	noURL := &http.Request{Method: http.MethodPost, Header: http.Header{}}
	if signErr := Sign(noURL, nil, creds, "bedrock", "us-east-2", signingTime(t)); signErr == nil {
		t.Error("Sign() of a request with no URL = nil; want an error")
	}

	if got := noURL.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q; want no header on a request with no URL", got)
	}

	noHost, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/model/m/invoke", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if signErr := Sign(noHost, nil, creds, "bedrock", "us-east-2", signingTime(t)); signErr == nil {
		t.Error("Sign() of a request with no host = nil; want an error")
	}

	if got := noHost.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q; want no header on a request with no host", got)
	}
}

// A request with no header map is signed into a fresh one rather than panicking on the first header it sets.
func TestGatewayRoutesSigV4SigningSignsARequestWithNoHeaderMap(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"https://bedrock-runtime.us-east-2.amazonaws.com/model/m/invoke", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header = nil

	creds := Credentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret}
	if signErr := Sign(req, nil, creds, "bedrock", "us-east-2", signingTime(t)); signErr != nil {
		t.Fatalf("Sign() = %v; want no error", signErr)
	}

	if got := req.Header.Get("X-Amz-Date"); got != "20150830T123600Z" {
		t.Errorf("X-Amz-Date = %q; want %q", got, "20150830T123600Z")
	}

	if authorization := req.Header.Get("Authorization"); !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") {
		t.Errorf("Authorization = %q; want the AWS4-HMAC-SHA256 scheme", authorization)
	}
}

// EscapePathSegment implements RFC 3986 segment escaping: unreserved bytes survive, everything else is percent-escaped
// with uppercase hex.
func TestGatewayRoutesSigV4SigningEscapePathSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "unreserved survives", in: "aZ09-._~", want: "aZ09-._~"},
		{name: "colon escaped", in: "a:b", want: "a%3Ab"},
		{name: "slash escaped", in: "a/b", want: "a%2Fb"},
		{name: "space escaped", in: "a b", want: "a%20b"},
		{name: "percent escaped", in: "a%b", want: "a%25b"},
		{name: "plus escaped", in: "a+b", want: "a%2Bb"},
		{name: "utf8 escaped per byte", in: "é", want: "%C3%A9"},
		{name: "empty", in: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := EscapePathSegment(tc.in); got != tc.want {
				t.Errorf("EscapePathSegment(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The canonical URI is the request's already-escaped path escaped a second time: SigV4 for every service but S3
// double-encodes, and a Bedrock ARN, full of colons and a slash, is where that stops being academic.
func TestGatewayRoutesSigV4SigningCanonicalURIDoubleEscapes(t *testing.T) {
	t.Parallel()

	const arn = "arn:aws:bedrock:us-east-2:123456789012:application-inference-profile/abcdef123456"

	escapedPath := "/model/" + EscapePathSegment(arn) + "/invoke"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty path is root", in: "", want: "/"},
		{name: "root", in: "/", want: "/"},
		{name: "plain segments survive", in: "/model/foo/invoke", want: "/model/foo/invoke"},
		{
			name: "already-escaped bytes escape again",
			in:   escapedPath,
			want: "/model/arn%253Aaws%253Abedrock%253Aus-east-2%253A123456789012%253A" +
				"application-inference-profile%252Fabcdef123456/invoke",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := canonicalURI(tc.in); got != tc.want {
				t.Errorf("canonicalURI(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The canonical query string is sorted by key and RFC 3986 escaped, so two requests that differ only in parameter order
// sign identically.
func TestGatewayRoutesSigV4SigningCanonicalQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "sorted by key", in: "b=2&a=1", want: "a=1&b=2"},
		{name: "value escaped", in: "a=x y", want: "a=x%20y"},
		{name: "repeated key sorted by value", in: "a=2&a=1", want: "a=1&a=2"},
		{name: "empty value keeps its equals", in: "a=", want: "a="},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := canonicalQuery(tc.in); got != tc.want {
				t.Errorf("canonicalQuery(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
