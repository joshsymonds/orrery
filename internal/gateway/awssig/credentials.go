package awssig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sharedFileEnv names the environment variable that moves the shared credentials file, as the AWS CLI reads it.
const sharedFileEnv = "AWS_SHARED_CREDENTIALS_FILE"

// homeEnv names the variable the default credentials file is found under.
const homeEnv = "HOME"

// defaultDir and defaultFile are where the shared credentials file lives under the home directory when sharedFileEnv
// is unset.
const (
	defaultDir  = ".aws"
	defaultFile = "credentials"
)

// LoadCredentials loads the static key pair of the named AWS profile from the shared credentials file: the file that
// sharedFileEnv names, or else HOME's .aws/credentials. getenv looks up environment variables and must not be nil.
// The file is read on every call, so a rotated key pair takes effect without a restart. The session token is read when
// the profile names one and is empty otherwise.
func LoadCredentials(profile string, getenv func(string) string) (Credentials, error) {
	if profile == "" {
		return Credentials{}, errors.New("aws profile name is empty")
	}

	path, err := credentialsPath(getenv)
	if err != nil {
		return Credentials{}, err
	}

	document, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, fmt.Errorf("read aws credentials for profile %q: %w", profile, err)
	}

	values := profileValues(string(document), profile)
	creds := Credentials{
		AccessKeyID:     values["aws_access_key_id"],
		SecretAccessKey: values["aws_secret_access_key"],
		SessionToken:    values["aws_session_token"],
	}

	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return Credentials{}, fmt.Errorf("aws profile %q names no access key pair", profile)
	}

	return creds, nil
}

// credentialsPath is the shared credentials file's location: sharedFileEnv when it is set, otherwise the file under
// HOME.
func credentialsPath(getenv func(string) string) (string, error) {
	if path := getenv(sharedFileEnv); path != "" {
		return path, nil
	}

	home := getenv(homeEnv)
	if home == "" {
		return "", fmt.Errorf("neither %s nor %s is set, so the aws credentials file has no location",
			sharedFileEnv, homeEnv)
	}

	return filepath.Join(home, defaultDir, defaultFile), nil
}

// profileValues returns the key/value pairs of the named profile's section of an INI-style credentials document. A
// section header is a line of the form [name]. Blank lines and lines beginning # or ; are comments. Keys and values are
// trimmed of surrounding space, and a key that appears twice in the section keeps its later value.
func profileValues(document, profile string) map[string]string {
	values := map[string]string{}
	inProfile := false

	for _, line := range strings.Split(document, "\n") {
		line = strings.TrimSpace(line)

		switch {
		case line == "", strings.HasPrefix(line, "#"), strings.HasPrefix(line, ";"):
			// A blank line or a comment carries no value.
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			inProfile = strings.TrimSpace(line[1:len(line)-1]) == profile
		case inProfile:
			key, value, found := strings.Cut(line, "=")
			if found {
				values[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
	}

	return values
}
