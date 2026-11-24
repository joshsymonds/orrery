// Package awssig signs AWS requests with SigV4 and loads the credentials they are signed with from a named AWS profile.
// It is standard-library only.
package awssig

import (
	"errors"
	"net/http"
	"time"
)

// errNotImplemented is returned by each entry point whose body is not written yet.
var errNotImplemented = errors.New("awssig: not implemented yet")

// Credentials are one set of AWS keys.
type Credentials struct {
	// AccessKeyID is the access key id.
	AccessKeyID string

	// SecretAccessKey is the secret access key that signs requests.
	SecretAccessKey string

	// SessionToken is the session token of temporary credentials. It is empty for a long-lived key pair.
	SessionToken string
}

// Sign signs req for service in region with creds, as of now, over body. It sets the request's X-Amz-Date header,
// its X-Amz-Security-Token header when creds carry a session token, and its Authorization header.
func Sign(_ *http.Request, _ []byte, _ Credentials, _, _ string, _ time.Time) error {
	return errNotImplemented
}

// LoadCredentials loads the credentials of the named AWS profile. getenv looks up environment variables.
func LoadCredentials(_ string, _ func(string) string) (Credentials, error) {
	return Credentials{}, errNotImplemented
}
