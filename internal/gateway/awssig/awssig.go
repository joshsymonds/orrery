// Package awssig speaks the parts of AWS that the gateway's Bedrock upstream needs and nothing else: SigV4 request
// signing, the binary event-stream framing a streamed Bedrock response arrives in, and the credentials of a named AWS
// profile, exported by the aws CLI. It is standard-library only.
package awssig

// Credentials are one set of AWS keys.
type Credentials struct {
	// AccessKeyID is the access key id.
	AccessKeyID string

	// SecretAccessKey is the secret access key that signs requests.
	SecretAccessKey string

	// SessionToken is the session token of temporary credentials. It is empty for a long-lived key pair.
	SessionToken string
}
