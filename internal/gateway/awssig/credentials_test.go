package awssig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway/awssig"
)

// sharedCredentials is a credentials file with two profiles. Every key and secret in it is invented.
const sharedCredentials = `[work]
aws_access_key_id = AKIDTESTWORK
aws_secret_access_key = invented-work-secret
aws_session_token = invented-work-token

[personal]
aws_access_key_id = AKIDTESTPERSONAL
aws_secret_access_key = invented-personal-secret
`

// writeCredentialsFile writes document as a file under a fresh directory and returns its path.
func writeCredentialsFile(t *testing.T, document string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write credentials file: %v", err)
	}

	return path
}

// environment returns a getenv over vars alone, so no test reads the real environment.
func environment(vars map[string]string) func(string) string {
	return func(name string) string {
		return vars[name]
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileReadsTheNamedSection reads each profile from the file the
// AWS_SHARED_CREDENTIALS_FILE variable names: the named profile's own key pair and session token come back, never a
// neighbouring profile's, and a profile with no session token has an empty one.
func TestGatewayRoutesSigV4SigningCredentialProfileReadsTheNamedSection(t *testing.T) {
	t.Parallel()

	getenv := environment(map[string]string{
		"AWS_SHARED_CREDENTIALS_FILE": writeCredentialsFile(t, sharedCredentials),
	})

	tests := []struct {
		profile     string
		wantKey     string
		wantSecret  string
		wantSession string
	}{
		{
			profile:     "work",
			wantKey:     "AKIDTESTWORK",
			wantSecret:  "invented-work-secret",
			wantSession: "invented-work-token",
		},
		{profile: "personal", wantKey: "AKIDTESTPERSONAL", wantSecret: "invented-personal-secret", wantSession: ""},
	}

	for _, tc := range tests {
		t.Run(tc.profile, func(t *testing.T) {
			t.Parallel()

			creds, err := awssig.LoadCredentials(tc.profile, getenv)
			if err != nil {
				t.Fatalf("LoadCredentials(%q) = %v; want the profile's credentials", tc.profile, err)
			}

			if creds.AccessKeyID != tc.wantKey {
				t.Errorf("AccessKeyID = %q; want %q", creds.AccessKeyID, tc.wantKey)
			}

			if creds.SecretAccessKey != tc.wantSecret {
				t.Errorf("SecretAccessKey = %q; want %q", creds.SecretAccessKey, tc.wantSecret)
			}

			if creds.SessionToken != tc.wantSession {
				t.Errorf("SessionToken = %q; want %q", creds.SessionToken, tc.wantSession)
			}
		})
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileDefaultsUnderHome checks that with AWS_SHARED_CREDENTIALS_FILE unset,
// the file is found at HOME's .aws/credentials.
func TestGatewayRoutesSigV4SigningCredentialProfileDefaultsUnderHome(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".aws"), 0o700); err != nil {
		t.Fatalf("make .aws: %v", err)
	}

	if err := os.WriteFile(filepath.Join(home, ".aws", "credentials"), []byte(sharedCredentials), 0o600); err != nil {
		t.Fatalf("write credentials file: %v", err)
	}

	creds, err := awssig.LoadCredentials("work", environment(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("LoadCredentials(\"work\") = %v; want the credentials under HOME", err)
	}

	if creds.AccessKeyID != "AKIDTESTWORK" {
		t.Errorf("AccessKeyID = %q; want %q", creds.AccessKeyID, "AKIDTESTWORK")
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileHonorsTheSharedFileVariable checks that AWS_SHARED_CREDENTIALS_FILE
// wins over HOME: a credentials file under HOME that names a different key must not be the one read.
func TestGatewayRoutesSigV4SigningCredentialProfileHonorsTheSharedFileVariable(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".aws"), 0o700); err != nil {
		t.Fatalf("make .aws: %v", err)
	}

	homeDocument := "[work]\naws_access_key_id = AKIDHOMEWORK\naws_secret_access_key = invented-home-secret\n"
	if err := os.WriteFile(filepath.Join(home, ".aws", "credentials"), []byte(homeDocument), 0o600); err != nil {
		t.Fatalf("write credentials file: %v", err)
	}

	getenv := environment(map[string]string{
		"HOME":                        home,
		"AWS_SHARED_CREDENTIALS_FILE": writeCredentialsFile(t, sharedCredentials),
	})

	creds, err := awssig.LoadCredentials("work", getenv)
	if err != nil {
		t.Fatalf("LoadCredentials(\"work\") = %v; want the credentials the variable names", err)
	}

	if creds.AccessKeyID != "AKIDTESTWORK" {
		t.Errorf("AccessKeyID = %q; want %q from the file the variable names", creds.AccessKeyID, "AKIDTESTWORK")
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileSkipsCommentsAndSpacing checks the reader's tolerance of the file as
// people write it: comments, blank lines, CRLF line endings, spacing around the equals sign, and a key that appears
// before any section, which belongs to no profile.
func TestGatewayRoutesSigV4SigningCredentialProfileSkipsCommentsAndSpacing(t *testing.T) {
	t.Parallel()

	document := "aws_access_key_id = AKIDORPHAN\r\n" +
		"# a comment about the work profile\r\n" +
		"\r\n" +
		"  [ work ]  \r\n" +
		"; another comment\r\n" +
		"aws_access_key_id   =   AKIDTESTSPACED  \r\n" +
		"aws_secret_access_key=invented-spaced-secret\r\n"

	getenv := environment(map[string]string{
		"AWS_SHARED_CREDENTIALS_FILE": writeCredentialsFile(t, document),
	})

	creds, err := awssig.LoadCredentials("work", getenv)
	if err != nil {
		t.Fatalf("LoadCredentials(\"work\") = %v; want the spaced profile's credentials", err)
	}

	if creds.AccessKeyID != "AKIDTESTSPACED" {
		t.Errorf("AccessKeyID = %q; want %q with its spacing trimmed", creds.AccessKeyID, "AKIDTESTSPACED")
	}

	if creds.SecretAccessKey != "invented-spaced-secret" {
		t.Errorf("SecretAccessKey = %q; want %q", creds.SecretAccessKey, "invented-spaced-secret")
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileRefusals checks that every way a profile can fail to yield a usable key
// pair is an error, and that the error carries none of the secret material the file holds.
func TestGatewayRoutesSigV4SigningCredentialProfileRefusals(t *testing.T) {
	t.Parallel()

	halfDocument := "[nosecret]\naws_access_key_id = AKIDTESTNOSECRET\n" +
		"[nokey]\naws_secret_access_key = invented-nokey-secret\n"
	shared := environment(map[string]string{"AWS_SHARED_CREDENTIALS_FILE": writeCredentialsFile(t, sharedCredentials)})
	half := environment(map[string]string{"AWS_SHARED_CREDENTIALS_FILE": writeCredentialsFile(t, halfDocument)})
	absent := environment(map[string]string{
		"AWS_SHARED_CREDENTIALS_FILE": filepath.Join(t.TempDir(), "absent"),
	})

	tests := []struct {
		name    string
		profile string
		getenv  func(string) string
	}{
		{name: "profile section absent", profile: "missing", getenv: shared},
		{name: "section with no secret", profile: "nosecret", getenv: half},
		{name: "section with no access key id", profile: "nokey", getenv: half},
		{name: "credentials file missing", profile: "work", getenv: absent},
		{name: "no location to read from", profile: "work", getenv: environment(map[string]string{})},
		{name: "empty profile name", profile: "", getenv: shared},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			creds, err := awssig.LoadCredentials(tc.profile, tc.getenv)
			if err == nil {
				t.Fatalf("LoadCredentials(%q) = %+v; want an error", tc.profile, creds)
			}

			if strings.Contains(err.Error(), "invented-") || strings.Contains(err.Error(), "AKID") {
				t.Errorf("error = %q; want it to carry no credential material from the file", err.Error())
			}
		})
	}
}
