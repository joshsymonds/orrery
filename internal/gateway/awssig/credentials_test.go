package awssig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/gateway/awssig"
)

// exportedDocument is the credential document a fake aws CLI prints for the work profile. Every key and secret in it is
// invented.
const exportedDocument = `{"Version":1,"AccessKeyId":"AKIDTESTWORK","SecretAccessKey":"invented-work-secret",` +
	`"SessionToken":"invented-work-token","Expiration":"2099-01-01T00:00:00Z"}`

// printDocument is a fake aws body that prints document as its output.
func printDocument(document string) string {
	return "printf '%s\\n' '" + document + "'\n"
}

// environment returns a getenv over vars alone, so no test reads the real environment.
func environment(vars map[string]string) func(string) string {
	return func(name string) string {
		return vars[name]
	}
}

// fakeAWS installs a script named aws in a fresh directory running body, and returns that directory and the path of
// the log every invocation appends its profile and arguments to. The directory is made the process's PATH, which is how
// the aws CLI is looked up, and the getenv a caller passes names the same directory.
func fakeAWS(t *testing.T, body string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")

	// The profile arrives in the environment, not the command line, so the log records AWS_PROFILE beside the
	// arguments.
	script := "#!/bin/sh\necho \"$AWS_PROFILE $*\" >> '" + logPath + "'\n" + body

	exectest.WriteFile(t, filepath.Join(dir, "aws"), []byte(script), 0o700)

	t.Setenv("PATH", dir)

	return dir, logPath
}

// callLog returns the recorded invocations, one line per exec.
func callLog(t *testing.T, logPath string) []string {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		t.Fatalf("read call log: %v", err)
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "\n")
}

// TestGatewayRoutesSigV4SigningCredentialProfileReadsTheNamedProfile checks that the credentials come from running the
// aws CLI's export with the named profile selected through AWS_PROFILE, once per call.
func TestGatewayRoutesSigV4SigningCredentialProfileReadsTheNamedProfile(t *testing.T) {
	dir, logPath := fakeAWS(t, printDocument(exportedDocument))

	creds, err := awssig.LoadCredentials("work", environment(map[string]string{"PATH": dir}))
	if err != nil {
		t.Fatalf("LoadCredentials(\"work\") = %v; want the exported credentials", err)
	}

	if creds.AccessKeyID != "AKIDTESTWORK" {
		t.Errorf("AccessKeyID = %q; want %q", creds.AccessKeyID, "AKIDTESTWORK")
	}

	if creds.SecretAccessKey != "invented-work-secret" {
		t.Errorf("SecretAccessKey = %q; want %q", creds.SecretAccessKey, "invented-work-secret")
	}

	calls := callLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("aws ran %d times; want 1", len(calls))
	}

	if want := "work configure export-credentials"; calls[0] != want {
		t.Errorf("aws ran %q; want %q", calls[0], want)
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileCarriesTheSessionToken checks that temporary credentials keep the
// session token the CLI exports, and that a static key pair, which exports none, yields an empty token.
func TestGatewayRoutesSigV4SigningCredentialProfileCarriesTheSessionToken(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		wantToken string
	}{
		{
			name:      "temporary credentials keep their session token",
			document:  exportedDocument,
			wantToken: "invented-work-token",
		},
		{
			name:      "static key pair has no session token",
			document:  `{"AccessKeyId":"AKIDTESTSTATIC","SecretAccessKey":"invented-static-secret"}`,
			wantToken: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := fakeAWS(t, printDocument(tc.document))

			creds, err := awssig.LoadCredentials("work", environment(map[string]string{"PATH": dir}))
			if err != nil {
				t.Fatalf("LoadCredentials(\"work\") = %v; want the exported credentials", err)
			}

			if creds.SessionToken != tc.wantToken {
				t.Errorf("SessionToken = %q; want %q", creds.SessionToken, tc.wantToken)
			}
		})
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileGetsOnlyItsEnvironment checks that the CLI runs with the profile and
// the variables it needs, looked up through getenv, and nothing else: the gateway's own secret never reaches it, and
// the profile arrives through AWS_PROFILE.
func TestGatewayRoutesSigV4SigningCredentialProfileGetsOnlyItsEnvironment(t *testing.T) {
	// The fake echoes the environment it was started with back as the secret, so the test reads what the child saw.
	body := `printf '{"AccessKeyId":"AKIDTESTENV","SecretAccessKey":"%s|%s|%s"}\n' ` +
		`"$AWS_PROFILE" "$HOME" "${INVENTED_GATEWAY_SECRET-unset}"` + "\n"
	dir, _ := fakeAWS(t, body)

	getenv := environment(map[string]string{
		"PATH":                    dir,
		"HOME":                    "/invented/home",
		"INVENTED_GATEWAY_SECRET": "invented-gateway-secret",
	})

	creds, err := awssig.LoadCredentials("work", getenv)
	if err != nil {
		t.Fatalf("LoadCredentials(\"work\") = %v; want the exported credentials", err)
	}

	if want := "work|/invented/home|unset"; creds.SecretAccessKey != want {
		t.Errorf("child environment = %q; want %q: the profile, HOME, and no gateway secret",
			creds.SecretAccessKey, want)
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileRefusals checks that every way the CLI can fail to yield a usable key
// pair is an error that names the profile and carries none of the output the CLI printed.
func TestGatewayRoutesSigV4SigningCredentialProfileRefusals(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "non-zero exit with output naming a secret", body: "printf 'invented-stderr-secret\\n' >&2\nexit 1\n"},
		{name: "unparsable output", body: printDocument("not-json")},
		{name: "document naming no key pair", body: printDocument(`{"AccessKeyId":""}`)},
		{name: "document with no secret", body: printDocument(`{"AccessKeyId":"AKIDTESTHALF"}`)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := fakeAWS(t, tc.body)

			creds, err := awssig.LoadCredentials("work", environment(map[string]string{"PATH": dir}))
			if err == nil {
				t.Fatalf("LoadCredentials(\"work\") = %+v; want an error", creds)
			}

			for _, leaked := range []string{"invented-", "AKID", "not-json"} {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("error = %q; want it to carry no output from the aws CLI", err.Error())
				}
			}

			if !strings.Contains(err.Error(), "work") {
				t.Errorf("error = %q; want it to name the profile", err.Error())
			}
		})
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileWithoutTheAWSBinary checks that a profile cannot be read when no aws
// CLI is on PATH: the refusal is an error, not an empty key pair.
func TestGatewayRoutesSigV4SigningCredentialProfileWithoutTheAWSBinary(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	if _, err := awssig.LoadCredentials("work", environment(map[string]string{"PATH": empty})); err == nil {
		t.Fatal("LoadCredentials(\"work\") = nil; want an error when aws is not on PATH")
	}
}

// TestGatewayRoutesSigV4SigningCredentialProfileEmptyNameRunsNothing checks that an empty profile name is refused
// before the CLI runs at all, rather than exporting whatever profile the CLI would default to.
func TestGatewayRoutesSigV4SigningCredentialProfileEmptyNameRunsNothing(t *testing.T) {
	dir, logPath := fakeAWS(t, printDocument(exportedDocument))

	if _, err := awssig.LoadCredentials("", environment(map[string]string{"PATH": dir})); err == nil {
		t.Fatal("LoadCredentials(\"\") = nil; want an error for an empty profile name")
	}

	if calls := callLog(t, logPath); len(calls) != 0 {
		t.Errorf("aws ran %d times for an empty profile name; want none", len(calls))
	}
}
