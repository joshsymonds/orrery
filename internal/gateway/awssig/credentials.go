package awssig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// The aws CLI invocation that exports a profile's credentials, and the variable that selects the profile. The profile
// is passed in the environment rather than the command line, so nothing read from configuration can become an argument.
const (
	awsBinary     = "aws"
	configureVerb = "configure"
	exportVerb    = "export-credentials"
	profileEnv    = "AWS_PROFILE"
)

// exportTimeout bounds one aws invocation. Exporting credentials is a local operation on a warm SSO cache and a network
// one on a cold one, so the bound is generous enough for the latter and short enough that a hung CLI cannot hold a
// request open.
const exportTimeout = 10 * time.Second

// Wire field names of the aws CLI's credential document. Only these are read; anything else it prints is ignored.
const (
	fieldAccessKeyID = "AccessKeyId"
	fieldSecretKey   = "SecretAccessKey"
	fieldSessionKey  = "SessionToken"
)

// childVariables names the only variables of the gateway's own environment the aws CLI is given. They are what the CLI
// needs to find its configuration and credentials, reach its endpoints through the proxies and certificate bundles an
// operator set, and run a profile's credential_process. Everything else is withheld: the gateway's environment carries
// secrets the CLI has no business seeing, and an ambient AWS key pair would override the profile.
func childVariables() []string {
	return []string{
		"HOME",
		"PATH",
		"AWS_CONFIG_FILE",
		"AWS_SHARED_CREDENTIALS_FILE",
		"AWS_CA_BUNDLE",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"AWS_STS_REGIONAL_ENDPOINTS",
		"AWS_ENDPOINT_URL",
		"SSL_CERT_FILE",
		"SSL_CERT_DIR",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"http_proxy",
		"https_proxy",
		"no_proxy",
	}
}

// LoadCredentials exports the credentials of the named AWS profile by running `aws configure export-credentials` with
// AWS_PROFILE set to profile. The aws CLI does the reading, not this package: it already knows how to refresh an SSO
// session, assume a role and run a credential_process, and a second reader of ~/.aws would be a second, subtly
// different idea of what a profile means. The CLI stops when ctx ends or exportTimeout passes, whichever is first.
// getenv looks up environment variables and must not be nil. The CLI's
// environment is built from getenv over childVariables and the profile alone. The CLI runs on every call, so a
// refreshed credential takes effect without a restart.
//
// Every failure is reported as a short sentence naming only the profile. The CLI's own output is dropped: it can carry
// credential material, and an error reaches the gateway's ledger and its callers.
func LoadCredentials(ctx context.Context, profile string, getenv func(string) string) (Credentials, error) {
	if profile == "" {
		return Credentials{}, errors.New("aws profile name is empty")
	}

	runCtx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()

	// The binary and its verbs are constants: the aws CLI is found on the process's PATH as any command is.
	cmd := exec.CommandContext(runCtx, awsBinary, configureVerb, exportVerb)
	cmd.Env = childEnvironment(profile, getenv)

	out, err := cmd.Output()
	if err != nil {
		return Credentials{}, fmt.Errorf("exporting aws credentials for profile %q failed", profile)
	}

	creds, err := parseCredentials(out)
	if err != nil {
		return Credentials{}, fmt.Errorf("aws credentials for profile %q could not be read", profile)
	}

	return creds, nil
}

// childEnvironment is the environment the aws CLI runs in: AWS_PROFILE naming the profile, plus each of childVariables
// that getenv reports set.
func childEnvironment(profile string, getenv func(string) string) []string {
	env := []string{profileEnv + "=" + profile}

	for _, name := range childVariables() {
		if value := getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}

	return env
}

// parseCredentials reads the key pair and session token out of the CLI's credential document, ignoring every other key
// rather than failing on one it has not seen. A document that names no key pair is refused.
func parseCredentials(document []byte) (Credentials, error) {
	var fields map[string]json.RawMessage

	if err := json.Unmarshal(document, &fields); err != nil {
		return Credentials{}, fmt.Errorf("decode credential document: %w", err)
	}

	creds := Credentials{
		AccessKeyID:     stringField(fields, fieldAccessKeyID),
		SecretAccessKey: stringField(fields, fieldSecretKey),
		SessionToken:    stringField(fields, fieldSessionKey),
	}

	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return Credentials{}, errors.New("credential document names no key pair")
	}

	return creds, nil
}

// stringField reads one string field, treating a missing field and one of some other type alike: the document did not
// say.
func stringField(fields map[string]json.RawMessage, name string) string {
	raw, present := fields[name]
	if !present {
		return ""
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}

	return value
}
